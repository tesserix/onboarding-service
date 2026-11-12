package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func staffRecoveryFixture(t *testing.T) (*Store, StaffIdentityCommand) {
	t.Helper()
	s, c, event := staffIdentityFixture(t)
	var err error
	c, err = s.PrepareStaffIdentity(t.Context(), c, event)
	if err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.dispatch"
	if claimed, err := s.ClaimStaffIdentityDispatch(t.Context(), c, event); err != nil || !claimed {
		t.Fatalf("dispatch: claimed=%v err=%v", claimed, err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"staff_identity_recovery_decisions", "staff_identity_recovery_requests"} {
			if _, err := s.pool.Exec(context.Background(), "DELETE FROM "+table+" WHERE command_id=$1", c.ID); err != nil {
				t.Error(err)
			}
		}
	})
	return s, c
}

const recoveryRequestSQL = `INSERT INTO staff_identity_recovery_requests
(id,command_id,original_actor_subject,original_user_id,requested_by,requested_action,replacement_actor_subject,reason,evidence_reference)
VALUES($1,$2,$3,$4,$5,$6,$7,'Investigated ambiguous dispatch','QA-RECOVERY-1')`
const recoveryDecisionSQL = `INSERT INTO staff_identity_recovery_decisions
(request_id,command_id,requested_by,original_actor_subject,original_user_id,requested_action,replacement_actor_subject,reviewed_by,decision,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'Reviewed source evidence')`

func recoveryRequest(t *testing.T, s *Store, c StaffIdentityCommand, action, replacement string) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), recoveryRequestSQL, id, c.ID, c.ActorSubject, c.ProviderUserID, "TEST-recovery-maker", action, replacement); err != nil {
		t.Fatalf("retain recovery request: %v", err)
	}
	return id
}

func recoveryDecision(ctx context.Context, s *Store, c StaffIdentityCommand, id uuid.UUID, action, replacement, reviewer, decision string) error {
	_, err := s.pool.Exec(ctx, recoveryDecisionSQL, id, c.ID, "TEST-recovery-maker", c.ActorSubject, c.ProviderUserID, action, replacement, reviewer, decision)
	return err
}

func requireRecoverySQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != code {
		t.Fatalf("want SQLSTATE %s, got %v", code, err)
	}
}

func TestStaffIdentityRecoveryRequiresIndependentRetainedDecision(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "close_unresolved", "")
	for _, reviewer := range []string{"TEST-recovery-maker", c.ActorSubject, c.ProviderUserID} {
		requireRecoverySQLState(t, recoveryDecision(t.Context(), s, c, id, "close_unresolved", "", reviewer, "approved"), "23514")
	}
	if err := recoveryDecision(t.Context(), s, c, id, "close_unresolved", "", "TEST-independent-reviewer", "approved"); err != nil {
		t.Fatal(err)
	}
	var who, decision string
	if err := s.pool.QueryRow(t.Context(), `SELECT reviewed_by,decision FROM staff_identity_recovery_decisions WHERE request_id=$1`, id).Scan(&who, &decision); err != nil || who != "TEST-independent-reviewer" || decision != "approved" {
		t.Fatalf("retained decision=%q reviewer=%q err=%v", decision, who, err)
	}
}

func TestStaffIdentityRecoveryReplacementInitiatorCannotReview(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "replace", "TEST-current-hms-admin")
	requireRecoverySQLState(t, recoveryDecision(t.Context(), s, c, id, "replace", "TEST-current-hms-admin", "TEST-current-hms-admin", "approved"), "23514")
	// Empty replacement facts must not bypass the composite request binding.
	requireRecoverySQLState(t, recoveryDecision(t.Context(), s, c, id, "replace", "", "TEST-independent-reviewer", "approved"), "23503")
}

func TestStaffIdentityRecoveryCannotBypassBindingWithNullReplacement(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "replace", "TEST-current-hms-admin")
	_, err := s.pool.Exec(t.Context(), recoveryDecisionSQL, id, c.ID, "TEST-recovery-maker", c.ActorSubject, c.ProviderUserID, "replace", nil, "TEST-independent-reviewer", "approved")
	requireRecoverySQLState(t, err, "23502")
}

func TestStaffIdentityRecoveryCannotForgeOriginalOrRequestBinding(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id := recoveryRequest(t, s, c, "close_unresolved", "")
	for _, change := range []func(*StaffIdentityCommand){
		func(c *StaffIdentityCommand) { c.ActorSubject = "TEST-forged-initiator" },
		func(c *StaffIdentityCommand) { c.ProviderUserID = "TEST-forged-target" },
		func(c *StaffIdentityCommand) { c.ID = uuid.New() },
	} {
		forged := c
		change(&forged)
		requireRecoverySQLState(t, recoveryDecision(t.Context(), s, forged, id, "close_unresolved", "", "TEST-independent-reviewer", "approved"), "23503")
	}
	_, err := s.pool.Exec(t.Context(), recoveryDecisionSQL, id, c.ID, "TEST-forged-maker", c.ActorSubject, c.ProviderUserID, "close_unresolved", "", "TEST-independent-reviewer", "approved")
	requireRecoverySQLState(t, err, "23503")
	for _, change := range []func(*StaffIdentityCommand){
		func(c *StaffIdentityCommand) { c.ActorSubject = "TEST-forged-initiator" },
		func(c *StaffIdentityCommand) { c.ProviderUserID = "TEST-forged-target" },
	} {
		forged := c
		change(&forged)
		fresh, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.pool.Exec(t.Context(), recoveryRequestSQL, fresh, forged.ID, forged.ActorSubject, forged.ProviderUserID, "TEST-recovery-maker", "close_unresolved", "")
		requireRecoverySQLState(t, err, "23503")
	}
}

func TestStaffIdentityRecoveryRejectionIsFinalButNewRequestCanWin(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	rejected := recoveryRequest(t, s, c, "close_unresolved", "")
	if err := recoveryDecision(t.Context(), s, c, rejected, "close_unresolved", "", "TEST-independent-reviewer", "rejected"); err != nil {
		t.Fatal(err)
	}
	requireRecoverySQLState(t, recoveryDecision(t.Context(), s, c, rejected, "close_unresolved", "", "TEST-other-reviewer", "approved"), "23505")
	accepted := recoveryRequest(t, s, c, "close_unresolved", "")
	if err := recoveryDecision(t.Context(), s, c, accepted, "close_unresolved", "", "TEST-independent-reviewer", "approved"); err != nil {
		t.Fatal(err)
	}
	_, err := s.pool.Exec(t.Context(), `DELETE FROM staff_identity_recovery_requests WHERE id=$1`, accepted)
	requireRecoverySQLState(t, err, "23503")
	var dispatches int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_dispatches WHERE command_id=$1`, c.ID).Scan(&dispatches); err != nil || dispatches != 1 {
		t.Fatalf("recovery changed original dispatch: count=%d err=%v", dispatches, err)
	}
}

func TestStaffIdentityRecoveryConcurrentApprovalsHaveOneWinner(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	ids := make([]uuid.UUID, 12)
	for i := range ids {
		ids[i] = recoveryRequest(t, s, c, "replace", "TEST-current-hms-admin")
	}
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() {
			err := recoveryDecision(t.Context(), s, c, id, "replace", "TEST-current-hms-admin", "TEST-independent-reviewer", "approved")
			if err == nil {
				wins.Add(1)
				return
			}
			var pgerr *pgconn.PgError
			if errors.As(err, &pgerr) && pgerr.Code == "23505" {
				conflicts.Add(1)
				return
			}
			t.Errorf("unexpected concurrent decision: %v", err)
		})
	}
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != 11 {
		t.Fatalf("approval winners=%d conflicts=%d", wins.Load(), conflicts.Load())
	}
}

func TestStaffIdentityRecoveryApplicationRoleCannotRewriteHistory(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	err = pgx.BeginFunc(t.Context(), s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SET LOCAL ROLE onboarding`); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), recoveryRequestSQL, id, c.ID, c.ActorSubject, c.ProviderUserID, "TEST-recovery-maker", "close_unresolved", ""); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), recoveryDecisionSQL, id, c.ID, "TEST-recovery-maker", c.ActorSubject, c.ProviderUserID, "close_unresolved", "", "TEST-independent-reviewer", "approved")
		return err
	})
	if err != nil {
		t.Fatalf("application INSERT denied: %v", err)
	}
	for _, table := range []string{"staff_identity_recovery_requests", "staff_identity_recovery_decisions"} {
		for _, statement := range []string{"UPDATE " + table + " SET reason='rewritten'", "DELETE FROM " + table, "TRUNCATE " + table} {
			tx, err := s.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `SET LOCAL ROLE onboarding`); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(t.Context(), statement)
			requireRecoverySQLState(t, err, "42501")
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestStaffIdentityRecoveryClosureAllowsLaterReviewedReplacement(t *testing.T) {
	s, c := staffRecoveryFixture(t)
	closure := recoveryRequest(t, s, c, "close_unresolved", "")
	if err := recoveryDecision(t.Context(), s, c, closure, "close_unresolved", "", "TEST-independent-reviewer", "approved"); err != nil {
		t.Fatal(err)
	}
	replacement := recoveryRequest(t, s, c, "replace", "TEST-current-hms-admin")
	if err := recoveryDecision(t.Context(), s, c, replacement, "replace", "TEST-current-hms-admin", "TEST-independent-reviewer", "approved"); err != nil {
		t.Fatalf("prior closure prevented later independently reviewed recovery: %v", err)
	}
}

func TestStaffIdentityRecoveryRequiresDispatchedCommandAndMandatoryEvidence(t *testing.T) {
	s, c, event := staffIdentityFixture(t)
	if _, err := s.PrepareStaffIdentity(t.Context(), c, event); err != nil {
		t.Fatal(err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(t.Context(), recoveryRequestSQL, id, c.ID, c.ActorSubject, c.ProviderUserID, "TEST-recovery-maker", "close_unresolved", "")
	requireRecoverySQLState(t, err, "23503")
	event.Action = "staff.identity.dispatch"
	if claimed, err := s.ClaimStaffIdentityDispatch(t.Context(), c, event); err != nil || !claimed {
		t.Fatalf("dispatch: claimed=%v err=%v", claimed, err)
	}
	for _, statement := range []string{
		`INSERT INTO staff_identity_recovery_requests(id,command_id,original_actor_subject,original_user_id,requested_by,requested_action,reason,evidence_reference) VALUES($1,$2,$3,$4,'TEST-maker','close_unresolved','  ','QA-1')`,
		`INSERT INTO staff_identity_recovery_requests(id,command_id,original_actor_subject,original_user_id,requested_by,requested_action,reason,evidence_reference) VALUES($1,$2,$3,$4,'TEST-maker','close_unresolved','Investigated','')`,
		`INSERT INTO staff_identity_recovery_requests(id,command_id,original_actor_subject,original_user_id,requested_by,requested_action,reason,evidence_reference) VALUES($1,$2,$3,$4,'TEST-maker','replace','Investigated','QA-1')`,
	} {
		_, err = s.pool.Exec(t.Context(), statement, id, c.ID, c.ActorSubject, c.ProviderUserID)
		requireRecoverySQLState(t, err, "23514")
	}
}
