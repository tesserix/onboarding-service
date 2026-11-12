package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOwnerBoundSignupStatusRequiresWorkloadAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Deps{CustomerWorkloads: customerCaller{}})
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/customer-signups/00000000-0000-0000-0000-000000000001/users/owner-synthetic", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("owner status bypassed workload authentication: %d", response.Code)
	}
}

func TestOwnerBoundStatusReportsUnavailableWithoutService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Deps{CustomerWorkloads: customerCaller{}})
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/customer-signups/00000000-0000-0000-0000-000000000001/users/owner-synthetic", nil)
	request.Header.Set("Authorization", "Bearer workload")
	request.Header.Set("X-Signup-Receipt-Hash", strings.Repeat("a", 64))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing service did not fail closed: %d", response.Code)
	}
}
