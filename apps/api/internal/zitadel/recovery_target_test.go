package zitadel

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaffMFARecoveryTargetRequiresOwnedOrdinaryVerifiedPasswordAccount(t *testing.T) {
	user := strings.Replace(verifiedMembershipHuman, `"human":{`, `"human":{"passwordChanged":"2026-01-01T00:00:00.123456789Z",`, 1)
	emptyMembers := `{"details":{"totalResult":"0","processedSequence":"703"},"result":[]}`
	for _, tc := range []struct {
		name, user, members     string
		ineligible, unavailable bool
	}{
		{"owned_ordinary", user, emptyMembers, false, false},
		{"native_owner", user, `{"details":{"totalResult":"1","processedSequence":"704"},"result":[{"userId":"synthetic-human","roles":["ORG_OWNER"]}]}`, true, false},
		{"native_admin", user, `{"details":{"totalResult":"1","processedSequence":"704"},"result":[{"userId":"synthetic-human","roles":["ORG_USER_MANAGER"]}]}`, true, false},
		{"foreign_owner", strings.Replace(user, "helivanta-"+membershipTestTenant, "foreign-org", 1), emptyMembers, true, false},
		{"unverified", strings.Replace(user, `"isVerified":true`, `"isVerified":false`, 1), emptyMembers, true, false},
		{"inactive", strings.Replace(user, "USER_STATE_ACTIVE", "USER_STATE_INACTIVE", 1), emptyMembers, true, false},
		{"ambiguous_user", strings.Replace(user, `"human":`, `"machine":{},"human":`, 1), emptyMembers, false, true},
		{"missing_member_details", user, `{"result":[]}`, false, true},
		{"missing_count", user, `{"details":{"processedSequence":"703"},"result":[]}`, false, true},
		{"native_default_zero_count", user, `{"details":{"processedSequence":"703","viewTimestamp":"2026-01-01T00:00:00Z"}}`, false, false},
		{"native_default_zero_sequence", user, `{"details":{"viewTimestamp":"2026-01-01T00:00:00Z"}}`, false, false},
		{"native_explicit_zero_sequence", user, `{"details":{"totalResult":"0","processedSequence":"0","viewTimestamp":"2026-01-01T00:00:00Z"},"result":[]}`, false, false},
		{"null_default_sequence", user, `{"details":{"processedSequence":null,"viewTimestamp":"2026-01-01T00:00:00Z"}}`, false, true},
		{"null_default_timestamp", user, `{"details":{"viewTimestamp":null}}`, false, true},
		{"empty_default_sequence", user, `{"details":{"processedSequence":"","viewTimestamp":"2026-01-01T00:00:00Z"}}`, false, true},
		{"recased_default_sequence", user, `{"details":{"ProcessedSequence":"0","viewTimestamp":"2026-01-01T00:00:00Z"}}`, false, true},
		{"future_default_timestamp", user, `{"details":{"viewTimestamp":"9999-01-01T00:00:00Z"}}`, false, true},
		{"zero_sequence_with_member", user, `{"details":{"totalResult":"1","processedSequence":"0","viewTimestamp":"2026-01-01T00:00:00Z"},"result":[{"userId":"synthetic-human","roles":["ORG_OWNER"]}]}`, false, true},
		{"malformed_default_zero_proof", user, `{"details":{"processedSequence":"703","viewTimestamp":"unknown"}}`, false, true},
		{"missing_password", strings.Replace(user, `"passwordChanged":"2026-01-01T00:00:00.123456789Z",`, "", 1), emptyMembers, true, false},
		{"null_password_time", strings.Replace(user, `"passwordChanged":"2026-01-01T00:00:00.123456789Z"`, `"passwordChanged":null`, 1), emptyMembers, false, true},
		{"missing_projection_sequence", user, `{"details":{"totalResult":"0"},"result":[]}`, false, true},
		{"null_count", user, `{"details":{"totalResult":null,"processedSequence":"703"},"result":[]}`, false, true},
		{"unproven_zero_projection", user, `{"details":{"totalResult":"0","processedSequence":"0"},"result":[]}`, false, true},
		{"omitted_empty_list", user, `{"details":{"totalResult":"0","processedSequence":"703"}}`, false, false},
		{"null_list", user, `{"details":{"totalResult":"0","processedSequence":"703"},"result":null}`, false, true},
		{"false_zero_with_member", user, `{"details":{"totalResult":"0","processedSequence":"703"},"result":[{"userId":"synthetic-human","roles":["ORG_OWNER"]}]}`, false, true},
		{"missing_member_result", user, `{"details":{"totalResult":"1","processedSequence":"704"}}`, false, true},
		{"foreign_member_result", user, `{"details":{"totalResult":"1","processedSequence":"704"},"result":[{"userId":"foreign-human","roles":["ORG_OWNER"]}]}`, false, true},
		{"missing_roles", user, `{"details":{"totalResult":"1","processedSequence":"704"},"result":[{"userId":"synthetic-human"}]}`, false, true},
		{"recased_count", user, `{"details":{"TotalResult":"0","processedSequence":"703"},"result":[]}`, false, true},
		{"duplicate_count", user, `{"details":{"totalResult":"1","totalResult":"0","processedSequence":"703"},"result":[]}`, false, true},
		{"too_many_results", user, `{"details":{"totalResult":"501","processedSequence":"703"},"result":[]}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes atomic.Int32
			client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer test-pat", r.Header.Get("Authorization"))
				require.Equal(t, "helivanta-"+membershipTestTenant, r.Header.Get("x-zitadel-orgid"))
				switch r.URL.Path {
				case "/v2/users/synthetic-human":
					require.Equal(t, http.MethodGet, r.Method)
					_, _ = w.Write([]byte(tc.user))
				case "/management/v1/orgs/me/members/_search":
					require.Equal(t, http.MethodPost, r.Method)
					var query map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&query))
					require.Equal(t, map[string]any{"query": map[string]any{"limit": float64(1)}, "queries": []any{map[string]any{"userIdQuery": map[string]any{"userId": "synthetic-human"}}}}, query)
					_, _ = w.Write([]byte(tc.members))
				default:
					writes.Add(1)
					w.WriteHeader(500)
				}
			})
			err := client.CheckStaffMFARecoveryTarget(t.Context(), "helivanta-"+membershipTestTenant, "synthetic-human")
			switch {
			case tc.ineligible:
				require.ErrorIs(t, err, ErrRecoveryTargetIneligible)
			case tc.unavailable:
				require.ErrorIs(t, err, ErrIdentityUnavailable)
			default:
				require.NoError(t, err)
			}
			require.Zero(t, writes.Load())
		})
	}
}
