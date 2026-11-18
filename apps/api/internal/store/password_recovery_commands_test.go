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
			op, send, err := s.ReservePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{})
			if err != nil {
				t.Error(err)
			}
			if op.State != "processing" {
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
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "unconfirmed", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	op, send, err := s.ReservePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{})
	if err != nil || send || op.State != "unconfirmed" {
		t.Fatal("uncertain command replayed", err)
	}
	if _, _, err := s.ReservePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
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
		if _, _, err := s.ReservePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, tc.browser, tc.tenant, "account", tc.baseline); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong proof or stale native cutoff admitted", err)
		}
	}
	command := uuid.Must(uuid.NewV7())
	cutoff := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if _, send, err := s.ReservePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil || !send {
		t.Fatal(err)
	}
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "applied", "42", cutoff); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "completed", "42", cutoff); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReservePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
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
		if _, send, err := s.ReservePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil || !send {
			t.Fatal("valid attempt denied", err)
		}
		if err := s.RecordPasswordRecoveryCommand(t.Context(), command, "refused", "", time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.ReservePasswordRecoveryCommand(t.Context(), uuid.Must(uuid.NewV7()), in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); !errors.Is(err, ErrConflict) {
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
	if _, _, err := s.ReservePasswordRecoveryCommand(t.Context(), command, in.ID, in.BrowserHash, in.TenantID, "account", time.Time{}); err != nil {
		t.Fatal(err)
	}
	op, err := s.LatestPasswordRecoveryCommand(t.Context(), in.ID, in.BrowserHash)
	if err != nil || op.ID != command || op.State != "processing" {
		t.Fatal("status lost retained command", err)
	}
	if _, err := s.LatestPasswordRecoveryCommand(t.Context(), in.ID, recoveryHash("foreign")); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign browser disclosed native outcome", err)
	}
}
