package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
)

func mcpCall(router http.Handler, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2025-06-18")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestMCPFailsClosedWithoutDedicatedBearerAuthority(t *testing.T) {
	router := NewRouter(Deps{MCPTokens: stubTokens{claims: map[string]auth.Claims{}}})
	w := mcpCall(router, "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if w.Code != 401 {
		t.Fatalf("missing bearer status=%d", w.Code)
	}
	w = mcpCall(router, "forged", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if w.Code != 401 {
		t.Fatalf("forged bearer status=%d", w.Code)
	}
}

func TestMCPIsStatelessAndReadCredentialsCannotDiscoverOrInvokeWrites(t *testing.T) {
	router := NewRouter(Deps{MCPTokens: stubTokens{claims: map[string]auth.Claims{"reader": {Subject: "agent-reader", MCPRead: true}}}})
	w := mcpCall(router, "reader", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "get_hospital_onboarding") || strings.Contains(w.Body.String(), "request_hospital_onboarding") || w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("tool list=%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"cacheScope":"public"`) {
		t.Fatal("caller-filtered tool catalog must not be publicly cacheable")
	}
	w = mcpCall(router, "reader", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"request_hospital_onboarding","arguments":{}}}`)
	if strings.Contains(w.Body.String(), `"structuredContent"`) || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("write exposed: %s", w.Body.String())
	}
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer reader")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 405 {
		t.Fatalf("stateful transport enabled: %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Authorization", "Bearer reader")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin allowed: %d", w.Code)
	}
}

func TestMCPHospitalWritesPersistReplayConflictAndRejectIdentityOverrides(t *testing.T) {
	h := newHarness(t)
	if err := h.store.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Helivanta", ZitadelProjectID: "proj-helivanta", MachineUserID: "svc-helivanta", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao, Tenancy: store.TenancyOrg, Roles: []string{}, DataResidency: "in"}); err != nil {
		t.Fatal(err)
	}
	claims := hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithHospitals(onboarding.HospitalConfig{Claims: claims, Product: "helivanta"}))
	router := NewRouter(Deps{Service: svc, Store: h.store, MCPTokens: stubTokens{claims: map[string]auth.Claims{"creator": {Subject: "dedicated-agent", MCPRead: true, MCPCreate: true}, "reader": {Subject: "other-agent", MCPRead: true}}}})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"request_hospital_onboarding","arguments":{"displayName":"TEST MCP Hospital","initialAdminUserId":"verified-admin","idempotencyKey":"TEST-mcp-onboarding-0001"}}}`
	w := mcpCall(router, "creator", body)
	if w.Code != 200 {
		t.Fatalf("write status=%d %s", w.Code, w.Body.String())
	}
	var result struct {
		Result struct {
			StructuredContent HospitalMCPResult `json:"structuredContent"`
			IsError           bool              `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Result.IsError || result.Result.StructuredContent.TenantID == "" {
		t.Fatalf("claim not persisted: %s %v", w.Body.String(), err)
	}
	replay := mcpCall(router, "creator", body)
	if replay.Body.String() != w.Body.String() {
		t.Fatalf("non-durable replay: %s", replay.Body.String())
	}
	w = mcpCall(router, "creator", strings.Replace(body, "TEST MCP Hospital", "DIFFERENT Hospital", 1))
	if !strings.Contains(w.Body.String(), "conflict") {
		t.Fatalf("changed intent accepted: %s", w.Body.String())
	}
	w = mcpCall(router, "creator", strings.Replace(body, `"displayName":`, `"product":"other-product","displayName":`, 1))
	if !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatalf("identity override accepted: %s", w.Body.String())
	}
	get := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_hospital_onboarding","arguments":{"tenantId":"` + result.Result.StructuredContent.TenantID + `"}}}`
	w = mcpCall(router, "reader", get)
	if !strings.Contains(w.Body.String(), `"state":"pending"`) {
		t.Fatalf("request cannot resume: %s", w.Body.String())
	}
	w = mcpCall(router, "reader", strings.Replace(get, result.Result.StructuredContent.TenantID, "not-a-tenant", 1))
	if !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("malformed locator was not hidden: %s", w.Body.String())
	}
}

func TestMCPAdoptionRequiresSeparateAuthorityAndPreservesPolicy(t *testing.T) {
	h := newHarness(t)
	if err := h.store.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Helivanta", ZitadelProjectID: "proj-helivanta", MachineUserID: "svc-helivanta", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao, Tenancy: store.TenancyOrg, Roles: []string{}, DataResidency: "in"}); err != nil {
		t.Fatal(err)
	}
	claims := hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithHospitals(onboarding.HospitalConfig{Claims: claims, Product: "helivanta"}))
	router := NewRouter(Deps{Service: svc, Store: h.store, MCPTokens: stubTokens{claims: map[string]auth.Claims{"creator": {Subject: "agent", MCPRead: true, MCPCreate: true}, "adopter": {Subject: "agent-adopter", MCPRead: true, MCPAdopt: true}}}})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"adopt_hospital_onboarding","arguments":{"displayName":"Existing Hospital","initialAdminUserId":"verified-admin","idempotencyKey":"0000000000000001","tenantId":"11111111-1111-4111-8111-111111111111","organizationId":"existing-org","preserveLoginPolicy":true}}}`
	w := mcpCall(router, "creator", body)
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("creator could adopt: %s", w.Body.String())
	}
	w = mcpCall(router, "adopter", strings.Replace(body, `"preserveLoginPolicy":true`, `"preserveLoginPolicy":false`, 1))
	if !strings.Contains(w.Body.String(), "invalid_request") {
		t.Fatalf("policy weakening accepted: %s", w.Body.String())
	}
	w = mcpCall(router, "adopter", body)
	if strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), `"tenantId":"11111111-1111-4111-8111-111111111111"`) {
		t.Fatalf("adoption failed: %s", w.Body.String())
	}
	stored, err := claims.Get(t.Context(), "helivanta", "11111111-1111-4111-8111-111111111111")
	if err != nil || !stored.Spec.PreserveLoginPolicy || stored.Spec.OrganizationID != "existing-org" {
		t.Fatalf("binding changed: %+v %v", stored, err)
	}
}
