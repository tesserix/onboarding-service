package customercontract_test

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"testing"
)

func TestWorkspaceExistingOwnerRequiresExplicitPrivateBinding(t *testing.T) {
	tenant := uuid.NewString()
	resource := uuid.Must(uuid.NewV7()).String()
	for _, tc := range []struct {
		existing bool
		subject  string
		valid    bool
	}{
		{true, "native-owner-123", true}, {false, "native-owner-123", false},
		{true, "bad subject", false}, {true, "", false}, {false, "owner-" + tenant, true},
	} {
		var in customercontract.Workspace
		raw := fmt.Sprintf(`{"tenantId":%q,"organizationId":%q,"resourceId":%q,"ownerSubject":%q,"existingOwner":%t,"name":"TEST Hospital","kind":"hospital","countryCode":"IN","stateCode":"MH"}`, tenant, "helivanta-"+tenant, resource, tc.subject, tc.existing)
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if got := in.Validate() == nil; got != tc.valid {
			t.Fatalf("existing=%v subject=%q accepted=%v", tc.existing, tc.subject, got)
		}
	}
}
