package onboarding

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func (s *Service) CustomerIdentityProjection(ctx context.Context, in customercontract.IdentityProjectionInput) (customercontract.IdentityProjection, error) {
	var empty customercontract.IdentityProjection
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.idp == nil || s.signupIdentity == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	org, err := s.customerIdentityScope(ctx, in.TenantID)
	if err != nil {
		return empty, err
	}
	result, err := s.signupIdentity.IdentityProjection(ctx, org, in.Subject, in.Purpose)
	if err != nil || result.Validate() != nil || result.TenantID != in.TenantID || result.Subject != in.Subject || result.OrganizationID != org || result.Purpose != in.Purpose {
		return empty, ErrUnavailable
	}
	detail, err := json.Marshal(struct {
		Purpose string `json:"purpose"`
	}{Purpose: in.Purpose})
	if err != nil {
		return empty, ErrUnavailable
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: in.TenantID, Action: "customer.identity.read", Outcome: audit.OutcomeSuccess, RequestID: in.RequestID, Target: in.Subject, Detail: string(detail)}); err != nil {
		return empty, ErrUnavailable
	}
	return result, nil
}
