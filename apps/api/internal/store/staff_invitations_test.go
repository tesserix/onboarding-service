package store

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffInvitationsRetainOneDispatchAndRejectChangedScopeAndEarlyResend(t *testing.T) {
	s, c, event := staffIdentityFixture(t)
	ctx := t.Context()
	var err error
	if c, err = s.PrepareStaffIdentity(ctx, c, event); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimStaffIdentityDispatch(ctx, c, event); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordStaffIdentityCreation(ctx, c, time.Now().UTC(), event); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM staff_identity_invitation_attempts WHERE command_id=$1`, c.ID); err != nil {
			t.Error(err)
		}
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM staff_identity_setups WHERE command_id=$1`, c.ID); err != nil {
			t.Error(err)
		}
	})
	scope := StaffIdentityScope{c.ID, c.OrgID, c.ProductKey, c.MachineSubject, c.ActorSubject, c.ProviderOrgID, c.ProviderProjectID}
	id := uuid.Must(uuid.NewV7())
	hash := bytes.Repeat([]byte{1}, 32)
	first, claimed, err := s.PrepareStaffInvitation(ctx, scope, id, hash, "email", event)
	if err != nil || !claimed || first.ID != id || first.Phase != "email" || first.AcceptedAt != nil || first.ExpiresAt.Sub(first.CreatedAt) != 30*time.Minute {
		t.Fatalf("initial dispatch: claimed=%t error=%v", claimed, err)
	}
	again, claimed, err := s.PrepareStaffInvitation(ctx, scope, id, bytes.Repeat([]byte{2}, 32), "email", event)
	if err != nil || claimed || again.ID != first.ID {
		t.Fatalf("retry redispatched: claimed=%t error=%v", claimed, err)
	}
	if _, claimed, err = s.PrepareStaffInvitation(ctx, scope, uuid.Must(uuid.NewV7()), hash, "email", event); !errors.Is(err, ErrConflict) || claimed {
		t.Fatal("cooldown did not prevent new dispatch")
	}
	foreign := scope
	foreign.OrgID = uuid.New()
	if _, _, err = s.PrepareStaffInvitation(ctx, foreign, id, hash, "email", event); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign tenant received invitation")
	}
	if _, err = s.StaffInvitationByTicket(ctx, id, bytes.Repeat([]byte{3}, 32)); !errors.Is(err, ErrNotFound) {
		t.Fatal("wrong capability found invitation")
	}
	if err = s.RecordStaffInvitationMail(ctx, scope, id, "test-message", event); err != nil {
		t.Fatal(err)
	}
	row, err := s.StaffInvitationByTicket(ctx, id, hash)
	if err != nil || row.AcceptedAt == nil {
		t.Fatal("provider acceptance not retained")
	}
	lease, err := s.ClaimStaffInvitationProof(ctx, id, hash, event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimStaffInvitationProof(ctx, id, hash, event); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent credential mutation permitted")
	}
	if err = s.FinishStaffInvitationProof(ctx, lease, false, event); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimStaffInvitationProof(ctx, id, hash, event)
	if err != nil {
		t.Fatal("invalid proof consumed valid capability")
	}
	if err = s.FinishStaffInvitationProof(ctx, lease, true, event); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimStaffInvitationProof(ctx, id, hash, event); !errors.Is(err, ErrConflict) {
		t.Fatal("successful proof was reusable")
	}
}

func invitationFixture(t *testing.T) (*Store, StaffIdentityScope, audit.Event) {
	t.Helper()
	s, c, event := staffIdentityFixture(t)
	ctx := t.Context()
	var err error
	if c, err = s.PrepareStaffIdentity(ctx, c, event); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimStaffIdentityDispatch(ctx, c, event); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordStaffIdentityCreation(ctx, c, time.Now().UTC(), event); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"staff_identity_invitation_attempts", "staff_identity_setups"} {
			if _, err := s.pool.Exec(context.Background(), "DELETE FROM "+table+" WHERE command_id=$1", c.ID); err != nil {
				t.Error(err)
			}
		}
	})
	return s, invitationScope(c), event
}

func TestStaffInvitationLimitsExpiryConcurrencyAndAuditRollback(t *testing.T) {
	s, scope, event := invitationFixture(t)
	ctx := t.Context()
	id := uuid.Must(uuid.NewV7())
	hash := bytes.Repeat([]byte{1}, 32)
	bad := event
	bad.Outcome = "INVALID"
	if _, claimed, err := s.PrepareStaffInvitation(ctx, scope, id, hash, "email", bad); err == nil || claimed {
		t.Fatal("unaudited dispatch committed")
	}
	if _, claimed, err := s.PrepareStaffInvitation(ctx, scope, id, hash, "email", event); err != nil || !claimed {
		t.Fatal("rolled-back dispatch cannot be retried")
	}
	if err := s.RecordStaffInvitationMail(ctx, scope, id, "test-message", bad); err == nil {
		t.Fatal("unaudited provider acceptance committed")
	}
	if v, err := s.StaffInvitationByTicket(ctx, id, hash); err != nil || v.AcceptedAt != nil {
		t.Fatal("failed audit retained acceptance")
	}
	if err := s.RecordStaffInvitationMail(ctx, scope, id, "test-message", event); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimStaffInvitationProof(ctx, id, hash, bad); err == nil {
		t.Fatal("unaudited proof claim committed")
	}
	var claims atomic.Int32
	var winner StaffInvitationProof
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			proof, err := s.ClaimStaffInvitationProof(ctx, id, hash, event)
			if err == nil {
				claims.Add(1)
				mu.Lock()
				winner = proof
				mu.Unlock()
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("proof lease allowed concurrent dispatches")
	}
	// A timed-out lease never permits the same uncertain proof to be dispatched again.
	if _, err := s.pool.Exec(ctx, `UPDATE staff_identity_setups SET lease_until=now()-interval '1 second' WHERE command_id=$1`, scope.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimStaffInvitationProof(ctx, id, hash, event); !errors.Is(err, ErrConflict) {
		t.Fatal("uncertain native proof was redispatched")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE staff_identity_setups SET lease_until=now()+interval '30 seconds' WHERE command_id=$1`, scope.CommandID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishStaffInvitationProof(ctx, winner, false, bad); err == nil {
		t.Fatal("unaudited outcome committed")
	}
	if err := s.FinishStaffInvitationProof(ctx, winner, false, event); err != nil {
		t.Fatal(err)
	}
	for range 9 {
		proof, err := s.ClaimStaffInvitationProof(ctx, id, hash, event)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.FinishStaffInvitationProof(ctx, proof, false, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClaimStaffInvitationProof(ctx, id, hash, event); !errors.Is(err, ErrConflict) {
		t.Fatal("proof attempt budget was bypassed")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE staff_identity_invitation_attempts SET created_at=created_at-interval '2 minutes',expires_at=expires_at-interval '2 minutes' WHERE command_id=$1`, scope.CommandID); err != nil {
		t.Fatal(err)
	}
	for n := 2; n <= 5; n++ {
		next := uuid.Must(uuid.NewV7())
		nextHash := bytes.Repeat([]byte{byte(n)}, 32)
		if _, claimed, err := s.PrepareStaffInvitation(ctx, scope, next, nextHash, "email", event); err != nil || !claimed {
			t.Fatalf("delivery %d rejected: %v", n, err)
		}
		if err := s.RecordStaffInvitationMail(ctx, scope, next, "test-message", event); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE staff_identity_invitation_attempts SET created_at=created_at-interval '2 minutes',expires_at=expires_at-interval '2 minutes' WHERE id=$1`, next); err != nil {
			t.Fatal(err)
		}
	}
	if _, claimed, err := s.PrepareStaffInvitation(ctx, scope, uuid.Must(uuid.NewV7()), bytes.Repeat([]byte{9}, 32), "password", event); !errors.Is(err, ErrConflict) || claimed {
		t.Fatal("daily budget was bypassed through a different phase")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE staff_identity_invitation_attempts SET created_at=created_at-interval '25 hours',expires_at=expires_at-interval '25 hours' WHERE command_id=$1`, scope.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimStaffInvitationProof(ctx, id, hash, event); !errors.Is(err, ErrConflict) {
		t.Fatal("expired link permitted proof")
	}
	if _, claimed, err := s.PrepareStaffInvitation(ctx, scope, uuid.Must(uuid.NewV7()), bytes.Repeat([]byte{9}, 32), "password", event); err != nil || !claimed {
		t.Fatal("rolling window did not release delivery budget")
	}
}
