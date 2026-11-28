package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"log/slog"
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
	started := time.Now()
	// Recorded before the deferred cancel, so the context state is the caller's or the budget's.
	unavailable := func(phase string) (customercontract.IdentityProjection, error) {
		s.recordIdentityProjectionFailure(ctx, phase, started)
		return empty, ErrUnavailable
	}
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
	if err != nil {
		return unavailable("native_identity")
	}
	if result.Validate() != nil || result.TenantID != in.TenantID || result.Subject != in.Subject || result.OrganizationID != org || result.Purpose != in.Purpose {
		return unavailable("native_projection")
	}
	if result.State == "active" && in.IncludeCredentialState != (result.Credentials != nil) {
		return unavailable("native_credentials")
	}
	if result.MFARecovery != nil {
		return unavailable("native_projection")
	}
	if in.IncludeMFARecovery && result.State == "active" {
		requirement, err := s.store.StaffMFARecoveryRequirement(ctx, in.Subject)
		if err != nil {
			return unavailable("mfa_recovery")
		}
		result.MFARecovery = &requirement
		if result.Validate() != nil {
			return unavailable("mfa_recovery")
		}
	}
	if in.Purpose == "assignment_candidate" || in.IncludeCredentialState {
		currentOrg, err := s.customerIdentityScope(ctx, in.TenantID)
		if err != nil {
			return empty, err
		}
		if currentOrg != org {
			return unavailable("scope_recheck")
		}
	}
	if result.TenantName != "" {
		return unavailable("native_projection")
	}
	if in.IncludeTenantName && result.Profile != nil {
		tenant, _ := uuid.Parse(in.TenantID)
		customer, err := s.store.Organization(ctx, tenant)
		if err != nil || customer.State != "active" || customer.ZitadelOrgID != org || customer.CreatedByProduct != "helivanta" || customer.Name == "" {
			return unavailable("tenant_name")
		}
		result.TenantName = customer.Name
		if result.Validate() != nil {
			return unavailable("tenant_name")
		}
	}
	detail, err := json.Marshal(struct {
		Purpose         string `json:"purpose"`
		CredentialState bool   `json:"credentialState,omitempty"`
		MFARecovery     bool   `json:"mfaRecovery,omitempty"`
	}{Purpose: in.Purpose, CredentialState: in.IncludeCredentialState, MFARecovery: in.IncludeMFARecovery})
	if err != nil {
		return unavailable("audit")
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: in.TenantID, Action: "customer.identity.read", Outcome: audit.OutcomeSuccess, RequestID: in.RequestID, Target: in.Subject, Detail: string(detail)}); err != nil {
		return unavailable("audit")
	}
	return result, nil
}

// recordIdentityProjectionFailure names the failed step with fixed fields only:
// no subject, tenant, upstream body or error text reaches the log.
func (s *Service) recordIdentityProjectionFailure(ctx context.Context, phase string, started time.Time) {
	logger := s.log
	if logger == nil {
		logger = slog.Default()
	}
	logger.ErrorContext(ctx, "identity projection unavailable", "phase", phase, "elapsed_ms", time.Since(started).Milliseconds(), "parent_deadline", errors.Is(ctx.Err(), context.DeadlineExceeded), "parent_cancelled", errors.Is(ctx.Err(), context.Canceled))
}
