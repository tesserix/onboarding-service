package onboarding

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// IAM supplies fresh administrator/ordinary-target authority and the committed
// application fence. Workforce supplies the reviewed link; neither comes from UI.
type StaffMFARecoverySource interface {
	CheckStaffMFARecoveryTarget(context.Context, string, string) error
	StaffTOTPConfigured(context.Context, string, string) (bool, error)
	RemoveRecoveryTOTP(context.Context, string, string) (zitadel.TOTPRemovalEvidence, error)
}

func WithStaffMFARecovery(product string, source StaffMFARecoverySource) Option {
	return func(s *Service) {
		if product == "helivanta" {
			s.staffMFAProduct = product
			s.staffMFASource = source
		}
	}
}

func (s *Service) staffMFAScope(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, actor string, action authz.Action) (store.StaffIdentityScope, error) {
	var empty store.StaffIdentityScope
	if p.Kind != authz.KindProduct || p.ProductKey != "helivanta" || p.Subject == "" {
		s.auditTarget(ctx, p, string(action), tenant.String(), command.String(), audit.OutcomeDenied, nil)
		return empty, ErrNotFound
	}
	if s.staffMFAProduct != "helivanta" || s.staffMFASource == nil {
		return empty, ErrUnavailable
	}
	if tenant == uuid.Nil || command.Version() != 7 || command.Variant() != uuid.RFC4122 || !enrolSourceID.MatchString(actor) {
		return empty, ErrInvalid
	}
	if err := s.guard(ctx, p, action, tenant.String(), nil); err != nil {
		return empty, err
	}
	proof, err := s.ProductIdentity(ctx, p, tenant)
	if err != nil {
		return empty, err
	}
	if proof.State != "active" {
		return empty, ErrNotFound
	}
	return store.StaffIdentityScope{CommandID: command, OrgID: tenant, ProductKey: p.ProductKey, MachineSubject: p.Subject, ActorSubject: actor,
		ProviderOrgID: proof.OrganizationID, ProviderProjectID: proof.ProjectID}, nil
}

func (s *Service) staffMFARequestScope(ctx context.Context, p authz.Principal, in customercontract.StaffMFARecoveryRequest, action authz.Action) (store.StaffIdentityScope, error) {
	if in.Validate() != nil {
		return store.StaffIdentityScope{}, ErrInvalid
	}
	tenant, err := uuid.Parse(in.Target.TenantID)
	if err != nil {
		return store.StaffIdentityScope{}, ErrInvalid
	}
	command, err := uuid.Parse(in.Intent.CommandID)
	if err != nil {
		return store.StaffIdentityScope{}, ErrInvalid
	}
	return s.staffMFAScope(ctx, p, tenant, command, in.ActorSubject, action)
}

func staffMFAEvent(ctx context.Context, scope store.StaffIdentityScope, action string) audit.Event {
	return audit.Event{ActorType: audit.ActorProduct, ActorID: scope.MachineSubject, ProductKey: scope.ProductKey, OrgID: scope.OrgID.String(),
		Target: scope.CommandID.String(), Action: "staff.mfa.recovery." + action, Outcome: audit.OutcomeSuccess, RequestID: RequestIDFrom(ctx),
		Detail: encodeDetail(struct {
			ActorSubject string `json:"actor_subject"`
		}{scope.ActorSubject})}
}

func staffMFACommand(scope store.StaffIdentityScope, in customercontract.StaffMFARecoveryRequest) store.StaffMFARecoveryCommand {
	return store.StaffMFARecoveryCommand{Request: in, MachineSubject: scope.MachineSubject, ProviderOrgID: scope.ProviderOrgID, ProviderProjectID: scope.ProviderProjectID}
}

func staffMFAReceipt(c store.StaffMFARecoveryCommand) customercontract.StaffMFARecoveryReceipt {
	return customercontract.StaffMFARecoveryReceipt{CommandID: c.Request.Intent.CommandID, State: c.State}
}

func staffMFAStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrIdempotencyConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}

func (s *Service) checkStaffMFATarget(ctx context.Context, p authz.Principal, scope store.StaffIdentityScope, subject string) error {
	err := s.staffMFASource.CheckStaffMFARecoveryTarget(ctx, scope.ProviderOrgID, subject)
	if errors.Is(err, zitadel.ErrRecoveryTargetIneligible) {
		s.auditTarget(ctx, p, "staff.mfa.recovery.target", scope.OrgID.String(), scope.CommandID.String(), audit.OutcomeDenied, nil)
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) PrepareStaffMFARecovery(ctx context.Context, p authz.Principal, in customercontract.StaffMFARecoveryRequest) (customercontract.StaffMFARecoveryReceipt, error) {
	var empty customercontract.StaffMFARecoveryReceipt
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scope, err := s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFAPrepare)
	if err != nil {
		return empty, err
	}
	if err = s.checkStaffMFATarget(ctx, p, scope, in.Target.Subject); err != nil {
		return empty, err
	}
	current, err := s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFAPrepare)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	command, _, err := s.store.PrepareStaffMFARecovery(ctx, staffMFACommand(scope, in), staffMFAEvent(ctx, scope, "prepare"))
	if err != nil {
		return empty, staffMFAStoreError(err)
	}
	current, err = s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFAPrepare)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	return staffMFAReceipt(command), nil
}

func (s *Service) readStaffMFA(ctx context.Context, scope store.StaffIdentityScope) (store.StaffMFARecoveryCommand, error) {
	command, err := s.store.ReadStaffMFARecovery(ctx, scope, staffMFAEvent(ctx, scope, "read"))
	if err != nil {
		return store.StaffMFARecoveryCommand{}, staffMFAStoreError(err)
	}
	return command, nil
}

func (s *Service) ReadStaffMFARecovery(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, actor string) (customercontract.StaffMFARecoverySnapshot, error) {
	var empty customercontract.StaffMFARecoverySnapshot
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	scope, err := s.staffMFAScope(ctx, p, tenant, command, actor, authz.ActionStaffMFARead)
	if err != nil {
		return empty, err
	}
	stored, err := s.readStaffMFA(ctx, scope)
	if err != nil {
		return empty, err
	}
	current, err := s.staffMFAScope(ctx, p, tenant, command, actor, authz.ActionStaffMFARead)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	out := customercontract.StaffMFARecoverySnapshot{Request: stored.Request, Receipt: staffMFAReceipt(stored)}
	if out.Validate() != nil {
		return empty, ErrUnavailable
	}
	return out, nil
}

func (s *Service) CancelStaffMFARecovery(ctx context.Context, p authz.Principal, in customercontract.StaffMFARecoveryRequest) (customercontract.StaffMFARecoveryReceipt, error) {
	var empty customercontract.StaffMFARecoveryReceipt
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	scope, err := s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFACancel)
	if err != nil {
		return empty, err
	}
	command, err := s.store.CancelStaffMFARecovery(ctx, staffMFACommand(scope, in), staffMFAEvent(ctx, scope, "cancel"))
	if err != nil {
		return empty, staffMFAStoreError(err)
	}
	current, err := s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFACancel)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	return staffMFAReceipt(command), nil
}
