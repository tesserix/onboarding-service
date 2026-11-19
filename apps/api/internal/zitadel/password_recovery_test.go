package zitadel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPasswordRecoveryReadsOnlyTheVerifiedMailboxAccountInItsExactOrganization(t *testing.T) {
	for _, tc := range []struct {
		name, state, org string
		verified         bool
		count            int
		missing, locked  bool
	}{
		{"active", "USER_STATE_ACTIVE", "helivanta-tenant", true, 1, false, false},
		{"locked", "USER_STATE_LOCKED", "helivanta-tenant", true, 1, false, true},
		{"disabled", "USER_STATE_INACTIVE", "helivanta-tenant", true, 1, true, false},
		{"unverified", "USER_STATE_ACTIVE", "helivanta-tenant", false, 1, true, false},
		{"foreign", "USER_STATE_ACTIVE", "foreign-org", true, 1, true, false},
		{"ambiguous", "USER_STATE_ACTIVE", "helivanta-tenant", true, 2, true, false},
		{"absent", "USER_STATE_ACTIVE", "helivanta-tenant", true, 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v2/users" || r.Header.Get("X-Zitadel-Orgid") != "helivanta-tenant" {
					t.Error("unscoped recovery lookup")
				}
				var in struct {
					Query   map[string]string              `json:"query"`
					Queries []map[string]map[string]string `json:"queries"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Fatal(err)
				}
				if in.Query["limit"] != "2" || len(in.Queries) != 2 || in.Queries[0]["organizationIdQuery"]["organizationId"] != "helivanta-tenant" || in.Queries[1]["emailQuery"]["emailAddress"] != "person@example.test" {
					t.Error("recovery searched beyond exact tenant/mailbox")
				}
				result := []any{}
				for range tc.count {
					result = append(result, map[string]any{"userId": "account", "state": tc.state, "details": map[string]string{"resourceOwner": tc.org}, "human": map[string]any{"email": map[string]any{"email": "person@example.test", "isVerified": tc.verified}}})
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"result": result}); err != nil {
					t.Error(err)
				}
			}))
			defer native.Close()
			client := New(native.URL, StaticToken("synthetic"), "platform", nil)
			account, err := client.TenantRecoveryAccount(t.Context(), "helivanta-tenant", "person@example.test")
			if tc.missing {
				if !errors.Is(err, ErrIdentityMissing) || account.Subject != "" {
					t.Fatal("ineligible recovery account disclosed", err)
				}
				return
			}
			if err != nil || account.Subject != "account" || account.Locked != tc.locked {
				t.Fatal("wrong recovery account state", err)
			}
			subject, err := client.TenantLoginSubject(t.Context(), "helivanta-tenant", "person@example.test")
			if tc.locked {
				if !errors.Is(err, ErrIdentityMissing) || subject != "" {
					t.Fatal("recovery reader widened ordinary login", err)
				}
			} else if err != nil || subject != "account" {
				t.Fatal("ordinary login changed", err)
			}
		})
	}
}
