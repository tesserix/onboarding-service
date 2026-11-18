package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// ReadStaffMFARecovery is an exact current-scope read and never prepares a command.
func (s *Store) ReadStaffMFARecovery(ctx context.Context, scope StaffIdentityScope, event audit.Event) (StaffMFARecoveryCommand, error) {
	if scope.OrgID == uuid.Nil || scope.CommandID.Version() != 7 || scope.CommandID.Variant() != uuid.RFC4122 || scope.ProductKey != "helivanta" {
		return StaffMFARecoveryCommand{}, ErrNotFound
	}
	var out StaffMFARecoveryCommand
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockStaffIdentityScope(ctx, tx, StaffIdentityCommand{OrgID: scope.OrgID, ProductKey: scope.ProductKey, MachineSubject: scope.MachineSubject, ProviderOrgID: scope.ProviderOrgID, ProviderProjectID: scope.ProviderProjectID}); err != nil {
			return err
		}
		var err error
		out, err = scanStaffMFA(tx.QueryRow(ctx, `SELECT `+staffMFAColumns+` FROM staff_mfa_recovery_commands
 WHERE id=$1 AND org_id=$2 AND machine_subject=$3 AND actor_subject=$4 AND provider_org_id=$5 AND provider_project_id=$6`,
			scope.CommandID, scope.OrgID, scope.MachineSubject, scope.ActorSubject, scope.ProviderOrgID, scope.ProviderProjectID))
		if err != nil {
			return err
		}
		if err = staffMFAShape(out, event, "read"); err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffMFARecoveryCommand{}, err
	}
	return out, nil
}
