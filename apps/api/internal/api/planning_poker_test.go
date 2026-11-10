package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlanningPokerProvisioningRequiresItsExactWorkload(t *testing.T) {
	router := NewRouter(Deps{PlanningPokerWorkloads: customerCaller{}})
	for _, path := range []string{"/internal/v1/planning-poker/orgs", "/internal/v1/planning-poker/orgs/org/idps", "/internal/v1/planning-poker/orgs/org/members"} {
		for _, token := range []string{"", "Bearer browser", "Bearer foreign-workload"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("foreign caller reached %s: %d", path, rec.Code)
			}
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/planning-poker/orgs", nil)
	req.Header.Add("Authorization", "Bearer workload")
	req.Header.Add("Authorization", "Bearer workload")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("ambiguous authorization accepted")
	}
}
