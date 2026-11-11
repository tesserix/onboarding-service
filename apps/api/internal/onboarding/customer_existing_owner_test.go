package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestPublicSignupCannotAdoptForeignAccountEvenWhenEmailMatches(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	row := newCustomerMailRow(t, svc, st)
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("foreign identity must be rejected before native lookup")
		w.WriteHeader(500)
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	next := sha256.Sum256([]byte("next-receipt"))
	proof := customercontract.ExistingOwnerInput{Subject: "foreign-owner", AuthTime: time.Now().Unix(), NextReceiptHash: hex.EncodeToString(next[:])}
	if _, err := svc.ContinueCustomer(t.Context(), row.ID, row.ReceiptHash, proof); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign account adopted: %v", err)
	}
	current, err := st.CustomerSignup(t.Context(), row.ID, row.ReceiptHash)
	if err != nil || current.ExistingOwnerSubject != "" || current.OwnerSubject != row.OwnerSubject || current.ReceiptVerified {
		t.Fatalf("foreign proof changed signup: %v", err)
	}
}

func TestNewCustomerCanContinueWithTheirOwnVerifiedNativeSignIn(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	row := newCustomerMailRow(t, svc, st)
	verified := false
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/users" {
			_, _ = w.Write([]byte(`{"details":{"totalResult":"0"},"result":[]}`))
			return
		}
		if r.URL.Path != "/v2/users/"+row.OwnerSubject {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": row.OwnerSubject, "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": "helivanta-" + row.TenantID.String()}, "human": map[string]any{"email": map[string]any{"email": row.Input.Owner.Email, "isVerified": verified}}}})
	}))
	t.Cleanup(native.Close)
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic-native-token"), "platform", nil)
	if err := svc.DeliverCustomers(t.Context(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	next := sha256.Sum256([]byte("next-native-receipt"))
	proof := customercontract.ExistingOwnerInput{Subject: row.OwnerSubject, AuthTime: time.Now().Unix(), NextReceiptHash: hex.EncodeToString(next[:])}
	if _, err := svc.ContinueCustomer(t.Context(), row.ID, row.ReceiptHash, proof); !errors.Is(err, ErrInvalid) {
		t.Fatal("unverified native sign-in did not require verification", err)
	}
	verified = true
	if _, err := svc.ContinueCustomer(t.Context(), row.ID, row.ReceiptHash, proof); err != nil {
		t.Fatal("verified new owner could not continue", err)
	}
	current, err := st.CustomerSignup(t.Context(), row.ID, next[:])
	if err != nil || !current.ReceiptVerified || !current.PasswordConfigured || current.ExistingOwnerSubject != "" || current.OwnerSubject != row.OwnerSubject || current.ResourceID != row.ResourceID {
		t.Fatal("new owner changed mode or lost workspace", err)
	}
}
