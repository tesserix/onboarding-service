package zitadel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantLoginSubjectRequiresOneActiveVerifiedHumanInsideTheExactOrg(t *testing.T) {
	for _, tc := range []struct {
		name, org     string
		count, status int
		verified      bool
		want          error
	}{
		{"own", "helivanta-tenant", 1, 200, true, nil}, {"foreign", "other", 1, 200, true, ErrIdentityMissing}, {"ambiguous", "helivanta-tenant", 2, 200, true, ErrIdentityMissing}, {"unknown", "helivanta-tenant", 0, 200, true, ErrIdentityMissing}, {"unverified", "helivanta-tenant", 1, 200, false, ErrIdentityMissing}, {"outage", "helivanta-tenant", 0, 503, false, ErrIdentityUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v2/users" || r.Header.Get("X-Zitadel-Orgid") != "helivanta-tenant" {
					t.Error("unscoped native request")
				}
				var in struct {
					Query   map[string]string              `json:"query"`
					Queries []map[string]map[string]string `json:"queries"`
				}
				_ = json.NewDecoder(r.Body).Decode(&in)
				if in.Query["limit"] != "2" || len(in.Queries) != 2 || in.Queries[0]["organizationIdQuery"]["organizationId"] != "helivanta-tenant" || in.Queries[1]["emailQuery"]["emailAddress"] != "same@example.test" {
					t.Error("unbounded or unscoped identity search")
				}
				w.WriteHeader(tc.status)
				users := []any{}
				for range tc.count {
					users = append(users, map[string]any{"userId": "staff", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": tc.org}, "human": map[string]any{"email": map[string]any{"email": "same@example.test", "isVerified": tc.verified}}})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": users})
			}))
			defer native.Close()
			c := New(native.URL, StaticToken("synthetic"), "platform", nil)
			subject, err := c.TenantLoginSubject(t.Context(), "helivanta-tenant", "same@example.test")
			if tc.want != nil {
				if !errors.Is(err, tc.want) || subject != "" {
					t.Fatalf("invalid proof accepted: %v", err)
				}
			} else if err != nil || subject != "staff" {
				t.Fatalf("own account rejected: %v", err)
			}
		})
	}
}
