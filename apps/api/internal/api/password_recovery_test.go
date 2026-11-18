package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestPasswordRecoveryChallengeRequiresExactPrivateWorkloadAndStrictInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Deps{CustomerWorkloads: customerCaller{}})
	id := uuid.Must(uuid.NewV7()).String()
	browser := strings.Repeat("a", 64)
	validStart := `{"id":"` + id + `","tenantId":"` + uuid.NewString() + `","email":"person@example.test","browserHash":"` + browser + `","sourceHash":"` + browser + `"}`
	validProof := `{"id":"` + id + `","browserHash":"` + browser + `","code":"12345678"}`
	for _, path := range []string{"/internal/v1/customer-signups/password-recovery/start", "/internal/v1/customer-signups/password-recovery/verify", "/internal/v1/customer-signups/password-recovery/status"} {
		for _, authorization := range [][]string{nil, {"Bearer browser"}, {"Bearer foreign-workload"}, {"Bearer workload", "Bearer workload"}} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			for _, value := range authorization {
				req.Header.Add("Authorization", value)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != 401 || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("untrusted recovery caller: %s %d", path, rec.Code)
			}
		}
		body := validStart
		if strings.HasSuffix(path, "verify") {
			body = validProof
		}
		if strings.HasSuffix(path, "status") {
			body = `{"id":"` + id + `","browserHash":"` + browser + `"}`
		}
		for _, tc := range []struct {
			body   string
			status int
		}{
			{`{}`, 400}, {strings.TrimSuffix(body, "}") + `,"unexpected":true}`, 400}, {strings.TrimSuffix(body, "}") + `,"id":"` + id + `"}`, 400}, {body, 503},
		} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer workload")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("strict recovery boundary: %s got %d want %d", path, rec.Code, tc.status)
			}
		}
	}
}
