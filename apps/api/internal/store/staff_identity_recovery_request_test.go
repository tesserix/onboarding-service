package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffRecoveryProposalFreezesInputAcrossMachineRotation(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	// The recovering product credential can differ from the original initiator's.
	if _, err := s.pool.Exec(t.Context(), `UPDATE products SET machine_user_id='TEST-current-machine' WHERE key=$1`, c.ProductKey); err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: "TEST-current-machine", ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryRequest{ID: id, RequestedBy: "TEST-platform-maker", Action: "close_unresolved", Reason: "Investigate uncertain dispatch", EvidenceReference: "QA-RECOVERY-2"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.RequestedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Action: "staff.identity.recovery.propose", Target: c.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	got, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, event)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.CommandID != c.ID || got.OriginalActorSubject != c.ActorSubject || got.OriginalUserID != c.ProviderUserID || got.CreatedAt.IsZero() {
		t.Fatal("proposal lost original command identity or recording time")
	}
	replay, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, event)
	if err != nil || replay != got {
		t.Fatalf("exact proposal retry changed retained facts: %v", err)
	}
	for _, change := range []func(*StaffIdentityRecoveryRequest){
		func(r *StaffIdentityRecoveryRequest) { r.RequestedBy = "TEST-other-maker" },
		func(r *StaffIdentityRecoveryRequest) { r.Reason = "Changed reason" },
		func(r *StaffIdentityRecoveryRequest) { r.EvidenceReference = "QA-DIFFERENT" },
		func(r *StaffIdentityRecoveryRequest) {
			r.Action = "replace"
			r.ReplacementActorSubject = "TEST-hms-admin"
		},
	} {
		changed := in
		change(&changed)
		changedEvent := event
		changedEvent.ActorID = changed.RequestedBy
		if _, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, changed, changedEvent); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("changed proposal accepted: %v", err)
		}
	}
	if err := s.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, event); !errors.Is(err, ErrNotFound) {
		t.Fatalf("withdrawn proposal replay returned private facts: %v", err)
	}
}

func TestStaffRecoveryProposalAuditFailurePersistsNoRequest(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryRequest{ID: id, RequestedBy: "TEST-platform-maker", Action: "close_unresolved", Reason: "Investigate uncertain dispatch", EvidenceReference: "QA-RECOVERY-3"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.RequestedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Action: "staff.identity.recovery.propose", Target: c.ID.String(), Outcome: "INVALID", RequestID: id.String()}
	got, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, event)
	if err == nil || got.ID != uuid.Nil {
		t.Fatal("failed audit released proposal")
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_requests WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed audit retained request: count=%d err=%v", count, err)
	}
}

func TestStaffRecoveryProposalRejectsForeignAndChangedScope(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryRequest{ID: id, RequestedBy: "TEST-platform-maker", Action: "close_unresolved", Reason: "Investigate uncertain dispatch", EvidenceReference: "QA-RECOVERY-4"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.RequestedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Action: "staff.identity.recovery.propose", Target: c.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	for _, tc := range []struct {
		change func(*StaffIdentityRecoveryScope)
		want   error
	}{
		{func(s *StaffIdentityRecoveryScope) { s.CommandID = uuid.New() }, ErrNotFound},
		{func(s *StaffIdentityRecoveryScope) { s.OrgID = uuid.New() }, ErrNotFound},
		{func(s *StaffIdentityRecoveryScope) { s.ProductKey = "foreign-product" }, ErrNotFound},
		{func(s *StaffIdentityRecoveryScope) { s.MachineSubject = "TEST-foreign-machine" }, ErrNotFound},
		{func(s *StaffIdentityRecoveryScope) { s.ProviderOrgID = "TEST-changed-source" }, ErrConflict},
		{func(s *StaffIdentityRecoveryScope) { s.ProviderProjectID = "TEST-changed-project" }, ErrConflict},
	} {
		changed := scope
		tc.change(&changed)
		got, err := s.ProposeStaffIdentityRecovery(t.Context(), changed, in, event)
		if !errors.Is(err, tc.want) || got.ID != uuid.Nil {
			t.Fatalf("foreign/changed scope released proposal: %v", err)
		}
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_requests WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied scope persisted request: count=%d err=%v", count, err)
	}
	if _, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, event); err != nil {
		t.Fatal(err)
	}
	event.Outcome = "INVALID"
	got, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, event)
	if err == nil || got.ID != uuid.Nil {
		t.Fatal("failed replay audit disclosed existing private request")
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_requests WHERE id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed replay audit changed retained request: count=%d err=%v", count, err)
	}
}

func TestStaffRecoveryProposalRequiresMatchingPlatformAudit(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryRequest{ID: id, RequestedBy: "TEST-platform-maker", Action: "close_unresolved", Reason: "Investigate uncertain dispatch", EvidenceReference: "QA-RECOVERY-5"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.RequestedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Action: "staff.identity.recovery.propose", Target: c.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	for _, change := range []func(*audit.Event){
		func(e *audit.Event) { e.ActorType = audit.ActorProduct },
		func(e *audit.Event) { e.ActorID = "TEST-different-operator" },
		func(e *audit.Event) { e.ProductKey = "foreign-product" },
		func(e *audit.Event) { e.OrgID = uuid.NewString() },
		func(e *audit.Event) { e.Target = uuid.NewString() },
		func(e *audit.Event) { e.Action = "staff.identity.other" },
	} {
		changed := event
		change(&changed)
		got, err := s.ProposeStaffIdentityRecovery(t.Context(), scope, in, changed)
		if !errors.Is(err, ErrConflict) || got.ID != uuid.Nil {
			t.Fatalf("misattributed recovery audit accepted: %v", err)
		}
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_requests WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("misattributed audit retained request: count=%d err=%v", count, err)
	}
}
