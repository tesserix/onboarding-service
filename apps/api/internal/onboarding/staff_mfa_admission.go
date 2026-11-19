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

func (s *Service) staffMFAAdmissionRecord(ctx context.Context, in customercontract.StaffMFAAdmissionRead) (store.StaffMFARecoveryCommand, error) {
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
	err := s.signupIdentity.CheckStaffMFARecoveryTarget(ctx, c.ProviderOrgID, c.Request.Target.Subject)
	if errors.Is(err, zitadel.ErrRecoveryTargetIneligible) {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func mfaAdmissionAudit(in customercontract.StaffMFAAdmissionRead, c store.StaffMFARecoveryCommand, action string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, ActorID: audit.HelivantaWorkload, ProductKey: "helivanta", OrgID: c.Request.Target.TenantID,
		Action: "staff.mfa.recovery." + action, Target: in.GenerationID, Outcome: audit.OutcomeSuccess, RequestID: in.RequestID,
		Detail: encodeDetail(struct {
			Subject     string `json:"subject"`
			LoginTenant string `json:"login_tenant"`
		}{in.Subject, in.TenantID})}
}

// StaffMFAAdmissionSource returns the frozen target for HMS current-source checks.
// It does not authenticate a native session or grant a recovered generation.
func (s *Service) StaffMFAAdmissionSource(ctx context.Context, in customercontract.StaffMFAAdmissionRead) (customercontract.StaffMFAAdmissionSource, error) {
	var empty customercontract.StaffMFAAdmissionSource
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command, err := s.staffMFAAdmissionRecord(ctx, in)
	if err != nil {
		return empty, err
	}
	if err = s.checkMFAAdmissionAccount(ctx, command); err != nil {
		return empty, err
	}
	current, err := s.staffMFAAdmissionRecord(ctx, in)
	if err != nil {
		return empty, err
	}
	if current.Request != command.Request || current.ProviderOrgID != command.ProviderOrgID || current.ProviderProjectID != command.ProviderProjectID || current.MachineSubject != command.MachineSubject {
		return empty, ErrConflict
	}
	if err = s.store.AppendAudit(ctx, mfaAdmissionAudit(in, command, "source")); err != nil {
		return empty, ErrUnavailable
	}
	out := customercontract.StaffMFAAdmissionSource{GenerationID: in.GenerationID, Target: command.Request.Target}
	if out.Validate() != nil {
		return empty, ErrUnavailable
	}
	return out, nil
}

// AdmitStaffMFASession accepts only the private HMS assertion of a rechecked
// reviewed target and a sid from an already verified native ID token.
func (s *Service) AdmitStaffMFASession(ctx context.Context, in customercontract.StaffMFAAdmissionInput) (customercontract.StaffMFAAdmissionReceipt, error) {
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
	if command.Request.Target != in.Target {
		return empty, ErrConflict
	}
	if err = s.checkMFAAdmissionAccount(ctx, command); err != nil {
		return empty, err
	}
	removed, err := time.Parse(time.RFC3339Nano, command.NativeChangedAt)
	if err != nil {
		return empty, ErrUnavailable
	}
	removal := zitadel.TOTPRemovalEvidence{Sequence: command.NativeSequence, ChangedAt: removed}
	enrolled, err := s.signupIdentity.RecoveryTOTPEnrollment(ctx, command.ProviderOrgID, in.Scope.Subject, removal)
	if err != nil {
		return empty, ErrUnavailable
	}
	if !enrolled.Verified {
		return empty, ErrMFAAuthenticationRequired
	}
	proof, err := s.signupIdentity.VerifyRecoverySession(ctx, command.ProviderOrgID, in.Scope.Subject, in.NativeSessionID, removed, enrolled.VerifiedAt)
	if errors.Is(err, zitadel.ErrRecoverySessionRejected) {
		return empty, ErrMFAAuthenticationRequired
	}
	if err != nil {
		return empty, ErrUnavailable
	}
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
	current, err := s.staffMFAAdmissionRecord(ctx, in.Scope)
	if err != nil {
		return empty, err
	}
	if current.Request != command.Request || current.ProviderOrgID != command.ProviderOrgID || current.ProviderProjectID != command.ProviderProjectID || current.MachineSubject != command.MachineSubject {
		return empty, ErrConflict
	}
	if err = s.store.AdmitStaffMFARecovery(ctx, command, enrolled.Sequence, enrolled.VerifiedAt.UTC().Format(time.RFC3339Nano), mfaAdmissionAudit(in.Scope, command, "admit")); err != nil {
		return empty, staffMFAStoreError(err)
	}
	if _, err = s.staffMFAAdmissionRecord(ctx, in.Scope); err != nil {
		return empty, err
	}
	return customercontract.StaffMFAAdmissionReceipt{GenerationID: in.Scope.GenerationID}, nil
}
