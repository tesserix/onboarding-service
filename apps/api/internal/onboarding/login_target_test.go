package onboarding

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCustomerLoginTargetRequiresActiveEnrolmentAndAuditedNativeBinding(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	tenant := uuid.New()
	org := "helivanta-" + tenant.String()
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []any{map[string]any{"userId": "fresh-staff", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "same@example.test", "isVerified": true}}}}})
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	in := customercontract.LoginTargetInput{TenantID: tenant.String(), Email: "same@example.test", RequestID: uuid.NewString()}
	if _, err := svc.CustomerLoginTarget(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != 0 {
		t.Fatal("unknown tenant queried native identity", err)
	}
	if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "TEST", CreatedByProduct: "helivanta"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CustomerLoginTarget(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != 0 {
		t.Fatal("unenrolled tenant queried native identity", err)
	}
	if err := st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-admin"}); err != nil {
		t.Fatal(err)
	}
	target, err := svc.CustomerLoginTarget(t.Context(), in)
	if err != nil || target.Subject != "fresh-staff" || target.OrganizationID != org || target.Validate() != nil {
		t.Fatal("own target resolution failed", err)
	}
	if err = st.RevokeGrant(t.Context(), tenant, "helivanta"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CustomerLoginTarget(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != 1 {
		t.Fatal("revoked enrolment disclosed identity", err)
	}
}
