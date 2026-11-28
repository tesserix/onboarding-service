package zitadel

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestNativeMFAAdmissionProofsLogOnlyTheUnavailableStage(t *testing.T) {
	for _, test := range []struct {
		name, message, stage string
		enrollment           bool
		status               int
	}{
		{"enrollment read", "Staff MFA enrollment proof unavailable", "read", true, 503},
		{"enrollment decode", "Staff MFA enrollment proof unavailable", "decode", true, 200},
		{"session read", "Staff MFA session proof unavailable", "read", false, 503},
		{"session decode", "Staff MFA session proof unavailable", "decode", false, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"private":"private-provider-detail"}`))
			}))
			defer server.Close()
			client := New(server.URL, StaticToken("private-token"), "private-admin-org", slog.New(slog.NewJSONHandler(&output, nil)))
			reset := time.Now().Add(-time.Minute)
			var err error
			if test.enrollment {
				_, err = client.RecoveryFactorEnrollment(t.Context(), "private-org", "private-user", FactorRemovalEvidence{Sequence: "1703", ChangedAt: reset})
			} else {
				_, err = client.VerifyRecoverySession(t.Context(), "private-org", "private-user", "private-session", "totp", reset, reset.Add(time.Second))
			}
			require.ErrorIs(t, err, ErrIdentityUnavailable)
			var record map[string]any
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record))
			require.Equal(t, test.message, record["msg"])
			require.Equal(t, test.stage, record["stage"])
			for _, private := range []string{"private-provider-detail", "private-token", "private-admin-org", "private-org", "private-user", "private-session"} {
				require.NotContains(t, output.String(), private)
			}
		})
	}
}
