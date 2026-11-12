package onboarding

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

func TestCustomerOwnerStatusKeepsReceiptAndOwnerScope(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	ctx := context.Background()
	id := uuid.New()
	hash := sha256.Sum256([]byte("synthetic-status-receipt"))
	address := customercontract.BusinessAddress{Line1: "10 Test Road", City: "Bhubaneswar", District: "Khordha", PostalCode: "751001", StateCode: customercontract.Odisha, CountryCode: customercontract.India}
	in := customercontract.Signup{Setup: customercontract.Setup{Name: "QA owner status", Kind: customercontract.Pharmacy, CountryCode: customercontract.India, StateCode: customercontract.Odisha}, Owner: &identity.OrganizationInitialAdmin{Email: "owner-status@example.test", GivenName: "Owner", FamilyName: "Status"}}
	in.Profile = &customercontract.OrganizationProfile{Contact: customercontract.BusinessContact{Phone: "+919876543210", Email: "office@example.test"}, HeadOffice: address, MainBranch: customercontract.MainBranch{Name: "Main branch", Address: address}}
	_, err := svc.StartCustomer(ctx, id, hash[:], in)
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.CustomerSignup(ctx, id, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CustomerForOwner(ctx, id, hash[:], row.OwnerSubject); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"owner-foreign", "", "../other"} {
		if _, err := svc.CustomerForOwner(ctx, id, hash[:], owner); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign owner status disclosed: %q %v", owner, err)
		}
	}
	foreign := sha256.Sum256([]byte("foreign-receipt"))
	if _, err := svc.CustomerForOwner(ctx, id, foreign[:], row.OwnerSubject); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign receipt disclosed status: %v", err)
	}
	after, err := st.CustomerSignup(ctx, id, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if after.State != row.State || after.ReceiptVerified != row.ReceiptVerified || after.PasswordConfigured != row.PasswordConfigured {
		t.Fatal("status read changed signup proof state")
	}
}
