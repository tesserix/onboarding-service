package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func verifiedRecovery(t *testing.T, s *Store) PasswordRecoveryReservation {
	t.Helper()
	in := recoveryReservation()
	if _, err := s.ReservePasswordRecovery(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, in.CodeHash, in.CreatedAt); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestPasswordRecoveryCommandsSerializeAndRetainUncertaintyWithoutASecondWrite(t *testing.T) {
	s := newStore(t)
	in := verifiedRecovery(t, s)
	command := uuid.Must(uuid.NewV7())
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			op, send, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{})
			if err != nil {
				t.Error(err)
			}
			if op.State != "prepared" {
				t.Error("wrong initial receipt")
			}
			if send {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("admitted %d native dispatches", admitted.Load())
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "unconfirmed", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	op, send, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{})
	if err != nil || send || op.State != "unconfirmed" {
		t.Fatal("uncertain command replayed", err)
	}
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("unconfirmed native account fence released", err)
	}
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "completed", "42", time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("uncertainty fabricated completion", err)
	}
}

func TestPasswordRecoveryCommandRequiresCurrentExactProofAndFencesOlderCredentials(t *testing.T) {
	s := newStore(t)
	in := verifiedRecovery(t, s)
	for _, tc := range []struct {
		browser  []byte
		tenant   uuid.UUID
		baseline time.Time
	}{
		{recoveryHash("foreign"), in.TenantID, time.Time{}}, {in.BrowserHash, uuid.New(), time.Time{}}, {in.BrowserHash, in.TenantID, time.Now().Add(time.Hour)},
	} {
		if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, tc.browser, tc.tenant, "account", tc.baseline); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong proof or stale native cutoff admitted", err)
		}
	}
	command := uuid.Must(uuid.NewV7())
	cutoff := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if _, send, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil || !send {
		t.Fatal(err)
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "applied", "42", cutoff); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "completed", "42", cutoff); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("completed cutoff admitted stale second reset", err)
	}
	if _, err := s.PasswordRecoveryCommand(t.Context(), command, in.ID, recoveryHash("other")); !errors.Is(err, ErrNotFound) {
		t.Fatal("command disclosed to another browser", err)
	}
}

func TestPasswordRecoveryAllowsOnlyFiveKnownRefusedCommandsPerMailboxProof(t *testing.T) {
	s := newStore(t)
	in := verifiedRecovery(t, s)
	for range 5 {
		command := uuid.Must(uuid.NewV7())
		if _, send, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil || !send {
			t.Fatal("valid attempt denied", err)
		}
		if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "refused", "", time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("native command attempt budget bypassed", err)
	}
}

func TestPasswordRecoveryStatusUsesItsLastBrowserBoundCommand(t *testing.T) {
	s := newStore(t)
	in := verifiedRecovery(t, s)
	if _, err := s.LatestPasswordRecoveryCommand(t.Context(), in.ID, in.BrowserHash); !errors.Is(err, ErrNotFound) {
		t.Fatal("invented prior native command", err)
	}
	command := uuid.Must(uuid.NewV7())
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	op, err := s.LatestPasswordRecoveryCommand(t.Context(), in.ID, in.BrowserHash)
	if err != nil || op.ID != command || op.State != "prepared" {
		t.Fatal("status lost retained command", err)
	}
	if _, err := s.LatestPasswordRecoveryCommand(t.Context(), in.ID, recoveryHash("foreign")); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign browser disclosed native outcome", err)
	}
}

func TestPreparedRecoveryCanBeCancelledBeforeALatePasswordButNotAfterDispatch(t *testing.T) {
	s := newStore(t)
	in := verifiedRecovery(t, s)
	command := uuid.Must(uuid.NewV7())
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("missing intent dispatched", err)
	}
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled late password dispatched", err)
	}
	if op, created, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil || created || op.State != "cancelled" {
		t.Fatal("cancelled intent revived", err)
	}
	next := uuid.Must(uuid.NewV7())
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), next, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), next, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPasswordRecoveryCommand(t.Context(), next, in.ID, in.BrowserHash); !errors.Is(err, ErrConflict) {
		t.Fatal("processing command cancelled", err)
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), next, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("repeated password admitted", err)
	}
}

func TestPreparedRecoveryCancelAndDispatchHaveExactlyOneWinner(t *testing.T) {
	s := newStore(t)
	in := verifiedRecovery(t, s)
	command := uuid.Must(uuid.NewV7())
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := s.CancelPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash); err == nil {
			winners.Add(1)
		} else if !errors.Is(err, ErrConflict) {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err == nil {
			winners.Add(1)
		} else if !errors.Is(err, ErrConflict) {
			t.Error(err)
		}
	})
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("got %d winners", winners.Load())
	}
}

func TestExpiredPreparedIntentIsCancelledButUncertainNativeWriteRemainsFenced(t *testing.T) {
	s := newStore(t)
	old := verifiedRecovery(t, s)
	first := uuid.Must(uuid.NewV7())
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), first, old.ID, old.BrowserHash, old.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	// Simulate the existing bounded proof cleanup; command history remains retained.
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM password_recovery_challenges WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	next := recoveryReservation()
	next.TenantID = old.TenantID
	if _, err := s.ReservePasswordRecovery(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPasswordRecovery(t.Context(), next.ID, next.BrowserHash, next.CodeHash, next.CreatedAt); err != nil {
		t.Fatal(err)
	}
	command := uuid.Must(uuid.NewV7())
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), command, next.ID, next.BrowserHash, next.TenantID, "account", time.Time{}); err != nil {
		t.Fatal("abandoned prepared intent blocked fresh mailbox proof forever", err)
	}
	var state string
	if err := s.pool.QueryRow(t.Context(), `SELECT state FROM password_recovery_commands WHERE id=$1`, first).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal("expired intent was not durably fenced", err)
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), first, old.ID, old.BrowserHash, old.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("expired late password dispatched", err)
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, next.ID, next.BrowserHash, next.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "unconfirmed", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM password_recovery_challenges WHERE id=$1`, next.ID); err != nil {
		t.Fatal(err)
	}
	third := recoveryReservation()
	third.TenantID = old.TenantID
	if _, err := s.ReservePasswordRecovery(t.Context(), third); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPasswordRecovery(t.Context(), third.ID, third.BrowserHash, third.CodeHash, third.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), third.ID, third.BrowserHash, third.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
		t.Fatal("cleanup released uncertain native fence", err)
	}
}

func TestPreparedRecoveryDispatchRechecksExactProofSubjectAndNativeBaseline(t *testing.T) {
	s := newStore(t)
	in := verifiedRecovery(t, s)
	command := uuid.Must(uuid.NewV7())
	baseline := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, _, err := s.PreparePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", baseline); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		browser  []byte
		tenant   uuid.UUID
		subject  string
		baseline time.Time
	}{
		{recoveryHash("foreign"), in.TenantID, "account", baseline},
		{in.BrowserHash, uuid.New(), "account", baseline},
		{in.BrowserHash, in.TenantID, "different-account", baseline},
		{in.BrowserHash, in.TenantID, "account", baseline.Add(time.Second)},
		{in.BrowserHash, in.TenantID, "account", time.Time{}},
	} {
		if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, tc.browser, tc.tenant, tc.subject, tc.baseline); !errors.Is(err, ErrConflict) {
			t.Fatal("mismatched intent admitted", err)
		}
	}
	if op, err := s.PasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash); err != nil || op.State != "prepared" {
		t.Fatal("refusal consumed intent", err)
	}
	if _, err := s.DispatchPasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", baseline); err != nil {
		t.Fatal(err)
	}
}
