package customercontract

import (
	"strings"
	"testing"
)

func TestCredentialStateIsExplicitAndRestrictedToAnActiveStatusRead(t *testing.T) {
	in := IdentityProjectionInput{Purpose: "status", TenantID: "11111111-1111-4111-8111-111111111111", Subject: "staff", RequestID: "11111111-1111-4111-8111-111111111112", IncludeCredentialState: true}
	if in.Validate() != nil {
		t.Fatal("explicit status read refused")
	}
	for _, purpose := range []string{"profile", "membership", "assignment_candidate"} {
		in.Purpose = purpose
		if in.Validate() == nil {
			t.Fatal("credential state disclosed for another purpose")
		}
	}
	const base = `{"purpose":"status","tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"helivanta-11111111-1111-4111-8111-111111111111","subject":"staff","state":"active","admission":"unassessed"`
	for _, state := range []string{
		`{"passwordConfigured":false}`,
		`{"passwordConfigured":true,"passwordChangedAt":"2026-11-18T10:11:12Z"}`,
	} {
		if _, err := DecodeIdentityProjection([]byte(base + `,"credentials":` + state + `}`)); err != nil {
			t.Fatal("valid credential evidence refused", err)
		}
	}
	for _, state := range []string{
		`{}`, `null`,
		`{"passwordConfigured":null}`,
		`{"passwordConfigured":true}`,
		`{"passwordConfigured":false,"passwordChangedAt":"2026-11-18T10:11:12Z"}`,
		`{"passwordConfigured":true,"passwordChangedAt":"not-a-date"}`,
		`{"passwordConfigured":true,"passwordChangedAt":"0001-01-01T00:00:00Z"}`,
		`{"passwordConfigured":false,"PasswordConfigured":true}`,
		`{"passwordConfigured":false,"passwordConfigured":true}`,
	} {
		if _, err := DecodeIdentityProjection([]byte(base + `,"credentials":` + state + `}`)); err == nil {
			t.Fatal("unproven credential state accepted", state)
		}
	}
	good := base + `,"credentials":{"passwordConfigured":true,"passwordChangedAt":"2026-11-18T10:11:12Z"}}`
	for _, bad := range []string{
		strings.Replace(good, `"state":"active"`, `"state":"inactive"`, 1),
		strings.Replace(good, `"purpose":"status"`, `"purpose":"profile"`, 1),
	} {
		if _, err := DecodeIdentityProjection([]byte(bad)); err == nil {
			t.Fatal("credential state escaped its disclosure scope")
		}
	}
}
