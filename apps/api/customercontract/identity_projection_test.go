package customercontract

import (
	"strings"
	"testing"
)

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
