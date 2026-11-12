package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryProofIsBrowserBoundOneUseAndFiveAttempts(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	id, _ := uuid.NewV7()
	browser := sha256.Sum256([]byte("browser"))
	code := sha256.Sum256([]byte("code"))
	source := sha256.Sum256([]byte(uuid.NewString()))
	if err := s.ReserveDiscovery(t.Context(), id, browser[:], source[:], "proof@example.test", code[:], now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DiscoveryEmail(t.Context(), id, browser[:], now); !errors.Is(err, ErrNotFound) {
		t.Fatal("unverified browser disclosed email")
	}
	foreign := sha256.Sum256([]byte("foreign"))
	if err := s.VerifyDiscovery(t.Context(), id, foreign[:], code[:], now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign browser accepted")
	}
	for i := 0; i < 4; i++ {
		if err := s.VerifyDiscovery(t.Context(), id, browser[:], foreign[:], now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong code accepted")
		}
	}
	if err := s.VerifyDiscovery(t.Context(), id, browser[:], code[:], now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyDiscovery(t.Context(), id, browser[:], code[:], now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatal("proof replay accepted")
	}
	email, err := s.DiscoveryEmail(t.Context(), id, browser[:], now.Add(time.Second))
	if err != nil || email != "proof@example.test" {
		t.Fatal("verified recovery failed", err)
	}
	if _, err = s.DiscoveryEmail(t.Context(), id, browser[:], now.Add(10*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired proof accepted")
	}
}
func TestDiscoveryConcurrentProofAndRecipientBudget(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	browser := sha256.Sum256([]byte("browser"))
	code := sha256.Sum256([]byte("code"))
	source := sha256.Sum256([]byte(uuid.NewString()))
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			id, _ := uuid.NewV7()
			err := s.ReserveDiscovery(context.Background(), id, browser[:], source[:], "budget@example.test", code[:], now)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrDiscoveryLimited) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatalf("recipient hourly budget admitted %d", accepted.Load())
	}
	id, _ := uuid.NewV7()
	if err := s.ReserveDiscovery(t.Context(), id, browser[:], source[:], "concurrent@example.test", code[:], now); err != nil {
		t.Fatal(err)
	}
	accepted.Store(0)
	for range 8 {
		wg.Go(func() {
			if s.VerifyDiscovery(context.Background(), id, browser[:], code[:], now.Add(time.Second)) == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("proof accepted %d times", accepted.Load())
	}
}

func TestDiscoveryDailyRecipientAndHourlySourceLimits(t *testing.T) {
	s := newStore(t)
	base := time.Now().UTC().Add(-20 * time.Hour).Truncate(time.Microsecond)
	browser := sha256.Sum256([]byte("browser"))
	code := sha256.Sum256([]byte("code"))
	source := sha256.Sum256([]byte("source"))
	reserve := func(email string, at time.Time) error {
		id, _ := uuid.NewV7()
		return s.ReserveDiscovery(t.Context(), id, browser[:], source[:], email, code[:], at)
	}
	for i := 0; i < 10; i++ {
		if err := reserve("daily@example.test", base.Add(time.Duration(i)*2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := reserve("daily@example.test", base.Add(20*time.Hour)); !errors.Is(err, ErrDiscoveryLimited) {
		t.Fatal("daily recipient limit not enforced", err)
	}
	later := base.Add(26 * time.Hour)
	for i := 0; i < 10; i++ {
		if err := reserve(fmt.Sprintf("source-%d@example.test", i), later); err != nil {
			t.Fatal(err)
		}
	}
	if err := reserve("source-over@example.test", later); !errors.Is(err, ErrDiscoveryLimited) {
		t.Fatal("hourly source limit not enforced", err)
	}
}
func TestDiscoveryFifthWrongCodeExhaustsProofAndExpiryAlwaysRefuses(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	id, _ := uuid.NewV7()
	browser := sha256.Sum256([]byte("browser"))
	code := sha256.Sum256([]byte("right"))
	wrong := sha256.Sum256([]byte("wrong"))
	source := sha256.Sum256([]byte("source"))
	if err := s.ReserveDiscovery(t.Context(), id, browser[:], source[:], "exhausted@example.test", code[:], now); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := s.VerifyDiscovery(t.Context(), id, browser[:], wrong[:], now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
			t.Fatal("wrong code admitted", err)
		}
	}
	if err := s.VerifyDiscovery(t.Context(), id, browser[:], code[:], now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatal("exhausted proof admitted", err)
	}
	id, _ = uuid.NewV7()
	if err := s.ReserveDiscovery(t.Context(), id, browser[:], source[:], "expired@example.test", code[:], now); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyDiscovery(t.Context(), id, browser[:], code[:], now.Add(10*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired proof admitted", err)
	}
}
