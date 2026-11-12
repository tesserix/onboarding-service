//go:build staff_provider_fixture

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// This fixture uses real provider/registry/ledger proof and asserted console tokens.
// It is not browser login, recipient setup or normal HMS reviewer acceptance.
func TestStaffRecoveryHTTPActualProvider(t *testing.T) {
	root := os.Getenv("STAFF_PROVIDER_FIXTURE_PATH")
	if !filepath.IsAbs(root) {
		t.Fatal("explicit isolated provider fixture required")
	}
	raw, err := os.ReadFile(filepath.Join(root, "probe-state.json"))
	if err != nil {
		t.Fatal("private fixture state unavailable")
	}
	var fixture struct{ Org string }
	if json.Unmarshal(raw, &fixture) != nil || fixture.Org == "" {
		t.Fatal("private fixture organization unavailable")
	}
	patPath := filepath.Join(root, "secrets", "qa.pat")
	info, err := os.Stat(patPath)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private fixture credential permissions required")
	}
	pat, err := os.ReadFile(patPath)
	if err != nil {
		t.Fatal("private fixture credential unavailable")
	}
	token := strings.TrimSpace(string(pat))
	provider := zitadel.New("http://localhost:32880", zitadel.StaticToken(token), fixture.Org, nil)
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path, org string, in any) map[string]json.RawMessage {
		t.Helper()
		body, err := json.Marshal(in)
		if err != nil {
			t.Fatal("fixture request unavailable")
		}
		req, err := http.NewRequestWithContext(t.Context(), method, "http://localhost:32880"+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal("fixture request unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-zitadel-orgid", org)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("isolated provider unavailable")
		}
		defer func() { _ = res.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("fixture protocol HTTP %d at %s", res.StatusCode, path)
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(data, &result) != nil {
			t.Fatal("invalid fixture response")
		}
		return result
	}
	id := func(result map[string]json.RawMessage, key string) string {
		t.Helper()
		var v string
		if json.Unmarshal(result[key], &v) != nil || v == "" {
			t.Fatal("fixture identity unavailable")
		}
		return v
	}
	h := newHarness(t)
	label := "recovery-http-" + uuid.NewString()
	tenant := uuid.New()
	org, err := provider.CreateOrg(t.Context(), label)
	if err != nil {
		t.Fatal("isolated tenant creation failed")
	}
	if err := provider.SetOrgMetadata(t.Context(), org, "tenant_id", tenant.String()); err != nil {
		t.Fatal("isolated tenant binding failed")
	}
	productProject, err := provider.CreateProject(t.Context(), label+"-product", []string{"planning-poker-user"})
	if err != nil {
		t.Fatal("isolated product project failed")
	}
	if err := provider.GrantProject(t.Context(), productProject, org, []string{"planning-poker-user"}); err != nil {
		t.Fatal("isolated project entitlement failed")
	}
	consoleProject, err := provider.CreateProject(t.Context(), label+"-console", []string{"platform", "rotate-credentials"})
	if err != nil {
		t.Fatal("isolated console project failed")
	}
	operator := func(suffix string) (string, string) {
		t.Helper()
		subject := id(call(http.MethodPost, "/v2/users/human", fixture.Org, map[string]any{"organizationId": fixture.Org, "username": label + suffix + "@example.invalid", "profile": map[string]string{"givenName": "Synthetic", "familyName": "RecoveryReviewer"}, "email": map[string]any{"email": label + suffix + "@example.invalid", "isVerified": true}}), "userId")
		grant := id(call(http.MethodPost, "/management/v1/users/"+subject+"/grants", fixture.Org, map[string]any{"projectId": consoleProject, "roleKeys": []string{"platform", "rotate-credentials"}}), "userGrantId")
		return subject, grant
	}
	maker, _ := operator("-maker")
	reviewer, grant := operator("-reviewer")
	if _, err := h.fixturePool.Exec(t.Context(), `UPDATE products SET zitadel_project_id=$1 WHERE key='planning-poker'`, productProject); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "Synthetic recovery HTTP tenant", BillingEmail: "synthetic@example.invalid", State: "active", CreatedByProduct: "planning-poker"}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Grant(t.Context(), tenant, "planning-poker", []string{"planning-poker-user"}); err != nil {
		t.Fatal(err)
	}
	c := store.StaffIdentityCommand{ID: uuid.Must(uuid.NewV7()), OrgID: tenant, ProductKey: "planning-poker", MachineSubject: "svc-planning-poker", ActorSubject: maker, ProviderOrgID: org, ProviderProjectID: productProject, ProviderUserID: "TEST-recovery-http-" + uuid.NewString(), BindingNonce: "TEST-binding-" + uuid.NewString(), Email: label + "-recipient@example.invalid", GivenName: "Synthetic", FamilyName: "RecoveryRecipient", Reason: "Isolated real-source HTTP acceptance fixture"}
	event := audit.Event{ActorType: audit.ActorProduct, ActorID: c.MachineSubject, ProductKey: c.ProductKey, OrgID: tenant.String(), Action: "staff.identity.prepare", Target: c.ID.String(), Outcome: audit.OutcomeSuccess}
	c, err = h.store.PrepareStaffIdentity(t.Context(), c, event)
	if err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.dispatch"
	if _, err := h.store.ClaimStaffIdentityDispatch(t.Context(), c, event); err != nil {
		t.Fatal(err)
	}
	request, successor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	t.Cleanup(func() {
		for _, item := range []struct {
			table, key string
			value      any
		}{
			{"staff_identity_creations", "command_id", c.ID}, {"staff_identity_history_cursors", "command_id", c.ID}, {"staff_identity_replacements", "request_id", request}, {"staff_identity_commands", "id", successor}, {"staff_identity_recovery_decisions", "request_id", request}, {"staff_identity_recovery_requests", "id", request}, {"staff_identity_dispatches", "command_id", c.ID}, {"staff_identity_contact_claims", "root_command_id", c.ID}, {"staff_identity_commands", "id", c.ID},
		} {
			if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+item.table+" WHERE "+item.key+"=$1", item.value); err != nil {
				t.Error(err)
			}
		}
	})
	capturePath := filepath.Join(root, "recovery-http-"+c.ID.String()+".json")
	capture, err := json.Marshal(map[string]any{"purpose": "local HTTP proof fixture with asserted console tokens; no browser or production acceptance", "tenant": tenant, "organization": org, "product_project": productProject, "console_project": consoleProject, "maker": maker, "reviewer": reviewer, "reviewer_grant": grant, "command": c, "request": request, "successor": successor})
	if err != nil {
		t.Fatal("private recreation capture unavailable")
	}
	if err := os.WriteFile(capturePath, capture, 0600); err != nil {
		t.Fatal("private recreation capture unavailable")
	}
	h.identity.verify = provider.VerifyOrganization
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", provider), onboarding.WithStaffRecovery(consoleProject, provider))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: fixture.Org, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"maker": {Console: true, Subject: maker, OrgID: fixture.Org, Roles: []string{authz.RolePlatformAdmin}}, "reviewer": {Console: true, Subject: reviewer, OrgID: fixture.Org, Roles: []string{authz.RolePlatformAdmin}},
	}}})
	path := staffIdentityPath(c) + "/recovery"
	proposal := `{"requested_action":"replace","reason":"Investigated unresolved dispatch","evidence_reference":"QA:REAL-SOURCE/HTTP","replacement_actor_subject":"` + maker + `"}`
	if rec := h.do(t, http.MethodPut, path+"/requests/"+request.String(), "maker", proposal); rec.Code != http.StatusOK {
		t.Fatalf("actual-source proposal HTTP %d", rec.Code)
	}
	decisionPath := path + "/requests/" + request.String() + "/decision"
	decision := `{"decision":"approved","reason":"Independent actual-source review","successor_command_id":"` + successor.String() + `"}`
	if rec := h.do(t, http.MethodPut, decisionPath, "maker", decision); rec.Code != http.StatusConflict {
		t.Fatalf("actual-source self review HTTP %d", rec.Code)
	}
	first := h.do(t, http.MethodPut, decisionPath, "reviewer", decision)
	if first.Code != http.StatusOK {
		t.Fatalf("actual-source absent approval HTTP %d", first.Code)
	}
	input := zitadel.StaffIdentityInput{UserID: c.ProviderUserID, OrganizationID: org, Email: c.Email, GivenName: c.GivenName, FamilyName: c.FamilyName, BindingNonce: c.BindingNonce}
	created, err := provider.CreateStaffIdentity(t.Context(), input)
	if err != nil {
		t.Fatal("single isolated late creation failed")
	}
	// Delivery events may advance the current head; retry fresh proof, never creation.
	var recCode int
	for attempt := 0; attempt < 3; attempt++ {
		rec := h.do(t, http.MethodPost, path+"/reconciliations", "reviewer", "")
		recCode = rec.Code
		if recCode != http.StatusServiceUnavailable {
			break
		}
	}
	if recCode != http.StatusOK {
		t.Fatalf("actual late orphan reconciliation HTTP %d", recCode)
	}
	replay := h.do(t, http.MethodPut, decisionPath, "reviewer", decision)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatal("changed live source altered frozen actual-source decision")
	}
	var birthText string
	var childDispatches int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT source_creation_time FROM staff_identity_creations WHERE command_id=$1`, c.ID).Scan(&birthText); err != nil || birthText != created.CreatedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatal("actual late source birth was not retained")
	}
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_dispatches WHERE command_id=$1`, successor).Scan(&childDispatches); err != nil || childDispatches != 0 {
		t.Fatal("recovery dispatched successor")
	}
	if dispatched, err := h.store.ClaimStaffIdentityDispatch(t.Context(), c, event); dispatched || !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatal("late actual source fact reopened superseded original dispatch")
	}
	call(http.MethodPost, "/management/v1/users/"+reviewer+"/grants/"+grant+"/_deactivate", fixture.Org, map[string]any{})
	if rec := h.do(t, http.MethodPut, decisionPath, "reviewer", decision); rec.Code != http.StatusNotFound {
		t.Fatalf("actual withdrawn grant recovered decision HTTP %d", rec.Code)
	}
	call(http.MethodPost, "/management/v1/users/"+reviewer+"/grants/"+grant+"/_reactivate", fixture.Org, map[string]any{})
	if rec := h.do(t, http.MethodPut, decisionPath, "reviewer", decision); rec.Code != http.StatusOK {
		t.Fatalf("actual restored grant recovery HTTP %d", rec.Code)
	}
	t.Log("actual-source HTTP proposal, independent approval, late orphan reconciliation, immutable retry and grant withdrawal passed; one creation, zero successor dispatches")
}
