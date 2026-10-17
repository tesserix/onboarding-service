package onboarding

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type EnrolOrgInput struct {
	TenantID     uuid.UUID `json:"tenantId" binding:"required"`
	ZitadelOrgID string    `json:"zitadelOrgId" binding:"required,max=200"`
	ProductKey   string    `json:"productKey" binding:"required,max=64"`
	Name         string    `json:"name" binding:"required,max=200"`
	Reason       string    `json:"reason" binding:"required,max=500"`
}

var enrolSourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// EnrolOrganization adopts an already-active source binding without changing it.
func (s *Service) EnrolOrganization(ctx context.Context, p authz.Principal, in EnrolOrgInput) (store.Organization, error) {
	empty := store.Organization{}
	if err := s.guard(ctx, p, authz.ActionOrgEnrol, "", nil); err != nil {
		return empty, err
	}
	if p.Subject == "" || in.TenantID == uuid.Nil || !enrolSourceID.MatchString(in.ZitadelOrgID) || !enrolSourceID.MatchString(in.ProductKey) || len(in.ProductKey) > 64 || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 {
		return empty, ErrInvalid
	}
	if in.ProductKey == operatorManagedHospitalProduct {
		return empty, errHospitalWorkflow
	}
	product, err := s.store.Product(ctx, in.ProductKey)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if product.State != "active" || product.Identity != store.IdentityZitadel {
		return empty, ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	org := store.Organization{ID: in.TenantID, ZitadelOrgID: in.ZitadelOrgID, Name: strings.TrimSpace(in.Name), CreatedByProduct: product.Key}
	started := time.Now().UTC()
	proof, err := s.idp.VerifyOrganization(ctx, org.ZitadelOrgID, org.ID.String(), product.ZitadelProjectID)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil || !currentIdentityProof(proof, org, product, started, time.Now().UTC()) {
		return empty, ErrUnavailable
	}
	if proof.State != "active" {
		return empty, ErrConflict
	}
	event := audit.Event{
		ActorType: actorType(p), ActorID: p.Subject, ProductKey: product.Key,
		OrgID: org.ID.String(), Action: string(authz.ActionOrgEnrol),
		Outcome: audit.OutcomeSuccess, RequestID: RequestIDFrom(ctx),
		Detail: encodeDetail(map[string]any{"reason": strings.TrimSpace(in.Reason), "identity": proof}),
	}
	result, err := s.store.EnrolOrganization(ctx, org, product, event)
	if errors.Is(err, store.ErrConflict) {
		return empty, ErrConflict
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	return result, nil
}
