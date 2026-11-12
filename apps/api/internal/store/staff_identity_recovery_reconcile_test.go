package store

import (
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffRecoveryReconciliationRetainsOriginalFactsAcrossMachineRotation(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	if _, err := s.pool.Exec(t.Context(), `UPDATE products SET machine_user_id='TEST-current-machine' WHERE key=$1`, c.ProductKey); err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: "TEST-current-machine", ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	actor := "TEST-current-operator"
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: actor, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.reconcile", Outcome: audit.OutcomeSuccess}
	born := time.Now().UTC().Add(time.Nanosecond)
	first, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, actor, born, event)
	if err != nil || first.CommandID != c.ID || first.ProviderUserID != c.ProviderUserID || !first.ProviderCreatedAt.Equal(born) || first.RecordedAt.IsZero() {
		t.Fatalf("reconciliation lost original account/birth: %v", err)
	}
	second, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, actor, born, event)
	if err != nil || second != first {
		t.Fatalf("reconciliation retry changed original receipt: %v", err)
	}
	read := event
	read.Action = "staff.identity.recovery.read"
	op, err := s.ReadStaffIdentityRecovery(t.Context(), scope, actor, read)
	if err != nil || op.Command != c || op.Creation == nil || op.Creation.ProviderUserID != c.ProviderUserID || op.DispatchedAt == nil {
		t.Fatalf("reconciliation rewrote historical command: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE target=$1 AND action=$2 AND actor_id=$3`, c.ID.String(), event.Action, actor).Scan(&count); err != nil || count != 2 {
		t.Fatalf("reconciliation/retry disclosure not audited: %d %v", count, err)
	}
}

func TestStaffRecoveryReconciliationRejectsUnboundScopeAndUnauditedWrites(t *testing.T) {
	for _, kind := range []string{"empty actor", "wrong actor", "wrong type", "wrong action", "wrong outcome", "wrong target", "wrong audit tenant", "wrong audit product", "foreign command", "foreign tenant", "wrong machine", "wrong source org", "wrong source project", "zero birth", "audit failure", "withdrawn entitlement"} {
		t.Run(kind, func(t *testing.T) {
			s, c := staffRecoveryFixture(t)
			scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
			actor := "TEST-operator"
			born := time.Now().UTC()
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: actor, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.reconcile", Outcome: audit.OutcomeSuccess}
			switch kind {
			case "empty actor":
				actor = ""
			case "wrong actor":
				event.ActorID = "TEST-foreign"
			case "wrong type":
				event.ActorType = audit.ActorProduct
			case "wrong action":
				event.Action = "staff.identity.created"
			case "wrong outcome":
				event.Outcome = audit.OutcomeDenied
			case "wrong target":
				event.Target = uuid.NewString()
			case "wrong audit tenant":
				event.OrgID = uuid.NewString()
			case "wrong audit product":
				event.ProductKey = "foreign"
			case "foreign command":
				scope.CommandID = uuid.Must(uuid.NewV7())
				event.Target = scope.CommandID.String()
			case "foreign tenant":
				scope.OrgID = uuid.New()
				event.OrgID = scope.OrgID.String()
			case "wrong machine":
				scope.MachineSubject = "TEST-foreign"
			case "wrong source org":
				scope.ProviderOrgID = "TEST-foreign"
			case "wrong source project":
				scope.ProviderProjectID = "TEST-foreign"
			case "zero birth":
				born = time.Time{}
			case "audit failure":
				event.Detail = "{"
			case "withdrawn entitlement":
				if err := s.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, actor, born, event)
			if err == nil || got.CommandID != uuid.Nil || got.ProviderUserID != "" {
				t.Fatalf("unauthorized reconciliation retained facts: %v", err)
			}
			var count int
			if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_creations WHERE command_id=$1`, c.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed reconciliation retained receipt: %d %v", count, err)
			}
		})
	}
}

func TestStaffRecoveryReconciliationPreservesReceiptOnFailedReplayAndChangedBirth(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-operator", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.reconcile", Outcome: audit.OutcomeSuccess}
	born := time.Now().UTC()
	first, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, event.ActorID, born, event)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, event.ActorID, born.Add(time.Nanosecond), event); !errors.Is(err, ErrIdempotencyConflict) || got.CommandID != uuid.Nil {
		t.Fatalf("changed frozen birth accepted: %v", err)
	}
	bad := event
	bad.Detail = "{"
	if got, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, event.ActorID, born, bad); err == nil || got.CommandID != uuid.Nil {
		t.Fatal("failed replay audit released receipt")
	}
	got, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, event.ActorID, born, event)
	if err != nil || got != first {
		t.Fatalf("failed replay rewrote original receipt: %v", err)
	}
}

func TestStaffRecoveryReconciliationCannotRestoreClosedCommandEligibility(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "close_unresolved", "")
	if err := recoveryDecision(t.Context(), s, c, id, "close_unresolved", "", "TEST-independent-reviewer", "approved"); err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-operator", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.reconcile", Outcome: audit.OutcomeSuccess}
	if _, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, event.ActorID, time.Now().UTC(), event); err != nil {
		t.Fatal(err)
	}
	read := event
	read.Action = "staff.identity.recovery.read"
	op, err := s.ReadStaffIdentityRecovery(t.Context(), scope, event.ActorID, read)
	if err != nil || op.RecoveryState != "closed_unresolved" || op.Creation == nil || op.Command != c {
		t.Fatalf("reconciliation changed closed authority: %v", err)
	}
	dispatch := event
	dispatch.ActorType = audit.ActorProduct
	dispatch.ActorID = c.MachineSubject
	dispatch.Action = "staff.identity.dispatch"
	if claimed, err := s.ClaimStaffIdentityDispatch(t.Context(), c, dispatch); claimed || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reconciliation reopened closed dispatch: claimed=%t err=%v", claimed, err)
	}
}
