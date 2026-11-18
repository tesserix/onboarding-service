package onboarding

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestCustomerCredentialProjectionRequiresCurrentScopeAndAuditsItsOptIn(t *testing.T) {
	for _, mode := range []string{"legacy", "explicit", "revoked_during_read", "storage_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			svc, idp, st := newService(t)
			seedProduct(t, st, "helivanta")
			svc.idp = securityIdentity{idp}
			tenant := uuid.New()
			org := "helivanta-" + tenant.String()
			_, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "TEST Recovery Hospital", CreatedByProduct: "helivanta"})
			if err != nil {
				t.Fatal(err)
			}
			if err = st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-admin"}); err != nil {
				t.Fatal(err)
			}
			auditPool, err := pgxpool.New(t.Context(), os.Getenv("ONBOARDING_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(auditPool.Close)
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/users/recovery-staff" || r.Method != http.MethodGet {
					t.Error("unexpected native credential read")
				}
				if mode == "revoked_during_read" {
					if err := st.RevokeGrant(r.Context(), tenant, "helivanta"); err != nil {
						t.Error(err)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": "recovery-staff", "state": "USER_STATE_ACTIVE", "human": map[string]string{"passwordChanged": "2026-11-18T10:11:12Z"}}})
			}))
			t.Cleanup(native.Close)
			svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
			if mode == "storage_unavailable" {
				closed, err := pgxpool.New(t.Context(), os.Getenv("ONBOARDING_TEST_DSN"))
				if err != nil {
					t.Fatal(err)
				}
				closed.Close()
				svc.store = store.New(closed)
			}
			in := customercontract.IdentityProjectionInput{Purpose: "status", TenantID: tenant.String(), Subject: "recovery-staff", RequestID: uuid.NewString(), IncludeCredentialState: mode != "legacy"}
			out, err := svc.CustomerIdentityProjection(t.Context(), in)
			if mode == "revoked_during_read" || mode == "storage_unavailable" {
				want := ErrNotFound
				if mode == "storage_unavailable" {
					want = ErrUnavailable
				}
				if !errors.Is(err, want) || out.Credentials != nil || out.Subject != "" {
					t.Fatal("failed authority or persistence disclosed credential state", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "legacy" {
				if out.Credentials != nil {
					t.Fatal("legacy status disclosed new credential facts")
				}
			} else if out.Credentials == nil || !out.Credentials.PasswordConfigured || out.Credentials.PasswordChangedAt != "2026-11-18T10:11:12Z" {
				t.Fatal("native change evidence was lost")
			}
			var audits int
			err = auditPool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE request_id=$1 AND target='recovery-staff' AND action='customer.identity.read' AND detail::jsonb->>'purpose'='status' AND coalesce((detail::jsonb->>'credentialState')::boolean,false)=$2`, in.RequestID, mode != "legacy").Scan(&audits)
			if err != nil || audits != 1 {
				t.Fatal("credential disclosure lacks its attributable audit", err, audits)
			}
		})
	}
}
