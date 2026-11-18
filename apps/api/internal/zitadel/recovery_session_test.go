package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecoverySessionRequiresExactFreshNativeProof(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(-2 * time.Minute)
	enrolled := now.Add(-time.Minute)
	base := map[string]any{
		"id": "native-session", "sequence": "703",
		"creationDate": reset.Add(time.Second), "changeDate": now.Add(-time.Second),
		"expirationDate": now.Add(time.Hour),
		"factors": map[string]any{
			"user":     map[string]any{"id": "synthetic-human", "organizationId": "synthetic-org", "verifiedAt": reset.Add(time.Second)},
			"password": map[string]any{"verifiedAt": reset.Add(2 * time.Second)},
			"totp":     map[string]any{"verifiedAt": enrolled.Add(time.Second)},
		},
	}
	for _, tc := range []struct {
		name                  string
		edit                  func(map[string]any)
		rejected, unavailable bool
	}{
		{name: "fresh_exact_proof"},
		{name: "no_native_expiry", edit: func(m map[string]any) { delete(m, "expirationDate") }},
		{name: "foreign_session", edit: func(m map[string]any) { m["id"] = "another-session" }, rejected: true},
		{name: "foreign_user", edit: func(m map[string]any) { recoveryTestFactor(m, "user")["id"] = "another-user" }, rejected: true},
		{name: "foreign_organization", edit: func(m map[string]any) { recoveryTestFactor(m, "user")["organizationId"] = "another-org" }, rejected: true},
		{name: "old_attempt", edit: func(m map[string]any) { m["creationDate"] = reset.Add(-time.Second) }, rejected: true},
		{name: "cutoff_attempt", edit: func(m map[string]any) { m["creationDate"] = reset }, rejected: true},
		{name: "old_primary_with_fresh_totp", edit: func(m map[string]any) { recoveryTestFactor(m, "password")["verifiedAt"] = reset.Add(-time.Nanosecond) }, rejected: true},
		{name: "cutoff_primary", edit: func(m map[string]any) { recoveryTestFactor(m, "password")["verifiedAt"] = reset }, rejected: true},
		{name: "old_factor", edit: func(m map[string]any) { recoveryTestFactor(m, "totp")["verifiedAt"] = enrolled.Add(-time.Nanosecond) }, rejected: true},
		{name: "cutoff_factor", edit: func(m map[string]any) { recoveryTestFactor(m, "totp")["verifiedAt"] = enrolled }, rejected: true},
		{name: "passwordless", edit: func(m map[string]any) { delete(m["factors"].(map[string]any), "password") }, rejected: true},
		{name: "no_totp", edit: func(m map[string]any) { delete(m["factors"].(map[string]any), "totp") }, rejected: true},
		{name: "expired", edit: func(m map[string]any) { m["expirationDate"] = now.Add(-time.Second) }, rejected: true},
		{name: "malformed_expiry", edit: func(m map[string]any) { m["expirationDate"] = "tomorrow" }, unavailable: true},
		{name: "null_expiry", edit: func(m map[string]any) { m["expirationDate"] = nil }, unavailable: true},
		{name: "missing_sequence", edit: func(m map[string]any) { delete(m, "sequence") }, unavailable: true},
		{name: "zero_sequence", edit: func(m map[string]any) { m["sequence"] = "0" }, unavailable: true},
		{name: "malformed_primary", edit: func(m map[string]any) { recoveryTestFactor(m, "password")["verifiedAt"] = "yesterday" }, unavailable: true},
		{name: "future_primary", edit: func(m map[string]any) { recoveryTestFactor(m, "password")["verifiedAt"] = now.Add(time.Hour) }, unavailable: true},
		{name: "missing_user_proof", edit: func(m map[string]any) { delete(recoveryTestFactor(m, "user"), "verifiedAt") }, unavailable: true},
		{name: "missing_user", edit: func(m map[string]any) { delete(m["factors"].(map[string]any), "user") }, unavailable: true},
		{name: "malformed_user_proof", edit: func(m map[string]any) { recoveryTestFactor(m, "user")["verifiedAt"] = nil }, unavailable: true},
		{name: "future_user_proof", edit: func(m map[string]any) { recoveryTestFactor(m, "user")["verifiedAt"] = now.Add(time.Hour) }, unavailable: true},
		{name: "future_change", edit: func(m map[string]any) { m["changeDate"] = now.Add(time.Hour) }, unavailable: true},
		{name: "change_before_creation", edit: func(m map[string]any) { m["changeDate"] = reset.Add(-time.Second) }, unavailable: true},
		{name: "duplicate_session_id", edit: func(m map[string]any) { m["ID"] = "another-session" }, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(base)
			require.NoError(t, err)
			var session map[string]any
			require.NoError(t, json.Unmarshal(raw, &session))
			if tc.edit != nil {
				tc.edit(session)
			}
			client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/v2/sessions/native-session", r.URL.Path)
				require.Empty(t, r.URL.RawQuery)
				require.Equal(t, "Bearer test-pat", r.Header.Get("Authorization"))
				require.Equal(t, "synthetic-org", r.Header.Get("x-zitadel-orgid"))
				require.Equal(t, "no-cache", r.Header.Get("Cache-Control"))
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"session": session}))
			})
			proof, err := client.VerifyRecoverySession(t.Context(), "synthetic-org", "synthetic-human", "native-session", reset, enrolled)
			switch {
			case tc.rejected:
				require.ErrorIs(t, err, ErrRecoverySessionRejected)
				require.Zero(t, proof)
			case tc.unavailable:
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.Zero(t, proof)
			default:
				require.NoError(t, err)
				require.Equal(t, "703", proof.Sequence)
				require.True(t, proof.PasswordVerifiedAt.After(reset))
				require.True(t, proof.TOTPVerifiedAt.After(enrolled))
			}
		})
	}
}

func TestRecoverySessionRefusesInvalidSelectorsAndCutoffsWithoutIO(t *testing.T) {
	now := time.Now().UTC()
	var requests atomic.Int32
	client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) })
	for _, input := range []struct {
		org, user, session string
		reset, enrolled    time.Time
	}{
		{"", "user", "session", now.Add(-time.Minute), now},
		{"org", "../user", "session", now.Add(-time.Minute), now},
		{"org", "user", "../session", now.Add(-time.Minute), now},
		{"org", "user", "session", time.Time{}, now},
		{"org", "user", "session", now, time.Time{}},
		{"org", "user", "session", now, now.Add(-time.Second)},
		{"org", "user", "session", now, now.Add(time.Hour)},
	} {
		_, err := client.VerifyRecoverySession(t.Context(), input.org, input.user, input.session, input.reset, input.enrolled)
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
	require.Zero(t, requests.Load())
}

func TestRecoverySessionFailsClosedOnNativeDependencyAndMalformedResponse(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"session":null}`, `{"session":{}}`, `{"session":{}} {}`, strings.Repeat(" ", identityReadLimit+1)} {
		client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		_, err := client.VerifyRecoverySession(t.Context(), "org", "user", "session", time.Now().Add(-time.Minute), time.Now().Add(-time.Second))
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
		_, err := client.VerifyRecoverySession(t.Context(), "org", "user", "session", time.Now().Add(-time.Minute), time.Now().Add(-time.Second))
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
}

func TestRecoverySessionDoesNotFollowRedirectOrIgnoreCancellation(t *testing.T) {
	var disclosures atomic.Int32
	other := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { disclosures.Add(1); w.WriteHeader(500) })
	client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.base+"/collect", http.StatusTemporaryRedirect)
	})
	reset, enrolled := time.Now().Add(-time.Minute), time.Now().Add(-time.Second)
	_, err := client.VerifyRecoverySession(t.Context(), "org", "user", "session", reset, enrolled)
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	require.Zero(t, disclosures.Load())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.VerifyRecoverySession(ctx, "org", "user", "session", reset, enrolled)
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	require.Zero(t, disclosures.Load())
}

func recoveryTestFactor(m map[string]any, key string) map[string]any {
	return m["factors"].(map[string]any)[key].(map[string]any)
}
