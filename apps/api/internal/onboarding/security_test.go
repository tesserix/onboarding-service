package onboarding

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSecurityViewFingerprintsNativePolicyAndClaimForStaleRequests(t *testing.T) {
	in := customercontract.SecurityRequest{TenantID: "11111111-1111-4111-8111-111111111111", Subject: "owner", RequestID: "11111111-1111-4111-8111-111111111112"}
	claim := hospitalclaims.Result{ResourceVersion: "7"}
	proof := zitadel.OwnerSecurityProof{RequireMFA: true, ExternalMFA: true, NoGrace: true, Sequence: "13"}
	applied := securityView(in, claim, proof)
	if applied.Validate() != nil || applied.State != customercontract.SecurityApplied {
		t.Fatalf("invalid view %+v", applied)
	}
	proof.RequireMFA = false
	drift := securityView(in, claim, proof)
	if drift.State != customercontract.SecurityDrifted || drift.Revision == applied.Revision {
		t.Fatal("native drift reused an obsolete fingerprint")
	}
	claim.SecurityRequest = in.RequestID
	pending := securityView(in, claim, proof)
	if pending.State != customercontract.SecurityPending || pending.Revision == drift.Revision {
		t.Fatal("pending reconcile presented as applied")
	}
	proof.RequireMFA = true
	if securityView(in, claim, proof).State != customercontract.SecurityApplied {
		t.Fatal("fresh native enforcement was hidden")
	}
}

type securityIdentity struct{ *fakeIdentity }

func (s securityIdentity) VerifyOrganization(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
	return zitadel.OrganizationIdentity{State: "active", OrganizationID: org, TenantID: tenant, ProjectID: project}, nil
}

func TestOwnerSecurityReconciliationIsAuditedPendingAndRevocable(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	tenant := uuid.New()
	orgID := "security-org"
	if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: orgID, Name: "TEST security", CreatedByProduct: "helivanta"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-admin"}); err != nil {
		t.Fatal(err)
	}
	var applied atomic.Bool
	var owner atomic.Bool
	owner.Store(true)
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/users/owner":
			_, _ = fmt.Fprint(w, `{"user":{"userId":"owner","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"security-org"},"human":{"email":{"isVerified":true}}}}`)
		case "/management/v1/orgs/me/members/_search":
			if !owner.Load() {
				_, _ = fmt.Fprint(w, `{"details":{"totalResult":"0"},"result":[]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"details":{"totalResult":"1"},"result":[{"userId":"owner","roles":["ORG_OWNER"],"details":{"resourceOwner":"security-org"}}]}`)
			}
		case "/management/v1/policies/login":
			_, _ = fmt.Fprintf(w, `{"policy":{"forceMfa":%t,"mfaInitSkipLifetime":"0s","details":{"resourceOwner":"security-org","sequence":"13"}}}`, applied.Load())
		default:
			http.NotFound(w, r)
		}
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("test-native"), "platform", nil)
	claim := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "identity.tesserix.app/v1alpha1", "kind": "ZitadelOrganization", "metadata": map[string]any{"name": "helivanta-" + tenant.String(), "namespace": "identity-operator", "resourceVersion": "7", "generation": int64(1)}, "spec": map[string]any{"product": "helivanta", "tenantId": tenant.String(), "initialAdminUserId": "owner"}, "status": map[string]any{"activated": true, "organizationId": orgID, "projectId": "proj-helivanta", "archiveGeneration": "1", "observedGeneration": int64(1), "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}}
	svc.hospitals = &HospitalConfig{Claims: hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme(), claim), "identity-operator")}
	in := customercontract.SecurityRequest{TenantID: tenant.String(), Subject: "owner", RequestID: uuid.NewString()}
	before, err := svc.OwnerSecurity(t.Context(), in, false)
	if err != nil || before.State != customercontract.SecurityDrifted {
		t.Fatalf("cannot read drift: %+v %v", before, err)
	}
	in.ExpectedRevision = strings.Repeat("a", 64)
	if _, err = svc.OwnerSecurity(t.Context(), in, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale policy accepted: %v", err)
	}
	in.ExpectedRevision = before.Revision
	pending, err := svc.OwnerSecurity(t.Context(), in, true)
	if err != nil || pending.State != customercontract.SecurityPending {
		t.Fatalf("request claimed enforcement: %+v %v", pending, err)
	}
	if _, err = svc.OwnerSecurity(t.Context(), in, true); err != nil {
		t.Fatalf("identical request not replayable: %v", err)
	}
	current, err := svc.OwnerSecurity(t.Context(), in, false)
	if err != nil || current.State != customercontract.SecurityPending {
		t.Fatalf("unapplied native policy became success: %+v %v", current, err)
	}
	applied.Store(true)
	current, err = svc.OwnerSecurity(t.Context(), in, false)
	if err != nil || current.State != customercontract.SecurityApplied {
		t.Fatalf("fresh enforcement not confirmed: %+v %v", current, err)
	}
	owner.Store(false)
	if _, err = svc.OwnerSecurity(t.Context(), in, false); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revoked owner retained read: %v", err)
	}
	owner.Store(true)
	if err = st.RevokeGrant(t.Context(), tenant, "helivanta"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.OwnerSecurity(t.Context(), in, false); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revoked enrollment retained access: %v", err)
	}
	records, err := st.AuditChain(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foundRead, foundRequest := false, false
	for _, record := range records {
		if record.OrgID == tenant.String() && record.ActorID == "owner" {
			foundRead = foundRead || record.Action == "owner.security.read"
			foundRequest = foundRequest || record.Action == "owner.security.reconcile"
		}
	}
	if !foundRead || !foundRequest {
		t.Fatal("sensitive access evidence missing")
	}
}
