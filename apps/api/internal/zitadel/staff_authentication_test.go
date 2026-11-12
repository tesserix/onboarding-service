package zitadel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaffPasswordConfigurationReadsExactNativeAccountWithoutChangingCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, body              string
		configured, unavailable bool
	}{
		{"password", `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`, true, false},
		{"no credential", `{"details":{"timestamp":"2026-11-13T00:00:00Z"}}`, false, false},
		{"totp only", `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_TOTP"]}`, false, false},
		{"unknown method", `{"details":{"totalResult":"1"},"authMethodTypes":["UNKNOWN"]}`, false, true},
		{"missing details", `{"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`, false, true},
		{"duplicate method", `{"details":{"totalResult":"2"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD","AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`, false, true},
		{"count mismatch", `{"details":{"totalResult":"2"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`, false, true},
		{"duplicate key", `{"details":{"totalResult":"1"},"authMethodTypes":[],"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.RequestURI() != "/v2/users/TEST-staff/authentication_methods" || r.Header.Get("x-zitadel-orgid") != "TEST-org" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("unexpected account, organization or credential command")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(server.Close)
			client := New(server.URL, StaticToken("test-token"), "platform", nil)
			got, err := client.StaffPasswordConfigured(t.Context(), "TEST-org", "TEST-staff")
			if got != tc.configured || (err != nil) != tc.unavailable {
				t.Fatalf("configuration=%t, error=%v", got, err)
			}
		})
	}
}
