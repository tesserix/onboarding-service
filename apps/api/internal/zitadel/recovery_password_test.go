package zitadel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecoveryPasswordRequiresNativeCodeAndAcknowledgedOwnedEvidence(t *testing.T) {
	const org = "helivanta-11111111-1111-1111-1111-111111111111"
	const changed = "2026-11-19T03:04:05.123456Z"
	for _, tc := range []struct {
		name            string
		status          int
		body            string
		policy, unknown bool
	}{
		{"acknowledged", 200, `{"details":{"resourceOwner":"` + org + `","sequence":"42","changeDate":"` + changed + `"}}`, false, false},
		{"foreign owner", 200, `{"details":{"resourceOwner":"other","sequence":"42","changeDate":"` + changed + `"}}`, false, true},
		{"no mutation evidence", 200, `{}`, false, true},
		{"duplicate evidence", 200, `{"details":{},"details":{"resourceOwner":"` + org + `","sequence":"42","changeDate":"` + changed + `"}}`, false, true},
		{"policy refusal", 400, `{"code":3,"message":"invalid","details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"DOMAIN-HuJf6"}]}`, true, false},
		{"unknown refusal", 400, `{"code":3,"message":"invalid"}`, false, true},
		{"unavailable", 503, `{}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v2/users/account/password" || r.Header.Get("X-Zitadel-Orgid") != org {
					t.Error("wrong native password scope")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				var code string
				_ = json.Unmarshal(body["verificationCode"], &code)
				if code != "native-code" || body["currentPassword"] != nil {
					t.Error("native reset proof missing")
				}
				var password struct {
					Password       string `json:"password"`
					ChangeRequired bool   `json:"changeRequired"`
				}
				if json.Unmarshal(body["newPassword"], &password) != nil || password.Password != "synthetic-password" || password.ChangeRequired {
					t.Error("wrong native password handoff")
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer native.Close()
			client := New(native.URL, StaticToken("synthetic"), "platform", nil)
			proof, err := client.SetRecoveryPassword(t.Context(), org, "account", "native-code", "synthetic-password")
			if tc.policy {
				if !errors.Is(err, ErrRecoveryPasswordPolicy) {
					t.Fatal("policy refusal not recognized", err)
				}
			} else if tc.unknown {
				if !errors.Is(err, ErrIdentityUnavailable) || !proof.ChangedAt.IsZero() {
					t.Fatal("unconfirmed response reported as success", err)
				}
			} else if err != nil || proof.Sequence != "42" || !proof.ChangedAt.Equal(mustRecoveryTime(t, changed)) {
				t.Fatal("native evidence lost", err)
			}
			if _, err := client.SetRecoveryPassword(t.Context(), org, "account", "", "synthetic-password"); err == nil || calls != 1 {
				t.Fatal("code-free native password operation admitted", err)
			}
		})
	}
}
func mustRecoveryTime(t *testing.T, value string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRecoveryCredentialStateRequiresOwnedVerifiedHumanAndNativeCutoff(t *testing.T) {
	const org = "helivanta-11111111-1111-1111-1111-111111111111"
	for _, tc := range []struct {
		name, owner, email, state, changed string
		verified, want                     bool
	}{
		{"owned", org, "person@example.test", "USER_STATE_ACTIVE", "2026-01-01T00:00:00Z", true, true},
		{"foreign", "other", "person@example.test", "USER_STATE_ACTIVE", "2026-01-01T00:00:00Z", true, false},
		{"changed contact", org, "other@example.test", "USER_STATE_ACTIVE", "2026-01-01T00:00:00Z", true, false},
		{"unverified", org, "person@example.test", "USER_STATE_ACTIVE", "2026-01-01T00:00:00Z", false, false},
		{"locked", org, "person@example.test", "USER_STATE_LOCKED", "2026-01-01T00:00:00Z", true, false},
		{"malformed cutoff", org, "person@example.test", "USER_STATE_ACTIVE", "yesterday", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v2/users/account" || r.Header.Get("X-Zitadel-Orgid") != org {
					t.Error("unscoped native credential observation")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": "account", "state": tc.state, "details": map[string]string{"resourceOwner": tc.owner}, "human": map[string]any{"email": map[string]any{"email": tc.email, "isVerified": tc.verified}, "passwordChanged": tc.changed}}})
			}))
			defer native.Close()
			client := New(native.URL, StaticToken("synthetic"), "platform", nil)
			state, err := client.RecoveryCredentialState(t.Context(), org, "account", "person@example.test")
			if tc.want {
				if err != nil || !state.PasswordConfigured || state.PasswordChangedAt != tc.changed {
					t.Fatal("native cutoff not preserved", err)
				}
			} else if err == nil {
				t.Fatal("ineligible native credential accepted")
			}
		})
	}
}
