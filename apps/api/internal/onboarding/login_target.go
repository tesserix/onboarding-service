package onboarding

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// CustomerLoginTarget resolves a tenant-owned account for the verified API workload.
func (s *Service) CustomerLoginTarget(ctx context.Context, in customercontract.LoginTargetInput) (customercontract.LoginTarget, error) {
	var empty customercontract.LoginTarget
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.idp == nil || s.signupIdentity == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	orgID, err := s.customerIdentityScope(ctx, in.TenantID)
	if err != nil {
		return empty, err
	}
	subject, err := s.signupIdentity.TenantLoginSubject(ctx, orgID, in.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: in.TenantID, Action: "customer.login.target_read", Outcome: audit.OutcomeSuccess, RequestID: in.RequestID}); err != nil {
		return empty, ErrUnavailable
	}
	return customercontract.LoginTarget{TenantID: in.TenantID, OrganizationID: orgID, Subject: subject}, nil
}

func (s *Service) customerIdentityScope(ctx context.Context, tenantID string) (string, error) {
	binding, err := s.verifiedCustomerIdentity(ctx, tenantID, true)
	return binding.organization.ZitadelOrgID, err
}

type customerIdentityBinding struct {
	organization store.Organization
	product      store.Product
}

func (s *Service) verifiedCustomerIdentity(ctx context.Context, tenantID string, tenantOwned bool) (customerIdentityBinding, error) {
	var empty customerIdentityBinding
	tenant, err := uuid.Parse(tenantID)
	if err != nil || tenant == uuid.Nil || tenant.String() != tenantID {
		return empty, ErrNotFound
	}
	org, err := s.store.Organization(ctx, tenant)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if org.State != "active" || !enrolSourceID.MatchString(org.ZitadelOrgID) {
		return empty, ErrNotFound
	}
	if tenantOwned && (org.CreatedByProduct != "helivanta" || org.ZitadelOrgID != "helivanta-"+tenantID) {
		return empty, ErrNotFound
	}
	if strings.HasPrefix(org.ZitadelOrgID, "helivanta-") && org.ZitadelOrgID != "helivanta-"+tenantID {
		return empty, ErrNotFound
	}
	product, err := s.store.Product(ctx, "helivanta")
	if err != nil {
		return empty, ErrUnavailable
	}
	if product.State != "active" || product.Identity != store.IdentityZitadel {
		return empty, ErrNotFound
	}
	enrolled, err := s.store.Entitled(ctx, tenantID, "helivanta")
	if err != nil {
		return empty, ErrUnavailable
	}
	if !enrolled {
		return empty, ErrNotFound
	}
	started := time.Now().UTC()
	proof, err := s.idp.VerifyOrganization(ctx, org.ZitadelOrgID, tenantID, product.ZitadelProjectID)
	if err != nil || !currentIdentityProof(proof, org, product, started, time.Now().UTC()) {
		return empty, ErrUnavailable
	}
	if proof.State != "active" {
		return empty, ErrNotFound
	}
	return customerIdentityBinding{organization: org, product: product}, nil
}
