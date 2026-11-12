package customercontract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIdentityProfileProjectionAcceptsCurrentTenantName(t *testing.T) {
	raw := `{"purpose":"profile","tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"helivanta-11111111-1111-4111-8111-111111111111","subject":"staff","state":"active","admission":"unassessed","profile":{"displayName":"Synthetic Staff","email":"synthetic@example.test"},"tenantName":"Yes Hospital"}`
	view, err := DecodeIdentityProjection([]byte(raw))
	if err != nil {
		t.Fatal("current tenant business name was refused", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil || !strings.Contains(string(encoded), `"tenantName":"Yes Hospital"`) {
		t.Fatal("current tenant business name was lost", err)
	}
}

func TestIdentityProfileTenantNameIsBoundedInUnicodeCodePoints(t *testing.T) {
	view := IdentityProjection{Purpose: "profile", TenantID: "11111111-1111-4111-8111-111111111111", OrganizationID: "helivanta-11111111-1111-4111-8111-111111111111", Subject: "staff", State: "active", Admission: "unassessed", Profile: &IdentityProfile{DisplayName: "Synthetic Staff", Email: "synthetic@example.test"}}
	for _, name := range []string{strings.Repeat("ଔ", 200), strings.Repeat("अ", 200), strings.Repeat("🏥", 200)} {
		view.TenantName = name
		if view.Validate() != nil {
			t.Fatal("valid 200-code-point tenant name refused")
		}
		view.TenantName = name + "x"
		if view.Validate() == nil {
			t.Fatal("201-code-point tenant name accepted")
		}
	}
}

func TestTenantNameDisclosureRequiresAnExplicitActiveProfileRead(t *testing.T) {
	in := IdentityProjectionInput{Purpose: "profile", TenantID: "11111111-1111-4111-8111-111111111111", Subject: "staff", RequestID: "11111111-1111-4111-8111-111111111112"}
	old, err := json.Marshal(in)
	if err != nil || strings.Contains(string(old), "includeTenantName") {
		t.Fatal("legacy request shape changed", err)
	}
	in.IncludeTenantName = true
	if in.Validate() != nil {
		t.Fatal("profile opt-in refused")
	}
	for _, purpose := range []string{"membership", "status"} {
		in.Purpose = purpose
		if in.Validate() == nil {
			t.Fatal("non-profile name disclosure accepted", purpose)
		}
	}
	view := IdentityProjection{Purpose: "profile", TenantID: in.TenantID, OrganizationID: "helivanta-" + in.TenantID, Subject: "staff", State: "active", Admission: "unassessed", Profile: &IdentityProfile{DisplayName: "Synthetic Staff", Email: "synthetic@example.test"}, TenantName: "Yes Hospital"}
	for _, name := range []string{" Yes Hospital", "Yes Hospital ", "Yes\nHospital", string([]byte{0xff})} {
		view.TenantName = name
		if view.Validate() == nil {
			t.Fatal("malformed tenant name accepted")
		}
	}
	view.TenantName = "Yes Hospital"
	view.Profile = nil
	if view.Validate() == nil {
		t.Fatal("name disclosed without an active owned profile")
	}
}

func TestPrivateIdentityProjectionKeepsStateSeparateFromNewGrantAdmission(t *testing.T) {
	in := IdentityProjectionInput{Purpose: "membership", TenantID: "11111111-1111-4111-8111-111111111111", Subject: "fresh-staff", RequestID: "11111111-1111-4111-8111-111111111112"}
	if in.Validate() != nil {
		t.Fatal("valid private target rejected")
	}
	view := IdentityProjection{Purpose: "profile", TenantID: in.TenantID, OrganizationID: "helivanta-" + in.TenantID, Subject: in.Subject, State: "active", Admission: "unassessed", Profile: &IdentityProfile{DisplayName: "Synthetic Staff", Email: "synthetic@example.test"}}
	if view.Validate() != nil {
		t.Fatal("own eligible profile rejected")
	}
	view.Purpose = "membership"
	view.Admission = "ineligible"
	if view.Validate() == nil {
		t.Fatal("ineligible identity disclosed profile")
	}
	view.Profile = nil
	if view.Validate() != nil {
		t.Fatal("minimal active legacy state rejected")
	}
	view.State = "inactive"
	view.Admission = "eligible"
	if view.Validate() == nil {
		t.Fatal("inactive identity accepted for new grants")
	}
	view.Admission = "ineligible"
	view.OrganizationID = "foreign"
	if view.Validate() == nil {
		t.Fatal("foreign tenant scope accepted")
	}
	in.Subject = "../../native/users?token=unsafe"
	if in.Validate() == nil {
		t.Fatal("unsafe identity source accepted")
	}
}

func TestIdentityProjectionDecoderRejectsAmbiguousOrForeignProfilePayloads(t *testing.T) {
	good := `{"purpose":"membership","tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"helivanta-11111111-1111-4111-8111-111111111111","subject":"staff","state":"active","admission":"eligible"}`
	if _, err := DecodeIdentityProjection([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(good, `"state":"active"`, `"state":"inactive","state":"active"`, 1),
		strings.Replace(good, `"state":"active"`, `"State":"active"`, 1),
		strings.Replace(good, `"state":"active"`, `"state":null`, 1),
		strings.Replace(good, `"state":"active"`, `"state":"unknown"`, 1),
		strings.TrimSuffix(good, "}") + `,"profile":null}`,
		strings.TrimSuffix(good, "}") + `,"secret":"not-a-field"}`,
		good + `{}`,
	} {
		if _, err := DecodeIdentityProjection([]byte(bad)); err == nil {
			t.Fatal("ambiguous private projection accepted")
		}
	}
}
