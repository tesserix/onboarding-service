package zitadel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverySearchRequiresExactActiveVerifiedHumanAndBoundedResults(t *testing.T) {
	for _, tc := range []struct {
		name, state, email, owner string
		verified                  bool
		want                      int
	}{
		{"staff", "USER_STATE_ACTIVE", "owner@example.test", "helivanta-019a71f0-1234-7000-8000-000000000001", true, 1},
		{"inactive", "USER_STATE_INACTIVE", "owner@example.test", "helivanta-019a71f0-1234-7000-8000-000000000001", true, 0},
		{"unverified", "USER_STATE_ACTIVE", "owner@example.test", "helivanta-019a71f0-1234-7000-8000-000000000001", false, 0},
		{"foreign-product", "USER_STATE_ACTIVE", "owner@example.test", "platform", true, 0},
		{"wrong-email", "USER_STATE_ACTIVE", "other@example.test", "helivanta-019a71f0-1234-7000-8000-000000000001", true, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/users" || r.Method != "POST" {
					t.Error("wrong provider route")
				}
				var in map[string]any
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					t.Error("bad query")
				}
				if in["query"].(map[string]any)["limit"] != "21" {
					t.Error("unbounded query")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]string{"totalResult": "1", "timestamp": "2026-11-12T00:00:00Z"}, "result": []any{map[string]any{"userId": "tenant-staff", "state": tc.state, "details": map[string]string{"resourceOwner": tc.owner}, "human": map[string]any{"email": map[string]any{"email": tc.email, "isVerified": tc.verified}}}}})
			}))
			defer server.Close()
			c := New(server.URL, StaticToken("synthetic"), "platform", nil)
			got, err := c.DiscoveryAccounts(t.Context(), "owner@example.test")
			if tc.want < 0 {
				if !errors.Is(err, ErrIdentityUnavailable) {
					t.Fatal("malformed result did not fail closed", err)
				}
				return
			}
			if err != nil || len(got) != tc.want {
				t.Fatal("incorrect eligible accounts", len(got), err)
			}
		})
	}
}
