package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tesserix/onboarding-service/api/customercontract"
)

func TestStaffMFAContractPreservesExactSafeVersionsAndBrowserScope(t *testing.T) {
	schema, err := contractSchema(reflect.TypeFor[customercontract.StaffMFAResetInput](), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"expectedLinkVersion", "expectedStaffVersion"} {
		if !strings.Contains(schema, `"`+name+`":z.number().int().safe().min(1).max(9007199254740991)`) {
			t.Fatal("Staff version lost canonical numeric bounds", schema)
		}
	}
	if strings.Contains(schema, `"subject"`) || strings.Contains(schema, `"actorSubject"`) || strings.Contains(schema, `"target"`) {
		t.Fatal("browser intent contains server-resolved account authority")
	}
	generated, err := generateContracts()
	if err != nil || !strings.Contains(generated, "StaffMFAResetInputContract = "+schema) {
		t.Fatal("Staff reset browser contract is not registered", err)
	}
}
