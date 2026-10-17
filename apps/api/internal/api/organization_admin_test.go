package api

import (
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"net/http"
	"testing"
)

func TestVerifiedCustomerUserDoesNotBecomeOrganizationOwner(t *testing.T) {
	h := newHarness(t)
	org, e := h.store.CreateOrganization(t.Context(), store.Organization{ID: uuid.New(), ZitadelOrgID: "customer-org", Name: "TEST Customer", CreatedByProduct: "planning-poker"})
	if e != nil {
		t.Fatal(e)
	}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil)
	h.router = NewRouter(Deps{Service: svc, Store: h.store, Tokens: stubTokens{claims: map[string]auth.Claims{"customer": {Subject: "ordinary-user", OrgID: "customer-org", Email: "user@example.test", EmailVerified: true, Roles: []string{"ORG_OWNER"}}}}})
	w := h.do(t, http.MethodGet, "/v1/orgs/"+org.ID.String()+"/members", "customer", "")
	if w.Code != 401 {
		t.Fatalf("verified customer became management owner: %d %s", w.Code, w.Body.String())
	}
	h.identity.orgMembers = []onboarding.Member{{UserID: "ordinary-user", Roles: []string{"ORG_OWNER"}}}
	w = h.do(t, http.MethodGet, "/v1/orgs/"+org.ID.String()+"/members", "customer", "")
	if w.Code != 200 {
		t.Fatalf("live owner denied: %d %s", w.Code, w.Body.String())
	}
	h.identity.orgMembers = nil
	w = h.do(t, http.MethodGet, "/v1/orgs/"+org.ID.String()+"/members", "customer", "")
	if w.Code != 401 {
		t.Fatal("owner removal did not take effect")
	}
}
