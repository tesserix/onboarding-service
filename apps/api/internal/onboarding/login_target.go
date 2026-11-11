package onboarding

import (
	"context"
	"errors"
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
	tenant, _ := uuid.Parse(in.TenantID)
	org, err := s.store.Organization(ctx, tenant)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if org.State != "active" || org.CreatedByProduct != "helivanta" || org.ZitadelOrgID != "helivanta-"+in.TenantID {
		return empty, ErrNotFound
	}
	product, err := s.store.Product(ctx, "helivanta")
	if err != nil {
		return empty, ErrUnavailable
	}
	if product.State != "active" {
		return empty, ErrNotFound
	}
	grants, err := s.store.Grants(ctx, tenant)
	if err != nil {
		return empty, ErrUnavailable
	}
	enrolled := false
	for _, grant := range grants {
		if grant.ProductKey == "helivanta" && grant.RevokedAt == nil {
			enrolled = true
		}
	}
	if !enrolled {
		return empty, ErrNotFound
	}
	proof, err := s.idp.VerifyOrganization(ctx, org.ZitadelOrgID, in.TenantID, product.ZitadelProjectID)
	if err != nil {
		return empty, ErrUnavailable
	}
	if proof.State != "active" || proof.OrganizationID != org.ZitadelOrgID || proof.TenantID != in.TenantID || proof.ProjectID != product.ZitadelProjectID {
		return empty, ErrNotFound
	}
	subject, err := s.signupIdentity.TenantLoginSubject(ctx, org.ZitadelOrgID, in.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: in.TenantID, Action: "customer.login.target_read", Outcome: audit.OutcomeSuccess, RequestID: in.RequestID}); err != nil {
		return empty, ErrUnavailable
	}
	return customercontract.LoginTarget{TenantID: in.TenantID, OrganizationID: org.ZitadelOrgID, Subject: subject}, nil
}
