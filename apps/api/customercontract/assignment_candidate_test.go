package customercontract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssignmentCandidateIdentityHasItsOwnPurposeAndNoGrantAuthority(t *testing.T) {
	in := IdentityProjectionInput{Purpose: "assignment_candidate", TenantID: "11111111-1111-4111-8111-111111111111", Subject: "synthetic-human", RequestID: "22222222-2222-4222-8222-222222222222"}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	in.IncludeTenantName = true
	if in.Validate() == nil {
		t.Fatal("candidate selection accepted unrelated tenant-name disclosure")
	}
	base := IdentityProjection{Purpose: "assignment_candidate", TenantID: in.TenantID, OrganizationID: "helivanta-" + in.TenantID, Subject: in.Subject, State: "active", Admission: "unassessed", Candidate: &AssignmentCandidateIdentity{DisplayName: "Synthetic Staff", Email: "synthetic@example.invalid"}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*IdentityProjection){
		"self profile purpose": func(v *IdentityProjection) { v.Purpose = "profile" },
		"membership purpose":   func(v *IdentityProjection) { v.Purpose = "membership"; v.Admission = "eligible" },
		"inactive":             func(v *IdentityProjection) { v.State = "inactive" },
		"grant admission":      func(v *IdentityProjection) { v.Admission = "eligible" },
		"unrelated profile": func(v *IdentityProjection) {
			v.Profile = &IdentityProfile{DisplayName: "Private", Email: "private@example.invalid"}
		},
		"unrelated tenant name":  func(v *IdentityProjection) { v.TenantName = "Private Tenant" },
		"foreign tenant binding": func(v *IdentityProjection) { v.OrganizationID = "helivanta-22222222-2222-4222-8222-222222222222" },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			if value.Validate() == nil {
				t.Fatal("invalid candidate authority accepted")
			}
		})
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeIdentityProjection(raw); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(string(raw), `"displayName":`, `"DisplayName":`, 1),
		strings.Replace(string(raw), `"displayName":`, `"displayName":"Other","displayName":`, 1),
		strings.Replace(string(raw), `"email":"synthetic@example.invalid"`, `"email":null`, 1),
		strings.Replace(string(raw), `"candidate":{`, `"candidate":{"role":"tenant_admin",`, 1),
	} {
		if _, err := DecodeIdentityProjection([]byte(invalid)); err == nil {
			t.Fatal("ambiguous candidate response accepted")
		}
	}
}
