package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffRecoveryCommandsRejectNonSuccessAuditOutcome(t *testing.T) {
	for _, command := range []string{"propose", "decide"} {
		t.Run(command, func(t *testing.T) {
			s, c := staffRecoveryFixture(t)
			scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
			id := uuid.Must(uuid.NewV7())
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-maker", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.propose", Outcome: audit.OutcomeDenied}
			var err error
			if command == "propose" {
				_, err = s.ProposeStaffIdentityRecovery(t.Context(), scope, StaffIdentityRecoveryRequest{ID: id, RequestedBy: event.ActorID, Action: "close_unresolved", Reason: "Investigated uncertain dispatch", EvidenceReference: "QA:RECOVERY/AUDIT-1"}, event)
			} else {
				id = recoveryRequest(t, s, c, "close_unresolved", "")
				event.ActorID = "TEST-independent-reviewer"
				event.Action = "staff.identity.recovery.decide"
				_, err = s.DecideStaffIdentityRecovery(t.Context(), scope, StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: event.ActorID, Decision: "approved", Reason: "Investigated uncertain source"}, nil, event)
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("command committed with denied audit outcome: %v", err)
			}
			var count int
			query := `SELECT count(*) FROM staff_identity_recovery_requests WHERE id=$1`
			if command == "decide" {
				query = `SELECT count(*) FROM staff_identity_recovery_decisions WHERE request_id=$1`
			}
			if err := s.pool.QueryRow(t.Context(), query, id).Scan(&count); err != nil || count != 0 {
				t.Fatalf("denied audit retained successful command: %d %v", count, err)
			}
		})
	}
}
