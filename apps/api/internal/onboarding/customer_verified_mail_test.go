package onboarding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestVerifiedOwnerCannotReceiveAnotherVerificationCode(t *testing.T) {
	row := store.CustomerSignup{TenantID: uuid.New(), OwnerSubject: "owner-synthetic", Input: customercontract.Signup{Owner: &identity.OrganizationInitialAdmin{Email: "owner@example.test"}}}
	codes := 0
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/users/owner-synthetic" {
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": row.OwnerSubject, "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": "helivanta-" + row.TenantID.String()}, "human": map[string]any{"email": map[string]any{"email": row.Input.Owner.Email, "isVerified": true}}}})
			return
		}
		codes++
		w.WriteHeader(500)
	}))
	t.Cleanup(native.Close)
	mail, err := signupmail.New("/unused-key", "onboarding@helivanta.app", mailTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("verified owner reached provider")
		return nil, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{signupIdentity: zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil), signupMail: mail}
	id, err := svc.sendCustomerEmail(t.Context(), row, false)
	if err != nil || id != "" || codes != 0 {
		t.Fatalf("verified email requested another code: codes=%d err=%v", codes, err)
	}
}
