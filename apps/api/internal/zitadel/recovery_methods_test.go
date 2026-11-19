package zitadel

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaffTOTPConfiguredIsPresenceOnlyAndRejectsAmbiguousMethods(t *testing.T) {
	for _, tc := range []struct {
		body                 string
		present, unavailable bool
	}{
		{`{"details":{"totalResult":"2"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD","AUTHENTICATION_METHOD_TYPE_TOTP"]}`, true, false},
		{`{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`, false, false},
		{`{"details":{"timestamp":"2026-01-01T00:00:00Z"}}`, false, false},
		{`{"details":{"totalResult":"2"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_TOTP","AUTHENTICATION_METHOD_TYPE_TOTP"]}`, false, true},
		{`{"details":{"totalResult":"0"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_TOTP"]}`, false, true},
		{`{"details":{"totalResult":"1"},"authMethodTypes":["UNKNOWN"]}`, false, true},
		{`{"details":{"timestamp":"2026-01-01T00:00:00Z"},"authMethodTypes":null}`, false, true},
		{`{"details":{"totalResult":null,"timestamp":"2026-01-01T00:00:00Z"}}`, false, true},
	} {
		client := newProjectionClient(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/v2/users/synthetic-human/authentication_methods", r.URL.Path)
			require.Equal(t, "synthetic-org", r.Header.Get("x-zitadel-orgid"))
			_, _ = w.Write([]byte(tc.body))
		})
		present, err := client.StaffTOTPConfigured(t.Context(), "synthetic-org", "synthetic-human")
		if tc.unavailable {
			require.ErrorIs(t, err, ErrIdentityUnavailable)
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, tc.present, present)
	}
}
