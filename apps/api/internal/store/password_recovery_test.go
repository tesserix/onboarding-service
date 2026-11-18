package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func recoveryReservation() PasswordRecoveryReservation {
	return PasswordRecoveryReservation{
		ID: uuid.Must(uuid.NewV7()), TenantID: uuid.New(), BrowserHash: recoveryHash("browser"), SourceHash: recoveryHash(uuid.NewString()), Email: "recovery@example.test", CodeHash: recoveryHash("code"), CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
}
func recoveryHash(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }

func TestPasswordRecoveryProofRequiresItsBrowserAndCannotReplayOrResend(t *testing.T) {
	s := newStore(t)
	in := recoveryReservation()
	now := in.CreatedAt
	send, err := s.ReservePasswordRecovery(t.Context(), in)
	if err != nil || !send {
		t.Fatal("initial reservation", err)
	}
	in.CodeHash = recoveryHash("different generated retry code")
	send, err = s.ReservePasswordRecovery(t.Context(), in)
	if err != nil || send {
		t.Fatal("same command dispatched twice", err)
	}
	changed := in
	changed.TenantID = uuid.New()
	if _, err = s.ReservePasswordRecovery(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("rebound tenant admitted", err)
	}
	if _, err = s.VerifiedPasswordRecovery(t.Context(), in.ID, in.BrowserHash, now); !errors.Is(err, ErrNotFound) {
		t.Fatal("unverified proof disclosed recipient", err)
	}
	if err = s.VerifyPasswordRecovery(t.Context(), in.ID, recoveryHash("foreign"), recoveryHash("code"), now); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign browser proved ownership", err)
	}
	for range 4 {
		if err = s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, recoveryHash("wrong"), now); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong code accepted", err)
		}
	}
	if err = s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, recoveryHash("code"), now); err != nil {
		t.Fatal(err)
	}
	if err = s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, recoveryHash("code"), now); !errors.Is(err, ErrNotFound) {
		t.Fatal("proof replay admitted", err)
	}
	proof, err := s.VerifiedPasswordRecovery(t.Context(), in.ID, in.BrowserHash, now)
	if err != nil || proof.TenantID != in.TenantID || proof.Email != in.Email {
		t.Fatal("wrong verified account scope", err)
	}
	if _, err = s.VerifiedPasswordRecovery(t.Context(), in.ID, in.BrowserHash, now.Add(10*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired capability accepted", err)
	}
	in.ID = uuid.Must(uuid.NewV7())
	if send, err = s.ReservePasswordRecovery(t.Context(), in); err != nil || send {
		t.Fatal("mail resent after verification in same browser", err)
	}
}

func TestPasswordRecoveryConcurrentReservationAndProofAreSingleUse(t *testing.T) {
	s := newStore(t)
	in := recoveryReservation()
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			send, err := s.ReservePasswordRecovery(t.Context(), in)
			if err != nil {
				t.Error(err)
			}
			if send {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("reserved %d deliveries", admitted.Load())
	}
	admitted.Store(0)
	for range 8 {
		wg.Go(func() {
			err := s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, in.CodeHash, in.CreatedAt)
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrNotFound) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("verified %d proofs", admitted.Load())
	}
}

func TestPasswordRecoveryExhaustionExpiryAndRetainedDeliveryOutcome(t *testing.T) {
	s := newStore(t)
	in := recoveryReservation()
	if _, err := s.ReservePasswordRecovery(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, recoveryHash("wrong"), in.CreatedAt); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong proof admitted", err)
		}
	}
	if err := s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, in.CodeHash, in.CreatedAt); !errors.Is(err, ErrNotFound) {
		t.Fatal("exhausted proof admitted", err)
	}
	if err := s.RecordPasswordRecoveryDelivery(t.Context(), in.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPasswordRecoveryDelivery(t.Context(), in.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("uncertain delivery overwritten", err)
	}
	in.ID = uuid.Must(uuid.NewV7())
	if _, err := s.ReservePasswordRecovery(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, in.CodeHash, in.CreatedAt.Add(10*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired proof admitted", err)
	}
}

func TestPasswordRecoveryRecipientBudgetIncludesEveryTenantAndBrowser(t *testing.T) {
	s := newStore(t)
	in := recoveryReservation()
	for range 3 {
		in.ID = uuid.Must(uuid.NewV7())
		in.TenantID = uuid.New()
		in.BrowserHash = recoveryHash(uuid.NewString())
		if _, err := s.ReservePasswordRecovery(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	in.ID = uuid.Must(uuid.NewV7())
	if _, err := s.ReservePasswordRecovery(t.Context(), in); !errors.Is(err, ErrPasswordRecoveryLimited) {
		t.Fatal("recipient budget bypassed", err)
	}
}

func TestPasswordRecoverySourceAndDailyRecipientBudgets(t *testing.T) {
	s := newStore(t)
	in := recoveryReservation()
	base := in.CreatedAt
	for i := range 10 {
		in.ID = uuid.Must(uuid.NewV7())
		in.Email = fmt.Sprintf("recipient-%d@example.test", i)
		if _, err := s.ReservePasswordRecovery(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	in.ID = uuid.Must(uuid.NewV7())
	in.Email = "over@example.test"
	if _, err := s.ReservePasswordRecovery(t.Context(), in); !errors.Is(err, ErrPasswordRecoveryLimited) {
		t.Fatal("source hourly budget bypassed", err)
	}
	for i := range 10 {
		in.ID = uuid.Must(uuid.NewV7())
		in.SourceHash = recoveryHash(uuid.NewString())
		in.Email = "daily@example.test"
		in.CreatedAt = base.Add(time.Duration(i)*2*time.Hour - 20*time.Hour)
		if _, err := s.ReservePasswordRecovery(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	in.ID = uuid.Must(uuid.NewV7())
	in.CreatedAt = base
	if _, err := s.ReservePasswordRecovery(t.Context(), in); !errors.Is(err, ErrPasswordRecoveryLimited) {
		t.Fatal("recipient daily budget bypassed", err)
	}
}

func TestPasswordRecoveryApplicationCannotRewriteAuthorityOrRearmProof(t *testing.T) {
	s := newStore(t)
	in := recoveryReservation()
	if _, err := s.ReservePasswordRecovery(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPasswordRecovery(t.Context(), in.ID, in.BrowserHash, in.CodeHash, in.CreatedAt); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE password_recovery_challenges SET tenant_id=gen_random_uuid() WHERE id=$1`,
		`UPDATE password_recovery_challenges SET email='other@example.test' WHERE id=$1`,
		`UPDATE password_recovery_challenges SET verified_at=NULL WHERE id=$1`,
		`UPDATE password_recovery_challenges SET attempts=0 WHERE id=$1`,
		`UPDATE password_recovery_challenges SET expires_at=expires_at+interval '1 minute' WHERE id=$1`,
	} {
		err := pgx.BeginFunc(t.Context(), s.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(t.Context(), `SET LOCAL ROLE onboarding`); err != nil {
				return err
			}
			_, err := tx.Exec(t.Context(), query, in.ID)
			return err
		})
		if err == nil {
			t.Fatalf("application rewrote recovery authority: %s", query)
		}
	}
	if _, err := s.VerifiedPasswordRecovery(t.Context(), in.ID, in.BrowserHash, in.CreatedAt); err != nil {
		t.Fatal("failed rewrite destroyed proof", err)
	}
}
