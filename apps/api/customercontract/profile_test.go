package customercontract_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"strings"
	"testing"
)

const profileSignup = `{"name":"TEST Hospital","kind":"hospital","countryCode":"IN","stateCode":"MH","owner":{"givenName":"Test","familyName":"Owner","email":"owner@example.test"},"profile":{"contact":{"phone":"+919876543210","email":"office@example.test"},"headOffice":{"line1":"10 Test Road","line2":"Test Building","city":"Pune","district":"Pune","postalCode":"411001","stateCode":"MH","countryCode":"IN"},"mainBranch":{"name":"Pune Main Branch","address":{"line1":"20 Branch Road","line2":"","city":"Pune","district":"Pune","postalCode":"411002","stateCode":"MH","countryCode":"IN"}}}}`

func TestSignupValidatesBusinessContactAndBothAddresses(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"phone", "+919876543210", "invalid"},
		{"business_email", "office@example.test", "invalid"},
		{"email_without_public_domain", "office@example.test", "office@example"},
		{"address_bom", "10 Test Road", "\ufeff10 Test Road"},
		{"postal_code", "411001", "000000"},
		{"main_branch_postal", "411002", "41100"},
		{"empty_hq", "10 Test Road", ""},
		{"empty_branch_name", "Pune Main Branch", ""},
		{"blank_city", "Pune\"", " \""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in customercontract.Signup
			if err := json.Unmarshal([]byte(strings.Replace(profileSignup, tc.before, tc.after, 1)), &in); err != nil {
				t.Fatal(err)
			}
			if in.Validate(uuid.New()) == nil {
				t.Fatal("invalid business profile accepted")
			}
		})
	}
}

func TestProfileSurvivesWireRoundTripAndLegacyIntentsRemainReadable(t *testing.T) {
	var in customercontract.Signup
	if err := json.Unmarshal([]byte(profileSignup), &in); err != nil {
		t.Fatal(err)
	}
	if err := in.Validate(uuid.New()); err != nil {
		t.Fatal(err)
	}
	if in.Profile == nil || in.Profile.HeadOffice.Line1 != "10 Test Road" || in.Profile.MainBranch.Address.Line1 != "20 Branch Road" {
		t.Fatal("distinct address intent was lost")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var decoded customercontract.Signup
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Profile.Contact.Email != "office@example.test" || decoded.Profile.Contact.Phone != "+919876543210" {
		t.Fatal("business contact was lost")
	}
	for _, kind := range []customercontract.Kind{customercontract.Hospital, customercontract.Pharmacy} {
		decoded.Kind = kind
		if err = decoded.Validate(uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	decoded.Profile.HeadOffice.StateCode = customercontract.Odisha
	if decoded.Validate(uuid.New()) == nil {
		t.Fatal("head-office jurisdiction does not match signup")
	}
	legacy := validSignup()
	if err = legacy.Validate(uuid.New()); err != nil {
		t.Fatal("historic intent cannot be replayed", err)
	}
}

func TestAddressMaximumCountsUnicodeCodePoints(t *testing.T) {
	var in customercontract.Signup
	if err := json.Unmarshal([]byte(profileSignup), &in); err != nil {
		t.Fatal(err)
	}
	in.Profile.HeadOffice.City = strings.Repeat("😀", 100)
	if err := in.Validate(uuid.New()); err != nil {
		t.Fatal("100 Unicode code points rejected", err)
	}
	in.Profile.HeadOffice.City += "😀"
	if in.Validate(uuid.New()) == nil {
		t.Fatal("101 Unicode code points accepted")
	}
}
