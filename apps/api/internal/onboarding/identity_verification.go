package onboarding

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type ProductIdentity struct {
	zitadel.OrganizationIdentity
	ProductKey string `json:"product_key"`
}

func (s *Service) ProductIdentity(ctx context.Context, p authz.Principal, id uuid.UUID) (ProductIdentity, error) {
	empty := ProductIdentity{}
	if p.Kind != authz.KindProduct || p.ProductKey == "" || p.Subject == "" || id == uuid.Nil {
		s.audit(ctx, p, "org.identity.read", id.String(), audit.OutcomeDenied, nil)
		return empty, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.guard(ctx, p, authz.ActionOrgRead, id.String(), nil); err != nil {
		return empty, err
	}
	org, err := s.store.Organization(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	product, err := s.store.ProductByMachineUser(ctx, p.Subject)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if product.Key != p.ProductKey || product.State != "active" || product.Identity != store.IdentityZitadel {
		return empty, ErrNotFound
	}
	started := time.Now().UTC()
	proof, err := s.idp.VerifyOrganization(ctx, org.ZitadelOrgID, id.String(), product.ZitadelProjectID)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil || !currentIdentityProof(proof, org, product, started, time.Now().UTC()) {
		return empty, ErrUnavailable
	}
	currentProduct, err := s.store.ProductByMachineUser(ctx, p.Subject)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if currentProduct.Key != product.Key || currentProduct.State != "active" || currentProduct.Identity != product.Identity || currentProduct.ZitadelProjectID != product.ZitadelProjectID {
		return empty, ErrUnavailable
	}
	if err := s.guard(ctx, p, authz.ActionOrgRead, id.String(), nil); err != nil {
		return empty, err
	}
	return ProductIdentity{OrganizationIdentity: proof, ProductKey: product.Key}, nil
}

func currentIdentityProof(proof zitadel.OrganizationIdentity, org store.Organization, product store.Product, started, finished time.Time) bool {
	if proof.SchemaVersion != 1 || proof.TenantID != org.ID.String() || proof.OrganizationID != org.ZitadelOrgID || proof.ProjectID != product.ZitadelProjectID || proof.GrantID == "" || (proof.State != "active" && proof.State != "inactive") || proof.VerifiedAt.Before(started) || proof.VerifiedAt.After(finished) {
		return false
	}
	for _, seq := range []string{proof.SourceRevisions.Organization, proof.SourceRevisions.Metadata, proof.SourceRevisions.Project, proof.SourceRevisions.Grant} {
		if !positiveRevision(seq) {
			return false
		}
	}
	return true
}

func positiveRevision(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
}
