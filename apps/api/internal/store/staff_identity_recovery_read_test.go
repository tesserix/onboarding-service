package store

import (
	"errors"
	"github.com/google/uuid"
	"testing"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffRecoveryReadRetainsOriginalFactsAcrossMachineRotation(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	if _, err := s.pool.Exec(t.Context(), `UPDATE products SET machine_user_id='TEST-current-machine' WHERE key=$1`, c.ProductKey); err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: "TEST-current-machine", ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-current-operator", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.read", Outcome: audit.OutcomeSuccess}
	op, err := s.ReadStaffIdentityRecovery(t.Context(), scope, event.ActorID, event)
	if err != nil || op.Command != c || op.DispatchedAt == nil || op.Creation != nil || op.RecoveryState != "" {
		t.Fatalf("original facts changed or missing: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE target=$1 AND actor_id=$2 AND action=$3`, c.ID.String(), event.ActorID, event.Action).Scan(&count); err != nil || count != 1 {
		t.Fatalf("disclosure audit not retained: count=%d err=%v", count, err)
	}
	if err := s.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadStaffIdentityRecovery(t.Context(), scope, event.ActorID, event)
	if !errors.Is(err, ErrNotFound) || got.Command.ID != uuid.Nil {
		t.Fatalf("withdrawn scope returned recovery facts: %v", err)
	}
}

func TestStaffRecoveryReadRejectsChangedScopeAndUnauditedDisclosure(t *testing.T) {
	for _, kind := range []string{"operator", "actor type", "actor binding", "tenant audit", "product audit", "target audit", "action", "outcome", "foreign tenant", "foreign command", "foreign product", "foreign machine", "source org", "source project", "audit failure"} {
		t.Run(kind, func(t *testing.T) {
			s, c := staffRecoveryFixture(t)
			scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
			operator := "TEST-current-operator"
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: operator, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.read", Outcome: audit.OutcomeSuccess, RequestID: c.ID.String()}
			switch kind {
			case "operator":
				operator = ""
			case "actor type":
				event.ActorType = audit.ActorProduct
			case "actor binding":
				event.ActorID = "TEST-different-operator"
			case "tenant audit":
				event.OrgID = uuid.NewString()
			case "product audit":
				event.ProductKey = "foreign"
			case "target audit":
				event.Target = uuid.NewString()
			case "action":
				event.Action = "staff.identity.read"
			case "outcome":
				event.Outcome = audit.OutcomeDenied
			case "foreign tenant":
				scope.OrgID = uuid.New()
				event.OrgID = scope.OrgID.String()
			case "foreign command":
				scope.CommandID = uuid.Must(uuid.NewV7())
				event.Target = scope.CommandID.String()
			case "foreign product":
				scope.ProductKey = "foreign"
				event.ProductKey = scope.ProductKey
			case "foreign machine":
				scope.MachineSubject = "TEST-foreign-machine"
			case "source org":
				scope.ProviderOrgID = "TEST-foreign-org"
			case "source project":
				scope.ProviderProjectID = "TEST-foreign-project"
			case "audit failure":
				event.Detail = "{"
			}
			got, err := s.ReadStaffIdentityRecovery(t.Context(), scope, operator, event)
			if err == nil || got.Command.ID != uuid.Nil || got.Command.Email != "" || got.Creation != nil {
				t.Fatalf("invalid disclosure released state: %v", err)
			}
			var count int
			if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='staff.identity.recovery.read'`, c.ID.String()).Scan(&count); err != nil || count != 0 {
				t.Fatalf("denied disclosure retained success audit: count=%d err=%v", count, err)
			}
		})
	}
}
