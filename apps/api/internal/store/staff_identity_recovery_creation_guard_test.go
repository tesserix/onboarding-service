package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffRecoveryDecisionRejectsCreationArrivingAfterAbsenceInspection(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "replace", "TEST-next-initiator")
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	born := time.Now().UTC()
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-operator", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.reconcile", Outcome: audit.OutcomeSuccess}
	if _, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, event.ActorID, born, event); err != nil {
		t.Fatal(err)
	}
	child := uuid.Must(uuid.NewV7())
	trackRecoverySuccessors(t, s, child)
	event.ActorID = "TEST-independent-reviewer"
	event.Action = "staff.identity.recovery.decide"
	got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: event.ActorID, Decision: "approved", Reason: "Previously inspected absent account"}, &StaffIdentitySuccessor{ID: child, ProviderUserID: "TEST-child-" + child.String(), BindingNonce: "child-nonce-" + child.String()}, event, nil)
	if !errors.Is(err, ErrConflict) || got.Successor != nil {
		t.Fatalf("stale absence approved over retained creation: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_decisions WHERE request_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale approval remained committed: %d %v", count, err)
	}
}

func TestStaffRecoveryDecisionRequiresExactConfirmedCreationAndFreezesReplay(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "replace", "TEST-next-initiator")
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	born := time.Now().UTC()
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-operator", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.reconcile", Outcome: audit.OutcomeSuccess}
	if _, err := s.ReconcileStaffIdentityCreation(t.Context(), scope, event.ActorID, born, event); err != nil {
		t.Fatal(err)
	}
	child := uuid.Must(uuid.NewV7())
	trackRecoverySuccessors(t, s, child)
	event.ActorID = "TEST-independent-reviewer"
	event.Action = "staff.identity.recovery.decide"
	in := StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: event.ActorID, Decision: "approved", Reason: "Fresh source proof confirms removed owned account"}
	next := &StaffIdentitySuccessor{ID: child, ProviderUserID: "TEST-child-" + child.String(), BindingNonce: "child-nonce-" + child.String()}
	wrong := born.Add(time.Nanosecond)
	if _, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, next, event, &wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong confirmed birth accepted: %v", err)
	}
	first, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, next, event, &born)
	if err != nil || first.Successor == nil || first.Successor.ID != child {
		t.Fatalf("exact confirmed historical creation rejected: %v", err)
	}
	replay, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, next, event, nil)
	if err != nil || replay.Successor == nil || *replay.Successor != *first.Successor || replay.Decision != first.Decision {
		t.Fatalf("fresh precondition re-executed frozen approval: %v", err)
	}
}

func TestStaffRecoveryDecisionAndLateCreationAdmissionSerialize(t *testing.T) {
	for i := 0; i < 12; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, c := staffRecoveryFixture(t)
			id := recoveryRequest(t, s, c, "replace", "TEST-next-initiator")
			child := uuid.Must(uuid.NewV7())
			trackRecoverySuccessors(t, s, child)
			scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
			event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "TEST-independent-reviewer", ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.recovery.decide", Outcome: audit.OutcomeSuccess}
			in := StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: event.ActorID, Decision: "approved", Reason: "Fresh absent-source inspection"}
			next := &StaffIdentitySuccessor{ID: child, ProviderUserID: "TEST-child-" + child.String(), BindingNonce: "child-nonce-" + child.String()}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := make(chan struct{})
			recorded := make(chan error, 1)
			decided := make(chan error, 1)
			go func() {
				<-start
				created := event
				created.ActorType = audit.ActorProduct
				created.ActorID = c.MachineSubject
				created.Action = "staff.identity.created"
				_, err := s.RecordStaffIdentityCreation(ctx, c, time.Now().UTC(), created)
				recorded <- err
			}()
			go func() {
				<-start
				_, err := s.DecideStaffIdentityRecovery(ctx, scope, in, next, event, nil)
				decided <- err
			}()
			close(start)
			recordErr, decisionErr := <-recorded, <-decided
			if recordErr != nil || decisionErr != nil && !errors.Is(decisionErr, ErrConflict) {
				t.Fatalf("admission failed: record=%v decision=%v", recordErr, decisionErr)
			}
			var recordSeq, decisionSeq int64
			if err := s.pool.QueryRow(t.Context(), `SELECT coalesce(max(seq) FILTER(WHERE action='staff.identity.created'),0),coalesce(max(seq) FILTER(WHERE action='staff.identity.recovery.decide'),0) FROM audit_events WHERE target=$1`, c.ID.String()).Scan(&recordSeq, &decisionSeq); err != nil {
				t.Fatal(err)
			}
			if recordSeq == 0 || decisionErr == nil && (decisionSeq == 0 || recordSeq < decisionSeq) || decisionErr != nil && decisionSeq != 0 {
				t.Fatalf("approval crossed a committed earlier creation: record=%d decision=%d err=%v", recordSeq, decisionSeq, decisionErr)
			}
		})
	}
}
