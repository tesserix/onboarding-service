package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func securityResponses() map[string]string {
	return map[string]string{
		"/v2/users/owner-1":                      `{"user":{"userId":"owner-1","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"org-1"},"human":{"email":{"isVerified":true}}}}`,
		"/management/v1/orgs/me/members/_search": `{"details":{"totalResult":"1"},"result":[{"userId":"owner-1","roles":["ORG_OWNER"],"details":{"resourceOwner":"org-1","sequence":"12"}}]}`,
		"/management/v1/policies/login":          `{"isDefault":false,"policy":{"forceMfa":true,"forceMfaLocalOnly":false,"mfaInitSkipLifetime":"0s","details":{"resourceOwner":"org-1","sequence":"13"}}}`,
	}
}
func TestOwnerSecurityRequiresFreshNativeOwnerAndCurrentPolicy(t *testing.T) {
	proof, err := ownerSecurityClient(t, securityResponses()).OwnerSecurity(context.Background(), "org-1", "owner-1")
	if err != nil || !proof.RequireMFA || !proof.ExternalMFA || !proof.NoGrace || proof.Sequence != "13" {
		t.Fatalf("unexpected proof: %+v %v", proof, err)
	}
}
func TestOwnerSecurityFailsClosedOnRevocationForeignBindingAndMissingPolicy(t *testing.T) {
	for _, tc := range []struct{ path, from, to string }{
		{"/v2/users/owner-1", "USER_STATE_ACTIVE", "USER_STATE_INACTIVE"},
		{"/v2/users/owner-1", `"resourceOwner":"org-1"`, `"resourceOwner":"other"`},
		{"/v2/users/owner-1", `"isVerified":true`, `"isVerified":false`},
		{"/management/v1/orgs/me/members/_search", "ORG_OWNER", "ORG_USER_MANAGER"},
		{"/management/v1/orgs/me/members/_search", `"userId":"owner-1"`, `"userId":"other"`},
		{"/management/v1/orgs/me/members/_search", `"totalResult":"1"`, `"totalResult":"2"`},
		{"/management/v1/policies/login", `"sequence":"13"`, `"sequence":"0"`},
		{"/management/v1/policies/login", `"resourceOwner":"org-1"`, `"resourceOwner":"other"`},
	} {
		t.Run(tc.path+tc.from, func(t *testing.T) {
			replies := securityResponses()
			replies[tc.path] = strings.Replace(replies[tc.path], tc.from, tc.to, 1)
			if _, err := ownerSecurityClient(t, replies).OwnerSecurity(t.Context(), "org-1", "owner-1"); err == nil {
				t.Fatal("unverified security disclosed")
			}
		})
	}
	replies := securityResponses()
	delete(replies, "/management/v1/policies/login")
	if _, err := ownerSecurityClient(t, replies).OwnerSecurity(t.Context(), "org-1", "owner-1"); err == nil {
		t.Fatal("missing policy accepted")
	}
}
func TestOwnerSecurityReportsDriftRatherThanRequestedPolicy(t *testing.T) {
	replies := securityResponses()
	replies["/management/v1/policies/login"] = strings.Replace(replies["/management/v1/policies/login"], `"forceMfa":true`, `"forceMfa":false`, 1)
	proof, err := ownerSecurityClient(t, replies).OwnerSecurity(t.Context(), "org-1", "owner-1")
	if err != nil || proof.RequireMFA {
		t.Fatalf("drift hidden: %+v %v", proof, err)
	}
}

func ownerSecurityClient(t *testing.T, replies map[string]string) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer source-token" || r.Header.Get("x-zitadel-orgid") != "org-1" {
			t.Error("missing native scope")
		}
		if strings.HasSuffix(r.URL.Path, "/_search") {
			var body struct {
				Query struct {
					Limit string `json:"limit"`
				} `json:"query"`
				Queries []map[string]map[string]string `json:"queries"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Query.Limit != "2" || len(body.Queries) != 1 || body.Queries[0]["userIdQuery"]["userId"] != "owner-1" {
				t.Error("unbounded native membership query")
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
	client := New(server.URL, StaticToken("source-token"), "platform-1", nil)
	client.http = server.Client()
	return client
}
