package customercontract_test

import (
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"strings"
	"testing"
)

func TestSignupRejectsUnboundedOrUnsupportedIntentBeforeProvisioning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*customercontract.Signup)
	}{
		{"blank_name", func(s *customercontract.Signup) { s.Name = " " }},
		{"long_name", func(s *customercontract.Signup) { s.Name = strings.Repeat("a", 151) }},
		{"control_name", func(s *customercontract.Signup) { s.Name = "Hospital\nOther" }},
		{"padded_name", func(s *customercontract.Signup) { s.Name = " Hospital" }},
		{"unsupported_kind", func(s *customercontract.Signup) { s.Kind = "clinic" }},
		{"unsupported_country", func(s *customercontract.Signup) { s.CountryCode = "US" }},
		{"unsupported_state", func(s *customercontract.Signup) { s.StateCode = "KA" }},
		{"missing_owner", func(s *customercontract.Signup) { s.Owner = nil }},
		{"invalid_email", func(s *customercontract.Signup) { s.Owner.Email = "invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validSignup()
			tc.change(&s)
			if s.Validate(uuid.New()) == nil {
				t.Fatal("unsafe signup accepted")
			}
		})
	}
	for _, kind := range []customercontract.Kind{customercontract.Hospital, customercontract.Pharmacy} {
		for _, state := range []customercontract.StateCode{customercontract.Maharashtra, customercontract.Odisha} {
			s := validSignup()
			s.Kind = kind
			s.StateCode = state
			if err := s.Validate(uuid.New()); err != nil {
				t.Fatalf("supported signup rejected: %s %s %v", kind, state, err)
			}
		}
	}
	if validSignup().Validate(uuid.Nil) == nil {
		t.Fatal("missing request identity accepted")
	}
}
func validSignup() customercontract.Signup {
	return customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Hospital", Kind: "hospital", CountryCode: "IN", StateCode: "MH"}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: "owner@example.test"}}
}

func TestReadyReceiptRequiresCompleteInitialWorkspaceBinding(t *testing.T) {
	receipt := customercontract.Receipt{State: customercontract.Ready}
	if receipt.Validate() == nil {
		t.Fatal("ready accepted without completed tenant and resource")
	}
	receipt.Name = "TEST Hospital"
	receipt.Kind = customercontract.Hospital
	receipt.TenantID = uuid.NewString()
	receipt.ResourceID = uuid.Must(uuid.NewV7()).String()
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt.ResourceID = "not-a-resource"
	if receipt.Validate() == nil {
		t.Fatal("ready accepted with malformed resource identity")
	}
}

func TestWorkspaceRequiresExactNewOwnerBindingAndStableV7Resource(t *testing.T) {
	tenant := uuid.New()
	good := customercontract.Workspace{TenantID: tenant.String(), OrganizationID: "helivanta-" + tenant.String(), OwnerSubject: "owner-" + tenant.String(), ResourceID: uuid.Must(uuid.NewV7()).String(), Setup: customercontract.Setup{Name: "TEST Hospital", Kind: customercontract.Hospital, CountryCode: customercontract.India, StateCode: customercontract.Odisha}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*customercontract.Workspace){
		func(w *customercontract.Workspace) { w.OrganizationID = "other" },
		func(w *customercontract.Workspace) { w.OwnerSubject = "other" },
		func(w *customercontract.Workspace) { w.ResourceID = uuid.New().String() },
		func(w *customercontract.Workspace) { w.TenantID = "bad" },
		func(w *customercontract.Workspace) { w.StateCode = "XX" },
	} {
		changed := good
		change(&changed)
		if changed.Validate() == nil {
			t.Fatal("foreign or invalid workspace binding accepted")
		}
	}
}
