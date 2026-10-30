package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// ReadStaffIdentityRecovery retains original private facts under current operator scope.
func (s *Store) ReadStaffIdentityRecovery(ctx context.Context, scope StaffIdentityRecoveryScope, operator string, event audit.Event) (StaffIdentityOperation, error) {
	if operator == "" || event.ActorType != audit.ActorPlatform || event.ActorID != operator || event.ProductKey != scope.ProductKey || event.OrgID != scope.OrgID.String() || event.Target != scope.CommandID.String() || (event.Action != "staff.identity.recovery.read" && event.Action != "staff.identity.recovery.inspect") || event.Outcome != audit.OutcomeSuccess {
		return StaffIdentityOperation{}, ErrConflict
	}
	var result StaffIdentityOperation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		command, err := readStaffIdentityRecoveryTarget(ctx, tx, scope)
		if err != nil {
			return err
		}
		result, err = readStaffIdentityOperation(ctx, tx, command)
		if err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffIdentityOperation{}, err
	}
	return result, nil
}
