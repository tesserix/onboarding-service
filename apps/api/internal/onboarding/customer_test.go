package onboarding

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestCustomerIntakeIsDurableReplayableAndNeverCreatesIdentityInline(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	id := uuid.New()
	hash := sha256.Sum256([]byte("test-private-receipt"))
	in := customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Pharmacy", Kind: customercontract.Pharmacy, CountryCode: customercontract.India, StateCode: customercontract.Odisha}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: "owner@example.test"}}
	receipt, err := svc.StartCustomer(t.Context(), id, hash[:], in)
	if err != nil || receipt.State != customercontract.Provisioning {
		t.Fatalf("intake not durably accepted: %v %v", receipt, err)
	}
	row, err := st.CustomerSignup(t.Context(), id, hash[:])
	if err != nil || row.Input.Kind != customercontract.Pharmacy || row.ResourceID.Version() != 7 {
		t.Fatalf("immutable workspace intent missing: %v", err)
	}
	before := row.ResourceID
	if _, err = svc.StartCustomer(t.Context(), id, hash[:], in); err != nil {
		t.Fatal(err)
	}
	row, err = st.CustomerSignup(t.Context(), id, hash[:])
	if err != nil || row.ResourceID != before {
		t.Fatalf("replay changed initial resource: %v", err)
	}
	in.Name = "Other"
	if _, err = svc.StartCustomer(t.Context(), id, hash[:], in); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed signup accepted: %v", err)
	}
	if len(idp.calls) != 0 {
		t.Fatal("intake made a non-durable native call")
	}
}

func TestCustomerDeliveryCreatesOnePrivateClaimAndWaitsForNativeProof(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v2/users" {
			t.Errorf("unexpected native delivery %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"details":{"totalResult":"0"},"result":[]}`))
	}))
	t.Cleanup(native.Close)
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("test-native-machine"), "test-platform", nil)
	svc.hospitals = &HospitalConfig{Product: "helivanta", Claims: hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")}
	id := uuid.New()
	hash := sha256.Sum256([]byte("test-receipt"))
	in := customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Hospital", Kind: customercontract.Hospital, CountryCode: customercontract.India, StateCode: customercontract.Maharashtra}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: "test@example.test"}}
	if _, err := svc.StartCustomer(t.Context(), id, hash[:], in); err != nil {
		t.Fatal(err)
	}
	row, err := st.CustomerSignup(t.Context(), id, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	now := row.RetryAt.Add(time.Second)
	if err = svc.DeliverCustomers(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	row, err = st.CustomerSignup(t.Context(), id, hash[:])
	if err != nil || row.State != "identity_requested" || row.Attempts != 0 {
		t.Fatalf("delivery did not wait for proof: state=%s attempts=%d err=%v", row.State, row.Attempts, err)
	}
	claim, err := svc.hospitals.Claims.Get(t.Context(), "helivanta", row.TenantID.String())
	if err != nil || claim.Spec.InitialAdminUserID != row.OwnerSubject || claim.Spec.InitialAdmin == nil || claim.Spec.InitialAdmin.Email != in.Owner.Email {
		t.Fatalf("native claim lost immutable owner: %v", err)
	}
	receipt, err := svc.Customer(t.Context(), id, hash[:])
	if err != nil || receipt.State == customercontract.Ready {
		t.Fatalf("unproven workspace became ready: %v", err)
	}
}

func TestBusinessInitializationRequiresCurrentNativeAuthorityBeforeCallingHelivanta(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	tenant := uuid.New()
	resource := uuid.Must(uuid.NewV7())
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(customercontract.Receipt{State: customercontract.Ready, Name: "TEST Hospital", Kind: customercontract.Hospital, TenantID: tenant.String(), ResourceID: resource.String()})
	}))
	t.Cleanup(target.Close)
	path := t.TempDir() + "/token"
	if err := os.WriteFile(path, []byte("synthetic-workload-token"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.hospitals = &HospitalConfig{HelivantaURL: target.URL + "/hospital-enrolments", HelivantaTokenFile: path}
	row := store.CustomerSignup{TenantID: tenant, ResourceID: resource, OwnerSubject: "owner-" + tenant.String(), Input: customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Hospital", Kind: customercontract.Hospital, CountryCode: customercontract.India, StateCode: customercontract.Maharashtra}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: "owner@example.test"}}}
	if err := svc.initializeCustomer(t.Context(), row); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale native ownership initialized a business: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("unverified owner reached business initialization")
	}
}
