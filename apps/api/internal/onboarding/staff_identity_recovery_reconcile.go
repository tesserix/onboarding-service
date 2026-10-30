package onboarding

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/authz"
)

// ReconcileStaffIdentityCreation retains only fresh matching source creation facts.
func (s *Service) ReconcileStaffIdentityCreation(ctx context.Context, p authz.Principal, tenant, command uuid.UUID) (StaffIdentityReceipt, error) {
	empty := StaffIdentityReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	scope, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryReconcile)
	if err != nil {
		return empty, err
	}
	op, err := s.store.ReadStaffIdentityRecovery(ctx, scope, p.Subject, recoveryReadEvent(ctx, p, scope, "staff.identity.recovery.inspect"))
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	if op.DispatchedAt == nil {
		return empty, ErrConflict
	}
	proof, err := s.inspectStaffRecoverySubject(ctx, op.Command)
	if err != nil {
		return empty, err
	}
	if proof.Disposition != "matching" {
		return empty, ErrConflict
	}
	current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryReconcile)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrConflict
	}
	detail, err := json.Marshal(map[string]any{"source_disposition": proof.Disposition, "observed_at": proof.VerifiedAt})
	if err != nil {
		return empty, ErrUnavailable
	}
	event := recoveryReadEvent(ctx, p, current, "staff.identity.recovery.reconcile")
	event.Detail = string(detail)
	if _, err := s.store.ReconcileStaffIdentityCreation(ctx, current, p.Subject, proof.CreatedAt, event); err != nil {
		return empty, recoveryStoreError(err)
	}
	final, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryReconcile)
	if err != nil {
		return empty, err
	}
	if final != scope {
		return empty, ErrConflict
	}
	result, err := s.store.ReadStaffIdentityRecovery(ctx, final, p.Subject, recoveryReadEvent(ctx, p, final, "staff.identity.recovery.read"))
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	return staffIdentityReceipt(result), nil
}
