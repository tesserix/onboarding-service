package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// ReconcileStaffIdentityCreation retains a matching creation under current operator scope.
func (s *Store) ReconcileStaffIdentityCreation(ctx context.Context, scope StaffIdentityRecoveryScope, operator string, created time.Time, event audit.Event) (StaffIdentityCreation, error) {
	empty := StaffIdentityCreation{}
	if created.IsZero() || operator == "" || event.ActorType != audit.ActorPlatform || event.ActorID != operator || event.ProductKey != scope.ProductKey || event.OrgID != scope.OrgID.String() || event.Target != scope.CommandID.String() || event.Action != "staff.identity.recovery.reconcile" || event.Outcome != audit.OutcomeSuccess {
		return empty, ErrConflict
	}
	var result StaffIdentityCreation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		command, err := readStaffIdentityRecoveryTarget(ctx, tx, scope)
		if err != nil {
			return err
		}
		result, _, err = recordStaffIdentityCreation(ctx, tx, command, created)
		if err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return empty, err
	}
	return result, nil
}
