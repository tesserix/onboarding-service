package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
)

func TestExistingCustomerMustProveMatchingNativeIdentityBeforeClaimCreation(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	var email atomic.Value
	email.Store("other@example.test")
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v2/users":
			_, _ = w.Write([]byte(`{"details":{"totalResult":"1"},"result":[{"userId":"existing-owner","human":{"email":{"email":"owner@example.test"}}}]}`))
		case r.Method == "GET" && r.URL.Path == "/v2/users/existing-owner":
			_, _ = w.Write([]byte(`{"user":{"userId":"existing-owner","state":"USER_STATE_ACTIVE","human":{"email":{"email":"` + email.Load().(string) + `","isVerified":true}}}}`))
		default:
			t.Errorf("unexpected native command %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(native.Close)
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic-native-token"), "platform", nil)
	svc.hospitals = &HospitalConfig{Product: "helivanta", Claims: hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")}
	id := uuid.New()
	hash := sha256.Sum256([]byte("existing-receipt"))
	next := sha256.Sum256([]byte("new-receipt"))
	input := customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Hospital", Kind: customercontract.Hospital, CountryCode: customercontract.India, StateCode: customercontract.Maharashtra}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: "owner@example.test"}}
	if _, err := svc.StartCustomer(t.Context(), id, hash[:], input); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverCustomers(t.Context(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	original, err := st.CustomerSignup(t.Context(), id, hash[:])
	if err != nil || original.State != "existing_account" {
		t.Fatalf("existing account not retained: %s %v", original.State, err)
	}
	proof := customercontract.ExistingOwnerInput{Subject: "existing-owner", AuthTime: time.Now().Unix(), NextReceiptHash: hex.EncodeToString(next[:])}
	if _, err = svc.ContinueCustomer(t.Context(), id, hash[:], proof); !errors.Is(err, ErrInvalid) {
		t.Fatal("different owner email accepted", err)
	}
	unchanged, err := st.CustomerSignup(t.Context(), id, hash[:])
	if err != nil || unchanged.ExistingOwnerSubject != "" {
		t.Fatal("failed identity proof changed intake", err)
	}
	email.Store("owner@example.test")
	receipt, err := svc.ContinueCustomer(t.Context(), id, hash[:], proof)
	if err != nil || receipt.State != customercontract.Provisioning {
		t.Fatalf("authenticated continuation failed: %v %v", receipt, err)
	}
	if err = svc.DeliverCustomers(t.Context(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	row, err := st.CustomerSignup(t.Context(), id, next[:])
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "identity_requested" || row.ExistingOwnerSubject != "existing-owner" || row.ResourceID != original.ResourceID || row.Input.Name != original.Input.Name || !row.ReceiptVerified || !row.PasswordConfigured {
		t.Fatalf("lost existing owner or original intent: %+v", row)
	}
	claim, err := svc.hospitals.Claims.Get(t.Context(), "helivanta", row.TenantID.String())
	if err != nil || claim.Spec.InitialAdminUserID != "existing-owner" || claim.Spec.InitialAdmin != nil {
		t.Fatalf("existing owner replaced with a new person: %+v %v", claim.Spec, err)
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
