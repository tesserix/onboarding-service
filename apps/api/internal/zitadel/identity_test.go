package zitadel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const identityTenant = "11111111-1111-4111-8111-111111111111"

func TestVerifyOrganizationRejectsAmbiguousJSONSource(t *testing.T) {
	for _, kind := range []string{"duplicate", "case alias", "unicode fold alias", "invalid UTF8", "excessive nesting"} {
		t.Run(kind, func(t *testing.T) {
			replies := identityResponses()
			path := "/admin/v1/orgs/org-1"
			body := replies[path]
			switch kind {
			case "duplicate":
				body = strings.Replace(body, `"state":"ORG_STATE_ACTIVE"`, `"state":"ORG_STATE_INACTIVE","state":"ORG_STATE_ACTIVE"`, 1)
			case "case alias":
				body = strings.Replace(body, `"state":"ORG_STATE_ACTIVE"`, `"State":"ORG_STATE_INACTIVE","state":"ORG_STATE_ACTIVE"`, 1)
			case "unicode fold alias":
				body = strings.Replace(body, `"state":"ORG_STATE_ACTIVE"`, `"ſtate":"ORG_STATE_INACTIVE","state":"ORG_STATE_ACTIVE"`, 1)
			case "invalid UTF8":
				body = strings.Replace(body, "PRIVATE_BUSINESS_NAME", string([]byte{0xff}), 1)
			case "excessive nesting":
				body = strings.TrimSuffix(body, "}") + `,"unused":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}"
			}
			replies[path] = body
			got, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
			if !errors.Is(err, ErrIdentityUnavailable) || got.State != "" {
				t.Fatal("ambiguous source JSON established organisation identity")
			}
		})
	}
}

func identityResponses() map[string]string {
	return map[string]string{
		"/admin/v1/orgs/org-1":                             `{"org":{"id":"org-1","state":"ORG_STATE_ACTIVE","name":"PRIVATE_BUSINESS_NAME","details":{"sequence":"101"}}}`,
		"/management/v1/metadata/tenant_id":                `{"metadata":{"key":"tenant_id","value":"` + base64.StdEncoding.EncodeToString([]byte(identityTenant)) + `","details":{"sequence":"102"}}}`,
		"/management/v1/projects/project-1":                `{"project":{"id":"project-1","state":"PROJECT_STATE_ACTIVE","details":{"sequence":"103","resourceOwner":"platform-1"}}}`,
		"/management/v1/projectgrants/_search":             `{"details":{"totalResult":"1"},"result":[{"grantId":"grant-1","grantedOrgId":"org-1","projectId":"project-1","projectOwnerId":"platform-1","state":"PROJECT_GRANT_STATE_ACTIVE","details":{"sequence":"104"}}]}`,
		"/management/v1/projects/project-1/grants/grant-1": `{"projectGrant":{"grantId":"grant-1","grantedOrgId":"org-1","projectId":"project-1","projectOwnerId":"platform-1","state":"PROJECT_GRANT_STATE_ACTIVE","details":{"sequence":"104","resourceOwner":"platform-1"}}}`,
	}
}

func TestVerifyOrganizationDoesNotAcceptADeletedGrantFromSearch(t *testing.T) {
	replies := identityResponses()
	delete(replies, "/management/v1/projects/project-1/grants/grant-1")
	client := identityClient(t, replies)
	proof, err := client.VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
	if !errors.Is(err, ErrIdentityMissing) || proof.State == "active" {
		t.Fatalf("stale search confirmed a deleted grant: state=%s error=%v", proof.State, err)
	}
}

func TestVerifyOrganizationValidatesTheExactGrantLookup(t *testing.T) {
	path := "/management/v1/projects/project-1/grants/grant-1"
	for _, tc := range []struct{ name, from, to string }{
		{"grant ID", `"grantId":"grant-1"`, `"grantId":"other"`},
		{"target organisation", `"grantedOrgId":"org-1"`, `"grantedOrgId":"other"`},
		{"project", `"projectId":"project-1"`, `"projectId":"other"`},
		{"project owner", `"projectOwnerId":"platform-1"`, `"projectOwnerId":"other"`},
		{"resource owner", `"resourceOwner":"platform-1"`, `"resourceOwner":"other"`},
		{"older revision", `"sequence":"104"`, `"sequence":"103"`},
		{"unknown state", `PROJECT_GRANT_STATE_ACTIVE`, `PROJECT_GRANT_STATE_UNSPECIFIED`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := identityResponses()
			replies[path] = strings.Replace(replies[path], tc.from, tc.to, 1)
			proof, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
			if !errors.Is(err, ErrIdentityUnavailable) || proof.State == "active" {
				t.Fatal("an uncertain exact grant lookup established eligibility")
			}
		})
	}
}

func TestVerifyOrganizationUsesTheLatestExactGrantRevision(t *testing.T) {
	replies := identityResponses()
	path := "/management/v1/projects/project-1/grants/grant-1"
	replies[path] = strings.ReplaceAll(replies[path], `"sequence":"104"`, `"sequence":"105"`)
	proof, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
	if err != nil || proof.SourceRevisions.Grant != "105" || proof.State != "active" {
		t.Fatal("exact source provenance was replaced with the older search revision")
	}
	replies[path] = strings.ReplaceAll(replies[path], "PROJECT_GRANT_STATE_ACTIVE", "PROJECT_GRANT_STATE_INACTIVE")
	proof, err = identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
	if err != nil || proof.State != "inactive" || proof.SourceRevisions.Grant != "105" {
		t.Fatal("a current suspension was hidden by an active search result")
	}
}

func identityClient(t *testing.T, replies map[string]string) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer TEST-source-token" {
			t.Error("missing source credential")
		}
		expectedOrg := "platform-1"
		if strings.Contains(r.URL.Path, "/metadata/") {
			expectedOrg = "org-1"
		}
		if strings.HasPrefix(r.URL.Path, "/management/") && r.Header.Get("x-zitadel-orgid") != expectedOrg {
			t.Error("wrong source org scope")
		}
		if r.URL.Path == "/management/v1/projectgrants/_search" {
			if r.Method != http.MethodPost {
				t.Error("grant search must use POST")
			}
			var body struct {
				Query struct {
					Limit string `json:"limit"`
				} `json:"query"`
				Queries []map[string]map[string]string `json:"queries"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Query.Limit != "2" || len(body.Queries) != 2 || body.Queries[0]["projectIdQuery"]["projectId"] != "project-1" || body.Queries[1]["grantedOrgIdQuery"]["grantedOrgId"] != "org-1" {
				t.Error("source grant filter was not bounded to the exact project and org")
			}
		}
		raw, ok := replies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
	client.http = server.Client()
	return client
}

func TestVerifyOrganizationUsesLiveScopedIdentityWithoutPrivateFields(t *testing.T) {
	c := identityClient(t, identityResponses())
	before := time.Now().Add(-time.Second)
	got, err := c.VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != identityTenant || got.OrganizationID != "org-1" || got.ProjectID != "project-1" || got.GrantID != "grant-1" || got.State != "active" || got.SchemaVersion != 1 {
		t.Fatalf("incorrect identity: %+v", got)
	}
	if got.VerifiedAt.Before(before) || got.VerifiedAt.After(time.Now()) {
		t.Fatal("observation time is not current")
	}
	if got.SourceRevisions.Organization != "101" || got.SourceRevisions.Metadata != "102" || got.SourceRevisions.Project != "103" || got.SourceRevisions.Grant != "104" {
		t.Fatal("source provenance missing")
	}
	raw, _ := json.Marshal(got)
	for _, private := range []string{"PRIVATE_BUSINESS_NAME", "TEST-source-token", "billingEmail", "primaryDomain", "roles"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private source information escaped the observation")
		}
	}
}

func TestVerifyOrganizationFailsClosedOnMissingOrMismatchedFacts(t *testing.T) {
	cases := []struct{ name, path, from, to string }{
		{"wrong org", "/admin/v1/orgs/org-1", `"id":"org-1"`, `"id":"org-2"`},
		{"wrong project", "/management/v1/projects/project-1", `"id":"project-1"`, `"id":"project-2"`},
		{"wrong project owner", "/management/v1/projects/project-1", `"resourceOwner":"platform-1"`, `"resourceOwner":"other"`},
		{"wrong grant org", "/management/v1/projectgrants/_search", `"grantedOrgId":"org-1"`, `"grantedOrgId":"org-2"`},
		{"wrong grant project", "/management/v1/projectgrants/_search", `"projectId":"project-1"`, `"projectId":"project-2"`},
		{"wrong grant owner", "/management/v1/projectgrants/_search", `"projectOwnerId":"platform-1"`, `"projectOwnerId":"other"`},
		{"duplicate grant total", "/management/v1/projectgrants/_search", `"totalResult":"1"`, `"totalResult":"2"`},
		{"unknown org state", "/admin/v1/orgs/org-1", `ORG_STATE_ACTIVE`, `ORG_STATE_UNSPECIFIED`},
		{"missing source sequence", "/admin/v1/orgs/org-1", `"sequence":"101"`, `"sequence":"0"`},
		{"wrong tenant binding", "/management/v1/metadata/tenant_id", base64.StdEncoding.EncodeToString([]byte(identityTenant)), base64.StdEncoding.EncodeToString([]byte("22222222-2222-4222-8222-222222222222"))},
		{"invalid metadata encoding", "/management/v1/metadata/tenant_id", base64.StdEncoding.EncodeToString([]byte(identityTenant)), "!!!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replies := identityResponses()
			replies[tc.path] = strings.Replace(replies[tc.path], tc.from, tc.to, 1)
			_, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
			if !errors.Is(err, ErrIdentityUnavailable) {
				t.Fatalf("untrusted source fact must not establish identity: %v", err)
			}
		})
	}
}

func TestVerifyOrganizationReturnsInactiveForExplicitSourceSuspension(t *testing.T) {
	for _, tc := range []struct{ path, from, to string }{{"/admin/v1/orgs/org-1", "ORG_STATE_ACTIVE", "ORG_STATE_INACTIVE"}, {"/management/v1/projects/project-1", "PROJECT_STATE_ACTIVE", "PROJECT_STATE_INACTIVE"}, {"/management/v1/projectgrants/_search", "PROJECT_GRANT_STATE_ACTIVE", "PROJECT_GRANT_STATE_INACTIVE"}} {
		replies := identityResponses()
		replies[tc.path] = strings.Replace(replies[tc.path], tc.from, tc.to, 1)
		got, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
		if err != nil || got.State != "inactive" {
			t.Fatalf("source suspension must deny active identity: %+v %v", got, err)
		}
	}
}

func TestVerifyOrganizationRejectsAbsentAndDuplicateGrants(t *testing.T) {
	for _, raw := range []string{`{"details":{"totalResult":"0"},"result":[]}`, `{"details":{"totalResult":"2"},"result":[{"grantId":"a"},{"grantId":"b"}]}`} {
		replies := identityResponses()
		replies["/management/v1/projectgrants/_search"] = raw
		_, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
		if err == nil {
			t.Fatal("missing or ambiguous grant established identity")
		}
	}
}

func TestVerifyOrganizationRejectsRedirectsAndBoundsResponses(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true); w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c := New(source.URL, StaticToken("TEST-private-token"), "platform-1", nil)
	c.http = source.Client()
	if _, err := c.VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1"); err == nil || strings.Contains(err.Error(), "TEST-private-token") {
		t.Fatal("redirect must fail without credential disclosure")
	}
	if leaked.Load() {
		t.Fatal("source redirect followed")
	}
	replies := identityResponses()
	replies["/admin/v1/orgs/org-1"] = strings.Repeat("x", 70<<10)
	if _, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1"); err == nil {
		t.Fatal("oversized source accepted")
	}
}

func TestVerifyOrganizationHonorsCancellationAndRejectsUnsafeIDs(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); <-r.Context().Done() }))
	defer srv.Close()
	c := New(srv.URL, StaticToken("TEST-token"), "platform-1", nil)
	c.http = srv.Client()
	if _, err := c.VerifyOrganization(context.Background(), "../org", identityTenant, "project-1"); err == nil || requests.Load() != 0 {
		t.Fatal("unsafe source ID reached the provider")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.VerifyOrganization(ctx, "org-1", identityTenant, "project-1"); err == nil {
		t.Fatal("cancelled identity lookup succeeded")
	}
}

func TestVerifyOrganizationBoundsAnUnresponsiveSource(t *testing.T) {
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		<-r.Context().Done()
	}))
	defer source.Close()
	c := New(source.URL, StaticToken("TEST-token"), "platform-1", nil)
	c.http = source.Client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.VerifyOrganization(ctx, "org-1", identityTenant, "project-1"); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, ErrIdentityUnavailable) {
			t.Fatal("unresponsive source established identity")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("source read exceeded its bounded deadline")
	}
}

func TestVerifyOrganizationDoesNotTreatMalformedSourceAsMissingIdentity(t *testing.T) {
	replies := identityResponses()
	replies["/management/v1/projectgrants/_search"] = `{"message":"SOURCE_ERROR_WITH_HTTP_200"}`
	_, err := identityClient(t, replies).VerifyOrganization(context.Background(), "org-1", identityTenant, "project-1")
	if !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("malformed source became a policy denial: %v", err)
	}
}
