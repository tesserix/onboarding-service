package onboarding

import (
	"crypto/sha256"
	"errors"
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"testing"
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
