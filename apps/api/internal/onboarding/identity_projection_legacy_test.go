package onboarding

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestRetainedSelfReadNeedsEnrolmentAndCurrentAuthorityBeforeDisclosure(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	tenant := uuid.New()
	const org = "123456789"
	var calls atomic.Int32
	var revokeDuringRead atomic.Bool
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v2/users/retained-user" {
			t.Error("native read selected another subject")
		}
		if revokeDuringRead.Load() {
			if err := st.RevokeGrant(r.Context(), tenant, "helivanta"); err != nil {
				t.Error(err)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": "retained-user", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "retained@example.test", "isVerified": true}, "profile": map[string]string{"displayName": "TEST Retained User"}}}})
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	in := customercontract.IdentityProjectionInput{Purpose: "status", TenantID: tenant.String(), Subject: "retained-user", RequestID: uuid.NewString()}
	if _, err := svc.CustomerIdentityProjection(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != 0 {
		t.Fatal("an unregistered tenant reached native identity", err)
	}
	// Enrolment preserves the original native ID and creation provenance.
	if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "TEST Retained Pharmacy", CreatedByProduct: "console"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CustomerIdentityProjection(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != 0 {
		t.Fatal("an unenrolled tenant reached native identity", err)
	}
	if err := st.Grant(t.Context(), tenant, "helivanta", []string{}); err != nil {
		t.Fatal(err)
	}
	proof, err := svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.State != "active" || proof.Profile != nil || proof.OrganizationID != org || proof.TenantID != tenant.String() {
		t.Fatal("registered legacy status could not be read", err)
	}
	in.Purpose, in.IncludeTenantName, in.RequestID = "profile", true, uuid.NewString()
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.Profile == nil || proof.TenantName != "TEST Retained Pharmacy" {
		t.Fatal("registered legacy self profile could not be read", err)
	}
	before := calls.Load()
	in.Purpose, in.IncludeTenantName, in.RequestID = "membership", false, uuid.NewString()
	if _, err = svc.CustomerIdentityProjection(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != before {
		t.Fatal("legacy reads widened new-grant admission", err)
	}
	if _, err = svc.CustomerLoginTarget(t.Context(), customercontract.LoginTargetInput{TenantID: tenant.String(), Email: "retained@example.test", RequestID: uuid.NewString()}); !errors.Is(err, ErrNotFound) {
		t.Fatal("legacy reads widened new tenant-account lookup", err)
	}
	in.Purpose, in.RequestID = "status", uuid.NewString()
	revokeDuringRead.Store(true)
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if !errors.Is(err, ErrUnavailable) || proof.State != "" || proof.Profile != nil {
		t.Fatal("a grant revoked during source I/O still disclosed a projection", err)
	}
	pool, err := pgxpool.New(t.Context(), os.Getenv("ONBOARDING_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var count int
	if err = pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='customer.identity.read' AND request_id=$1`, in.RequestID).Scan(&count); err != nil || count != 0 {
		t.Fatal("a refused read produced a success audit", err, count)
	}
}
