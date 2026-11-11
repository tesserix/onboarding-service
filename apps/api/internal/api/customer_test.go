package api

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type customerCaller struct{}

func (customerCaller) Verify(_ context.Context, token string) error {
	if token != "Bearer workload" {
		return errors.New("denied")
	}
	return nil
}
func TestCustomerSignupEndpointsRequireUnambiguousExactWorkloadCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Deps{CustomerWorkloads: customerCaller{}})
	for _, path := range []string{"/internal/v1/customer-signups", "/internal/v1/customer-signups/verify-email", "/internal/v1/customer-signups/password", "/internal/v1/customer-signups/email-link", "/internal/v1/customer-signups/password-link", "/internal/v1/customer-signups/login-target", "/internal/v1/customer-signups/identity-projection"} {
		for _, token := range []string{"", "Bearer browser", "Bearer foreign-workload"} {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			request.Header.Set("Authorization", token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("%s accepted unauthorized caller: %d", path, response.Code)
			}
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/customer-signups/00000000-0000-0000-0000-000000000001", nil)
	request.Header.Add("Authorization", "Bearer workload")
	request.Header.Add("Authorization", "Bearer workload")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("ambiguous credential accepted: %d", response.Code)
	}
}

func TestOwnerSecurityRequiresSeparateExactWorkloadAudience(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Deps{CustomerWorkloads: customerCaller{}})
	for _, path := range []string{"/internal/v1/owner-security/read", "/internal/v1/owner-security/reconcile"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer workload")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("signup audience admitted to owner security: %d", response.Code)
		}
	}
}
