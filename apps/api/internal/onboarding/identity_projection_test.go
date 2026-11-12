package onboarding

import (
	"context"
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

func TestCustomerIdentityProjectionScopesAdmissionAndAuditsBeforeDisclosure(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	tenant := uuid.New()
	org := "helivanta-" + tenant.String()
	var calls atomic.Int32
	var nativeStatus atomic.Int32
	var foreign atomic.Bool
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v2/users/fresh-staff" {
			t.Error("unexpected identity request")
		}
		if status := nativeStatus.Load(); status != 0 {
			w.WriteHeader(int(status))
			if status == http.StatusNotFound {
				_, _ = w.Write([]byte(`{"code":5,"details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"QUERY-Dfbg2"}]}`))
			}
			return
		}
		owner := org
		if foreign.Load() {
			owner = "foreign-org"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": "fresh-staff", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": owner}, "human": map[string]any{"profile": map[string]string{"displayName": "Synthetic Staff"}, "email": map[string]any{"email": "synthetic@example.test", "isVerified": true}}}})
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	in := customercontract.IdentityProjectionInput{Purpose: "membership", TenantID: tenant.String(), Subject: "fresh-staff", RequestID: uuid.NewString()}
	if _, err := svc.CustomerIdentityProjection(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != 0 {
		t.Fatal("unknown tenant queried identity", err)
	}
	if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "TEST", CreatedByProduct: "helivanta"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CustomerIdentityProjection(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != 0 {
		t.Fatal("unenrolled tenant queried identity", err)
	}
	if err := st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-admin"}); err != nil {
		t.Fatal(err)
	}
	proof, err := svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.Validate() != nil || proof.State != "active" || proof.Admission != "eligible" || proof.Profile != nil {
		t.Fatal("owned projection failed", err)
	}
	auditPool, err := pgxpool.New(t.Context(), os.Getenv("ONBOARDING_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer auditPool.Close()
	var count int
	if err = auditPool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='customer.identity.read' AND request_id=$1 AND target='fresh-staff' AND detail::jsonb->>'purpose'='membership'`, in.RequestID).Scan(&count); err != nil || count != 1 {
		t.Fatal("projection was disclosed without audit", err, count)
	}
	in.Purpose = "profile"
	in.RequestID = uuid.NewString()
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.Profile == nil || proof.TenantName != "" {
		t.Fatal("legacy profile response changed", err)
	}
	in.IncludeTenantName = true
	in.RequestID = uuid.NewString()
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.Profile == nil || proof.TenantName != "TEST" {
		t.Fatal("current customer's name was not projected", err)
	}
	foreign.Store(true)
	in.RequestID = uuid.NewString()
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.Profile == nil || proof.TenantName != "TEST" {
		t.Fatal("legacy profile replaced the verified current customer's name with native home ownership", err)
	}
	in.Purpose = "membership"
	in.IncludeTenantName = false
	foreign.Store(true)
	in.RequestID = uuid.NewString()
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.State != "active" || proof.Admission != "ineligible" || proof.Profile != nil {
		t.Fatal("legacy state leaked foreign profile or authority", err)
	}
	nativeStatus.Store(404)
	in.RequestID = uuid.NewString()
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if err != nil || proof.State != "inactive" || proof.Admission != "ineligible" || proof.Profile != nil {
		t.Fatal("authoritative missing identity not inactive", err)
	}
	nativeStatus.Store(403)
	in.RequestID = uuid.NewString()
	proof, err = svc.CustomerIdentityProjection(t.Context(), in)
	if !errors.Is(err, ErrUnavailable) || proof.State != "" {
		t.Fatal("native refusal fabricated an identity state", err)
	}
	nativeStatus.Store(0)
	if err = st.RevokeGrant(t.Context(), tenant, "helivanta"); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if _, err = svc.CustomerIdentityProjection(t.Context(), in); !errors.Is(err, ErrNotFound) || calls.Load() != before {
		t.Fatal("revoked tenant queried identity", err)
	}
}

func TestCustomerIdentityProjectionAuditOutageDoesNotDisclose(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	pool, err := pgxpool.New(context.Background(), os.Getenv("ONBOARDING_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	svc.store = store.New(pool)
	tenant := uuid.New()
	org := "helivanta-" + tenant.String()
	if _, err = st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "TEST", CreatedByProduct: "helivanta"}); err != nil {
		t.Fatal(err)
	}
	if err = st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-admin"}); err != nil {
		t.Fatal(err)
	}
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pool.Close()
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": "fresh-staff", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "synthetic@example.test", "isVerified": true}}}})
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	result, err := svc.CustomerIdentityProjection(t.Context(), customercontract.IdentityProjectionInput{Purpose: "membership", TenantID: tenant.String(), Subject: "fresh-staff", RequestID: uuid.NewString()})
	if !errors.Is(err, ErrUnavailable) || result.State != "" || result.Profile != nil {
		t.Fatal("audit failure disclosed projection", err)
	}
}
