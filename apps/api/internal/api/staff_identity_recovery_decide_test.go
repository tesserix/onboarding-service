package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestStaffRecoveryDecisionHTTPRequiresIndependentReviewerAndFreezesApprovedRetry(t *testing.T) {
	h, c, _, request := recoveryReviewFixture(t)
	calls := 0
	subject := recoverySubjectSource{inspect: func(_ context.Context, in zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffRecoverySubjectProof, error) {
		calls++
		return zitadel.StaffRecoverySubjectProof{UserID: in.UserID, OrganizationID: in.OrganizationID, Disposition: "absent", VerifiedAt: time.Now().UTC()}, nil
	}}
	operator := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		if !write {
			t.Error("decision used read-only authority")
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", subject), onboarding.WithStaffRecovery("TEST-console-project", operator))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"maker":    {Console: true, Subject: request.RequestedBy, OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
		"reviewer": {Console: true, Subject: "TEST-independent-reviewer", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
	}}})
	path := staffIdentityPath(c) + "/recovery/requests/" + request.ID.String() + "/decision"
	body := `{"decision":"approved","reason":"Independent investigation of frozen source facts"}`
	if rec := h.do(t, http.MethodPut, path, "maker", body); rec.Code != http.StatusConflict || calls != 0 {
		t.Fatalf("maker approved own request: HTTP %d", rec.Code)
	}
	first := h.do(t, http.MethodPut, path, "reviewer", body)
	if first.Code != http.StatusOK {
		t.Fatalf("independent decision HTTP %d: %s", first.Code, first.Body.String())
	}
	got := decode(t, first)
	decision, ok := got["decision"].(map[string]any)
	if !ok || decision["decision"] != "approved" || decision["reviewed_by"] != "TEST-independent-reviewer" || got["request_id"] != request.ID.String() || calls != 1 {
		t.Fatal("approved receipt lost frozen reviewer or fresh source check")
	}
	replay := h.do(t, http.MethodPut, path, "reviewer", body)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() || calls != 1 {
		t.Fatal("exact decision retry changed receipt or re-executed approval source preconditions")
	}
	if rec := h.do(t, http.MethodPut, path, "reviewer", `{"decision":"rejected","reason":"Changed immutable decision"}`); rec.Code != http.StatusConflict {
		t.Fatalf("final approval changed: HTTP %d", rec.Code)
	}
	var successors int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_replacements WHERE parent_command_id=$1`, c.ID).Scan(&successors); err != nil || successors != 0 {
		t.Fatal("closure decision prepared or dispatched unwanted successor")
	}
}

func TestStaffRecoveryDecisionHTTPBoundaryAndRevokedRetry(t *testing.T) {
	h, c, _, request := recoveryReviewFixture(t)
	revoked := false
	source := recoverySubjectSource{inspect: func(context.Context, zitadel.StaffIdentityInput, time.Time) (zitadel.StaffRecoverySubjectProof, error) {
		t.Fatal("rejection inspected subject")
		return zitadel.StaffRecoverySubjectProof{}, nil
	}}
	operator := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		if revoked {
			return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", source), onboarding.WithStaffRecovery("TEST-console-project", operator))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{claims: map[string]auth.Claims{productToken: {Subject: c.MachineSubject}}}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"reviewer": {Console: true, Subject: "TEST-independent-reviewer", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
		"reader":   {Console: true, Subject: "TEST-reader", OrgID: platformOrg, Roles: []string{authz.RolePlatformOperator}},
	}}})
	path := staffIdentityPath(c) + "/recovery/requests/" + request.ID.String() + "/decision"
	body := `{"decision":"rejected","reason":"Insufficient independent evidence"}`
	for _, token := range []string{productToken, "reader"} {
		if rec := h.do(t, http.MethodPut, path, token, body); rec.Code != http.StatusNotFound {
			t.Fatalf("untrusted decision HTTP %d", rec.Code)
		}
	}
	for _, bad := range []string{`{}`, `{"decision":"approved","reason":" "}`, `{"decision":"invalid","reason":"Reviewed"}`, `{"decision":"rejected","reason":"Reviewed","reviewed_by":"TEST-other"}`, `{"decision":"rejected","reason":"Reviewed","successor_command_id":"invalid"}`, `{"decision":"rejected","decision":"approved","reason":"Reviewed"}`} {
		if rec := h.do(t, http.MethodPut, path, "reviewer", bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid decision HTTP %d", rec.Code)
		}
	}
	if rec := h.do(t, http.MethodPut, path+"?decision=approved", "reviewer", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("query decision HTTP %d", rec.Code)
	}
	foreign := strings.Replace(path, c.OrgID.String(), uuid.Must(uuid.NewV7()).String(), 1)
	if rec := h.do(t, http.MethodPut, foreign, "reviewer", body); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant decision HTTP %d", rec.Code)
	}
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer reviewer")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "TEST-decision-"+request.ID.String())
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}
	first := do()
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rejection HTTP %d", first.Code)
	}
	revoked = true
	if rec := do(); rec.Code != http.StatusNotFound || rec.Header().Get("Idempotent-Replay") != "" {
		t.Fatalf("cached decision bypassed revoked authority HTTP %d", rec.Code)
	}
	revoked = false
	replay := do()
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatal("authorized rejection retry changed immutable receipt")
	}
}

func TestStaffRecoveryDecisionHTTPReplacementDispositions(t *testing.T) {
	for _, disposition := range []string{"absent", "matching", "removed", "unavailable"} {
		t.Run(disposition, func(t *testing.T) {
			h, c, scope, _ := recoveryReviewFixture(t)
			request := store.StaffIdentityRecoveryRequest{ID: uuid.Must(uuid.NewV7()), RequestedBy: "TEST-new-maker", Action: "replace", ReplacementActorSubject: "TEST-new-initiator", Reason: "Replace investigated dispatch", EvidenceReference: "QA:REPLACE/HTTP"}
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: request.RequestedBy, ProductKey: scope.ProductKey, OrgID: scope.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.propose", Outcome: audit.OutcomeSuccess}
			if _, err := h.store.ProposeStaffIdentityRecovery(t.Context(), scope, request, event); err != nil {
				t.Fatal(err)
			}
			successor := uuid.Must(uuid.NewV7())
			t.Cleanup(func() {
				for _, item := range []struct {
					table, key string
					id         uuid.UUID
				}{
					{"staff_identity_replacements", "request_id", request.ID}, {"staff_identity_commands", "id", successor}, {"staff_identity_recovery_decisions", "request_id", request.ID}, {"staff_identity_recovery_requests", "id", request.ID},
				} {
					if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+item.table+" WHERE "+item.key+"=$1", item.id); err != nil {
						t.Error(err)
					}
				}
			})
			calls := 0
			source := recoverySubjectSource{staffSource: staffSource{create: func(context.Context, zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
				t.Fatal("decision created provider account")
				return zitadel.StaffIdentityCreated{}, nil
			}}, inspect: func(_ context.Context, in zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffRecoverySubjectProof, error) {
				calls++
				if disposition == "unavailable" {
					return zitadel.StaffRecoverySubjectProof{}, zitadel.ErrIdentityUnavailable
				}
				proof := zitadel.StaffRecoverySubjectProof{UserID: in.UserID, OrganizationID: in.OrganizationID, Disposition: disposition, VerifiedAt: time.Now().UTC()}
				if disposition != "absent" {
					proof.CreatedAt = c.CreatedAt.Add(time.Nanosecond)
					proof.ObjectRevision = "3"
				}
				return proof, nil
			}}
			operator := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
				return currentRecoveryOperator(subject, project, write), nil
			}}
			svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", source), onboarding.WithStaffRecovery("TEST-console-project", operator))
			h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{"reviewer": {Console: true, Subject: "TEST-independent-reviewer", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}}}}})
			path := staffIdentityPath(c) + "/recovery/requests/" + request.ID.String() + "/decision"
			body := `{"decision":"approved","reason":"Independent replacement review","successor_command_id":"` + successor.String() + `"}`
			rec := h.do(t, http.MethodPut, path, "reviewer", body)
			expected := http.StatusOK
			if disposition == "matching" {
				expected = http.StatusConflict
			}
			if disposition == "unavailable" {
				expected = http.StatusServiceUnavailable
			}
			if rec.Code != expected {
				t.Fatalf("%s decision HTTP %d: %s", disposition, rec.Code, rec.Body.String())
			}
			var decisions, children, receipts, dispatches int
			for _, query := range []struct {
				sql  string
				dest *int
			}{
				{`SELECT count(*) FROM staff_identity_recovery_decisions WHERE request_id=$1`, &decisions},
				{`SELECT count(*) FROM staff_identity_replacements WHERE request_id=$1`, &children},
			} {
				if err := h.fixturePool.QueryRow(t.Context(), query.sql, request.ID).Scan(query.dest); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_creations WHERE command_id=$1`, c.ID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_dispatches WHERE command_id=$1`, successor).Scan(&dispatches); err != nil {
				t.Fatal(err)
			}
			want := 0
			if expected == http.StatusOK {
				want = 1
			}
			wantReceipt := 0
			if disposition == "matching" || disposition == "removed" {
				wantReceipt = 1
			}
			if decisions != want || children != want || receipts != wantReceipt || dispatches != 0 {
				t.Fatalf("unexpected decision/child/receipt/dispatch counts %d/%d/%d/%d", decisions, children, receipts, dispatches)
			}
			if want == 1 {
				replay := h.do(t, http.MethodPut, path, "reviewer", body)
				if replay.Code != http.StatusOK || replay.Body.String() != rec.Body.String() || calls != 1 {
					t.Fatal("replacement replay reran source checks or changed frozen successor")
				}
				var actor, user, nonce string
				if err := h.fixturePool.QueryRow(t.Context(), `SELECT actor_subject,provider_user_id,binding_nonce FROM staff_identity_commands WHERE id=$1`, successor).Scan(&actor, &user, &nonce); err != nil || actor != request.ReplacementActorSubject || user == c.ProviderUserID || nonce == c.BindingNonce || len(nonce) != 43 {
					t.Fatal("prepared successor lost independent source binding")
				}
			}
		})
	}
}

func TestStaffRecoveryDecisionHTTPAuthorityWithdrawalPreservesOnlyCommittedHistory(t *testing.T) {
	for _, withdrawAt := range []int{2, 3, 4} {
		t.Run(fmt.Sprint(withdrawAt), func(t *testing.T) {
			h, c, _, request := recoveryReviewFixture(t)
			proofs, subjects := 0, 0
			withdrawn := true
			source := recoverySubjectSource{inspect: func(_ context.Context, in zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffRecoverySubjectProof, error) {
				subjects++
				return zitadel.StaffRecoverySubjectProof{UserID: in.UserID, OrganizationID: in.OrganizationID, Disposition: "absent", VerifiedAt: time.Now().UTC()}, nil
			}}
			operator := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
				proofs++
				if withdrawn && proofs >= withdrawAt {
					return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
				}
				return currentRecoveryOperator(subject, project, write), nil
			}}
			svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", source), onboarding.WithStaffRecovery("TEST-console-project", operator))
			h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{"reviewer": {Console: true, Subject: "TEST-independent-reviewer", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}}}}})
			path := staffIdentityPath(c) + "/recovery/requests/" + request.ID.String() + "/decision"
			body := `{"decision":"approved","reason":"Independently investigated source"}`
			rec := h.do(t, http.MethodPut, path, "reviewer", body)
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "reviewed_by") {
				t.Fatalf("withdrawal released decision HTTP %d", rec.Code)
			}
			var decisions int
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_decisions WHERE request_id=$1`, request.ID).Scan(&decisions); err != nil {
				t.Fatal(err)
			}
			want := 0
			if withdrawAt == 4 {
				want = 1
			}
			if decisions != want {
				t.Fatalf("withdrawal stage %d retained %d decisions, want %d", withdrawAt, decisions, want)
			}
			withdrawn = false
			rec = h.do(t, http.MethodPut, path, "reviewer", body)
			expectedSubjects := 2
			if want == 1 {
				expectedSubjects = 1
			}
			if rec.Code != http.StatusOK || subjects != expectedSubjects {
				t.Fatalf("authorized retry HTTP %d, subject proofs %d", rec.Code, subjects)
			}
		})
	}
}
