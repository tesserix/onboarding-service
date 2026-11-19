package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMFAAdmissionRequiresExactCustomerWorkloadAndBoundedPrivateBodies(t *testing.T) {
	router := NewRouter(Deps{CustomerWorkloads: customerCaller{}})
	const scope = `{"tenantId":"11111111-1111-4111-8111-111111111111","subject":"staff","generationId":"019f39c2-1738-7998-b5de-6d4214f614e0","requestId":"22222222-2222-4222-8222-222222222222"}`
	good := `{"scope":` + scope + `,"nativeSessionId":"signed-native-session"}`
	for _, tc := range []struct {
		name, token, body, query string
		duplicate                bool
		status                   int
	}{
		{"anonymous", "", good, "", false, 401},
		{"browser", "Bearer browser", good, "", false, 401},
		{"foreign_workload", "Bearer foreign-workload", good, "", false, 401},
		{"duplicate_workload", "Bearer workload", good, "", true, 401},
		{"null", "Bearer workload", "null", "", false, 400},
		{"legacy_staff_target", "Bearer workload", strings.TrimSuffix(good, "}") + `,"target":{}}`, "", false, 400},
		{"unknown", "Bearer workload", strings.TrimSuffix(good, "}") + `,"approved":true}`, "", false, 400},
		{"oversized", "Bearer workload", good + strings.Repeat(" ", 8193), "", false, 400},
		{"query", "Bearer workload", good, "?actor=browser", false, 400},
		{"dependency_unavailable", "Bearer workload", good, "", false, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/internal/v1/customer-signups/staff-mfa-recovery/admit"+tc.query, strings.NewReader(tc.body))
			r.Header.Set("Authorization", tc.token)
			if tc.duplicate {
				r.Header.Add("Authorization", tc.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, r)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			require.NotContains(t, response.Body.String(), "signed-native-session")
		})
	}
}
