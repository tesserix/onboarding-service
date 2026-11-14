package customercontract

import (
	"encoding/json"
	"testing"
)

func TestLegacySelfProjectionPreservesScopeWithoutNewGrantAdmission(t *testing.T) {
	for _, purpose := range []string{"status", "profile"} {
		t.Run(purpose, func(t *testing.T) {
			view := IdentityProjection{Purpose: purpose, TenantID: "11111111-1111-4111-8111-111111111111", OrganizationID: "123456789", Subject: "retained-user", State: "active", Admission: "unassessed"}
			if purpose == "profile" {
				view.Profile = &IdentityProfile{DisplayName: "TEST Retained User", Email: "retained@example.test"}
			}
			raw, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = DecodeIdentityProjection(raw); err != nil {
				t.Fatal("an owner-verified retained organisation could not serve the current caller", err)
			}
			view.OrganizationID = "helivanta-22222222-2222-4222-8222-222222222222"
			if view.Validate() == nil {
				t.Fatal("a deterministic organisation for another tenant was accepted")
			}
			view.OrganizationID = "123456789/foreign"
			if view.Validate() == nil {
				t.Fatal("an unsafe native organisation identifier was accepted")
			}
		})
	}
	for _, admission := range []string{"eligible", "ineligible"} {
		view := IdentityProjection{Purpose: "membership", TenantID: "11111111-1111-4111-8111-111111111111", OrganizationID: "123456789", Subject: "retained-user", State: "active", Admission: admission}
		if view.Validate() == nil {
			t.Fatal("legacy self-read compatibility widened new-grant admission", admission)
		}
	}
}
