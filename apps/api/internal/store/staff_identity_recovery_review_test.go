package store

import (
	"errors"
	"github.com/google/uuid"
	"testing"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffRecoveryReviewRetainsRequestAndCurrentImmutableDecision(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryRequest{ID: uuid.Must(uuid.NewV7()), RequestedBy: "TEST-recovery-maker", Action: "close_unresolved", Reason: "Investigated uncertain dispatch", EvidenceReference: "QA:RECOVERY/REVIEW-1"}
	propose := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.RequestedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.propose", Outcome: audit.OutcomeSuccess}
	request, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, propose)
	if err != nil {
		t.Fatal(err)
	}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-current-reader", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: request.ID.String(), Action: "staff.identity.recovery.request.read", Outcome: audit.OutcomeSuccess}
	first, err := s.ReadStaffIdentityRecoveryRequest(t.Context(), scope, request.ID, event.ActorID, event)
	if err != nil || first.Request != request || first.Decision != nil || first.SuccessorCommandID != nil {
		t.Fatalf("undecided recovery request facts changed: %v", err)
	}
	decision := StaffIdentityRecoveryDecision{RequestID: request.ID, ReviewedBy: "TEST-independent-reviewer", Decision: "rejected", Reason: "Investigation does not justify replacement"}
	decideEvent := event
	decideEvent.ActorID = decision.ReviewedBy
	decideEvent.Target = c.ID.String()
	decideEvent.Action = "staff.identity.recovery.decide"
	result, err := s.DecideStaffIdentityRecovery(t.Context(), scope, decision, nil, decideEvent)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ReadStaffIdentityRecoveryRequest(t.Context(), scope, request.ID, event.ActorID, event)
	if err != nil || after.Request != request || after.Decision == nil || *after.Decision != result.Decision || after.SuccessorCommandID != nil {
		t.Fatalf("review read lost immutable final rejection: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE target=$1 AND actor_id=$2 AND action=$3`, request.ID.String(), event.ActorID, event.Action).Scan(&count); err != nil || count != 2 {
		t.Fatalf("exact request disclosures unaudited: %d %v", count, err)
	}
}

func TestStaffRecoveryReviewRequiresCompleteApprovedReplacementLineage(t *testing.T) {
	for _, bare := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "bare approval"}[bare], func(t *testing.T) {
			s, c := staffRecoveryFixture(t)
			id := recoveryRequest(t, s, c, "replace", "TEST-next-initiator")
			scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-reader", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: id.String(), Action: "staff.identity.recovery.request.read", Outcome: audit.OutcomeSuccess}
			child := uuid.Must(uuid.NewV7())
			if bare {
				if err := recoveryDecision(t.Context(), s, c, id, "replace", "TEST-next-initiator", "TEST-independent-reviewer", "approved"); err != nil {
					t.Fatal(err)
				}
			} else {
				trackRecoverySuccessors(t, s, child)
				decide := event
				decide.ActorID = "TEST-independent-reviewer"
				decide.Target = c.ID.String()
				decide.Action = "staff.identity.recovery.decide"
				if _, err := s.DecideStaffIdentityRecovery(t.Context(), scope, StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: decide.ActorID, Decision: "approved", Reason: "Investigated source absence"}, &StaffIdentitySuccessor{ID: child, ProviderUserID: "TEST-next-" + child.String(), BindingNonce: "next-nonce-" + child.String()}, decide); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.ReadStaffIdentityRecoveryRequest(t.Context(), scope, id, event.ActorID, event)
			if bare {
				if !errors.Is(err, ErrConflict) || got.Request.ID != uuid.Nil {
					t.Fatalf("incomplete approval released review facts: %v", err)
				}
			} else if err != nil || got.Request.ID != id || got.Decision == nil || got.Decision.Decision != "approved" || got.SuccessorCommandID == nil || *got.SuccessorCommandID != child {
				t.Fatalf("complete approval lost retained lineage: %v", err)
			}
		})
	}
}

func TestStaffRecoveryReviewRejectsForeignOrUnauditedRequestDisclosure(t *testing.T) {
	for _, kind := range []string{"missing request", "foreign command", "foreign tenant", "wrong machine", "wrong source", "empty operator", "wrong actor", "wrong type", "wrong target", "wrong action", "wrong outcome", "audit failure", "revoked entitlement"} {
		t.Run(kind, func(t *testing.T) {
			s, c := staffRecoveryFixture(t)
			id := recoveryRequest(t, s, c, "close_unresolved", "")
			scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
			operator := "TEST-reader"
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: operator, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: id.String(), Action: "staff.identity.recovery.request.read", Outcome: audit.OutcomeSuccess, RequestID: c.ID.String()}
			switch kind {
			case "missing request":
				id = uuid.Must(uuid.NewV7())
				event.Target = id.String()
			case "foreign command":
				scope.CommandID = uuid.Must(uuid.NewV7())
			case "foreign tenant":
				scope.OrgID = uuid.New()
				event.OrgID = scope.OrgID.String()
			case "wrong machine":
				scope.MachineSubject = "TEST-foreign-machine"
			case "wrong source":
				scope.ProviderOrgID = "TEST-foreign-org"
			case "empty operator":
				operator = ""
			case "wrong actor":
				event.ActorID = "TEST-foreign-actor"
			case "wrong type":
				event.ActorType = audit.ActorProduct
			case "wrong target":
				event.Target = c.ID.String()
			case "wrong action":
				event.Action = "staff.identity.recovery.read"
			case "wrong outcome":
				event.Outcome = audit.OutcomeDenied
			case "audit failure":
				event.Detail = "{"
			case "revoked entitlement":
				if err := s.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.ReadStaffIdentityRecoveryRequest(t.Context(), scope, id, operator, event)
			if err == nil || got.Request.ID != uuid.Nil || got.Request.Reason != "" || got.Decision != nil {
				t.Fatalf("invalid review disclosure returned facts: %v", err)
			}
			var count int
			if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='staff.identity.recovery.request.read'`, c.ID.String()).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed disclosure retained success audit: %d %v", count, err)
			}
		})
	}
}
