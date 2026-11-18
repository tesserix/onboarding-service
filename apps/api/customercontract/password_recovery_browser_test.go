package customercontract_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

func TestRecoveryBrowserInputsCannotSupplyTenantOrPrivateCapability(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	valid := `{"auth_request_id":"V2_test","request_id":"` + id + `","email":"person@example.test"}`
	var request customercontract.PasswordRecoveryRequest
	if json.Unmarshal([]byte(valid), &request) != nil || request.Validate() != nil {
		t.Fatal("valid recovery request refused")
	}
	for _, raw := range []string{strings.TrimSuffix(valid, "}") + `,"tenantId":"` + uuid.NewString() + `"}`, strings.TrimSuffix(valid, "}") + `,"request_id":"` + id + `"}`, strings.Replace(valid, `"auth_request_id":"V2_test"`, `"auth_request_id":null`, 1)} {
		if json.Unmarshal([]byte(raw), &request) == nil {
			t.Fatal("browser supplied ambiguous or private recovery authority")
		}
	}
	var password customercontract.PasswordRecoverySubmit
	if json.Unmarshal([]byte(`{"command_id":"`+id+`","password":"synthetic-password"}`), &password) != nil || password.Validate() != nil {
		t.Fatal("valid native command submission refused")
	}
	for _, raw := range []string{`{"command_id":"` + id + `","password":""}`, `{"command_id":"` + id + `","password":"synthetic","browserHash":"` + strings.Repeat("a", 64) + `"}`} {
		if json.Unmarshal([]byte(raw), &password) == nil {
			t.Fatal("invalid or private password handoff admitted")
		}
	}
}

func TestRecoveryReceiptsRejectInventedOrAmbiguousOutcomes(t *testing.T) {
	for _, target := range []func() any{func() any { return new(customercontract.PasswordRecoveryStatus) }, func() any { return new(customercontract.PasswordRecoveryResult) }} {
		for _, raw := range []string{`{"state":"invented_success"}`, `{"state":"completed","state":"unconfirmed"}`, `{"STATE":"completed"}`, `{"state":null}`} {
			if json.Unmarshal([]byte(raw), target()) == nil {
				t.Fatal("unverified recovery outcome admitted")
			}
		}
	}
}
