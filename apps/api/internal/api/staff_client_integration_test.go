package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"github.com/tesserix/onboarding-service/api/staffclient"
	"github.com/tesserix/onboarding-service/api/staffcontract"
)

type staffClientToken struct{}

func (staffClientToken) Token(context.Context) (string, error) { return productToken, nil }

type ownerRouterTransport struct{ router http.Handler }

func (t ownerRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.router.ServeHTTP(w, req)
	return w.Result(), nil
}

func TestStaffPublicClientUsesTheRealOwnerScopeAndOneCreationDispatch(t *testing.T) {
	creates := 0
	var born time.Time
	h, c, _ := staffCreationFixture(t, staffSource{
		create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
			creates++
			born = time.Now().UTC()
			return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: born}, nil
		},
		observe: func(_ context.Context, in zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffIdentityObservation, error) {
			return zitadel.StaffIdentityObservation{UserID: in.UserID, OrganizationID: in.OrganizationID, State: "active", EmailVerified: true, CreatedAt: born, ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
		},
	})
	client, err := staffclient.New(staffclient.Config{Endpoint: "http://onboarding-api.onboarding.svc.cluster.local:8080", ProductKey: "planning-poker", Tokens: staffClientToken{}, HTTP: &http.Client{Transport: ownerRouterTransport{h.router}}})
	if err != nil {
		t.Fatal(err)
	}
	scope := staffclient.Scope{TenantID: c.OrgID, CommandID: c.ID, ActorSubject: c.ActorSubject}
	in := staffcontract.StaffIdentityCreateInput{ActorSubject: c.ActorSubject, Email: c.Email, GivenName: c.GivenName, FamilyName: c.FamilyName, Reason: c.Reason}
	for range 2 {
		receipt, err := client.Create(t.Context(), scope, in)
		if err != nil || receipt.CommandID != c.ID || receipt.CommandState != "created" || receipt.UserID == "" {
			t.Fatalf("public create: %v", err)
		}
	}
	if creates != 1 {
		t.Fatal("SDK replay repeated native creation")
	}
	if receipt, err := client.Get(t.Context(), scope); err != nil || receipt.UserID == "" {
		t.Fatal("read did not preserve created identity")
	}
	if receipt, err := client.Observe(t.Context(), scope); err != nil || !receipt.Observation.EmailVerified {
		t.Fatal("SDK did not obtain a current scoped source observation")
	}
	scope.ActorSubject = "different-actor"
	if _, err := client.Get(t.Context(), scope); err == nil {
		t.Fatal("another initiating actor obtained a private receipt")
	}
}

func TestExpectedProductMismatchStopsBeforeAnyNativeAccountCreation(t *testing.T) {
	creates := 0
	h, c, _ := staffCreationFixture(t, staffSource{create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		creates++
		return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().UTC()}, nil
	}})
	client, err := staffclient.New(staffclient.Config{Endpoint: "http://onboarding-api.onboarding.svc.cluster.local:8080", ProductKey: "different-product", Tokens: staffClientToken{}, HTTP: &http.Client{Transport: ownerRouterTransport{h.router}}})
	if err != nil {
		t.Fatal(err)
	}
	scope := staffclient.Scope{TenantID: c.OrgID, CommandID: c.ID, ActorSubject: c.ActorSubject}
	if _, err = client.Create(t.Context(), scope, staffcontract.StaffIdentityCreateInput{ActorSubject: c.ActorSubject, Email: c.Email, GivenName: c.GivenName, FamilyName: c.FamilyName, Reason: c.Reason}); err == nil || creates != 0 {
		t.Fatal("incorrect product configuration caused a native mutation")
	}
}
