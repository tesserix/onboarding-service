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

func TestRecoveryTOTPRemovalSendsOneScopedWriteAndRequiresNativeAcknowledgement(t *testing.T) {
	changed := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	for _, tc := range []struct {
		name, body string
		status     int
		accepted   bool
	}{
		{"acknowledged", `{"details":{"sequence":"1703","resourceOwner":"org","changeDate":"` + changed + `"}}`, 200, true},
		{"foreign_owner", `{"details":{"sequence":"1703","resourceOwner":"other","changeDate":"` + changed + `"}}`, 200, false},
		{"missing_sequence", `{"details":{"resourceOwner":"org","changeDate":"` + changed + `"}}`, 200, false},
		{"zero_sequence", `{"details":{"sequence":"0","resourceOwner":"org","changeDate":"` + changed + `"}}`, 200, false},
		{"recased_sequence", `{"details":{"Sequence":"1703","resourceOwner":"org","changeDate":"` + changed + `"}}`, 200, false},
		{"duplicate_sequence", `{"details":{"sequence":"1703","sequence":"1704","resourceOwner":"org","changeDate":"` + changed + `"}}`, 200, false},
		{"no_acknowledgement", `{}`, 200, false},
		{"missing_time", `{"details":{"sequence":"1703","resourceOwner":"org"}}`, 200, false},
		{"future_time", `{"details":{"sequence":"1703","resourceOwner":"org","changeDate":"` + time.Now().Add(time.Hour).Format(time.RFC3339Nano) + `"}}`, 200, false},
		{"missing_factor_is_not_success", `{}`, 404, false},
		{"dependency_failure", `{}`, 503, false},
		{"empty_acknowledgement", ``, 204, false},
		{"malformed_acknowledgement", `{`, 200, false},
		{"oversized_acknowledgement", strings.Repeat(" ", identityReadLimit+1), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/v2/users/user/totp", r.URL.Path)
				require.Empty(t, r.URL.RawQuery)
				require.Equal(t, "Bearer test-pat", r.Header.Get("Authorization"))
				require.Equal(t, "org", r.Header.Get("x-zitadel-orgid"))
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			proof, err := client.RemoveRecoveryTOTP(t.Context(), "org", "user")
			require.Equal(t, int32(1), calls.Load())
			if !tc.accepted {
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.Zero(t, proof)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "1703", proof.Sequence)
			require.Equal(t, changed, proof.ChangedAt.Format(time.RFC3339Nano))
		})
	}
}

func TestRecoveryTOTPEnrollmentRequiresLatestAuthoritativeLifecycleEvent(t *testing.T) {
	removed := time.Now().UTC().Add(-time.Minute)
	verified := removed.Add(time.Second)
	for _, tc := range []struct {
		name, sequence, kind, org, user, when string
		enrolled, unavailable                 bool
	}{
		{"verified_after_reset", "1704", "verified", "org", "user", verified.Format(time.RFC3339Nano), true, false},
		{"verified_same_native_instant_later_sequence", "1704", "verified", "org", "user", removed.Format(time.RFC3339Nano), true, false},
		{"only_own_removal", "1703", "removed", "org", "user", removed.Format(time.RFC3339Nano), false, false},
		{"new_unverified_enrollment", "1704", "added", "org", "user", verified.Format(time.RFC3339Nano), false, false},
		{"new_removal_invalidates_enrollment", "1705", "removed", "org", "user", verified.Format(time.RFC3339Nano), false, false},
		{"old_verification", "1702", "verified", "org", "user", removed.Add(-time.Second).Format(time.RFC3339Nano), false, true},
		{"verification_at_removal_sequence", "1703", "verified", "org", "user", removed.Format(time.RFC3339Nano), false, true},
		{"foreign_org", "1704", "verified", "other", "user", verified.Format(time.RFC3339Nano), false, true},
		{"foreign_user", "1704", "verified", "org", "other", verified.Format(time.RFC3339Nano), false, true},
		{"ordinary_factor_check_is_not_enrollment", "1704", "check.succeeded", "org", "user", verified.Format(time.RFC3339Nano), false, true},
		{"old_time_new_sequence", "1704", "verified", "org", "user", removed.Add(-time.Second).Format(time.RFC3339Nano), false, true},
		{"future_event", "1704", "verified", "org", "user", time.Now().Add(time.Hour).Format(time.RFC3339Nano), false, true},
		{"wrong_acknowledgement_time", "1703", "removed", "org", "user", verified.Format(time.RFC3339Nano), false, true},
		{"malformed_sequence", "01704", "verified", "org", "user", verified.Format(time.RFC3339Nano), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/admin/v1/events/_search", r.URL.Path)
				require.Equal(t, "org", r.Header.Get("x-zitadel-orgid"))
				require.Equal(t, "Bearer test-pat", r.Header.Get("Authorization"))
				var query map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&query))
				require.Equal(t, "user", query["aggregateId"])
				require.Equal(t, "org", query["resourceOwner"])
				require.Equal(t, false, query["asc"])
				require.Equal(t, float64(1), query["limit"])
				require.ElementsMatch(t, []any{"user.human.mfa.otp.added", "user.human.mfa.otp.verified", "user.human.mfa.otp.removed"}, query["eventTypes"])
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"events": []any{map[string]any{
					"sequence": tc.sequence, "creationDate": tc.when, "type": map[string]string{"type": "user.human.mfa.otp." + tc.kind},
					"aggregate": map[string]any{"id": tc.user, "resourceOwner": tc.org, "type": map[string]string{"type": "user"}},
				}}}))
			})
			proof, err := client.RecoveryTOTPEnrollment(t.Context(), "org", "user", TOTPRemovalEvidence{Sequence: "1703", ChangedAt: removed})
			if tc.unavailable {
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.Zero(t, proof)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.enrolled, proof.Verified)
			if tc.enrolled {
				require.Equal(t, tc.sequence, proof.Sequence)
				require.Equal(t, tc.when, proof.VerifiedAt.Format(time.RFC3339Nano))
			} else {
				require.Zero(t, proof.VerifiedAt)
			}
		})
	}
}

func TestRecoveryTOTPRejectsInvalidAuthoritySelectorsAndEvidenceWithoutIO(t *testing.T) {
	var calls atomic.Int32
	client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) })
	for _, ids := range [][2]string{{"", "user"}, {"org", "../user"}, {"../org", "user"}} {
		_, err := client.RemoveRecoveryTOTP(t.Context(), ids[0], ids[1])
		require.ErrorIs(t, err, ErrIdentityUnavailable)
		_, err = client.RecoveryTOTPEnrollment(t.Context(), ids[0], ids[1], TOTPRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
	for _, evidence := range []TOTPRemovalEvidence{
		{}, {Sequence: "0", ChangedAt: time.Now()}, {Sequence: "01703", ChangedAt: time.Now()},
		{Sequence: "1703"}, {Sequence: "1703", ChangedAt: time.Now().Add(time.Hour)},
	} {
		_, err := client.RecoveryTOTPEnrollment(t.Context(), "org", "user", evidence)
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
	require.Zero(t, calls.Load())
}

func TestRecoveryTOTPRefusesMissingAmbiguousAndUnavailableEnrollmentEvidence(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"events":null}`, `{"events":[]}`, `{"Events":[]}`,
		`{"events":[{},{}]}`, `{"events":[{}]}`, `{"events":[]} {}`, strings.Repeat(" ", identityReadLimit+1),
	} {
		client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		_, err := client.RecoveryTOTPEnrollment(t.Context(), "org", "user", TOTPRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
		_, err := client.RecoveryTOTPEnrollment(t.Context(), "org", "user", TOTPRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
}

func TestRecoveryTOTPDoesNotRedirectWritesOrIgnoreCancellation(t *testing.T) {
	var disclosures atomic.Int32
	other := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { disclosures.Add(1); w.WriteHeader(500) })
	client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.base+"/collect", http.StatusTemporaryRedirect)
	})
	_, err := client.RemoveRecoveryTOTP(t.Context(), "org", "user")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	_, err = client.RecoveryTOTPEnrollment(t.Context(), "org", "user", TOTPRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	require.Zero(t, disclosures.Load())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.RemoveRecoveryTOTP(ctx, "org", "user")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	_, err = client.RecoveryTOTPEnrollment(ctx, "org", "user", TOTPRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	require.Zero(t, disclosures.Load())
}
