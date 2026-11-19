package onboarding

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCustomerLoginTargetsListsOnlyEligibleBindingsAndRefusesOverflow(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	users := []any{}
	tenants := []uuid.UUID{}
	for i, name := range []string{"Pharmacy", "Hospital", "Unenrolled"} {
		tenant := uuid.New()
		tenants = append(tenants, tenant)
		org := "helivanta-" + tenant.String()
		if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: name, CreatedByProduct: "helivanta"}); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if err := st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-user"}); err != nil {
				t.Fatal(err)
			}
		}
		users = append(users, map[string]any{"userId": fmt.Sprintf("staff-%d", i), "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "person@example.test", "isVerified": true}}})
	}
	status := http.StatusOK
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]string{"totalResult": fmt.Sprint(len(users))}, "result": users})
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	in := customercontract.LoginTargetsInput{Email: "person@example.test", RequestID: uuid.NewString()}

	got, err := svc.CustomerLoginTargets(t.Context(), in)
	if err != nil || got.Truncated || len(got.Targets) != 2 || got.Validate() != nil {
		t.Fatal("eligible targets missing", err, got)
	}
	first, second := got.Targets[0], got.Targets[1]
	if first.Name != "Hospital" || first.Subject != "staff-1" || first.OrganizationID != "helivanta-"+tenants[1].String() || first.Account != in.Email || second.Name != "Pharmacy" {
		t.Fatal("targets not bound to their own organisation in name order", got)
	}

	// Past five eligible accounts the answer carries no bindings at all.
	for i := range 4 {
		tenant := uuid.New()
		org := "helivanta-" + tenant.String()
		if _, err = st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: fmt.Sprintf("Extra %d", i), CreatedByProduct: "helivanta"}); err != nil {
			t.Fatal(err)
		}
		if err = st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-user"}); err != nil {
			t.Fatal(err)
		}
		users = append(users, map[string]any{"userId": fmt.Sprintf("extra-%d", i), "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "person@example.test", "isVerified": true}}})
	}
	got, err = svc.CustomerLoginTargets(t.Context(), in)
	if err != nil || !got.Truncated || len(got.Targets) != 0 {
		t.Fatal("overflow disclosed bindings", err, got)
	}

	users = []any{}
	got, err = svc.CustomerLoginTargets(t.Context(), in)
	if err != nil || got.Truncated || got.Targets == nil || len(got.Targets) != 0 {
		t.Fatal("unknown email must be an empty list, not an error", err, got)
	}

	status = http.StatusInternalServerError
	if _, err = svc.CustomerLoginTargets(t.Context(), in); !errors.Is(err, ErrUnavailable) {
		t.Fatal("native outage must be unavailable, never an empty list", err)
	}
	if _, err = svc.CustomerLoginTargets(t.Context(), customercontract.LoginTargetsInput{Email: "bad", RequestID: in.RequestID}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid input accepted", err)
	}
}
