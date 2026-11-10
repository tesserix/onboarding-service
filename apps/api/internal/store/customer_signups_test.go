package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"testing"
	"time"
)

func TestCustomerSignupKeepsIntentResourceAndReceiptImmutableAcrossReplay(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	event := audit.Event{ActorType: audit.ActorSystem, ActorID: "system:serviceaccount:helivanta:helivanta-api", ProductKey: "helivanta", OrgID: in.TenantID.String(), Action: "customer.signup.requested", Outcome: audit.OutcomeSuccess, RequestID: in.ID.String()}
	got, err := s.ReserveCustomerSignup(t.Context(), in, event)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "recorded" || got.Input.Name != in.Input.Name || got.OwnerSubject != in.OwnerSubject || got.ResourceID != in.ResourceID {
		t.Fatalf("lost signup intent: %+v", got)
	}
	replay := in
	replay.ResourceID = uuid.Must(uuid.NewV7())
	got, err = s.ReserveCustomerSignup(t.Context(), replay, event)
	if err != nil || got.ResourceID != in.ResourceID {
		t.Fatalf("replay changed resource identity: %v", err)
	}
	changed := in
	changed.IntentHash = digest("changed")
	if _, err = s.ReserveCustomerSignup(t.Context(), changed, event); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed intent accepted: %v", err)
	}
	changed = in
	changed.ReceiptHash = digest("foreign")
	if _, err = s.ReserveCustomerSignup(t.Context(), changed, event); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign receipt disclosed intent: %v", err)
	}
	duplicate := signupCommand(t)
	duplicate.OwnerEmailHash = in.OwnerEmailHash
	got, err = s.ReserveCustomerSignup(t.Context(), duplicate, event)
	if err != nil || got.State != "existing_account" {
		t.Fatalf("duplicate email reserved a second identity: state=%s err=%v", got.State, err)
	}
	var events int
	if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='customer.signup.requested'`, in.ID.String()).Scan(&events); err != nil || events != 1 {
		t.Fatalf("non-idempotent audit: count=%d err=%v", events, err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE customer_signups SET owner_subject='foreign-owner' WHERE id=$1`, in.ID); err == nil {
		t.Fatal("database allowed owner replacement")
	}
}
func signupCommand(t *testing.T) CustomerSignup {
	t.Helper()
	id, tenant := uuid.New(), uuid.New()
	input := customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Hospital", Kind: "hospital", CountryCode: "IN", StateCode: "MH"}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: id.String() + "@example.test"}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return CustomerSignup{ID: id, TenantID: tenant, ResourceID: uuid.Must(uuid.NewV7()), OwnerSubject: "owner-" + tenant.String(), ReceiptHash: digest(id.String()), IntentHash: digest(string(raw)), OwnerEmailHash: digest(input.Owner.Email), Input: input, RetryAt: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
}
func digest(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }

func TestSignupReceiptCannotReadAnotherCommandOrItsOwnerDetails(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	_, err := s.ReserveCustomerSignup(t.Context(), in, audit.Event{ActorType: audit.ActorSystem, ActorID: "signup-test", Outcome: audit.OutcomeSuccess})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.CustomerSignup(t.Context(), in.ID, in.ReceiptHash)
	if err != nil || got.OwnerSubject != in.OwnerSubject || got.Input.Owner.Email != in.Input.Owner.Email {
		t.Fatalf("own receipt failed: %v", err)
	}
	for _, hash := range [][]byte{nil, digest("foreign")} {
		if _, err = s.CustomerSignup(t.Context(), in.ID, hash); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign receipt read owner intent: %v", err)
		}
	}
	if _, err = s.CustomerSignup(t.Context(), uuid.New(), in.ReceiptHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown command resolved: %v", err)
	}
	got, err = s.CustomerSignupByTenant(t.Context(), in.TenantID)
	if err != nil || got.ID != in.ID {
		t.Fatalf("callback lost durable resource mapping: %v", err)
	}
	got, err = s.CustomerSignupBySubject(t.Context(), in.OwnerSubject)
	if err != nil || got.ID != in.ID {
		t.Fatalf("native proof lost command mapping: %v", err)
	}
}

func TestExpiredSignupReceiptCannotResumeACommand(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	event := audit.Event{ActorType: audit.ActorSystem, ActorID: "signup-test", Outcome: audit.OutcomeSuccess}
	if _, err := s.ReserveCustomerSignup(t.Context(), in, event); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE customer_signups SET receipt_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CustomerSignup(t.Context(), in.ID, in.ReceiptHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired receipt read private intent: %v", err)
	}
	if _, err := s.ReserveCustomerSignup(t.Context(), in, event); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired receipt resumed the command: %v", err)
	}
}

func TestSignupDeliveryLeaseRejectsStaleCompletionAfterRecovery(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	if _, err := s.ReserveCustomerSignup(t.Context(), in, audit.Event{ActorType: audit.ActorSystem, ActorID: "signup-test", Outcome: audit.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	now := in.RetryAt
	leased, err := s.LeaseCustomerSignups(t.Context(), 1, now)
	if err != nil || len(leased) != 1 || leased[0].ID != in.ID || leased[0].LeaseID == nil {
		t.Fatalf("command was not leased: count=%d err=%v", len(leased), err)
	}
	duplicate, err := s.LeaseCustomerSignups(t.Context(), 1, now)
	if err != nil || len(duplicate) != 0 {
		t.Fatalf("concurrent delivery duplicated: count=%d err=%v", len(duplicate), err)
	}
	recovered, err := s.LeaseCustomerSignups(t.Context(), 1, now.Add(2*time.Minute))
	if err != nil || len(recovered) != 1 || recovered[0].LeaseID == nil || *recovered[0].LeaseID == *leased[0].LeaseID {
		t.Fatalf("expired lease was not fenced: count=%d err=%v", len(recovered), err)
	}
	if err = s.FinishCustomerSignup(t.Context(), leased[0], "ready", "", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale worker completed new lease: %v", err)
	}
	if err = s.FinishCustomerSignup(t.Context(), recovered[0], "identity_requested", "", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := s.CustomerSignupByTenant(t.Context(), in.TenantID)
	if err != nil || current.State != "identity_requested" || current.LeaseID != nil || current.ResourceID != in.ResourceID {
		t.Fatalf("delivery changed durable identity: state=%s err=%v", current.State, err)
	}
	if err = s.FinishCustomerSignup(t.Context(), recovered[0], "ready", "", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused completion accepted: %v", err)
	}
}

func TestSignupProofBudgetIsSharedBoundedAndResetsAfterWindow(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := signupCommand(t)
	if _, err := s.ReserveCustomerSignup(t.Context(), in, audit.Event{ActorType: audit.ActorSystem, ActorID: "signup-test", Outcome: audit.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := s.ConsumeSignupProof(t.Context(), in.ID, now); err != nil {
			t.Fatalf("allowed attempt %d rejected: %v", i, err)
		}
	}
	if err := s.ConsumeSignupProof(t.Context(), in.ID, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("sixth native proof attempt allowed: %v", err)
	}
	if err := s.ConsumeSignupProof(t.Context(), in.ID, now.Add(15*time.Minute)); err != nil {
		t.Fatalf("new proof window rejected: %v", err)
	}
	if err := s.ConsumeSignupProof(t.Context(), uuid.New(), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown signup consumed proof budget: %v", err)
	}
}
