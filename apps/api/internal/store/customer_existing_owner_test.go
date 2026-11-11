package store

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"reflect"
	"testing"
	"time"
)

func pendingExistingSignup(t *testing.T, s *Store) CustomerSignup {
	t.Helper()
	in := signupCommand(t)
	in.Input.Owner.Email = "existing@example.test"
	raw, err := json.Marshal(in.Input)
	if err != nil {
		t.Fatal(err)
	}
	in.IntentHash = digest(string(raw))
	in.OwnerEmailHash = digest(in.Input.Owner.Email)
	_, err = s.ReserveCustomerSignup(t.Context(), in, audit.Event{ActorType: audit.ActorSystem, ActorID: "signup-test", Outcome: audit.OutcomeSuccess})
	if err != nil {
		t.Fatal(err)
	}
	// Retain a pre-change unbound intake to exercise legacy evidence guards.
	if _, err = s.pool.Exec(t.Context(), `UPDATE customer_signups SET state='existing_account' WHERE id=$1`, in.ID); err != nil {
		t.Fatal(err)
	}
	row, err := s.CustomerSignup(t.Context(), in.ID, in.ReceiptHash)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestExistingOwnerBindingRotatesReceiptPreservesIntentAndSurvivesReplay(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := pendingExistingSignup(t, s)
	proof := customercontract.ExistingOwnerInput{Subject: "existing-native-owner", AuthTime: time.Now().Unix(), NextReceiptHash: hex.EncodeToString(digest("next-receipt"))}
	got, err := s.ConfirmExistingOwner(t.Context(), in.ID, in.ReceiptHash, proof)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExistingOwnerSubject != proof.Subject {
		t.Fatal("existing owner was not bound")
	}
	if got.ExistingOwnerAuthTime == nil {
		t.Fatal("authentication evidence was not recorded")
	}
	if !got.ReceiptVerified {
		t.Fatal("receipt was not verified")
	}
	if !got.PasswordConfigured {
		t.Fatal("native authentication was not retained")
	}
	if got.State != "recorded" {
		t.Fatal("signup did not resume")
	}

	if got.OwnerSubject != in.OwnerSubject || got.TenantID != in.TenantID || got.ResourceID != in.ResourceID || !reflect.DeepEqual(got.Input, in.Input) {
		t.Fatal("continuation replaced immutable intent")
	}
	if _, err = s.CustomerSignup(t.Context(), in.ID, in.ReceiptHash); !errors.Is(err, ErrNotFound) {
		t.Fatal("old receipt still reads", err)
	}
	// A fresh authentication by the bound owner repairs a lost response without rebinding.
	proof.NextReceiptHash = hex.EncodeToString(digest("retry-receipt"))
	if _, err = s.ConfirmExistingOwner(t.Context(), in.ID, in.ReceiptHash, proof); err != nil {
		t.Fatal("lost response cannot recover", err)
	}
	proof.Subject = "different-owner"
	proof.NextReceiptHash = hex.EncodeToString(digest("foreign-next"))
	if _, err = s.ConfirmExistingOwner(t.Context(), in.ID, digest("retry-receipt"), proof); !errors.Is(err, ErrConflict) {
		t.Fatal("owner replacement accepted", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE customer_signups SET existing_owner_subject='different-owner' WHERE id=$1`, in.ID); err == nil {
		t.Fatal("database allowed owner replacement")
	}
	second := pendingExistingSignup(t, s)
	proof.Subject = "existing-native-owner"
	proof.NextReceiptHash = hex.EncodeToString(digest("second-workspace"))
	if _, err = s.ConfirmExistingOwner(t.Context(), second.ID, second.ReceiptHash, proof); err != nil {
		t.Fatal("same owner cannot own another workspace", err)
	}
	var count int
	if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='customer.signup.existing_owner.confirmed'`, in.ID.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("confirmation audit %d: %v", count, err)
	}
}

func TestExistingOwnerBindingRejectsStaleProofAndForeignReceipt(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "helivanta")
	in := pendingExistingSignup(t, s)
	proof := customercontract.ExistingOwnerInput{Subject: "existing-owner", AuthTime: time.Now().Add(-time.Hour).Unix(), NextReceiptHash: hex.EncodeToString(digest("next"))}
	if _, err := s.ConfirmExistingOwner(t.Context(), in.ID, in.ReceiptHash, proof); !errors.Is(err, ErrConflict) {
		t.Fatal("stale authentication accepted", err)
	}
	proof.AuthTime = time.Now().Add(time.Hour).Unix()
	if _, err := s.ConfirmExistingOwner(t.Context(), in.ID, in.ReceiptHash, proof); !errors.Is(err, ErrConflict) {
		t.Fatal("future authentication accepted", err)
	}
	proof.AuthTime = time.Now().Unix()
	if _, err := s.ConfirmExistingOwner(t.Context(), in.ID, digest("foreign"), proof); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign receipt accepted", err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE customer_signups SET existing_owner_subject='existing-owner',existing_owner_auth_time=now(),state='recorded',receipt_verified=true,primary_credential_configured=true,receipt_hash=$2 WHERE id=$1`, in.ID, digest("unverified")); err == nil {
		t.Fatal("database accepted unverified binding")
	}
}
