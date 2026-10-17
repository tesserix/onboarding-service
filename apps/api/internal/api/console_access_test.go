package api

import (
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"net/http"
	"strings"
	"testing"
)

func TestConsoleAccessPreservesActorAndSeparatesReadWriteAndDeletion(t *testing.T) {
	h := newHarness(t)
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil)
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{claims: map[string]auth.Claims{}}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"console-reader": {Console: true, Subject: "human-reader", OrgID: platformOrg, Roles: []string{authz.RolePlatformOperator}},
		"console-admin":  {Console: true, Subject: "human-admin", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
	}}})
	if rec := h.do(t, http.MethodGet, "/v1/me", "console-admin", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "human-admin") {
		t.Fatalf("actor lost: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.do(t, http.MethodGet, "/v1/products", "console-reader", ""); rec.Code != 200 {
		t.Fatalf("reader refused: %d", rec.Code)
	}
	if rec := h.do(t, http.MethodPost, "/v1/products", "console-reader", `{"key":"test","displayName":"Test","zitadelProjectId":"p1","machineUserId":"m1","identity":"zitadel","tenantSecrets":"openbao","tenancy":"org","dataResidency":"in"}`); rec.Code != 403 {
		t.Fatalf("reader write: %d", rec.Code)
	}
	if rec := h.do(t, http.MethodDelete, "/v1/orgs/11111111-1111-4111-8111-111111111111/grants/mark8ly", "console-admin", ""); rec.Code != 403 {
		t.Fatalf("delete without hard-delete: %d", rec.Code)
	}
	if rec := h.do(t, http.MethodGet, "/v1/products", "unknown", ""); rec.Code != 401 {
		t.Fatalf("unknown accepted: %d", rec.Code)
	}
}
