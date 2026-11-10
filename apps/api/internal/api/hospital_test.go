package api

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHospitalRequestsUsePrivateImmutableClaimsAndDenyForeignProducts(t *testing.T) {
	h := newHarness(t)
	if e := h.store.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Helivanta", ZitadelProjectID: "proj-helivanta", MachineUserID: "svc-helivanta", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao, Tenancy: store.TenancyOrg, Roles: []string{}, DataResidency: "in"}); e != nil {
		t.Fatal(e)
	}
	claims := hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithHospitals(onboarding.HospitalConfig{Claims: claims, Product: "helivanta"}))
	router := NewRouter(Deps{Service: svc, Store: h.store, Tokens: stubTokens{claims: map[string]auth.Claims{"helivanta": {Subject: "svc-helivanta"}, "foreign": {Subject: "svc-mark8ly"}}}})
	call := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/hospital-onboardings", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", "TEST-hospital-onboarding-1")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	body := `{"product":"helivanta","displayName":"TEST Hospital Group","initialAdminUserId":"verified-user"}`
	w := call("foreign", body)
	if w.Code != 404 {
		t.Fatalf("foreign product status=%d", w.Code)
	}
	w = call("helivanta", body)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"tenantId"`) {
		t.Fatalf("request not persisted: %d %s", w.Code, w.Body.String())
	}
	replay := call("helivanta", body)
	if replay.Code != 202 || replay.Body.String() != w.Body.String() {
		t.Fatal("request replay created a different tenant")
	}
	w = call("helivanta", `{"product":"helivanta","displayName":"TEST","initialAdminUserId":"user","tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"existing"}`)
	if w.Code != 409 && w.Code != 404 {
		t.Fatalf("product adopted existing tenant: %d", w.Code)
	}
}

func TestHospitalCompletionFailsClosedAndPreservesCanonicalBinding(t *testing.T) {
	h := newHarness(t)
	if e := h.store.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Helivanta", ZitadelProjectID: "proj-helivanta", MachineUserID: "svc-helivanta", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao, Tenancy: store.TenancyOrg, Roles: []string{}, DataResidency: "in"}); e != nil {
		t.Fatal(e)
	}
	claims := hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")
	tenant := "11111111-1111-4111-8111-111111111111"
	_, e := claims.Create(t.Context(), hospitalclaims.Spec{Product: "helivanta", TenantID: tenant, DisplayName: "TEST Group", OrganizationID: "org-hospital", InitialAdminUserID: "existing-admin"})
	if e != nil {
		t.Fatal(e)
	}
	tokenFile := t.TempDir() + "/token"
	if e = os.WriteFile(tokenFile, []byte("TEST-projected-token"), 0600); e != nil {
		t.Fatal(e)
	}
	calls := 0
	pending := true
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload struct {
			AllowBootstrap bool `json:"allowBootstrap"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.AllowBootstrap {
			t.Error("adoption must never bootstrap a missing clinical administrator")
		}
		if r.Header.Get("X-Onboarding-Token") != "Bearer TEST-projected-token" || r.Header.Get("Authorization") != "" {
			t.Error("missing scoped credential")
		}
		if pending {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"tenantId":"` + tenant + `","organizationId":"org-hospital","state":"complete"}`))
	}))
	defer backend.Close()
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithHospitals(onboarding.HospitalConfig{Claims: claims, Product: "helivanta", HelivantaURL: backend.URL, HelivantaTokenFile: tokenFile, HTTPClient: backend.Client()}))
	input := onboarding.CompleteHospitalInput{Product: "helivanta", TenantID: tenant, OrganizationID: "org-hospital", ProjectID: "proj-helivanta", DisplayName: "TEST Group", InitialAdminUserID: "existing-admin"}
	if e = svc.CompleteHospital(t.Context(), input); e == nil || calls != 0 {
		t.Fatal("source unavailable accepted")
	}
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		return identityObservation(org, tenant, project), nil
	}
	if e = svc.CompleteHospital(t.Context(), input); e == nil || calls != 1 {
		t.Fatalf("clinical failure accepted: %v calls=%d", e, calls)
	}
	pending = false
	if e = svc.CompleteHospital(t.Context(), input); e != nil {
		t.Fatal(e)
	}
	if e = svc.CompleteHospital(t.Context(), input); e != nil {
		t.Fatal(e)
	}
	org, e := h.store.Organization(t.Context(), uuid.MustParse(tenant))
	if e != nil || org.ID.String() != tenant || org.ZitadelOrgID != "org-hospital" {
		t.Fatalf("tenant moved: %+v %v", org, e)
	}
	input.InitialAdminUserID = "another-person"
	before := calls
	if e = svc.CompleteHospital(t.Context(), input); e == nil || calls != before {
		t.Fatal("controller request replaced initial admin")
	}
}

func TestHospitalStatusIsScopedAndInternalRouteRejectsProductSessions(t *testing.T) {
	h := newHarness(t)
	claims := hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")
	tenant := "11111111-1111-4111-8111-111111111111"
	if e := h.store.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Helivanta", ZitadelProjectID: "proj-helivanta", MachineUserID: "svc-helivanta", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao, Tenancy: store.TenancyOrg, Roles: []string{}, DataResidency: "in"}); e != nil {
		t.Fatal(e)
	}
	if _, e := claims.Create(t.Context(), hospitalclaims.Spec{Product: "helivanta", TenantID: tenant, DisplayName: "TEST Group", InitialAdminUserID: "human"}); e != nil {
		t.Fatal(e)
	}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithHospitals(onboarding.HospitalConfig{Claims: claims, Product: "helivanta"}))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, Tokens: stubTokens{claims: map[string]auth.Claims{"helivanta": {Subject: "svc-helivanta"}, "foreign": {Subject: "svc-mark8ly"}}}})
	path := "/v1/hospital-onboardings/" + tenant + "?product=helivanta"
	for _, token := range []string{"foreign", ""} {
		w := h.do(t, http.MethodGet, path, token, "")
		if w.Code != 404 && w.Code != 401 {
			t.Fatalf("foreign read status=%d", w.Code)
		}
	}
	w := h.do(t, http.MethodGet, path, "helivanta", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"pending"`) {
		t.Fatalf("pending state=%d %s", w.Code, w.Body.String())
	}
	for _, token := range []string{"helivanta", "foreign", ""} {
		w = h.do(t, http.MethodPost, "/internal/v1/hospital-enrolments", token, `{}`)
		if w.Code != 401 {
			t.Fatalf("ordinary product reached controller endpoint: %d", w.Code)
		}
	}
	input := onboarding.StartHospitalInput{Product: "helivanta", DisplayName: "TEST", InitialAdminUserID: "human", TenantID: tenant, OrganizationID: "existing"}
	if _, e := svc.StartHospital(t.Context(), authz.Principal{Kind: authz.KindProduct, Subject: "svc-helivanta", ProductKey: "helivanta"}, input, "TEST-fresh-adoption-key"); e != onboarding.ErrNotFound {
		t.Fatalf("product adoption decision=%v", e)
	}
}

func TestHospitalFirstOwnerDoesNotRequireAPreexistingIdentity(t *testing.T) {
	h := newHarness(t)
	if err := h.store.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Helivanta", ZitadelProjectID: "proj-helivanta", MachineUserID: "svc-helivanta", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao, Tenancy: store.TenancyOrg, Roles: []string{}, DataResidency: "in"}); err != nil {
		t.Fatal(err)
	}
	claims := hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")
	service := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithHospitals(onboarding.HospitalConfig{Claims: claims, Product: "helivanta"}))
	router := NewRouter(Deps{Service: service, Store: h.store, Tokens: stubTokens{claims: map[string]auth.Claims{"helivanta": {Subject: "svc-helivanta"}}}})
	call := func(key, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/hospital-onboardings", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer helivanta")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	input := `{"product":"helivanta","displayName":"TEST Hospital","initialAdmin":{"givenName":"Test","familyName":"Owner","email":"owner@example.test"}}`
	key := "TEST-first-owner-profile-1"
	response := call(key, input)
	if response.Code != 202 {
		t.Fatalf("new owner rejected: %d %s", response.Code, response.Body.String())
	}
	var result hospitalclaims.Result
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewSHA1(uuid.NameSpaceURL, []byte("hospital-onboarding:helivanta:svc-helivanta:"+key)).String()
	if result.Spec.TenantID != tenant || result.Spec.InitialAdminUserID != "owner-"+tenant || result.Spec.InitialAdmin == nil || result.Spec.InitialAdmin.Email != "owner@example.test" || result.Status.Activated {
		t.Fatalf("unexpected first owner binding: %+v", result)
	}
	replay := call(key, input)
	if replay.Code != 202 || replay.Body.String() != response.Body.String() {
		t.Fatalf("first-owner replay changed binding: %d", replay.Code)
	}
	changed := call(key, strings.ReplaceAll(input, "owner@example.test", "other@example.test"))
	if changed.Code != 409 {
		t.Fatalf("owner replacement accepted: %d", changed.Code)
	}
	for name, body := range map[string]string{
		"caller_selected_subject": strings.Replace(input, `"initialAdmin":`, `"initialAdminUserId":"foreign-subject","initialAdmin":`, 1),
		"adoption_profile":        strings.Replace(input, `"initialAdmin":`, `"tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"existing-org","initialAdmin":`, 1),
		"preserve_policy_profile": strings.Replace(input, `"initialAdmin":`, `"preserveLoginPolicy":true,"initialAdmin":`, 1),
		"invalid_contact":         strings.ReplaceAll(input, "owner@example.test", "not-an-email"),
		"missing_given_name":      strings.ReplaceAll(input, `"givenName":"Test"`, `"givenName":""`),
	} {
		t.Run(name, func(t *testing.T) {
			rejected := call("TEST-first-owner-invalid-"+name, body)
			if rejected.Code != 400 {
				t.Fatalf("unsafe first owner accepted: %d %s", rejected.Code, rejected.Body.String())
			}
		})
	}
	persisted, err := claims.Get(t.Context(), "helivanta", tenant)
	if err != nil || persisted.Spec.InitialAdmin == nil || persisted.Spec.InitialAdmin.Email != "owner@example.test" {
		t.Fatal("immutable first-owner profile lost")
	}
}
