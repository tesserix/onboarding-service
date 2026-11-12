package onboarding

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// StaffRecoveryOperatorSource verifies current human console authority.
type StaffRecoveryOperatorSource interface {
	VerifyStaffRecoveryOperator(context.Context, string, string, bool) (zitadel.StaffRecoveryOperatorProof, error)
}

// WithStaffRecovery pins the console project and current authority source.
func WithStaffRecovery(project string, source StaffRecoveryOperatorSource) Option {
	return func(s *Service) {
		s.staffRecoveryProject = project
		s.staffRecoverySource = source
	}
}

func (s *Service) staffRecoveryScope(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, action authz.Action) (store.StaffIdentityRecoveryScope, error) {
	empty := store.StaffIdentityRecoveryScope{}
	if err := s.guard(ctx, p, action, tenant.String(), nil); err != nil {
		return empty, err
	}
	if tenant == uuid.Nil || command.Version() != 7 || command.Variant() != uuid.RFC4122 || !enrolSourceID.MatchString(p.Subject) || !enrolSourceID.MatchString(p.ExternalOrgID) {
		return empty, ErrInvalid
	}
	if s.staffProduct == "" || s.staffRecoverySource == nil || !enrolSourceID.MatchString(s.staffRecoveryProject) {
		return empty, ErrUnavailable
	}
	write := action != authz.ActionStaffRecoveryRead
	started := time.Now().UTC()
	operator, err := s.staffRecoverySource.VerifyStaffRecoveryOperator(ctx, p.Subject, s.staffRecoveryProject, write)
	if errors.Is(err, zitadel.ErrStaffOperatorDenied) || errors.Is(err, zitadel.ErrIdentityMissing) {
		s.auditTarget(ctx, p, string(action), tenant.String(), command.String(), audit.OutcomeDenied, nil)
		return empty, ErrNotFound
	}
	if err != nil || operator.Subject != p.Subject || operator.OrganizationID != p.ExternalOrgID || operator.ProjectID != s.staffRecoveryProject || !enrolSourceID.MatchString(operator.GrantID) || operator.Write != write || operator.VerifiedAt.Before(started) || operator.VerifiedAt.After(time.Now().UTC()) {
		return empty, ErrUnavailable
	}
	org, err := s.store.Organization(ctx, tenant)
	if errors.Is(err, store.ErrNotFound) || (err == nil && org.State != "active") {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	product, err := s.store.Product(ctx, s.staffProduct)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (product.State != "active" || product.Identity != store.IdentityZitadel)) {
		return empty, ErrNotFound
	}
	if err != nil || !enrolSourceID.MatchString(product.MachineUserID) || !enrolSourceID.MatchString(product.ZitadelProjectID) || !enrolSourceID.MatchString(org.ZitadelOrgID) {
		return empty, ErrUnavailable
	}
	entitled, err := s.store.Entitled(ctx, tenant.String(), product.Key)
	if err != nil {
		return empty, ErrUnavailable
	}
	if !entitled {
		return empty, ErrNotFound
	}
	started = time.Now().UTC()
	proof, err := s.idp.VerifyOrganization(ctx, org.ZitadelOrgID, tenant.String(), product.ZitadelProjectID)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil || !currentIdentityProof(proof, org, product, started, time.Now().UTC()) {
		return empty, ErrUnavailable
	}
	if proof.State != "active" {
		return empty, ErrNotFound
	}
	return store.StaffIdentityRecoveryScope{CommandID: command, OrgID: tenant, ProductKey: product.Key, MachineSubject: product.MachineUserID, ProviderOrgID: proof.OrganizationID, ProviderProjectID: proof.ProjectID}, nil
}

func recoveryReadEvent(ctx context.Context, p authz.Principal, scope store.StaffIdentityRecoveryScope, action string) audit.Event {
	return audit.Event{ActorType: audit.ActorPlatform, ActorID: p.Subject, ProductKey: scope.ProductKey,
		OrgID: scope.OrgID.String(), Target: scope.CommandID.String(), Action: action,
		Outcome: audit.OutcomeSuccess, RequestID: RequestIDFrom(ctx)}
}

func recoveryStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrIdempotencyConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}

// StaffIdentityRecoveryReceipt rechecks live operator and source authority around inspection.
func (s *Service) StaffIdentityRecoveryReceipt(ctx context.Context, p authz.Principal, tenant, command uuid.UUID) (StaffIdentityReceipt, error) {
	empty := StaffIdentityReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	scope, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryRead)
	if err != nil {
		return empty, err
	}
	if _, err := s.store.ReadStaffIdentityRecovery(ctx, scope, p.Subject, recoveryReadEvent(ctx, p, scope, "staff.identity.recovery.inspect")); err != nil {
		return empty, recoveryStoreError(err)
	}
	current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryRead)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrConflict
	}
	op, err := s.store.ReadStaffIdentityRecovery(ctx, current, p.Subject, recoveryReadEvent(ctx, p, current, "staff.identity.recovery.read"))
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	return staffIdentityReceipt(op), nil
}
