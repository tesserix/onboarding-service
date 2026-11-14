package zitadel

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type assignmentCandidateTransport func(*http.Request) (*http.Response, error)

func (f assignmentCandidateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAssignmentCandidateProjectionReturnsOnlyTheOwnedVerifiedHumanIdentity(t *testing.T) {
	body := strings.Replace(verifiedMembershipHuman, `"human":{`, `"human":{"profile":{"displayName":"Synthetic Staff"},`, 1)
	calls := 0
	client := New("https://identity.example.invalid", StaticToken("synthetic-test-only"), "platform", nil)
	client.http = &http.Client{Transport: assignmentCandidateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v2/users/synthetic-human", r.URL.Path)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	result, err := client.IdentityProjection(t.Context(), "helivanta-"+membershipTestTenant, "synthetic-human", "assignment_candidate")
	require.NoError(t, err)
	require.NoError(t, result.Validate())
	require.Equal(t, 1, calls)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{"purpose":"assignment_candidate","tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"helivanta-11111111-1111-4111-8111-111111111111","subject":"synthetic-human","state":"active","admission":"unassessed","candidate":{"displayName":"Synthetic Staff","email":"private-contact@example.invalid"}}`, string(raw))
	require.NotContains(t, string(raw), "tenantName")
	require.Nil(t, result.Profile)
}

func TestAssignmentCandidateProjectionWithholdsUnownedUnverifiedOrUnavailableIdentity(t *testing.T) {
	owned := strings.Replace(verifiedMembershipHuman, `"human":{`, `"human":{"profile":{"displayName":"Synthetic Staff"},`, 1)
	for _, tc := range []struct {
		name, body  string
		status      int
		unavailable bool
	}{
		{name: "foreign owner", body: strings.Replace(owned, "helivanta-"+membershipTestTenant, "another-org", 1)},
		{name: "unverified", body: strings.Replace(owned, `"isVerified":true`, `"isVerified":false`, 1)},
		{name: "missing verification", body: strings.Replace(owned, `,"isVerified":true`, "", 1)},
		{name: "inactive", body: strings.Replace(owned, "USER_STATE_ACTIVE", "USER_STATE_INACTIVE", 1)},
		{name: "initial", body: strings.Replace(owned, "USER_STATE_ACTIVE", "USER_STATE_INITIAL", 1)},
		{name: "missing name", body: verifiedMembershipHuman},
		{name: "empty name", body: strings.Replace(owned, "Synthetic Staff", "", 1)},
		{name: "machine", body: `{"user":{"userId":"synthetic-human","state":"USER_STATE_ACTIVE","machine":{"name":"synthetic machine"}}}`},
		{name: "wrong subject", body: strings.Replace(owned, "synthetic-human", "another-subject", 1), unavailable: true},
		{name: "duplicate name", body: strings.Replace(owned, `"displayName":`, `"displayName":"Other","displayName":`, 1), unavailable: true},
		{name: "recased name", body: strings.Replace(owned, `"displayName":`, `"DisplayName":`, 1), unavailable: true},
		{name: "null name", body: strings.Replace(owned, `"Synthetic Staff"`, `null`, 1), unavailable: true},
		{name: "control name", body: strings.Replace(owned, "Synthetic Staff", `Synthetic\nStaff`, 1), unavailable: true},
		{name: "provider refused", body: `{"message":"private-contact@example.invalid"}`, status: 403, unavailable: true},
		{name: "provider unavailable", body: `{"message":"private-contact@example.invalid"}`, status: 503, unavailable: true},
		{name: "unproven missing", body: `{}`, status: 404, unavailable: true},
		{name: "bounded response", body: owned + strings.Repeat(" ", identityReadLimit), unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := New("https://identity.example.invalid", StaticToken("synthetic-test-only"), "platform", nil)
			client.http = &http.Client{Transport: assignmentCandidateTransport(func(*http.Request) (*http.Response, error) {
				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			result, err := client.IdentityProjection(t.Context(), "helivanta-"+membershipTestTenant, "synthetic-human", "assignment_candidate")
			if tc.unavailable {
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.NotContains(t, err.Error(), "private-contact")
			} else {
				require.NoError(t, err)
				require.NoError(t, result.Validate())
			}
			require.Nil(t, result.Candidate)
			require.Nil(t, result.Profile)
			require.Empty(t, result.TenantName)
		})
	}
}
