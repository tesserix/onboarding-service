package onboarding

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
)

func TestPublicSignupAlwaysCreatesTenantOwnedIdentityWithoutGlobalEmailLookup(t *testing.T) {
	for _, state := range []string{"recorded", "existing_account"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
			defer native.Close()
			svc := &Service{signupIdentity: zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil), hospitals: &HospitalConfig{Product: "helivanta", Claims: hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")}}
			id := uuid.New()
			tenant := customerTenantID(id.String())
			row := store.CustomerSignup{ID: id, TenantID: tenant, ResourceID: uuid.Must(uuid.NewV7()), OwnerSubject: "owner-" + tenant.String(), State: state, Input: customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Hospital", Kind: customercontract.Hospital, CountryCode: customercontract.India, StateCode: customercontract.Maharashtra}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: "shared@example.test"}}}
			got, code := svc.deliverCustomer(t.Context(), row)
			if got != "identity_requested" || code != "" || calls != 0 {
				t.Fatalf("fresh tenant used global identity: state=%s code=%s calls=%d", got, code, calls)
			}
			claim, err := svc.hospitals.Claims.Get(t.Context(), "helivanta", tenant.String())
			if err != nil || claim.Spec.InitialAdminUserID != row.OwnerSubject || claim.Spec.InitialAdmin == nil || claim.Spec.InitialAdmin.Email != row.Input.Owner.Email {
				t.Fatalf("new tenant lacks its own owner: %v", err)
			}
		})
	}
}
