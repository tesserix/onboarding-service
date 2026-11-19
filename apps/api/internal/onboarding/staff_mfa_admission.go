package onboarding

import (
	"context"
	"errors"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

var ErrMFAAuthenticationRequired = errors.New("fresh authenticator recovery authentication required")

func (s *Service) staffMFAAdmissionRecord(ctx context.Context, in customercontract.StaffMFAAdmissionScope) (store.StaffMFARecoveryCommand, error) {
	var empty store.StaffMFARecoveryCommand
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.idp == nil || s.signupIdentity == nil {
		return empty, ErrUnavailable
	}
	if _, err := s.customerIdentityScope(ctx, in.TenantID); err != nil {
		return empty, err
	}
	command, err := s.store.StaffMFAAdmissionCommand(ctx, in.Subject, in.GenerationID)
	if err != nil {
		return empty, staffMFAStoreError(err)
	}
	if command.Request.Validate() != nil || command.Request.Target.Subject != in.Subject {
		return empty, ErrUnavailable
	}
	org, err := s.customerIdentityScope(ctx, command.Request.Target.TenantID)
	if err != nil {
		return empty, err
	}
	if org != command.ProviderOrgID {
		return empty, ErrUnavailable
	}
	return command, nil
}

func (s *Service) checkMFAAdmissionAccount(ctx context.Context, c store.StaffMFARecoveryCommand) error {
	account, err := s.signupIdentity.IdentityProjection(ctx, c.ProviderOrgID, c.Request.Target.Subject, "membership")
	if err != nil || account.Validate() != nil || account.OrganizationID != c.ProviderOrgID || account.Subject != c.Request.Target.Subject {
		return ErrUnavailable
	}
	if account.State != "active" || account.Admission != "eligible" {
		return ErrNotFound
	}
	return nil
}

func mfaAdmissionAudit(in customercontract.StaffMFAAdmissionScope, c store.StaffMFARecoveryCommand, action string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, ActorID: audit.HelivantaWorkload, ProductKey: "helivanta", OrgID: c.Request.Target.TenantID,
		Action: "staff.mfa.recovery." + action, Target: in.GenerationID, Outcome: audit.OutcomeSuccess, RequestID: in.RequestID,
		Detail: encodeDetail(struct {
			Subject     string `json:"subject"`
			LoginTenant string `json:"login_tenant"`
		}{in.Subject, in.TenantID})}
}

// AdmitStaffMFASession accepts only the private HMS assertion of current membership
// and a sid from an already verified native ID token. It grants no Staff authority.
func (s *Service) AdmitStaffMFASession(ctx context.Context, in customercontract.StaffMFAAdmissionInput) (_ customercontract.StaffMFAAdmissionReceipt, resultErr error) {
	stage := "record"
	defer func() {
		if s != nil && s.log != nil && errors.Is(resultErr, ErrUnavailable) {
			s.log.Warn("Staff MFA recovery admission unavailable", "stage", stage)
		}
	}()
	var empty customercontract.StaffMFAAdmissionReceipt
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command, err := s.staffMFAAdmissionRecord(ctx, in.Scope)
	if err != nil {
		return empty, err
	}
	stage = "account"
	if err = s.checkMFAAdmissionAccount(ctx, command); err != nil {
		return empty, err
	}
	stage = "removal"
	removed, err := time.Parse(time.RFC3339Nano, command.NativeChangedAt)
	if err != nil {
		return empty, ErrUnavailable
	}
	removal := zitadel.TOTPRemovalEvidence{Sequence: command.NativeSequence, ChangedAt: removed}
	stage = "enrollment"
	enrolled, err := s.signupIdentity.RecoveryTOTPEnrollment(ctx, command.ProviderOrgID, in.Scope.Subject, removal)
	if err != nil {
		return empty, ErrUnavailable
	}
	if !enrolled.Verified {
		return empty, ErrMFAAuthenticationRequired
	}
	stage = "session"
	proof, err := s.signupIdentity.VerifyRecoverySession(ctx, command.ProviderOrgID, in.Scope.Subject, in.NativeSessionID, removed, enrolled.VerifiedAt)
	if errors.Is(err, zitadel.ErrRecoverySessionRejected) {
		return empty, ErrMFAAuthenticationRequired
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	stage = "enrollment_recheck"
	latest, err := s.signupIdentity.RecoveryTOTPEnrollment(ctx, command.ProviderOrgID, in.Scope.Subject, removal)
	if err != nil {
		return empty, ErrUnavailable
	}
	if !latest.Verified {
		return empty, ErrMFAAuthenticationRequired
	}
	if latest.Sequence != enrolled.Sequence || !latest.VerifiedAt.Equal(enrolled.VerifiedAt) {
		return empty, ErrConflict
	}
	stage = "account_recheck"
	if err = s.checkMFAAdmissionAccount(ctx, command); err != nil {
		return empty, err
	}
	stage = "credentials"
	credentials, err := s.signupIdentity.IdentityCredentialProjection(ctx, command.ProviderOrgID, in.Scope.Subject)
	if err != nil || credentials.Validate() != nil || credentials.Subject != in.Scope.Subject || credentials.OrganizationID != command.ProviderOrgID || credentials.State != "active" || credentials.Credentials == nil || !credentials.Credentials.PasswordConfigured {
		return empty, ErrUnavailable
	}
	changed, err := time.Parse(time.RFC3339Nano, credentials.Credentials.PasswordChangedAt)
	if err != nil {
		return empty, ErrUnavailable
	}
	if !proof.PasswordVerifiedAt.After(changed) {
		return empty, ErrMFAAuthenticationRequired
	}
	stage = "record_recheck"
	current, err := s.staffMFAAdmissionRecord(ctx, in.Scope)
	if err != nil {
		return empty, err
	}
	if current.Request != command.Request || current.ProviderOrgID != command.ProviderOrgID || current.ProviderProjectID != command.ProviderProjectID || current.MachineSubject != command.MachineSubject {
		return empty, ErrConflict
	}
	stage = "store"
	if err = s.store.AdmitStaffMFARecovery(ctx, command, enrolled.Sequence, enrolled.VerifiedAt.UTC().Format(time.RFC3339Nano), mfaAdmissionAudit(in.Scope, command, "admit")); err != nil {
		return empty, staffMFAStoreError(err)
	}
	stage = "commit_recheck"
	if _, err = s.staffMFAAdmissionRecord(ctx, in.Scope); err != nil {
		return empty, err
	}
	return customercontract.StaffMFAAdmissionReceipt{GenerationID: in.Scope.GenerationID}, nil
}
