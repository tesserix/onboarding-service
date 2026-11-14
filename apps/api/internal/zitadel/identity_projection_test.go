package zitadel

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newProjectionClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(server.URL, StaticToken("test-pat"), "platform", nil)
}

const membershipTestTenant = "11111111-1111-4111-8111-111111111111"

const confirmedMissingUserResponse = `{"code":5,"message":"User not found","details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"QUERY-Dfbg2","message":"Errors.User.NotFound"}]}`

const verifiedMembershipHuman = `{"user":{"userId":"synthetic-human","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"helivanta-11111111-1111-4111-8111-111111111111"},"human":{"email":{"email":"private-contact@example.invalid","isVerified":true}}}}`

func TestNativeIdentityProjectionRequiresOneMatchingVerifiedHumanProof(t *testing.T) {
	for _, tc := range []struct {
		name, body            string
		status                int
		eligible, unavailable bool
	}{
		{name: "verified_human", body: verifiedMembershipHuman, eligible: true},
		{name: "unverified_human", body: strings.Replace(verifiedMembershipHuman, `"isVerified":true`, `"isVerified":false`, 1)},
		{name: "elided_false", body: strings.Replace(verifiedMembershipHuman, `,"isVerified":true`, ``, 1)},
		{name: "inactive", body: strings.Replace(verifiedMembershipHuman, "USER_STATE_ACTIVE", "USER_STATE_INACTIVE", 1)},
		{name: "locked", body: strings.Replace(verifiedMembershipHuman, "USER_STATE_ACTIVE", "USER_STATE_LOCKED", 1)},
		{name: "suspended", body: strings.Replace(verifiedMembershipHuman, "USER_STATE_ACTIVE", "USER_STATE_SUSPEND", 1)},
		{name: "initial", body: strings.Replace(verifiedMembershipHuman, "USER_STATE_ACTIVE", "USER_STATE_INITIAL", 1)},
		{name: "deleted", body: strings.Replace(verifiedMembershipHuman, "USER_STATE_ACTIVE", "USER_STATE_DELETED", 1)},
		{name: "machine", body: `{"user":{"userId":"synthetic-human","state":"USER_STATE_ACTIVE","machine":{"name":"synthetic service"}}}`},
		{name: "confirmed_missing_identity", status: http.StatusNotFound, body: confirmedMissingUserResponse},
		{name: "wrong_missing_proof", status: http.StatusNotFound, body: strings.Replace(confirmedMissingUserResponse, "QUERY-Dfbg2", "QUERY-other", 1), unavailable: true},
		{name: "wrong_missing_code", status: http.StatusNotFound, body: strings.Replace(confirmedMissingUserResponse, `"code":5`, `"code":7`, 1), unavailable: true},
		{name: "duplicate_missing_proof", status: http.StatusNotFound, body: strings.Replace(confirmedMissingUserResponse, `"id":`, `"id":"QUERY-other","id":`, 1), unavailable: true},
		{name: "unproven_missing_identity", status: http.StatusNotFound, body: `{}`, unavailable: true},
		{name: "gateway_missing_route", status: http.StatusNotFound, body: `{"code":5,"message":"Not Found"}`, unavailable: true},
		{name: "html_missing_route", status: http.StatusNotFound, body: `<html>Not Found</html>`, unavailable: true},
		{name: "provider_outage", status: http.StatusServiceUnavailable, body: `{"message":"private-contact@example.invalid"}`, unavailable: true},
		{name: "provider_refusal", status: http.StatusForbidden, body: `{"message":"private-contact@example.invalid"}`, unavailable: true},
		{name: "wrong_subject", body: strings.Replace(verifiedMembershipHuman, "synthetic-human", "another-human", 1), unavailable: true},
		{name: "missing_subject", body: strings.Replace(verifiedMembershipHuman, `"userId":"synthetic-human",`, ``, 1), unavailable: true},
		{name: "unknown_state", body: strings.Replace(verifiedMembershipHuman, "USER_STATE_ACTIVE", "USER_STATE_FUTURE", 1), unavailable: true},
		{name: "missing_state", body: strings.Replace(verifiedMembershipHuman, `"state":"USER_STATE_ACTIVE",`, ``, 1), unavailable: true},
		{name: "duplicate_subject", body: strings.Replace(verifiedMembershipHuman, `"userId":`, `"userId":"another-human","userId":`, 1), unavailable: true},
		{name: "duplicate_user", body: strings.Replace(verifiedMembershipHuman, `{"user":`, `{"user":{},"user":`, 1), unavailable: true},
		{name: "conflicting_verification", body: strings.Replace(verifiedMembershipHuman, `"isVerified":true`, `"isVerified":false,"isVerified":true`, 1), unavailable: true},
		{name: "recased_verification", body: strings.Replace(verifiedMembershipHuman, `"isVerified":true`, `"IsVerified":false,"isVerified":true`, 1), unavailable: true},
		{name: "renamed_verification", body: strings.Replace(verifiedMembershipHuman, "isVerified", "is_verified", 1), unavailable: true},
		{name: "nonboolean_verification", body: strings.Replace(verifiedMembershipHuman, `"isVerified":true`, `"isVerified":"true"`, 1), unavailable: true},
		{name: "null_verification", body: strings.Replace(verifiedMembershipHuman, `"isVerified":true`, `"isVerified":null`, 1), unavailable: true},
		{name: "missing_email", body: strings.Replace(verifiedMembershipHuman, `"email":"private-contact@example.invalid",`, ``, 1), unavailable: true},
		{name: "empty_verified_email", body: strings.Replace(verifiedMembershipHuman, "private-contact@example.invalid", "", 1), unavailable: true},
		{name: "malformed_verified_email", body: strings.Replace(verifiedMembershipHuman, "private-contact@example.invalid", "invalid address", 1), unavailable: true},
		{name: "display_name_in_email", body: strings.Replace(verifiedMembershipHuman, "private-contact@example.invalid", "Staff <private-contact@example.invalid>", 1), unavailable: true},
		{name: "ambiguous_kind", body: strings.Replace(verifiedMembershipHuman, `"human":`, `"machine":{},"human":`, 1), unavailable: true},
		{name: "unknown_kind", body: `{"user":{"userId":"synthetic-human","state":"USER_STATE_ACTIVE"}}`, unavailable: true},
		{name: "null_human", body: `{"user":{"userId":"synthetic-human","state":"USER_STATE_ACTIVE","human":null}}`, unavailable: true},
		{name: "empty_response", body: `{}`, unavailable: true},
		{name: "invalid_json", body: `{`, unavailable: true},
		{name: "second_document", body: verifiedMembershipHuman + `{}`, unavailable: true},
		{name: "oversized_valid_prefix", body: verifiedMembershipHuman + strings.Repeat(" ", identityReadLimit), unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/v2/users/synthetic-human", r.URL.Path)
				require.Equal(t, "Bearer test-pat", r.Header.Get("Authorization"))
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			})
			projection, err := client.IdentityProjection(t.Context(), membershipTestTenant, "helivanta-"+membershipTestTenant, "synthetic-human", "membership")
			require.Equal(t, tc.eligible, projection.Admission == "eligible")
			require.EqualValues(t, 1, calls.Load(), "identity state and contact must share one observation")
			if tc.unavailable {
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.NotContains(t, err.Error(), "private-contact@example.invalid")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProjectionProfilesRequireExplicitCurrentUserPurpose(t *testing.T) {
	profile := strings.Replace(verifiedMembershipHuman, `"human":{`, `"human":{"profile":{"displayName":"Synthetic Staff"},`, 1)
	for _, tc := range []struct {
		name, body, admission, purpose string
		hasProfile                     bool
	}{
		{"owned", profile, "unassessed", "profile", true},
		{"foreign", strings.Replace(profile, "helivanta-"+membershipTestTenant, "foreign-org", 1), "unassessed", "profile", true},
		{"unverified", strings.Replace(profile, `"isVerified":true`, `"isVerified":false`, 1), "unassessed", "profile", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) })
			proof, err := client.IdentityProjection(t.Context(), membershipTestTenant, "helivanta-"+membershipTestTenant, "synthetic-human", tc.purpose)
			require.NoError(t, err)
			require.NoError(t, proof.Validate())
			require.Equal(t, "active", proof.State)
			require.Equal(t, tc.admission, proof.Admission)
			if tc.hasProfile {
				require.NotNil(t, proof.Profile)
				require.Equal(t, "Synthetic Staff", proof.Profile.DisplayName)
			} else {
				require.Nil(t, proof.Profile)
			}
		})
	}
}

func TestProjectionMissingClientInvalidScopeAndCancellationFailClosed(t *testing.T) {
	var missing *Client
	_, err := missing.IdentityProjection(t.Context(), membershipTestTenant, "helivanta-"+membershipTestTenant, "synthetic-human", "membership")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	var calls atomic.Int32
	client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(verifiedMembershipHuman))
	})
	_, err = client.IdentityProjection(t.Context(), membershipTestTenant, "foreign-org", "synthetic-human", "membership")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.IdentityProjection(ctx, membershipTestTenant, "helivanta-"+membershipTestTenant, "synthetic-human", "membership")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	require.Zero(t, calls.Load())
}

func TestIdentityStatusReadsNoProfileAndAssessesNoGrant(t *testing.T) {
	for _, tc := range []struct {
		native, expected string
		unavailable      bool
	}{
		{"USER_STATE_ACTIVE", "active", false}, {"USER_STATE_INACTIVE", "inactive", false}, {"USER_STATE_LOCKED", "inactive", false},
		{"USER_STATE_DELETED", "inactive", false}, {"USER_STATE_INITIAL", "inactive", false}, {"USER_STATE_SUSPEND", "inactive", false},
		{"USER_STATE_FUTURE", "", true}, {"", "", true},
	} {
		t.Run(tc.native, func(t *testing.T) {
			client := newProjectionClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"user":{"userId":"synthetic-human","state":%q}}`, tc.native)
			})
			proof, err := client.IdentityProjection(t.Context(), membershipTestTenant, "helivanta-"+membershipTestTenant, "synthetic-human", "status")
			if tc.unavailable {
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.Empty(t, proof.State)
			} else {
				require.NoError(t, err)
				require.NoError(t, proof.Validate())
				require.Equal(t, tc.expected, proof.State)
				require.Equal(t, "unassessed", proof.Admission)
				require.Nil(t, proof.Profile)
			}
		})
	}
}
