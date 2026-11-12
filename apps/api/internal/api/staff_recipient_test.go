package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStaffRecipientRequiresWorkloadAndRejectsAmbiguousOrUnexpectedProofFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(Deps{CustomerWorkloads: customerCaller{}})
	for _, action := range []string{"status", "email", "password", "resend"} {
		path := "/internal/v1/customer-signups/staff-invitations/" + action
		for _, token := range []string{"", "Bearer browser", "Bearer workload"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"invitation_id":"invalid","ticket":"invalid","isVerified":true}`))
			req.Header.Set("Authorization", token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := http.StatusUnauthorized
			if token == "Bearer workload" {
				want = http.StatusBadRequest
			}
			if w.Code != want {
				t.Fatalf("%s: got %d want %d", action, w.Code, want)
			}
		}
	}
}
