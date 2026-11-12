package store

import (
	"context"
	"crypto/sha256"
	"errors"
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
