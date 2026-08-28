package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type call struct {
	method string
	path   string
	orgID  string
	body   map[string]any
	auth   string
}

// recorder is a stand-in Zitadel that records what it was asked and replies
// with whatever the test queued for that path.
type recorder struct {
	calls   []call
	replies map[string]any
	status  map[string]int
}

func newRecorder() *recorder {
	return &recorder{replies: map[string]any{}, status: map[string]int{}}
}

func (r *recorder) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.calls = append(r.calls, call{
			method: req.Method, path: req.URL.Path,
			orgID: req.Header.Get("x-zitadel-orgid"),
			body:  body, auth: req.Header.Get("Authorization"),
		})
		if code, ok := r.status[req.URL.Path]; ok && code >= 400 {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
			return
		}
		reply := r.replies[req.URL.Path]
		if reply == nil {
			reply = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (r *recorder) find(method, pathSuffix string) *call {
	for i := range r.calls {
		if r.calls[i].method == method && strings.HasSuffix(r.calls[i].path, pathSuffix) {
			return &r.calls[i]
		}
	}
	return nil
}

func newClient(t *testing.T, rec *recorder) *Client {
	t.Helper()
	return New(rec.serve(t).URL, StaticToken("test-pat"), "TESSERIX-ORG-ID", nil)
}

func TestCreateOrgReturnsTheOrgID(t *testing.T) {
	rec := newRecorder()
	rec.replies["/management/v1/orgs"] = map[string]any{"id": "org-123"}
	c := newClient(t, rec)

	orgID, err := c.CreateOrg(context.Background(), "Apollo Hospitals")
	if err != nil {
		t.Fatal(err)
	}
	if orgID != "org-123" {
		t.Fatalf("want org-123, got %q", orgID)
	}
	if got := rec.find("POST", "/orgs"); got.auth != "Bearer test-pat" {
		t.Fatalf("token not sent: %q", got.auth)
	}
}

// Every /management/v1 call is org-scoped by this header. Omitting it operates
// on the wrong organization, silently.
func TestOrgScopedCallsCarryTheOrgHeader(t *testing.T) {
	rec := newRecorder()
	c := newClient(t, rec)
	ctx := context.Background()

	if err := c.SetOrgMetadata(ctx, "org-123", "tenant_id", "9d1f"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddOrgMember(ctx, "org-123", "user-1", "ORG_OWNER"); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if c.orgID != "org-123" {
			t.Fatalf("%s %s sent org header %q", c.method, c.path, c.orgID)
		}
	}
}

// SetOrgMetadata binds to /metadata/{key}, not /orgs/me/metadata/{key} — the
// org comes from the header alone. The plausible-looking path returns 404 and
// stranded a created org with no tenant_id.
func TestSetOrgMetadataUsesTheProtoPath(t *testing.T) {
	rec := newRecorder()
	c := newClient(t, rec)

	if err := c.SetOrgMetadata(context.Background(), "org-123", "tenant_id", "9d1f"); err != nil {
		t.Fatal(err)
	}
	got := rec.find("POST", "/management/v1/metadata/tenant_id")
	if got == nil {
		t.Fatalf("metadata not set at the proto path; calls: %+v", rec.calls)
	}
	if want := "OWQxZg=="; got.body["value"] != want {
		t.Fatalf("value not base64 bytes: got %v, want %s", got.body["value"], want)
	}
}

// A project grant is issued by the org that OWNS the project (TESSERIX), not by
// the org receiving it. Getting this backwards is a silent no-op.
func TestGrantProjectIsScopedToTheOwningOrg(t *testing.T) {
	rec := newRecorder()
	c := newClient(t, rec)

	if err := c.GrantProject(context.Background(), "proj-hms", "org-123", []string{"hms-user"}); err != nil {
		t.Fatal(err)
	}
	got := rec.find("POST", "/projects/proj-hms/grants")
	if got == nil {
		t.Fatalf("grant not issued; calls: %+v", rec.calls)
	}
	if got.orgID != "TESSERIX-ORG-ID" {
		t.Fatalf("grant scoped to %q, want the project's owning org", got.orgID)
	}
	if got.body["grantedOrgId"] != "org-123" {
		t.Fatalf("granted org missing: %+v", got.body)
	}
}

func TestRegistryCredentialLifecycleUsesTheCustomerOrganization(t *testing.T) {
	rec := newRecorder()
	rec.replies["/management/v1/users/machine"] = map[string]any{"userId": "svc-123"}
	rec.replies["/v2/users/svc-123/secret"] = map[string]any{"clientSecret": "shown-once"}
	rec.replies["/management/v1/projects/proj-registry/grants"] = map[string]any{"grantId": "grant-9"}
	rec.replies["/management/v1/users/svc-123/grants"] = map[string]any{"userGrantId": "user-grant-7"}
	c := newClient(t, rec)
	ctx := context.Background()

	userID, err := c.CreateMachineUserInOrg(ctx, "customer-org", "registry-acme-bot", "Acme publisher")
	if err != nil || userID != "svc-123" {
		t.Fatalf("create machine user: id=%q err=%v", userID, err)
	}
	secret, err := c.AddMachineSecret(ctx, "customer-org", userID)
	if err != nil || secret != "shown-once" {
		t.Fatalf("add secret: secret=%q err=%v", secret, err)
	}
	if err := c.SetUserMetadata(ctx, "customer-org", userID, map[string][]byte{
		"registry_namespaces": []byte(`["acme"]`),
		"registry_kinds":      []byte(`["Agent"]`),
	}); err != nil {
		t.Fatal(err)
	}
	grantID, err := c.CreateProjectGrant(ctx, "proj-registry", "customer-org", []string{"registry.publisher"})
	if err != nil || grantID != "grant-9" {
		t.Fatalf("create project grant: id=%q err=%v", grantID, err)
	}
	userGrantID, err := c.GrantUserProjectRoles(ctx, "customer-org", userID, "proj-registry", grantID, []string{"registry.publisher"})
	if err != nil || userGrantID != "user-grant-7" {
		t.Fatalf("grant user roles: id=%q err=%v", userGrantID, err)
	}
	if err := c.RemoveMachineSecret(ctx, "customer-org", userID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeactivateUser(ctx, "customer-org", userID); err != nil {
		t.Fatal(err)
	}
	if err := c.ReactivateUser(ctx, "customer-org", userID); err != nil {
		t.Fatal(err)
	}

	for _, call := range rec.calls {
		if strings.Contains(call.path, "/users/") && !strings.Contains(call.path, "/projects/") && call.orgID != "customer-org" {
			t.Fatalf("%s %s scoped to %q, want customer-org", call.method, call.path, call.orgID)
		}
	}
	metadata := rec.find(http.MethodPost, "/v2/users/svc-123/metadata")
	if metadata == nil {
		t.Fatal("credential restrictions were not written to signed user metadata")
	}
	entries, ok := metadata.body["metadata"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("metadata payload = %#v", metadata.body)
	}
	if rec.find(http.MethodDelete, "/v2/users/svc-123/secret") == nil {
		t.Fatal("revocation did not remove the machine secret")
	}
}

func TestCreateIDPBindsItToTheLoginPolicy(t *testing.T) {
	rec := newRecorder()
	rec.replies["/management/v1/idps/oidc"] = map[string]any{"id": "idp-9"}
	c := newClient(t, rec)

	idpID, err := c.CreateIDP(context.Background(), "org-123", IDPSpec{
		Type: "oidc", Name: "Okta", Issuer: "https://apollo.okta.com",
		ClientID: "abc", ClientSecret: "shh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if idpID != "idp-9" {
		t.Fatalf("want idp-9, got %q", idpID)
	}
	bind := rec.find("POST", "/policies/login/idps")
	if bind == nil {
		t.Fatal("IdP created but never bound to the login policy — it will never appear at login")
	}
	if bind.body["idpId"] != "idp-9" {
		t.Fatalf("bound the wrong IdP: %+v", bind.body)
	}
}

// An IdP that exists but is unbound is invisible at the login screen and the
// customer swears it is configured. Leave nothing behind.
func TestCreateIDPRollsBackWhenTheBindFails(t *testing.T) {
	rec := newRecorder()
	rec.replies["/management/v1/idps/oidc"] = map[string]any{"id": "idp-9"}
	rec.status["/management/v1/policies/login/idps"] = http.StatusInternalServerError
	c := newClient(t, rec)

	if _, err := c.CreateIDP(context.Background(), "org-123", IDPSpec{
		Type: "oidc", Name: "Okta", Issuer: "https://apollo.okta.com", ClientID: "abc",
	}); err == nil {
		t.Fatal("bind failure reported as success")
	}
	if rec.find("DELETE", "/idps/idp-9") == nil {
		t.Fatal("half-configured IdP left behind after a failed bind")
	}
}

func TestEntraUsesTheAzureEndpointNotGenericOIDC(t *testing.T) {
	rec := newRecorder()
	rec.replies["/management/v1/idps/azure"] = map[string]any{"id": "idp-e"}
	c := newClient(t, rec)

	if _, err := c.CreateIDP(context.Background(), "org-123", IDPSpec{
		Type: "entra", Name: "Contoso", ClientID: "abc", TenantID: "t-1",
	}); err != nil {
		t.Fatal(err)
	}
	if rec.find("POST", "/idps/azure") == nil {
		t.Fatal("Entra sent to the generic OIDC endpoint; claims arrive in the wrong shape")
	}
}

func TestUnknownIDPTypeIsRejectedBeforeAnyCallIsMade(t *testing.T) {
	rec := newRecorder()
	c := newClient(t, rec)

	if _, err := c.CreateIDP(context.Background(), "org-123", IDPSpec{Type: "carrier-pigeon", Name: "x"}); err == nil {
		t.Fatal("unknown IdP type accepted")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("made %d calls for an invalid type", len(rec.calls))
	}
}

func TestDomainChallengeReturnsTheRecordToPublish(t *testing.T) {
	rec := newRecorder()
	rec.replies["/management/v1/orgs/me/domains/apollo.com/validation/_generate"] =
		map[string]any{"token": "zitadel-verif-abc", "url": "https://apollo.com/.well-known/x"}
	c := newClient(t, rec)

	ch, err := c.GenerateDomainChallenge(context.Background(), "org-123", "apollo.com")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Type != "TXT" || ch.Value != "zitadel-verif-abc" || ch.Name != "_zitadel-challenge.apollo.com" {
		t.Fatalf("unusable challenge: %+v", ch)
	}
}

func TestAnUpstreamErrorIsNotSwallowed(t *testing.T) {
	rec := newRecorder()
	rec.status["/management/v1/orgs"] = http.StatusForbidden
	c := newClient(t, rec)

	_, err := c.CreateOrg(context.Background(), "Apollo")
	if err == nil {
		t.Fatal("403 reported as success")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("want APIError 403, got %v", err)
	}
}

// The secret must not travel into our own logs or error strings.
func TestIDPSpecRedactsItsSecret(t *testing.T) {
	spec := IDPSpec{Type: "oidc", Name: "Okta", ClientSecret: "super-secret-value"}
	if strings.Contains(spec.String(), "super-secret-value") {
		t.Fatalf("secret rendered in String(): %s", spec.String())
	}
}

// The platform org id is resolved from its name at boot, so no environment
// carries a hand-copied numeric id that would silently scope every call to the
// wrong tenant if it were stale.
func TestResolveOrgByNameReturnsTheOneExactMatch(t *testing.T) {
	rec := newRecorder()
	rec.replies["/admin/v1/orgs/_search"] = map[string]any{
		"result": []map[string]any{{"id": "org-tesserix", "name": "TESSERIX"}},
	}
	c := newClient(t, rec)

	id, err := c.ResolveOrgByName(context.Background(), "TESSERIX")
	if err != nil {
		t.Fatal(err)
	}
	if id != "org-tesserix" {
		t.Fatalf("want org-tesserix, got %q", id)
	}
}

func TestResolveOrgByNameRefusesAnythingButOneMatch(t *testing.T) {
	for name, result := range map[string][]map[string]any{
		"none": {},
		"two": {
			{"id": "org-a", "name": "TESSERIX"},
			{"id": "org-b", "name": "TESSERIX Labs"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := newRecorder()
			rec.replies["/admin/v1/orgs/_search"] = map[string]any{"result": result}
			if id, err := newClient(t, rec).ResolveOrgByName(context.Background(), "TESSERIX"); err == nil {
				t.Fatalf("an ambiguous lookup resolved to %q", id)
			}
		})
	}
}
