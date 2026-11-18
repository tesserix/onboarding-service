package onboarding

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
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
	var result customercontract.IdentityProjection
	if in.IncludeCredentialState {
		result, err = s.signupIdentity.IdentityCredentialProjection(ctx, org, in.Subject)
	} else {
		result, err = s.signupIdentity.IdentityProjection(ctx, org, in.Subject, in.Purpose)
	}
	if err != nil || result.Validate() != nil || result.TenantID != in.TenantID || result.Subject != in.Subject || result.OrganizationID != org || result.Purpose != in.Purpose {
		return empty, ErrUnavailable
	}
	if result.State == "active" && in.IncludeCredentialState != (result.Credentials != nil) {
		return empty, ErrUnavailable
	}
	if result.MFARecovery != nil {
		return empty, ErrUnavailable
	}
	if in.IncludeMFARecovery && result.State == "active" {
		requirement, err := s.store.StaffMFARecoveryRequirement(ctx, in.Subject)
		if err != nil {
			return empty, ErrUnavailable
		}
		result.MFARecovery = &requirement
		if result.Validate() != nil {
			return empty, ErrUnavailable
		}
	}
	if in.Purpose == "assignment_candidate" || in.IncludeCredentialState {
		currentOrg, err := s.customerIdentityScope(ctx, in.TenantID)
		if err != nil {
			return empty, err
		}
		if currentOrg != org {
			return empty, ErrUnavailable
		}
	}
	if result.TenantName != "" {
		return empty, ErrUnavailable
	}
	if in.IncludeTenantName && result.Profile != nil {
		tenant, _ := uuid.Parse(in.TenantID)
		customer, err := s.store.Organization(ctx, tenant)
		if err != nil || customer.State != "active" || customer.ZitadelOrgID != org || customer.CreatedByProduct != "helivanta" || customer.Name == "" {
			return empty, ErrUnavailable
		}
		result.TenantName = customer.Name
		if result.Validate() != nil {
			return empty, ErrUnavailable
		}
	}
	detail, err := json.Marshal(struct {
		Purpose         string `json:"purpose"`
		CredentialState bool   `json:"credentialState,omitempty"`
		MFARecovery     bool   `json:"mfaRecovery,omitempty"`
	}{Purpose: in.Purpose, CredentialState: in.IncludeCredentialState, MFARecovery: in.IncludeMFARecovery})
	if err != nil {
		return empty, ErrUnavailable
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: in.TenantID, Action: "customer.identity.read", Outcome: audit.OutcomeSuccess, RequestID: in.RequestID, Target: in.Subject, Detail: string(detail)}); err != nil {
		return empty, ErrUnavailable
	}
	return result, nil
}
