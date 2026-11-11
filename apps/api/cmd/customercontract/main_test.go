package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestBrowserContractsPreserveFlattenedIntentAndNativeOwnerFields(t *testing.T) {
	generated, err := generateContracts()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"owner":z.object({`, `"givenName":z.string()`, `"familyName":z.string()`, `"email":z.string()`, `"kind":z.enum(["hospital","pharmacy"])`, `"countryCode":z.enum(["IN"])`, `"stateCode":z.enum(["MH","OD"])`, `CustomerSignupInputContract`, `CustomerSignupReceiptContract`, `CustomerSignupProofContract`, `CustomerWorkspaceInputContract`} {
		if !strings.Contains(generated, required) {
			t.Fatalf("canonical browser field missing: %s", required)
		}
	}
	if strings.Contains(generated, `"Setup"`) {
		t.Fatal("embedded Go fields became a different wire object")
	}
	if strings.Count(generated, ".strict()") < 5 {
		t.Fatal("generated contracts permit undeclared authority fields")
	}
}

func TestSecurityBooleanFieldsAreGeneratedFromTheOwnerContract(t *testing.T) {
	schema, err := contractSchema(reflect.TypeFor[struct {
		Required bool `json:"required"`
	}](), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, `"required":z.boolean()`) {
		t.Fatal("native MFA booleans were not preserved in the browser contract")
	}
}

func TestMaximumLengthUsesUnicodeCodePoints(t *testing.T) {
	schema, err := contractSchema(reflect.TypeFor[struct {
		City string `json:"city" max:"100"`
	}](), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "[...value].length<=100") {
		t.Fatal("browser maximum must count Unicode code points", schema)
	}
}
