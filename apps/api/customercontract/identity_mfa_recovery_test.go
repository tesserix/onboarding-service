package customercontract

import (
	"strings"
	"testing"
)

func TestMFARecoveryRequirementIsExplicitAndRestrictedToCredentialStatus(t *testing.T) {
	in := IdentityProjectionInput{Purpose: "status", TenantID: "11111111-1111-4111-8111-111111111111", Subject: "staff", RequestID: "11111111-1111-4111-8111-111111111112", IncludeCredentialState: true, IncludeMFARecovery: true}
	if in.Validate() != nil {
		t.Fatal("explicit recovery read refused")
	}
	in.IncludeCredentialState = false
	if in.Validate() == nil {
		t.Fatal("recovery without credentials accepted")
	}
	in.IncludeCredentialState = true
	for _, purpose := range []string{"profile", "membership", "assignment_candidate"} {
		in.Purpose = purpose
		if in.Validate() == nil {
			t.Fatal("recovery escaped status purpose")
		}
	}
	const base = `{"purpose":"status","tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"helivanta-11111111-1111-4111-8111-111111111111","subject":"staff","state":"active","admission":"unassessed","credentials":{"passwordConfigured":true,"passwordChangedAt":"2026-11-18T10:11:12Z"}`
	const generation = "019f39c2-1738-7998-b5de-6d4214f614e0"
	for _, state := range []string{"none", "blocked", "enrollment_required", "completed"} {
		requirement := `{"state":"` + state + `"}`
		if state != "none" {
			requirement = strings.TrimSuffix(requirement, "}") + `,"generationId":"` + generation + `"}`
		}
		out, err := DecodeIdentityProjection([]byte(base + `,"mfaRecovery":` + requirement + `}`))
		if err != nil || out.MFARecovery == nil || out.MFARecovery.State != state {
			t.Fatal("valid recovery requirement refused", err)
		}
	}
	for _, requirement := range []string{
		`null`, `{}`, `{"state":"none","generationId":"` + generation + `"}`, `{"state":"none","generationId":""}`, `{"state":"completed"}`,
		`{"state":"completed","generationId":"11111111-1111-4111-8111-111111111111"}`,
		`{"state":"success","generationId":"` + generation + `"}`,
		`{"state":"blocked","generationId":"` + generation + `","tenantId":"private"}`,
		`{"state":"none","state":"completed"}`, `{"State":"none"}`, `{"state":"none","generationId":null}`,
	} {
		if _, err := DecodeIdentityProjection([]byte(base + `,"mfaRecovery":` + requirement + `}`)); err == nil {
			t.Fatal("unproven requirement accepted", requirement)
		}
	}
	good := base + `,"mfaRecovery":{"state":"none"}}`
	for _, bad := range []string{
		strings.Replace(good, `,"credentials":{"passwordConfigured":true,"passwordChangedAt":"2026-11-18T10:11:12Z"}`, "", 1),
		strings.Replace(good, `"state":"active"`, `"state":"inactive"`, 1),
		strings.Replace(good, `"purpose":"status"`, `"purpose":"profile"`, 1),
	} {
		if _, err := DecodeIdentityProjection([]byte(bad)); err == nil {
			t.Fatal("recovery escaped disclosure scope")
		}
	}
}
