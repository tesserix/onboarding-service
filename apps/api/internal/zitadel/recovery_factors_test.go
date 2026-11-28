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

const (
	recoveryTOTPFactor  = `{"state":"AUTH_FACTOR_STATE_READY","otp":{}}`
	recoveryNoMethods   = `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`
	recoveryKeyRemained = `{"details":{"totalResult":"2"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD","AUTHENTICATION_METHOD_TYPE_U2F"]}`
)

func recoveryKeyFactor(id, state string) string {
	return `{"state":"` + state + `","u2f":{"id":"` + id + `","name":"Key"}}`
}

func recoveryAck(sequence string, at time.Time) string {
	return `{"details":{"sequence":"` + sequence + `","resourceOwner":"org","changeDate":"` + at.Format(time.RFC3339Nano) + `"}}`
}

// recoveryFactorZitadel lists factors, answers each DELETE from acks (a
// missing path is a 503) and finally reports methods. It records every write.
func recoveryFactorZitadel(t *testing.T, factors string, acks map[string]string, methods string) (*Client, *[]string) {
	t.Helper()
	var writes []string
	client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-pat", r.Header.Get("Authorization"))
		require.Equal(t, "org", r.Header.Get("x-zitadel-orgid"))
		require.Empty(t, r.URL.RawQuery)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/management/v1/users/user/auth_factors/_search":
			_, _ = w.Write([]byte(factors))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/users/user/authentication_methods":
			_, _ = w.Write([]byte(methods))
		case r.Method == http.MethodDelete:
			writes = append(writes, r.URL.Path)
			body, ok := acks[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	return client, &writes
}

func TestRecoveryFactorRemovalRemovesEveryKeyAndTheTOTPThenRequiresNoneRemain(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	totp, keyA, keyB := "/v2/users/user/totp", "/v2/users/user/u2f/key-a", "/v2/users/user/u2f/key-b"
	for _, tc := range []struct {
		name, factors, methods string
		acks                   map[string]string
		writes                 []string
		accepted               bool
		sequence               string
	}{
		{"totp_only", `{"result":[` + recoveryTOTPFactor + `]}`, recoveryNoMethods,
			map[string]string{totp: recoveryAck("1703", at)}, []string{totp}, true, "1703"},
		{"key_only_any_state", `{"result":[` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `,` + recoveryKeyFactor("key-b", "AUTH_FACTOR_STATE_NOT_READY") + `]}`, recoveryNoMethods,
			map[string]string{keyA: recoveryAck("1703", at), keyB: recoveryAck("1704", at)}, []string{keyA, keyB}, true, "1704"},
		{"key_and_totp", `{"result":[` + recoveryTOTPFactor + `,` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `]}`, recoveryNoMethods,
			map[string]string{keyA: recoveryAck("1703", at), totp: recoveryAck("1704", at.Add(time.Millisecond))}, []string{keyA, totp}, true, "1704"},
		{"failure_part_way_stops_writes", `{"result":[` + recoveryTOTPFactor + `,` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `,` + recoveryKeyFactor("key-b", "AUTH_FACTOR_STATE_READY") + `]}`, recoveryNoMethods,
			map[string]string{keyA: recoveryAck("1703", at), totp: recoveryAck("1705", at)}, []string{keyA, keyB}, false, ""},
		{"sequence_not_increasing", `{"result":[` + recoveryTOTPFactor + `,` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `]}`, recoveryNoMethods,
			map[string]string{keyA: recoveryAck("1703", at), totp: recoveryAck("1703", at)}, []string{keyA, totp}, false, ""},
		{"time_going_back", `{"result":[` + recoveryTOTPFactor + `,` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `]}`, recoveryNoMethods,
			map[string]string{keyA: recoveryAck("1703", at), totp: recoveryAck("1704", at.Add(-time.Second))}, []string{keyA, totp}, false, ""},
		{"key_remains_after_removal", `{"result":[` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `]}`, recoveryKeyRemained,
			map[string]string{keyA: recoveryAck("1703", at)}, []string{keyA}, false, ""},
		{"nothing_to_remove", `{}`, recoveryNoMethods, nil, nil, false, ""},
		{"other_factor_kinds_are_left", `{"result":[{"state":"AUTH_FACTOR_STATE_READY","otpEmail":{}},` + recoveryTOTPFactor + `]}`, recoveryNoMethods,
			map[string]string{totp: recoveryAck("1703", at)}, []string{totp}, true, "1703"},
		{"duplicate_key", `{"result":[` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `,` + recoveryKeyFactor("key-a", "AUTH_FACTOR_STATE_READY") + `]}`, recoveryNoMethods, nil, nil, false, ""},
		{"unsafe_key_id", `{"result":[` + recoveryKeyFactor("../totp", "AUTH_FACTOR_STATE_READY") + `]}`, recoveryNoMethods, nil, nil, false, ""},
		{"recased_list", `{"Result":[` + recoveryTOTPFactor + `]}`, recoveryNoMethods, nil, nil, false, ""},
		{"foreign_owner_ack", `{"result":[` + recoveryTOTPFactor + `]}`, recoveryNoMethods,
			map[string]string{totp: strings.Replace(recoveryAck("1703", at), `"org"`, `"other"`, 1)}, []string{totp}, false, ""},
		{"future_ack", `{"result":[` + recoveryTOTPFactor + `]}`, recoveryNoMethods,
			map[string]string{totp: recoveryAck("1703", time.Now().Add(time.Hour))}, []string{totp}, false, ""},
		{"zero_sequence_ack", `{"result":[` + recoveryTOTPFactor + `]}`, recoveryNoMethods,
			map[string]string{totp: recoveryAck("0", at)}, []string{totp}, false, ""},
		{"empty_ack", `{"result":[` + recoveryTOTPFactor + `]}`, recoveryNoMethods,
			map[string]string{totp: `{}`}, []string{totp}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, writes := recoveryFactorZitadel(t, tc.factors, tc.acks, tc.methods)
			proof, err := client.RemoveRecoveryFactors(t.Context(), "org", "user")
			require.Equal(t, tc.writes, *writes)
			if !tc.accepted {
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.Zero(t, proof)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.sequence, proof.Sequence)
		})
	}
}

func TestRecoveryFactorEnrollmentRequiresLatestAuthoritativeLifecycleEvent(t *testing.T) {
	removed := time.Now().UTC().Add(-time.Minute)
	verified := removed.Add(time.Second)
	for _, tc := range []struct {
		name, sequence, kind, org, user, when string
		enrolled, unavailable                 bool
	}{
		{"verified_after_reset", "1704", "otp.verified", "org", "user", verified.Format(time.RFC3339Nano), true, false},
		{"verified_same_native_instant_later_sequence", "1704", "otp.verified", "org", "user", removed.Format(time.RFC3339Nano), true, false},
		{"only_own_removal", "1703", "otp.removed", "org", "user", removed.Format(time.RFC3339Nano), false, false},
		{"new_unverified_enrollment", "1704", "otp.added", "org", "user", verified.Format(time.RFC3339Nano), false, false},
		{"new_removal_invalidates_enrollment", "1705", "otp.removed", "org", "user", verified.Format(time.RFC3339Nano), false, false},
		{"old_verification", "1702", "otp.verified", "org", "user", removed.Add(-time.Second).Format(time.RFC3339Nano), false, true},
		{"verification_at_removal_sequence", "1703", "otp.verified", "org", "user", removed.Format(time.RFC3339Nano), false, true},
		{"foreign_org", "1704", "otp.verified", "other", "user", verified.Format(time.RFC3339Nano), false, true},
		{"foreign_user", "1704", "otp.verified", "org", "other", verified.Format(time.RFC3339Nano), false, true},
		{"ordinary_factor_check_is_not_enrollment", "1704", "otp.check.succeeded", "org", "user", verified.Format(time.RFC3339Nano), false, true},
		{"old_time_new_sequence", "1704", "otp.verified", "org", "user", removed.Add(-time.Second).Format(time.RFC3339Nano), false, true},
		{"future_event", "1704", "otp.verified", "org", "user", time.Now().Add(time.Hour).Format(time.RFC3339Nano), false, true},
		{"wrong_acknowledgement_time", "1703", "otp.removed", "org", "user", verified.Format(time.RFC3339Nano), false, true},
		{"key_verified_after_reset", "1704", "u2f.token.verified", "org", "user", verified.Format(time.RFC3339Nano), true, false},
		{"key_removal_is_the_baseline", "1703", "u2f.token.removed", "org", "user", removed.Format(time.RFC3339Nano), false, false},
		{"new_unverified_key", "1704", "u2f.token.added", "org", "user", verified.Format(time.RFC3339Nano), false, false},
		{"key_verified_at_removal_sequence", "1703", "u2f.token.verified", "org", "user", removed.Format(time.RFC3339Nano), false, true},
		{"key_sign_count_is_not_enrollment", "1704", "u2f.token.signcount.changed", "org", "user", verified.Format(time.RFC3339Nano), false, true},
		{"malformed_sequence", "01704", "otp.verified", "org", "user", verified.Format(time.RFC3339Nano), false, true},
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
				require.ElementsMatch(t, []any{"user.human.mfa.otp.added", "user.human.mfa.otp.verified", "user.human.mfa.otp.removed",
					"user.human.mfa.u2f.token.added", "user.human.mfa.u2f.token.verified", "user.human.mfa.u2f.token.removed"}, query["eventTypes"])
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"events": []any{map[string]any{
					"sequence": tc.sequence, "creationDate": tc.when, "type": map[string]string{"type": "user.human.mfa." + tc.kind},
					"aggregate": map[string]any{"id": tc.user, "resourceOwner": tc.org, "type": map[string]string{"type": "user"}},
				}}}))
			})
			proof, err := client.RecoveryFactorEnrollment(t.Context(), "org", "user", FactorRemovalEvidence{Sequence: "1703", ChangedAt: removed})
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
				require.Equal(t, map[bool]string{true: "security_key", false: "totp"}[strings.HasPrefix(tc.kind, "u2f.")], proof.Kind)
			} else {
				require.Zero(t, proof.VerifiedAt)
				require.Empty(t, proof.Kind)
			}
		})
	}
}

func TestRecoveryTOTPRejectsInvalidAuthoritySelectorsAndEvidenceWithoutIO(t *testing.T) {
	var calls atomic.Int32
	client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) })
	for _, ids := range [][2]string{{"", "user"}, {"org", "../user"}, {"../org", "user"}} {
		_, err := client.RemoveRecoveryFactors(t.Context(), ids[0], ids[1])
		require.ErrorIs(t, err, ErrIdentityUnavailable)
		_, err = client.RecoveryFactorEnrollment(t.Context(), ids[0], ids[1], FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
	for _, evidence := range []FactorRemovalEvidence{
		{}, {Sequence: "0", ChangedAt: time.Now()}, {Sequence: "01703", ChangedAt: time.Now()},
		{Sequence: "1703"}, {Sequence: "1703", ChangedAt: time.Now().Add(time.Hour)},
	} {
		_, err := client.RecoveryFactorEnrollment(t.Context(), "org", "user", evidence)
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
		_, err := client.RecoveryFactorEnrollment(t.Context(), "org", "user", FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
		_, err := client.RecoveryFactorEnrollment(t.Context(), "org", "user", FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
		require.ErrorIs(t, err, ErrIdentityUnavailable)
	}
}

func TestRecoveryTOTPDoesNotRedirectWritesOrIgnoreCancellation(t *testing.T) {
	var disclosures atomic.Int32
	other := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { disclosures.Add(1); w.WriteHeader(500) })
	client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.base+"/collect", http.StatusTemporaryRedirect)
	})
	_, err := client.RemoveRecoveryFactors(t.Context(), "org", "user")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	_, err = client.RecoveryFactorEnrollment(t.Context(), "org", "user", FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	require.Zero(t, disclosures.Load())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.RemoveRecoveryFactors(ctx, "org", "user")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	_, err = client.RecoveryFactorEnrollment(ctx, "org", "user", FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().Add(-time.Minute)})
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	require.Zero(t, disclosures.Load())
}
