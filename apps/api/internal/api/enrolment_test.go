package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestExistingOrganisationEnrolmentPreservesIdentityAndAuditsOnce(t *testing.T) {
	h := newHarness(t)
	id := uuid.New()
	calls := 0
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		calls++
		if org != "existing-source-org" || tenant != id.String() || project != "proj-planning-poker" {
			t.Fatal("enrolment verified a different source binding")
		}
		return identityObservation(org, tenant, project), nil
	}
	body := fmt.Sprintf(`{"tenantId":%q,"zitadelOrgId":"existing-source-org","productKey":"planning-poker","name":"Synthetic existing pharmacy","reason":"Adopt existing test tenant without replacing identity"}`, id)
	for range 2 {
		rec := h.do(t, http.MethodPost, "/v1/orgs/enrolments", platformToken, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("enrol existing organisation: %d %s", rec.Code, rec.Body.String())
		}
		if decode(t, rec)["id"] != id.String() {
			t.Fatal("enrolment changed the existing tenant UUID")
		}
	}
	org, err := h.store.Organization(context.Background(), id)
	if err != nil || org.ZitadelOrgID != "existing-source-org" || org.Name != "Synthetic existing pharmacy" {
		t.Fatalf("existing source binding was not preserved: %v", err)
	}
	grants, err := h.store.Grants(context.Background(), id)
	if err != nil || len(grants) != 1 || grants[0].ProductKey != "planning-poker" || grants[0].RevokedAt != nil || len(grants[0].Roles) != 0 {
		t.Fatalf("expected one entitlement without inferred roles: %v", err)
	}
	events, err := h.store.AuditChain(context.Background())
	if err != nil || audit.Verify(events) != nil {
		t.Fatal("invalid audit chain")
	}
	count := 0
	for _, event := range events {
		if event.OrgID == id.String() && event.Action == "org.enrol" && event.Outcome == audit.OutcomeSuccess {
			count++
			if event.ActorID != "sam" || event.ProductKey != "planning-poker" || event.Detail == "{}" {
				t.Fatal("enrolment audit lacks attribution")
			}
		}
	}
	if count != 1 || calls != 2 || h.identity.zitadelOrgs != 0 {
		t.Fatalf("effects were duplicated or identity recreated: audits=%d reads=%d creates=%d", count, calls, h.identity.zitadelOrgs)
	}
	rec := h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", productToken, "")
	if rec.Code != http.StatusOK || decode(t, rec)["tenant_id"] != id.String() || calls != 3 {
		t.Fatal("enrolment did not permit a fresh scoped product identity read")
	}
	if err := h.store.RevokeGrant(context.Background(), id, "planning-poker"); err != nil {
		t.Fatal(err)
	}
	rec = h.do(t, http.MethodGet, "/v1/orgs/"+id.String()+"/identity", productToken, "")
	if rec.Code != http.StatusNotFound || calls != 3 {
		t.Fatal("revocation did not block the enrolled identity before source I/O")
	}
}

func TestExistingOrganisationEnrolmentDeniesProductsBeforeSource(t *testing.T) {
	for _, token := range []string{productToken, otherToken, ""} {
		t.Run(token, func(t *testing.T) {
			h := newHarness(t)
			calls := 0
			h.identity.verify = func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error) {
				calls++
				return zitadel.OrganizationIdentity{}, nil
			}
			status := http.StatusForbidden
			if token == "" {
				status = http.StatusUnauthorized
			}
			body := fmt.Sprintf(`{"tenantId":%q,"zitadelOrgId":"existing-source-org","productKey":"planning-poker","name":"Test pharmacy","reason":"Test adoption"}`, uuid.New())
			rec := h.do(t, http.MethodPost, "/v1/orgs/enrolments", token, body)
			if rec.Code != status || calls != 0 {
				t.Fatalf("enrolment bypassed authorisation: status=%d reads=%d", rec.Code, calls)
			}
		})
	}
}

func TestExistingOrganisationEnrolmentRejectsUnverifiedBindings(t *testing.T) {
	for _, kind := range []string{"missing", "unavailable", "inactive", "wrong tenant", "wrong org", "wrong project", "missing revision", "missing reason", "invalid source", "missing product"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			id := uuid.New()
			calls := 0
			h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
				calls++
				proof := identityObservation(org, tenant, project)
				switch kind {
				case "missing":
					return proof, zitadel.ErrIdentityMissing
				case "unavailable":
					return proof, errors.New("PRIVATE_SOURCE_ERROR")
				case "inactive":
					proof.State = "inactive"
				case "wrong tenant":
					proof.TenantID = uuid.NewString()
				case "wrong org":
					proof.OrganizationID = "other-org"
				case "wrong project":
					proof.ProjectID = "other-project"
				case "missing revision":
					proof.SourceRevisions.Grant = ""
				}
				return proof, nil
			}
			body := fmt.Sprintf(`{"tenantId":%q,"zitadelOrgId":"existing-source-org","productKey":"planning-poker","name":"Test pharmacy","reason":"Test adoption"}`, id)
			status := http.StatusServiceUnavailable
			switch kind {
			case "missing", "missing product":
				status = http.StatusNotFound
			case "inactive":
				status = http.StatusConflict
			case "missing reason":
				body = strings.Replace(body, "Test adoption", "   ", 1)
				status = http.StatusBadRequest
			case "invalid source":
				body = strings.Replace(body, "existing-source-org", "../other", 1)
				status = http.StatusBadRequest
			}
			if kind == "missing product" {
				body = strings.Replace(body, "planning-poker", "unknown-product", 1)
			}
			rec := h.do(t, http.MethodPost, "/v1/orgs/enrolments", platformToken, body)
			if rec.Code != status || strings.Contains(rec.Body.String(), "PRIVATE_SOURCE_ERROR") {
				t.Fatalf("unsafe source acceptance: status=%d expected=%d", rec.Code, status)
			}
			if _, err := h.store.Organization(context.Background(), id); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("failed verification created a registry row")
			}
			if (kind == "missing reason" || kind == "invalid source" || kind == "missing product") && calls != 0 {
				t.Fatal("invalid claims reached provider")
			}
		})
	}
}

func TestExistingOrganisationEnrolmentDoesNotReviveSuspendedOrRevokedRecords(t *testing.T) {
	for _, kind := range []string{"inactive organisation", "revoked grant", "conflicting source", "conflicting tenant", "concurrent revocation"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			id := identityOrg(t, h)
			if kind == "inactive organisation" {
				if err := h.store.SetOrganizationState(context.Background(), id, "inactive"); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "revoked grant" {
				if err := h.store.RevokeGrant(context.Background(), id, "planning-poker"); err != nil {
					t.Fatal(err)
				}
			}
			h.identity.verify = func(ctx context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
				if kind == "concurrent revocation" {
					if err := h.store.RevokeGrant(ctx, id, "planning-poker"); err != nil {
						t.Fatal(err)
					}
				}
				return identityObservation(org, tenant, project), nil
			}
			requested := id
			source := "org-identity"
			if kind == "conflicting source" {
				source = "different-source"
			}
			if kind == "conflicting tenant" {
				requested = uuid.New()
			}
			body := fmt.Sprintf(`{"tenantId":%q,"zitadelOrgId":%q,"productKey":"planning-poker","name":"Must not overwrite","reason":"Test conflict"}`, requested, source)
			rec := h.do(t, http.MethodPost, "/v1/orgs/enrolments", platformToken, body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("unsafe enrolment transition: %d %s", rec.Code, rec.Body.String())
			}
			original, err := h.store.Organization(context.Background(), id)
			if err != nil || original.ZitadelOrgID != "org-identity" || original.Name != "PRIVATE_BUSINESS_NAME" {
				t.Fatal("enrolment rewrote an existing profile")
			}
		})
	}
}
