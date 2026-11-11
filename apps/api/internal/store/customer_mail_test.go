package store

import (
	"errors"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"testing"
	"time"
)

func TestInitialEmailAcceptanceRequiresCurrentLeaseAndIsImmutable(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	if _, err := s.ReserveCustomerSignup(t.Context(), in, audit.Event{ActorType: audit.ActorSystem, ActorID: "mail-test", Outcome: audit.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	rows, err := s.LeaseCustomerSignups(t.Context(), 1, now)
	if err != nil || len(rows) != 1 {
		t.Fatalf("missing lease: %v", err)
	}
	if err = s.FinishCustomerSignup(t.Context(), rows[0], "identity_requested", "", now); err != nil {
		t.Fatal(err)
	}
	rows, err = s.LeaseCustomerSignups(t.Context(), 1, now.Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatalf("missing mail lease: %v", err)
	}
	stale := rows[0]
	foreign := uuid.New()
	stale.LeaseID = &foreign
	if err = s.AcceptCustomerEmail(t.Context(), stale, "provider-message"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale worker recorded acceptance", err)
	}
	if err = s.AcceptCustomerEmail(t.Context(), rows[0], "provider-message"); err != nil {
		t.Fatal(err)
	}
	got, err := s.CustomerSignup(t.Context(), in.ID, in.ReceiptHash)
	if err != nil || got.EmailAcceptedAt == nil || got.EmailProviderID != "provider-message" {
		t.Fatal("lost mail acceptance", err)
	}
	if err = s.AcceptCustomerEmail(t.Context(), rows[0], "replacement"); !errors.Is(err, ErrConflict) {
		t.Fatal("acceptance replaced", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE customer_signups SET email_accepted_at=NULL,email_provider_id='' WHERE id=$1`, in.ID); err == nil {
		t.Fatal("database erased acceptance")
	}
	var count int
	if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='customer.signup.email_accepted'`, in.ID.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing attributable acceptance: %d %v", count, err)
	}
}

func TestSignupMailDailyBudgetIncludesEveryDeliveryAndRecoversAfter24Hours(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	if _, err := s.ReserveCustomerSignup(t.Context(), in, audit.Event{ActorType: audit.ActorSystem, ActorID: "signup-test", Outcome: audit.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for n := range 10 {
		if err := s.ConsumeSignupMail(t.Context(), in.ID, now.Add(time.Duration(n)*time.Hour)); err != nil {
			t.Fatalf("delivery %d: %v", n+1, err)
		}
	}
	if err := s.ConsumeSignupMail(t.Context(), in.ID, now.Add(10*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("eleventh email allowed inside 24 hours: %v", err)
	}
	if err := s.ConsumeSignupMail(t.Context(), in.ID, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("24 hour boundary did not release allowance: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='customer.signup.mail_reserved'`, in.TenantID).Scan(&count); err != nil || count != 11 {
		t.Fatalf("delivery reservations not attributable: count=%d err=%v", count, err)
	}
}

func TestConcurrentSignupMailCannotExceedLastDailyAllowance(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	if _, err := s.ReserveCustomerSignup(t.Context(), in, audit.Event{ActorType: audit.ActorSystem, ActorID: "signup-test", Outcome: audit.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for n := range 9 {
		if err := s.ConsumeSignupMail(t.Context(), in.ID, now.Add(time.Duration(n)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 8)
	for range 8 {
		go func() { results <- s.ConsumeSignupMail(t.Context(), in.ID, now.Add(10*time.Hour)) }()
	}
	accepted := 0
	for range 8 {
		err := <-results
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatalf("last daily allowance admitted %d concurrent requests", accepted)
	}
}
