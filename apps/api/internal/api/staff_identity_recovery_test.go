package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func closeStaffIdentityFixture(t *testing.T, h *harness, c store.StaffIdentityCommand) {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	scope := store.StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: "planning-poker", MachineSubject: "svc-planning-poker", ProviderOrgID: "org-identity", ProviderProjectID: "proj-planning-poker"}
	request := store.StaffIdentityRecoveryRequest{ID: id, RequestedBy: "TEST-platform-maker", Action: "close_unresolved", Reason: "Investigated uncertain source result", EvidenceReference: "QA-API-RECOVERY"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: request.RequestedBy, ProductKey: scope.ProductKey, OrgID: scope.OrgID.String(), Action: "staff.identity.recovery.propose", Target: c.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	if _, err := h.store.ProposeStaffIdentityRecovery(t.Context(), scope, request, event); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, item := range []struct{ table, key string }{{"staff_identity_recovery_decisions", "request_id"}, {"staff_identity_recovery_requests", "id"}} {
			if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+item.table+" WHERE "+item.key+"=$1", id); err != nil {
				t.Error(err)
			}
		}
	})
	decision := store.StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: "TEST-platform-reviewer", Decision: "approved", Reason: "Independent investigation closure"}
	event.ActorID = decision.ReviewedBy
	event.Action = "staff.identity.recovery.decide"
	if _, err := h.store.DecideStaffIdentityRecovery(t.Context(), scope, decision, nil, event, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStaffIdentityClosedReceiptAndObservationDenySourceAuthority(t *testing.T) {
	var calls atomic.Int32
	var c store.StaffIdentityCommand
	source := staffSource{observe: func(_ context.Context, _ zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffIdentityObservation, error) {
		calls.Add(1)
		return zitadel.StaffIdentityObservation{UserID: c.ProviderUserID, OrganizationID: c.ProviderOrgID, State: "active", EmailVerified: true, CreatedAt: c.CreatedAt, ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
	}}
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
	c = dispatchedStaffIdentity(t, h)
	closeStaffIdentityFixture(t, h, c)
	receipt := h.do(t, http.MethodGet, staffIdentityPath(c)+"?actor_subject=TEST-admin", productToken, "")
	if receipt.Code != http.StatusOK || decode(t, receipt)["recovery_state"] != "closed_unresolved" {
		t.Fatalf("receipt hid independent closure: status=%d", receipt.Code)
	}
	observed := h.do(t, http.MethodPost, staffIdentityPath(c)+"/observations", productToken, `{"actor_subject":"TEST-admin"}`)
	if observed.Code != http.StatusConflict || calls.Load() != 0 {
		t.Fatalf("closed identity observed as current authority: status=%d calls=%d", observed.Code, calls.Load())
	}
}

func TestStaffIdentityMidflightClosureDeniesObservationButRetainsSourceFact(t *testing.T) {
	var h *harness
	var c store.StaffIdentityCommand
	source := staffSource{observe: func(_ context.Context, _ zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffIdentityObservation, error) {
		closeStaffIdentityFixture(t, h, c)
		return zitadel.StaffIdentityObservation{UserID: c.ProviderUserID, OrganizationID: c.ProviderOrgID, State: "active", EmailVerified: true, CreatedAt: c.CreatedAt, ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
	}}
	h = newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
	c = dispatchedStaffIdentity(t, h)
	rec := h.do(t, http.MethodPost, staffIdentityPath(c)+"/observations", productToken, `{"actor_subject":"TEST-admin"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("midflight closure returned active authority: %d", rec.Code)
	}
	var count int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_creations WHERE command_id=$1`, c.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("closure erased late source creation fact: count=%d err=%v", count, err)
	}
}

func TestStaffIdentityMidflightClosureDeniesCreationSuccessButRetainsReceipt(t *testing.T) {
	var h *harness
	var c store.StaffIdentityCommand
	source := staffSource{create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		closeStaffIdentityFixture(t, h, c)
		return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().UTC()}, nil
	}}
	var body string
	h, c, body = staffCreationFixture(t, source)
	rec := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("midflight closure returned creation success: %d", rec.Code)
	}
	receipt := h.do(t, http.MethodGet, staffIdentityPath(c)+"?actor_subject=TEST-admin", productToken, "")
	decoded := decode(t, receipt)
	if receipt.Code != http.StatusOK || decoded["command_state"] != "created" || decoded["recovery_state"] != "closed_unresolved" {
		t.Fatal("late receipt lost creation or independent closure history")
	}
}

func TestStaffIdentitySupersededReceiptDeniesParentAndCreatesReviewedSuccessor(t *testing.T) {
	var creates, observes atomic.Int32
	source := staffSource{create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		creates.Add(1)
		return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().UTC()}, nil
	}, observe: func(context.Context, zitadel.StaffIdentityInput, time.Time) (zitadel.StaffIdentityObservation, error) {
		observes.Add(1)
		return zitadel.StaffIdentityObservation{}, zitadel.ErrIdentityUnavailable
	}}
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
	parent := dispatchedStaffIdentity(t, h)
	requestID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	childID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	scope := store.StaffIdentityRecoveryScope{CommandID: parent.ID, OrgID: parent.OrgID, ProductKey: parent.ProductKey, MachineSubject: parent.MachineSubject, ProviderOrgID: parent.ProviderOrgID, ProviderProjectID: parent.ProviderProjectID}
	request := store.StaffIdentityRecoveryRequest{ID: requestID, RequestedBy: "TEST-platform-maker", Action: "replace", ReplacementActorSubject: "TEST-next-admin", Reason: "Reviewed replacement request", EvidenceReference: "QA-API-SUCCESSOR"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: request.RequestedBy, ProductKey: scope.ProductKey, OrgID: scope.OrgID.String(), Action: "staff.identity.recovery.propose", Target: parent.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: requestID.String()}
	if _, err := h.store.ProposeStaffIdentityRecovery(t.Context(), scope, request, event); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, item := range []struct {
			table, key string
			id         uuid.UUID
		}{
			{"staff_identity_replacements", "successor_command_id", childID},
			{"staff_identity_creations", "command_id", childID},
			{"staff_identity_dispatches", "command_id", childID},
			{"staff_identity_commands", "id", childID},
			{"staff_identity_recovery_decisions", "request_id", requestID},
			{"staff_identity_recovery_requests", "id", requestID},
		} {
			if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+item.table+" WHERE "+item.key+"=$1", item.id); err != nil {
				t.Error(err)
			}
		}
	})
	decision := store.StaffIdentityRecoveryDecision{RequestID: requestID, ReviewedBy: "TEST-platform-reviewer", Decision: "approved", Reason: "Independent reviewed source conflict"}
	event.ActorID = decision.ReviewedBy
	event.Action = "staff.identity.recovery.decide"
	candidate := &store.StaffIdentitySuccessor{ID: childID, ProviderUserID: "TEST-source-successor", BindingNonce: "TEST-source-successor-binding"}
	approved, err := h.store.DecideStaffIdentityRecovery(t.Context(), scope, decision, candidate, event, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := h.do(t, http.MethodGet, staffIdentityPath(parent)+"?actor_subject=TEST-admin", productToken, "")
	got := decode(t, receipt)
	if receipt.Code != http.StatusOK || got["recovery_state"] != "superseded" || got["successor_command_id"] != childID.String() {
		t.Fatal("parent receipt lost reviewed replacement lineage")
	}
	for _, private := range []string{candidate.BindingNonce, candidate.ProviderUserID, request.Reason, decision.Reason, decision.ReviewedBy} {
		if strings.Contains(receipt.Body.String(), private) {
			t.Fatal("parent receipt disclosed private recovery/source facts")
		}
	}
	observed := h.do(t, http.MethodPost, staffIdentityPath(parent)+"/observations", productToken, `{"actor_subject":"TEST-admin"}`)
	if observed.Code != http.StatusConflict || observes.Load() != 0 {
		t.Fatal("superseded parent observation reached provider")
	}
	payload := func(c store.StaffIdentityCommand) string {
		raw, err := json.Marshal(map[string]string{"actor_subject": c.ActorSubject, "email": c.Email, "given_name": c.GivenName, "family_name": c.FamilyName, "reason": c.Reason})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	stale := h.do(t, http.MethodPut, staffIdentityPath(parent), productToken, payload(parent))
	if stale.Code != http.StatusConflict || creates.Load() != 0 {
		t.Fatal("superseded parent creation remained eligible")
	}
	child := *approved.Successor
	for range 2 {
		created := h.do(t, http.MethodPut, staffIdentityPath(child), productToken, payload(child))
		if created.Code != http.StatusOK || decode(t, created)["user_id"] != candidate.ProviderUserID {
			t.Fatalf("normal reviewed successor creation failed: %d", created.Code)
		}
	}
	if creates.Load() != 1 {
		t.Fatal("reviewed successor redispatched")
	}
}
