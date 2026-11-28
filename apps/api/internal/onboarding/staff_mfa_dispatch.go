package onboarding

import (
	"context"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// DispatchStaffMFARecovery never invents preparation or repeats a native write.
func (s *Service) DispatchStaffMFARecovery(ctx context.Context, p authz.Principal, in customercontract.StaffMFARecoveryRequest, fenceID string) (customercontract.StaffMFARecoveryReceipt, error) {
	var empty customercontract.StaffMFARecoveryReceipt
	if in.Validate() != nil || fenceID != in.Intent.CommandID {
		return empty, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scope, err := s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFADispatch)
	if err != nil {
		return empty, err
	}
	command, err := s.readStaffMFA(ctx, scope)
	if err != nil {
		return empty, err
	}
	if command.Request != in {
		return empty, ErrConflict
	}
	if command.State == "cancelled" {
		return empty, ErrConflict
	}
	if command.State != "prepared" {
		return s.discloseStaffMFA(ctx, p, in, scope, command)
	}
	if err = s.checkStaffMFATarget(ctx, p, scope, in.Target.Subject); err != nil {
		return empty, err
	}
	configured, err := s.staffMFASource.StaffSecondFactorsConfigured(ctx, scope.ProviderOrgID, in.Target.Subject)
	if err != nil {
		return empty, ErrUnavailable
	}
	current, err := s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFADispatch)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	if !configured {
		latest, err := s.readStaffMFA(ctx, scope)
		if err != nil {
			return empty, err
		}
		if latest.State != "prepared" {
			return s.discloseStaffMFA(ctx, p, in, scope, latest)
		}
		return s.CancelStaffMFARecovery(ctx, p, in)
	}
	command, send, err := s.store.DispatchStaffMFARecovery(ctx, command, fenceID, staffMFAEvent(ctx, scope, "dispatch"))
	if err != nil {
		return empty, staffMFAStoreError(err)
	}
	if send {
		started := time.Now().UTC()
		proof, sourceErr := s.staffMFASource.RemoveRecoveryFactors(ctx, scope.ProviderOrgID, in.Target.Subject)
		finished := time.Now().UTC()
		state, sequence, changed, action := "unconfirmed", "", "", "unconfirmed"
		if sourceErr == nil && positiveRevision(proof.Sequence) && !proof.ChangedAt.IsZero() &&
			!proof.ChangedAt.Before(started.Add(-zitadel.StaffIdentityClockSkew)) && !proof.ChangedAt.After(finished) {
			state, sequence, changed, action = "awaiting_enrollment", proof.Sequence, proof.ChangedAt.UTC().Format(time.RFC3339Nano), "removed"
		}
		// Outcome retention cannot depend on the caller remaining connected/entitled.
		recordCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = s.store.RecordStaffMFARecovery(recordCtx, command, state, sequence, changed, staffMFAEvent(recordCtx, scope, action))
		done()
		if err != nil {
			return empty, staffMFAStoreError(err)
		}
	}
	command, err = s.readStaffMFA(ctx, scope)
	if err != nil {
		return empty, err
	}
	return s.discloseStaffMFA(ctx, p, in, scope, command)
}

func (s *Service) discloseStaffMFA(ctx context.Context, p authz.Principal, in customercontract.StaffMFARecoveryRequest, scope store.StaffIdentityScope, command store.StaffMFARecoveryCommand) (customercontract.StaffMFARecoveryReceipt, error) {
	current, err := s.staffMFARequestScope(ctx, p, in, authz.ActionStaffMFARead)
	if err != nil {
		return customercontract.StaffMFARecoveryReceipt{}, err
	}
	if current != scope {
		return customercontract.StaffMFARecoveryReceipt{}, ErrUnavailable
	}
	return staffMFAReceipt(command), nil
}
