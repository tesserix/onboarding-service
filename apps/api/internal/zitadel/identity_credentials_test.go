package zitadel

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCredentialProjectionUsesNativePasswordChangeAndRefusesMissingEvidence(t *testing.T) {
	const changed = "2026-11-18T10:11:12.123456Z"
	for _, tc := range []struct {
		name, human, methods            string
		unavailable, password, inactive bool
		methodCalls                     int32
	}{
		{name: "native_password", human: `{"passwordChanged":"` + changed + `"}`, password: true},
		{name: "passwordless", human: `{}`, methods: `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSKEY"]}`, methodCalls: 1},
		{name: "missing_password_date", human: `{}`, methods: `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`, methodCalls: 1, unavailable: true},
		{name: "unknown_methods", human: `{}`, methods: `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_UNKNOWN"]}`, methodCalls: 1, unavailable: true},
		{name: "duplicate_inventory", human: `{}`, methods: `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"],"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSKEY"]}`, methodCalls: 1, unavailable: true},
		{name: "recased_inventory", human: `{}`, methods: `{"details":{"totalResult":"1"},"auth_method_types":["AUTHENTICATION_METHOD_TYPE_PASSWORD"],"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSKEY"]}`, methodCalls: 1, unavailable: true},
		{name: "null_date", human: `{"passwordChanged":null}`, unavailable: true},
		{name: "empty_date", human: `{"passwordChanged":""}`, unavailable: true},
		{name: "malformed_date", human: `{"passwordChanged":"yesterday"}`, unavailable: true},
		{name: "zero_date", human: `{"passwordChanged":"0001-01-01T00:00:00Z"}`, unavailable: true},
		{name: "duplicate_date", human: `{"passwordChanged":"` + changed + `","passwordChanged":"` + changed + `"}`, unavailable: true},
		{name: "recased_date", human: `{"PasswordChanged":"` + changed + `"}`, unavailable: true},
		{name: "renamed_date", human: `{"password_changed":"` + changed + `"}`, unavailable: true},
		{name: "null_human", human: `null`, unavailable: true},
		{name: "inactive", human: `null`, inactive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var methods atomic.Int32
			client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer test-pat", r.Header.Get("Authorization"))
				require.Equal(t, http.MethodGet, r.Method)
				switch r.URL.Path {
				case "/v2/users/synthetic-human":
					state := "USER_STATE_ACTIVE"
					if tc.inactive {
						state = "USER_STATE_INACTIVE"
					}
					_, _ = w.Write([]byte(`{"user":{"userId":"synthetic-human","state":"` + state + `","human":` + tc.human + `}}`))
				case "/v2/users/synthetic-human/authentication_methods":
					methods.Add(1)
					_, _ = w.Write([]byte(tc.methods))
				default:
					t.Errorf("unexpected native request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			out, err := client.IdentityCredentialProjection(t.Context(), "helivanta-"+membershipTestTenant, "synthetic-human")
			require.Equal(t, tc.methodCalls, methods.Load())
			if tc.unavailable {
				require.ErrorIs(t, err, ErrIdentityUnavailable)
				require.Empty(t, out.Subject)
				return
			}
			require.NoError(t, err)
			require.NoError(t, out.Validate())
			require.Nil(t, out.Profile)
			require.Nil(t, out.Candidate)
			if tc.inactive {
				require.Equal(t, "inactive", out.State)
				require.Nil(t, out.Credentials)
				return
			}
			require.NotNil(t, out.Credentials)
			require.Equal(t, tc.password, out.Credentials.PasswordConfigured)
			if tc.password {
				require.Equal(t, changed, out.Credentials.PasswordChangedAt)
			}
		})
	}
}
