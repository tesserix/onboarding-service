package zitadel

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaffMFARecoveryTargetLogsOnlyTheUnavailableProofStage(t *testing.T) {
	user := strings.Replace(verifiedMembershipHuman, `"human":{`, `"human":{"passwordChanged":"2026-01-01T00:00:00Z",`, 1)
	for _, tc := range []struct {
		name, stage string
		userStatus  int
	}{
		{"native read unavailable", "user_read", http.StatusServiceUnavailable},
		{"invalid member proof", "member_proof", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/users/synthetic-human" {
					w.WriteHeader(tc.userStatus)
					if tc.userStatus != http.StatusOK {
						_, _ = w.Write([]byte(`{"message":"private-provider-detail"}`))
						return
					}
					_, _ = w.Write([]byte(user))
					return
				}
				_, _ = w.Write([]byte(`{"details":null,"private":"private-provider-detail"}`))
			}))
			defer server.Close()
			client := New(server.URL, StaticToken("private-token"), "private-admin-org", slog.New(slog.NewJSONHandler(&output, nil)))
			err := client.CheckStaffMFARecoveryTarget(t.Context(), "helivanta-"+membershipTestTenant, "synthetic-human")
			require.ErrorIs(t, err, ErrIdentityUnavailable)
			var record map[string]any
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record))
			require.Equal(t, "Staff MFA target proof unavailable", record["msg"])
			require.Equal(t, tc.stage, record["stage"])
			for _, private := range []string{"private-provider-detail", "private-token", "private-admin-org", membershipTestTenant, "synthetic-human"} {
				require.NotContains(t, output.String(), private)
			}
		})
	}
}
