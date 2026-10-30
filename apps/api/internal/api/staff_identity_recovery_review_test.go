package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func recoveryReviewFixture(t *testing.T) (*harness, store.StaffIdentityCommand, store.StaffIdentityRecoveryScope, store.StaffIdentityRecoveryRequest) {
	t.Helper()
	h := newHarness(t)
	c := preparedStaffIdentity(t, h)
	if claimed, err := h.store.ClaimStaffIdentityDispatch(t.Context(), c, audit.Event{ActorType: audit.ActorProduct, ActorID: c.MachineSubject, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.dispatch", Outcome: audit.OutcomeSuccess}); err != nil || !claimed {
		t.Fatalf("fixture dispatch: %v", err)
	}
	id := uuid.Must(uuid.NewV7())
	t.Cleanup(func() {
		for _, table := range []string{"staff_identity_recovery_decisions", "staff_identity_recovery_requests"} {
			key := "request_id"
			if table == "staff_identity_recovery_requests" {
				key = "id"
			}
			if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+table+" WHERE "+key+"=$1", id); err != nil {
				t.Error(err)
			}
		}
	})
	scope := store.StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := store.StaffIdentityRecoveryRequest{ID: id, RequestedBy: "TEST-recovery-maker", Action: "close_unresolved", Reason: "Investigated retained dispatch", EvidenceReference: "QA:RECOVERY/REVIEW-HTTP-1"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.RequestedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.propose", Outcome: audit.OutcomeSuccess}
	request, err := h.store.ProposeStaffIdentityRecovery(t.Context(), scope, in, event)
	if err != nil {
		t.Fatal(err)
	}
	return h, c, scope, request
}

func TestStaffRecoveryHTTPReviewShowsRetainedRequestAndDecisionWithoutSourceBindings(t *testing.T) {
	h, c, scope, request := recoveryReviewFixture(t)
	id := request.ID
	proofCalls := 0
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		proofCalls++
		if write {
			t.Error("review disclosure required expanded write authority")
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{claims: map[string]auth.Claims{
		productToken:        {Subject: c.MachineSubject},
		"ordinary-platform": {Subject: "TEST-reader", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
	}}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"console-reader": {Console: true, Subject: "TEST-reader", OrgID: platformOrg, Roles: []string{authz.RolePlatformOperator}},
	}}})
	path := staffIdentityPath(c) + "/recovery/requests/" + id.String()
	rec := h.do(t, http.MethodGet, path, "console-reader", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("review HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var got onboarding.StaffIdentityRecoveryRequestReceipt
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RequestID != id || got.CommandID != c.ID || got.TenantID != c.OrgID || got.RequestedBy != request.RequestedBy || got.Action != request.Action || got.Reason != request.Reason || got.EvidenceReference != request.EvidenceReference || !got.RecordedAt.Equal(request.CreatedAt) || got.Decision != nil || proofCalls != 2 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("review lost frozen request facts or current read authority")
	}
	for _, private := range []string{c.Email, c.GivenName, c.FamilyName, c.BindingNonce, c.ProviderUserID, c.ProviderOrgID, c.ProviderProjectID, c.MachineSubject, c.ActorSubject} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("review disclosed private command/source binding")
		}
	}
	decision := store.StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: "TEST-independent-reviewer", Decision: "rejected", Reason: "Source investigation does not justify replacement"}
	decide := audit.Event{ActorType: audit.ActorPlatform, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Outcome: audit.OutcomeSuccess}
	decide.ActorID = decision.ReviewedBy
	decide.Action = "staff.identity.recovery.decide"
	retained, err := h.store.DecideStaffIdentityRecovery(t.Context(), scope, decision, nil, decide)
	if err != nil {
		t.Fatal(err)
	}
	rec = h.do(t, http.MethodGet, path, "console-reader", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Decision == nil || got.Decision.Decision != "rejected" || got.Decision.ReviewedBy != decision.ReviewedBy || got.Decision.Reason != decision.Reason || !got.Decision.RecordedAt.Equal(retained.Decision.CreatedAt) || got.Decision.SuccessorCommandID != nil {
		t.Fatalf("current retained rejection lost: HTTP %d", rec.Code)
	}
	for _, token := range []string{productToken, "ordinary-platform"} {
		if rec := h.do(t, http.MethodGet, path, token, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("untrusted review read HTTP %d", rec.Code)
		}
	}
	before := proofCalls
	for _, bad := range []struct{ suffix, body string }{{"?requested_by=TEST-reader", ""}, {"?", ""}, {"", "{}"}} {
		if rec := h.do(t, http.MethodGet, path+bad.suffix, "console-reader", bad.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("asserted query/body accepted: HTTP %d", rec.Code)
		}
	}
	for _, invalid := range []string{"invalid", uuid.NewString(), uuid.Nil.String()} {
		if rec := h.do(t, http.MethodGet, staffIdentityPath(c)+"/recovery/requests/"+invalid, "console-reader", ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid request identity accepted: HTTP %d", rec.Code)
		}
	}
	if proofCalls != before {
		t.Fatal("invalid read reached authority source")
	}
	if rec := h.do(t, http.MethodGet, staffIdentityPath(c)+"/recovery/requests/"+uuid.Must(uuid.NewV7()).String(), "console-reader", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign request disclosed: HTTP %d", rec.Code)
	}
}

func TestStaffRecoveryReviewRechecksAuthorityAndScopeBeforeEvidenceDisclosure(t *testing.T) {
	for _, kind := range []string{"revoked operator", "source unavailable", "withdrawn tenant entitlement", "rotated canonical machine"} {
		t.Run(kind, func(t *testing.T) {
			h, c, _, request := recoveryReviewFixture(t)
			calls := 0
			source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
				calls++
				if calls == 2 {
					switch kind {
					case "revoked operator":
						return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
					case "source unavailable":
						return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrIdentityUnavailable
					case "withdrawn tenant entitlement":
						if err := h.store.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
							t.Fatal(err)
						}
					case "rotated canonical machine":
						if _, err := h.fixturePool.Exec(t.Context(), `UPDATE products SET machine_user_id='TEST-rotated' WHERE key=$1`, c.ProductKey); err != nil {
							t.Fatal(err)
						}
					}
				}
				return currentRecoveryOperator(subject, project, write), nil
			}}
			svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
			p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-reader", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformOperator}}
			got, err := svc.StaffIdentityRecoveryRequest(t.Context(), p, c.OrgID, c.ID, request.ID)
			want := onboarding.ErrNotFound
			if kind == "source unavailable" {
				want = onboarding.ErrUnavailable
			}
			if kind == "rotated canonical machine" {
				want = onboarding.ErrConflict
			}
			if !errors.Is(err, want) || got.RequestID != uuid.Nil || got.Reason != "" || got.EvidenceReference != "" || got.Decision != nil {
				t.Fatalf("withdrawn evidence disclosure released data: %v", err)
			}
			var inspections, disclosures int
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE action='staff.identity.recovery.request.inspect'),count(*) FILTER(WHERE action='staff.identity.recovery.request.read') FROM audit_events WHERE target=$1 AND outcome='success'`, request.ID.String()).Scan(&inspections, &disclosures); err != nil || inspections != 1 || disclosures != 0 {
				t.Fatalf("failed disclosure audit mismatch: inspect=%d read=%d err=%v", inspections, disclosures, err)
			}
		})
	}
}

func TestStaffRecoveryReviewReturnsDecisionCommittedDuringReverification(t *testing.T) {
	h, c, scope, request := recoveryReviewFixture(t)
	calls := 0
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		calls++
		if calls == 2 {
			decision := store.StaffIdentityRecoveryDecision{RequestID: request.ID, ReviewedBy: "TEST-independent-reviewer", Decision: "rejected", Reason: "Final reviewed source investigation"}
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: decision.ReviewedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.decide", Outcome: audit.OutcomeSuccess}
			if _, err := h.store.DecideStaffIdentityRecovery(t.Context(), scope, decision, nil, event); err != nil {
				t.Fatal(err)
			}
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-reader", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformOperator}}
	got, err := svc.StaffIdentityRecoveryRequest(t.Context(), p, c.OrgID, c.ID, request.ID)
	if err != nil || got.RequestID != request.ID || got.Decision == nil || got.Decision.Decision != "rejected" || got.Decision.Reason != "Final reviewed source investigation" {
		t.Fatalf("review disclosed pre-verification stale decision: %v", err)
	}
}
