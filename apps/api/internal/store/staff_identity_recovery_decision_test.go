package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffRecoveryApprovedClosureRetainsDecisionAndOriginalDispatch(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "close_unresolved", "")
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: "TEST-independent-reviewer", Decision: "approved", Reason: "Source investigation remains unresolved"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.ReviewedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Action: "staff.identity.recovery.decide", Target: c.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, nil, event)
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision.RequestID != id || got.Decision.ReviewedBy != in.ReviewedBy || got.Decision.Decision != "approved" || got.Decision.Reason != in.Reason || got.Decision.CreatedAt.IsZero() || got.Successor != nil {
		t.Fatal("closure lost exact decision or prepared an unwanted successor")
	}
	var dispatches, roots int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_dispatches WHERE command_id=$1`, c.ID).Scan(&dispatches); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_contact_claims WHERE root_command_id=$1`, c.ID).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if dispatches != 1 || roots != 1 {
		t.Fatalf("closure erased original facts: dispatches=%d roots=%d", dispatches, roots)
	}
}

func TestStaffRecoveryReplacementPreparesExactSuccessorWithoutDispatch(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	requestID := recoveryRequest(t, s, c, "replace", "TEST-replacement-initiator")
	nextID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	trackRecoverySuccessors(t, s, nextID)
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryDecision{RequestID: requestID, ReviewedBy: "TEST-independent-reviewer", Decision: "approved", Reason: "Reviewed source conflict evidence"}
	candidate := &StaffIdentitySuccessor{ID: nextID, ProviderUserID: "TEST-replacement-" + nextID.String(), BindingNonce: "replacement-nonce-" + nextID.String()}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.ReviewedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Action: "staff.identity.recovery.decide", Target: c.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: requestID.String()}
	got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err != nil {
		t.Fatal(err)
	}
	if got.Successor == nil {
		t.Fatal("approval omitted its atomically prepared successor")
	}
	child := *got.Successor
	if child.ID != nextID || child.ProviderUserID != candidate.ProviderUserID || child.BindingNonce != candidate.BindingNonce || child.OrgID != c.OrgID || child.ProductKey != c.ProductKey || child.MachineSubject != c.MachineSubject || child.ActorSubject != "TEST-replacement-initiator" || child.ProviderOrgID != c.ProviderOrgID || child.ProviderProjectID != c.ProviderProjectID || child.Email != c.Email || child.GivenName != c.GivenName || child.FamilyName != c.FamilyName || child.Reason != "Investigated ambiguous dispatch" || child.CreatedAt.IsZero() {
		t.Fatal("successor changed the frozen source, identity or review bindings")
	}
	var root, parent uuid.UUID
	var generation int
	if err := s.pool.QueryRow(t.Context(), `SELECT root_command_id,parent_command_id,generation FROM staff_identity_replacements WHERE successor_command_id=$1`, nextID).Scan(&root, &parent, &generation); err != nil {
		t.Fatal(err)
	}
	if root != c.ID || parent != c.ID || generation != 1 {
		t.Fatal("successor lost permanent root lineage")
	}
	var dispatched, ownRoot int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_dispatches WHERE command_id=$1`, nextID).Scan(&dispatched); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_contact_claims WHERE root_command_id=$1`, nextID).Scan(&ownRoot); err != nil {
		t.Fatal(err)
	}
	if dispatched != 0 || ownRoot != 0 {
		t.Fatal("approval dispatched an identity or replaced its permanent contact root")
	}
}

func trackRecoverySuccessors(t *testing.T, s *Store, ids ...uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		for i := len(ids) - 1; i >= 0; i-- {
			id := ids[i]
			for _, table := range []string{"staff_identity_replacements", "staff_identity_recovery_decisions", "staff_identity_recovery_requests", "staff_identity_creations", "staff_identity_dispatches", "staff_identity_commands"} {
				key := "command_id"
				if table == "staff_identity_replacements" {
					key = "successor_command_id"
				}
				if table == "staff_identity_commands" {
					key = "id"
				}
				if _, err := s.pool.Exec(context.Background(), "DELETE FROM "+table+" WHERE "+key+"=$1", id); err != nil {
					t.Error(err)
				}
			}
		}
	})
}

func staffRecoveryDecisionFixture(t *testing.T, action string) (*Store, StaffIdentityCommand, StaffIdentityRecoveryScope, StaffIdentityRecoveryDecision, *StaffIdentitySuccessor, audit.Event) {
	t.Helper()
	s, c := staffRecoveryFixture(t)
	actor := ""
	if action == "replace" {
		actor = "TEST-replacement-initiator"
	}
	id := recoveryRequest(t, s, c, action, actor)
	scope := StaffIdentityRecoveryScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	in := StaffIdentityRecoveryDecision{RequestID: id, ReviewedBy: "TEST-independent-reviewer", Decision: "approved", Reason: "Reviewed source conflict evidence"}
	var successor *StaffIdentitySuccessor
	if action == "replace" {
		next, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		trackRecoverySuccessors(t, s, next)
		successor = &StaffIdentitySuccessor{ID: next, ProviderUserID: "TEST-replacement-" + next.String(), BindingNonce: "replacement-nonce-" + next.String()}
	}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: in.ReviewedBy, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Action: "staff.identity.recovery.decide", Target: c.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	return s, c, scope, in, successor, event
}

func TestStaffRecoverySuccessorDispatchRequiresExactApprovedLineage(t *testing.T) {
	s, parent, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
	got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err != nil {
		t.Fatal(err)
	}
	child := *got.Successor
	productEvent := audit.Event{ActorType: audit.ActorProduct, ActorID: child.MachineSubject, ProductKey: child.ProductKey, OrgID: child.OrgID.String(), Action: "staff.identity.prepare", Target: child.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: child.ID.String()}
	prepared, err := s.PrepareStaffIdentity(t.Context(), child, productEvent)
	if err != nil || prepared.ID != child.ID {
		t.Fatalf("approved successor cannot use normal preparation: %v", err)
	}
	productEvent.Action = "staff.identity.dispatch"
	claimed, err := s.ClaimStaffIdentityDispatch(t.Context(), child, productEvent)
	if err != nil || !claimed {
		t.Fatalf("approved successor dispatch: claimed=%v err=%v", claimed, err)
	}
	claimed, err = s.ClaimStaffIdentityDispatch(t.Context(), child, productEvent)
	if err != nil || claimed {
		t.Fatalf("successor redispatched: claimed=%v err=%v", claimed, err)
	}
	productEvent.Target = parent.ID.String()
	claimed, err = s.ClaimStaffIdentityDispatch(t.Context(), parent, productEvent)
	if err == nil || claimed {
		t.Fatalf("superseded parent remains eligible: claimed=%v err=%v", claimed, err)
	}
}

func TestStaffRecoveryReplacementRetryFreezesDecisionAndSuccessor(t *testing.T) {
	s, _, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
	first, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err != nil {
		t.Fatal(err)
	}
	regenerated := *candidate
	regenerated.ProviderUserID = "TEST-fresh-server-user"
	regenerated.BindingNonce = "TEST-fresh-server-nonce"
	replay, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, &regenerated, event)
	if err != nil || replay.Decision != first.Decision || replay.Successor == nil || *replay.Successor != *first.Successor {
		t.Fatalf("exact retry changed frozen decision/successor: %v", err)
	}
	changed := in
	changed.Reason = "Different decision reason"
	if got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, changed, candidate, event); !errors.Is(err, ErrIdempotencyConflict) || got.Successor != nil {
		t.Fatalf("changed reason accepted: %v", err)
	}
	changed = in
	changed.ReviewedBy = "TEST-other-reviewer"
	changedEvent := event
	changedEvent.ActorID = changed.ReviewedBy
	if _, err := s.DecideStaffIdentityRecovery(t.Context(), scope, changed, candidate, changedEvent); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed reviewer accepted: %v", err)
	}
	wrongChild := *candidate
	wrongChild.ID = uuid.New()
	if _, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, &wrongChild, event); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed successor accepted: %v", err)
	}
	event.Outcome = "INVALID"
	got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err == nil || got.Successor != nil || got.Decision.RequestID != uuid.Nil {
		t.Fatal("failed replay audit disclosed private recovery result")
	}
}

func TestStaffRecoveryDecisionAuditFailureRollsBackAllReplacementFacts(t *testing.T) {
	s, _, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
	event.Outcome = "INVALID"
	got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err == nil || got.Successor != nil || got.Decision.RequestID != uuid.Nil {
		t.Fatal("failed audit released replacement")
	}
	var decisions, children, lineage int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_decisions WHERE request_id=$1`, in.RequestID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_commands WHERE id=$1`, candidate.ID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_replacements WHERE request_id=$1`, in.RequestID).Scan(&lineage); err != nil {
		t.Fatal(err)
	}
	if decisions != 0 || children != 0 || lineage != 0 {
		t.Fatalf("partial recovery persisted: decisions=%d commands=%d lineage=%d", decisions, children, lineage)
	}
	event.Outcome = audit.OutcomeSuccess
	if _, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event); err != nil {
		t.Fatalf("rolled-back decision cannot retry: %v", err)
	}
}

func TestStaffRecoveryRejectsInterestedReviewersAndFinalRejection(t *testing.T) {
	s, c, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
	for _, reviewer := range []string{"TEST-recovery-maker", c.ActorSubject, c.ProviderUserID, "TEST-replacement-initiator"} {
		bad := in
		bad.ReviewedBy = reviewer
		badEvent := event
		badEvent.ActorID = reviewer
		if got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, bad, candidate, badEvent); !errors.Is(err, ErrConflict) || got.Successor != nil {
			t.Fatalf("interested reviewer accepted: %v", err)
		}
	}
	in.Decision = "rejected"
	rejected, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, nil, event)
	if err != nil || rejected.Successor != nil {
		t.Fatalf("rejection prepared successor: %v", err)
	}
	in.Decision = "approved"
	if _, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("rejected request approved: %v", err)
	}
}

func TestStaffRecoveryBareApprovalDoesNotAuthorizeReplacement(t *testing.T) {
	s, c, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
	in.Reason = "Reviewed source evidence"
	if err := recoveryDecision(t.Context(), s, c, in.RequestID, "replace", "TEST-replacement-initiator", in.ReviewedBy, "approved"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event); !errors.Is(err, ErrConflict) || got.Successor != nil {
		t.Fatalf("non-atomic approval materialized successor: %v", err)
	}
}

func TestStaffRecoveryReplacementLineageSupportsSecondGeneration(t *testing.T) {
	s, root, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
	first, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err != nil {
		t.Fatal(err)
	}
	child := *first.Successor
	childEvent := audit.Event{ActorType: audit.ActorProduct, ActorID: child.MachineSubject, ProductKey: child.ProductKey, OrgID: child.OrgID.String(), Action: "staff.identity.dispatch", Target: child.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: child.ID.String()}
	if claimed, err := s.ClaimStaffIdentityDispatch(t.Context(), child, childEvent); err != nil || !claimed {
		t.Fatalf("first successor dispatch: %v", err)
	}
	nextID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	trackRecoverySuccessors(t, s, nextID)
	next := &StaffIdentitySuccessor{ID: nextID, ProviderUserID: "TEST-second-" + nextID.String(), BindingNonce: "second-nonce-" + nextID.String()}
	scope.CommandID = child.ID
	in.RequestID = recoveryRequest(t, s, child, "replace", "TEST-second-initiator")
	event.Target = child.ID.String()
	event.RequestID = in.RequestID.String()
	if _, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, next, event); err != nil {
		t.Fatal(err)
	}
	var retainedRoot, parent uuid.UUID
	var generation int
	if err := s.pool.QueryRow(t.Context(), `SELECT root_command_id,parent_command_id,generation FROM staff_identity_replacements WHERE successor_command_id=$1`, nextID).Scan(&retainedRoot, &parent, &generation); err != nil {
		t.Fatal(err)
	}
	if retainedRoot != root.ID || parent != child.ID || generation != 2 {
		t.Fatal("second generation changed permanent contact root or parent")
	}
}

func TestStaffRecoveryConcurrentDecisionTransactionsHaveOneSuccessor(t *testing.T) {
	s, c, scope, _, _, event := staffRecoveryDecisionFixture(t, "close_unresolved")
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		id, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		trackRecoverySuccessors(t, s, id)
		candidate := &StaffIdentitySuccessor{ID: id, ProviderUserID: "TEST-race-" + id.String(), BindingNonce: "race-nonce-" + id.String()}
		request := recoveryRequest(t, s, c, "replace", "TEST-replacement-initiator")
		wg.Go(func() {
			in := StaffIdentityRecoveryDecision{RequestID: request, ReviewedBy: "TEST-independent-reviewer", Decision: "approved", Reason: "Reviewed independently"}
			localEvent := event
			localEvent.RequestID = request.String()
			_, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, localEvent)
			if err == nil {
				wins.Add(1)
			} else if errors.Is(err, ErrConflict) {
				conflicts.Add(1)
			} else {
				t.Errorf("unexpected decision race error: %v", err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != 9 {
		t.Fatalf("replacement winners=%d conflicts=%d", wins.Load(), conflicts.Load())
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_replacements WHERE parent_command_id=$1`, c.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replacement lineage count=%d err=%v", count, err)
	}
}

func TestStaffRecoveryDecisionRunsWithApplicationPrivileges(t *testing.T) {
	s, _, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
	cfg := s.pool.Config()
	previous := cfg.AfterConnect
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if previous != nil {
			if err := previous(ctx, conn); err != nil {
				return err
			}
		}
		_, err := conn.Exec(ctx, `SET ROLE onboarding`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	restricted := &Store{pool: pool}
	got, err := restricted.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err != nil || got.Successor == nil {
		t.Fatalf("application-role decision failed: %v", err)
	}
	replay, err := restricted.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
	if err != nil || replay.Successor == nil || replay.Successor.ID != got.Successor.ID {
		t.Fatalf("application-role decision retry failed: %v", err)
	}
	for _, statement := range []string{"UPDATE staff_identity_replacements SET generation=2", "DELETE FROM staff_identity_replacements", "TRUNCATE staff_identity_replacements"} {
		_, err := pool.Exec(t.Context(), statement)
		requireRecoverySQLState(t, err, "42501")
	}
}

func TestStaffRecoveryLaterGenerationRejectsHistoricalCreatorsAndAccounts(t *testing.T) {
	for _, depth := range []int{1, 2} {
		t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
			s, root, scope, in, candidate, event := staffRecoveryDecisionFixture(t, "replace")
			history := []StaffIdentityCommand{root}
			for generation := 1; generation <= depth; generation++ {
				got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
				if err != nil {
					t.Fatal(err)
				}
				child := *got.Successor
				history = append(history, child)
				productEvent := audit.Event{ActorType: audit.ActorProduct, ActorID: child.MachineSubject, ProductKey: child.ProductKey, OrgID: child.OrgID.String(), Action: "staff.identity.dispatch", Target: child.ID.String(), Outcome: audit.OutcomeSuccess, RequestID: child.ID.String()}
				if claimed, err := s.ClaimStaffIdentityDispatch(t.Context(), child, productEvent); err != nil || !claimed {
					t.Fatalf("generation dispatch: %v", err)
				}
				nextID, err := uuid.NewV7()
				if err != nil {
					t.Fatal(err)
				}
				trackRecoverySuccessors(t, s, nextID)
				candidate = &StaffIdentitySuccessor{ID: nextID, ProviderUserID: "TEST-history-" + nextID.String(), BindingNonce: "history-nonce-" + nextID.String()}
				scope.CommandID = child.ID
				in.RequestID = recoveryRequest(t, s, child, "replace", fmt.Sprintf("TEST-initiator-%d", generation+1))
				event.Target = child.ID.String()
				event.RequestID = in.RequestID.String()
			}
			prior := history[depth-1]
			for _, reviewer := range []string{prior.ActorSubject, prior.ProviderUserID} {
				in.ReviewedBy = reviewer
				event.ActorID = reviewer
				got, err := s.DecideStaffIdentityRecovery(t.Context(), scope, in, candidate, event)
				if !errors.Is(err, ErrConflict) || got.Successor != nil {
					t.Fatalf("historical creator/account approved a later generation: %v", err)
				}
			}
		})
	}
}
