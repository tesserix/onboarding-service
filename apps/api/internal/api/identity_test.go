package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func identityOrg(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := h.store.CreateOrganization(context.Background(), store.Organization{ID: id, ZitadelOrgID: "org-identity", Name: "PRIVATE_BUSINESS_NAME", BillingEmail: "PRIVATE@example.invalid", State: "active", CreatedByProduct: "planning-poker"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.store.Grant(context.Background(), id, "planning-poker", []string{"planning-poker-user"}); err != nil {
		t.Fatal(err)
	}
	return id
}

func identityObservation(org, tenant, project string) zitadel.OrganizationIdentity {
	return zitadel.OrganizationIdentity{SchemaVersion: 1, TenantID: tenant, OrganizationID: org, ProjectID: project, GrantID: "grant-identity", State: "active", VerifiedAt: time.Now().UTC(), SourceRevisions: zitadel.IdentityRevisions{Organization: "101", Metadata: "102", Project: "103", Grant: "104"}}
}

func TestProductIdentityIsLiveScopedAndMinimized(t *testing.T) {
	h := newHarness(t)
	id := identityOrg(t, h)
	calls := 0
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		calls++
		if org != "org-identity" || tenant != id.String() || project != "proj-planning-poker" {
			t.Error("source identity did not come from authorized projection")
		}
		return identityObservation(org, tenant, project), nil
	}
	for range 2 {
		rec := h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", productToken, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("identity %d %s", rec.Code, rec.Body.String())
		}
		body := decode(t, rec)
		if body["product_key"] != "planning-poker" || body["tenant_id"] != id.String() || body["state"] != "active" || body["schema_version"] != float64(1) {
			t.Fatalf("invalid identity %v", body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("identity response may be cached")
		}
		for _, private := range []string{"PRIVATE_BUSINESS_NAME", "PRIVATE@example.invalid", "billingEmail", "supportEmail", "primaryDomain", "labels", "roles", "machineUserId"} {
			if strings.Contains(rec.Body.String(), private) {
				t.Fatal("private organisation fields disclosed")
			}
		}
	}
	if calls != 2 {
		t.Fatal("fresh verification was replaced by cached projection")
	}
}

func TestProductIdentityDeniesOtherAndRevokedScopesBeforeSource(t *testing.T) {
	for _, kind := range []string{"other product", "unknown organisation", "revoked grant", "human principal", "unauthenticated"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			id := identityOrg(t, h)
			calls := 0
			h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
				calls++
				return identityObservation(org, tenant, project), nil
			}
			token := productToken
			expected := http.StatusNotFound
			switch kind {
			case "other product":
				token = otherToken
			case "unknown organisation":
				id = uuid.New()
			case "revoked grant":
				if err := h.store.RevokeGrant(context.Background(), id, "planning-poker"); err != nil {
					t.Fatal(err)
				}
			case "human principal":
				token = platformToken
			case "unauthenticated":
				token = ""
				expected = http.StatusUnauthorized
			}
			rec := h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", token, "")
			if rec.Code != expected || calls != 0 {
				t.Fatalf("scope denial status=%d source calls=%d", rec.Code, calls)
			}
		})
	}
}

func TestProductIdentityDoesNotAcceptCallerProductOrIdentityOverrides(t *testing.T) {
	h := newHarness(t)
	id := identityOrg(t, h)
	calls := 0
	h.identity.verify = func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error) {
		calls++
		return zitadel.OrganizationIdentity{}, nil
	}
	rec := h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity?productKey=mark8ly", productToken, "")
	if rec.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("caller selected identity source: %d calls %d", rec.Code, calls)
	}
}

func TestProductIdentityNeverFallsBackToProjectionOnSourceFailure(t *testing.T) {
	for _, kind := range []string{"source unavailable", "mismatched tenant", "mismatched org", "mismatched project", "stale observation", "missing provenance", "unknown state", "unknown schema"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			id := identityOrg(t, h)
			h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
				proof := identityObservation(org, tenant, project)
				switch kind {
				case "source unavailable":
					return proof, errors.New("PRIVATE_SOURCE_TOKEN_DO_NOT_LOG")
				case "mismatched tenant":
					proof.TenantID = uuid.NewString()
				case "mismatched org":
					proof.OrganizationID = "other"
				case "mismatched project":
					proof.ProjectID = "other"
				case "stale observation":
					proof.VerifiedAt = time.Now().Add(-time.Minute)
				case "missing provenance":
					proof.SourceRevisions.Metadata = ""
				case "unknown state":
					proof.State = "unknown"
				case "unknown schema":
					proof.SchemaVersion = 2
				}
				return proof, nil
			}
			rec := h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", productToken, "")
			if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "PRIVATE_") {
				t.Fatalf("projection fallback or sensitive error: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestProductIdentityReturnsInactiveAndMissingSourceSafely(t *testing.T) {
	h := newHarness(t)
	id := identityOrg(t, h)
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		proof := identityObservation(org, tenant, project)
		proof.State = "inactive"
		return proof, nil
	}
	rec := h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", productToken, "")
	if rec.Code != http.StatusOK || decode(t, rec)["state"] != "inactive" {
		t.Fatalf("inactive source: %d", rec.Code)
	}
	h.identity.verify = func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error) {
		return zitadel.OrganizationIdentity{}, zitadel.ErrIdentityMissing
	}
	if rec = h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", productToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing source %d", rec.Code)
	}
}

func TestProductIdentityRechecksGrantAfterSourceWithoutHoldingDatabaseLocks(t *testing.T) {
	h := newHarness(t)
	id := identityOrg(t, h)
	calls := 0
	h.identity.verify = func(ctx context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		calls++
		if err := h.store.RevokeGrant(ctx, id, "planning-poker"); err != nil {
			t.Fatal(err)
		}
		return identityObservation(org, tenant, project), nil
	}
	rec := h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", productToken, "")
	if rec.Code != http.StatusNotFound || calls != 1 {
		t.Fatalf("grant revoked during observation still allowed: %d calls=%d", rec.Code, calls)
	}
}
