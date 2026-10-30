package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// stubTokens hands back whatever claims the test registered for a raw token.
type stubTokens struct{ claims map[string]auth.Claims }

func (s stubTokens) Verify(_ context.Context, raw string) (auth.Claims, error) {
	c, ok := s.claims[raw]
	if !ok {
		return auth.Claims{}, auth.ErrInvalidToken
	}
	return c, nil
}

type fakeIdentity struct {
	zitadelOrgs int
	orgMembers  []onboarding.Member
	verify      func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error)
}

func (f *fakeIdentity) VerifyOrganization(ctx context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
	if f.verify == nil {
		return zitadel.OrganizationIdentity{}, zitadel.ErrIdentityUnavailable
	}
	return f.verify(ctx, org, tenant, project)
}

func (f *fakeIdentity) CreateOrg(context.Context, string) (string, error) {
	f.zitadelOrgs++
	return "zorg-" + time.Now().Format("150405.000000000"), nil
}
func (f *fakeIdentity) RemoveOrg(context.Context, string) error {
	f.zitadelOrgs--
	return nil
}
func (f *fakeIdentity) SetOrgMetadata(context.Context, string, string, string) error { return nil }
func (f *fakeIdentity) DeactivateOrg(context.Context, string) error                  { return nil }
func (f *fakeIdentity) ReactivateOrg(context.Context, string) error                  { return nil }
func (f *fakeIdentity) CreateHumanUser(_ context.Context, _, email, _, _ string) (string, error) {
	return "user-" + email, nil
}
func (f *fakeIdentity) AddOrgMember(context.Context, string, string, string) error { return nil }
func (f *fakeIdentity) RemoveOrgMember(context.Context, string, string) error      { return nil }
func (f *fakeIdentity) OrgMembers(context.Context, string) ([]onboarding.Member, error) {
	return f.orgMembers, nil
}
func (f *fakeIdentity) GrantProject(context.Context, string, string, []string) error { return nil }
func (f *fakeIdentity) CreateProjectGrant(context.Context, string, string, []string) (string, error) {
	return "project-grant-1", nil
}
func (f *fakeIdentity) CreateMachineUserInOrg(context.Context, string, string, string) (string, error) {
	return "machine-user-1", nil
}
func (f *fakeIdentity) AddMachineSecret(context.Context, string, string) (string, error) {
	return "shown-once", nil
}
func (f *fakeIdentity) RemoveMachineSecret(context.Context, string, string) error { return nil }
func (f *fakeIdentity) SetUserMetadata(context.Context, string, string, map[string][]byte) error {
	return nil
}
func (f *fakeIdentity) GrantUserProjectRoles(context.Context, string, string, string, string, []string) (string, error) {
	return "user-grant-1", nil
}
func (f *fakeIdentity) DeactivateUser(context.Context, string, string) error  { return nil }
func (f *fakeIdentity) ReactivateUser(context.Context, string, string) error  { return nil }
func (f *fakeIdentity) AddOrgDomain(context.Context, string, string) error    { return nil }
func (f *fakeIdentity) RemoveOrgDomain(context.Context, string, string) error { return nil }
func (f *fakeIdentity) GenerateDomainChallenge(_ context.Context, _, domain string) (zitadel.Challenge, error) {
	return zitadel.Challenge{Type: "TXT", Name: "_zitadel-challenge." + domain, Value: "tok"}, nil
}
func (f *fakeIdentity) ValidateDomain(context.Context, string, string) error { return nil }
func (f *fakeIdentity) CreateIDP(context.Context, string, zitadel.IDPSpec) (string, error) {
	return "idp-1", nil
}
func (f *fakeIdentity) DeleteIDP(context.Context, string, string) error { return nil }

const (
	platformToken = "platform-token"
	productToken  = "product-token"
	otherToken    = "other-product-token"
	platformOrg   = "TESSERIX-ORG-ID"
)

type harness struct {
	router      *gin.Engine
	store       *store.Store
	sessions    *auth.SessionCodec
	identity    *fakeIdentity
	fixturePool *pgxpool.Pool
}

func newHarness(t *testing.T, options ...onboarding.Option) *harness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := os.Getenv("ONBOARDING_TEST_DSN")
	if dsn == "" {
		t.Skip("ONBOARDING_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"idps", "domains", "org_products", "organizations", "products", "idempotency_keys"} {
		if _, err := pool.Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	st := store.New(pool)
	for _, key := range []string{"planning-poker", "mark8ly"} {
		err := st.CreateProduct(context.Background(), store.Product{
			Key: key, DisplayName: key, ZitadelProjectID: "proj-" + key, MachineUserID: "svc-" + key,
			Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao,
			Tenancy: store.TenancyOrg, Roles: []string{key + "-user"}, DataResidency: "in",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	sessions, err := auth.NewSessionCodec(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	identity := &fakeIdentity{}
	svc := onboarding.New(st, identity, authz.New(st), nil, options...)

	tokens := stubTokens{claims: map[string]auth.Claims{
		platformToken: {Subject: "sam", Email: "sam@tesserix.app", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
		productToken:  {Subject: "svc-planning-poker"},
		otherToken:    {Subject: "svc-mark8ly"},
	}}

	return &harness{
		router: NewRouter(Deps{
			Service: svc, Store: st, Tokens: tokens, Sessions: sessions,
			Allowlist: auth.ParseAllowlist("sam@tesserix.app"), PlatformOrgID: platformOrg,
		}),
		store:       st,
		sessions:    sessions,
		identity:    identity,
		fixturePool: pool,
	}
}

func (h *harness) do(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not json: %s", rec.Body.String())
	}
	return out
}

func (h *harness) createOrg(t *testing.T, token, name string) string {
	t.Helper()
	rec := h.do(t, http.MethodPost, "/v1/orgs", token,
		`{"name":"`+name+`","ownerEmail":"owner@`+strings.ToLower(name)+`.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org: %d %s", rec.Code, rec.Body.String())
	}
	return decode(t, rec)["id"].(string)
}

func TestHealthNeedsNoCredential(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(t, http.MethodGet, "/healthz", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("healthz %d", rec.Code)
	}
}

func TestAnUnauthenticatedCallIsRejected(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodGet, "/v1/orgs", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("errors must be problem documents, got %q", ct)
	}
}

func TestAnUnknownTokenIsRejected(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(t, http.MethodGet, "/v1/orgs", "forged", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestEveryResponseCarriesARequestIDAndSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodGet, "/v1/orgs", platformToken, "")
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("no correlation id on the response")
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestAProductCannotSeeAnotherProductsOrg(t *testing.T) {
	h := newHarness(t)
	id := h.createOrg(t, productToken, "Apollo")

	rec := h.do(t, http.MethodGet, "/v1/orgs/"+id, otherToken, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 so the id is not confirmed, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Apollo") {
		t.Fatal("the 404 body leaked the organization")
	}
}

func TestAProductCannotDeactivateAnOrg(t *testing.T) {
	h := newHarness(t)
	id := h.createOrg(t, productToken, "Apollo")

	rec := h.do(t, http.MethodPost, "/v1/orgs/"+id+"/deactivate", productToken, "")
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("a product deactivated an org: %d %s", rec.Code, rec.Body.String())
	}
	org, err := h.store.Organization(context.Background(), mustUUID(t, id))
	if err != nil {
		t.Fatal(err)
	}
	if org.State != "active" {
		t.Fatalf("org state is %q", org.State)
	}
}

func TestAMalformedOrgIDIsABadRequestNotAPanic(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(t, http.MethodGet, "/v1/orgs/not-a-uuid", platformToken, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestInvalidBodyIsRejectedWithoutReachingTheService(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{`{}`, `{"name":"A"}`, `{"name":"Apollo","ownerEmail":"not-an-email"}`, `not json`} {
		rec := h.do(t, http.MethodPost, "/v1/orgs", platformToken, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q accepted with %d", body, rec.Code)
		}
	}
}

func TestAnOversizedBodyIsRejected(t *testing.T) {
	h := newHarness(t)
	huge := `{"name":"` + strings.Repeat("a", 2<<20) + `","ownerEmail":"a@b.com"}`
	rec := h.do(t, http.MethodPost, "/v1/orgs", platformToken, huge)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("2MB body accepted with %d", rec.Code)
	}
}

// A retried POST must not create a second organization.
func TestIdempotencyKeyReplaysTheFirstResponse(t *testing.T) {
	h := newHarness(t)
	body := `{"name":"Apollo","ownerEmail":"cto@apollo.com"}`

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+platformToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "retry-1")
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}

	first, second := send(), send()
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("codes %d / %d", first.Code, second.Code)
	}
	if decode(t, first)["id"] != decode(t, second)["id"] {
		t.Fatal("the retry created a second organization")
	}
	orgs, err := h.store.Organizations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 {
		t.Fatalf("%d organizations exist after one logical request", len(orgs))
	}
}

func TestReusingAnIdempotencyKeyWithADifferentBodyIsAConflict(t *testing.T) {
	h := newHarness(t)
	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/orgs", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+platformToken)
		req.Header.Set("Idempotency-Key", "retry-2")
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}
	if rec := send(`{"name":"Apollo","ownerEmail":"a@apollo.com"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(`{"name":"Acme","ownerEmail":"a@acme.com"}`); rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
}

// Cookie-authenticated state changes need the double-submit token; a bearer
// token does not, because no browser attaches it automatically.
func TestACookieSessionCannotMutateWithoutTheCSRFToken(t *testing.T) {
	h := newHarness(t)
	cookie := h.session(t, "csrf-abc")

	req := httptest.NewRequest(http.MethodPost, "/v1/orgs", strings.NewReader(`{"name":"Apollo","ownerEmail":"a@b.com"}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 without the CSRF header, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/orgs", strings.NewReader(`{"name":"Apollo","ownerEmail":"a@b.com"}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-abc")
	rec = httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201 with the CSRF header, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestACookieSessionMayReadWithoutTheCSRFToken(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/orgs", nil)
	req.AddCookie(h.session(t, "csrf-abc"))
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func (h *harness) session(t *testing.T, csrf string) *http.Cookie {
	t.Helper()
	value, err := h.sessions.Encode(auth.Session{
		Subject: "sam", Email: "sam@tesserix.app", OrgID: platformOrg,
		Roles: []string{authz.RolePlatformAdmin}, CSRF: csrf,
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: value}
}

func TestDiscoveryIsAPostSoTheEmailIsNotLogged(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(t, http.MethodGet, "/v1/discovery/resolve", productToken, ""); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("discovery answered a GET with %d", rec.Code)
	}
	rec := h.do(t, http.MethodPost, "/v1/discovery/resolve", productToken, `{"email":"nobody@nowhere.example"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if decode(t, rec)["found"] != false {
		t.Fatalf("unknown domain resolved: %s", rec.Body.String())
	}
}

func TestAuditIsReadableByThePlatformOnly(t *testing.T) {
	h := newHarness(t)
	h.createOrg(t, platformToken, "Apollo")

	if rec := h.do(t, http.MethodGet, "/v1/audit", platformToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("platform cannot read the ledger: %d", rec.Code)
	}
	if rec := h.do(t, http.MethodGet, "/v1/audit", productToken, ""); rec.Code == http.StatusOK {
		t.Fatal("a product read the whole ledger")
	}
}

func TestProductRegistrationIsPlatformOnly(t *testing.T) {
	h := newHarness(t)
	body := `{"key":"newprod","displayName":"New","zitadelProjectId":"p1","machineUserId":"m1",` +
		`"identity":"zitadel","tenantSecrets":"openbao","tenancy":"org","dataResidency":"in"}`

	if rec := h.do(t, http.MethodPost, "/v1/products", productToken, body); rec.Code == http.StatusCreated {
		t.Fatal("a product registered another product")
	}
	if rec := h.do(t, http.MethodPost, "/v1/products", platformToken, body); rec.Code != http.StatusCreated {
		t.Fatalf("platform cannot register a product: %d %s", rec.Code, rec.Body.String())
	}
}

// The two questions every product must answer are enforced, not documented.
func TestAProductWithoutACapabilityAnswerIsRejected(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{
		`{"key":"a","displayName":"A","zitadelProjectId":"p","machineUserId":"m","tenantSecrets":"openbao","tenancy":"org"}`,
		`{"key":"b","displayName":"B","zitadelProjectId":"p","machineUserId":"m","identity":"zitadel","tenancy":"org"}`,
		`{"key":"c","displayName":"C","zitadelProjectId":"p","machineUserId":"m","identity":"carrier-pigeon","tenantSecrets":"openbao","tenancy":"org"}`,
	} {
		if rec := h.do(t, http.MethodPost, "/v1/products", platformToken, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("accepted %s with %d", body, rec.Code)
		}
	}
}

func TestErrorsDoNotLeakInternals(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/v1/orgs", platformToken, `{"name":"","ownerEmail":"x"}`)
	body := rec.Body.String()
	for _, leak := range []string{"pgx", "SQLSTATE", "goroutine", "onboarding-service/api/internal"} {
		if strings.Contains(body, leak) {
			t.Fatalf("error body leaks %q: %s", leak, body)
		}
	}
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
