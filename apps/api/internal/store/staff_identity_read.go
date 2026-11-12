package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// StaffIdentityScope binds a private read to current registry and initiating-actor facts.
type StaffIdentityScope struct {
	CommandID         uuid.UUID
	OrgID             uuid.UUID
	ProductKey        string
	MachineSubject    string
	ActorSubject      string
	ProviderOrgID     string
	ProviderProjectID string
}

// StaffIdentityOperation distinguishes retained dispatch from confirmed creation.
type StaffIdentityOperation struct {
	Command            StaffIdentityCommand
	DispatchedAt       *time.Time
	Creation           *StaffIdentityCreation
	RecoveryState      string
	SuccessorCommandID *uuid.UUID
}

func (StaffIdentityOperation) String() string { return "[redacted staff identity operation]" }

// ReadStaffIdentity reauthorizes exact private state and commits its disclosure audit.
func (s *Store) ReadStaffIdentity(ctx context.Context, scope StaffIdentityScope, event audit.Event) (StaffIdentityOperation, error) {
	var result StaffIdentityOperation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		result.Command, err = readScopedStaffIdentityCommand(ctx, tx, scope)
		if err != nil {
			return err
		}
		result, err = readStaffIdentityOperation(ctx, tx, result.Command)
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

func readStaffIdentityOperation(ctx context.Context, tx pgx.Tx, command StaffIdentityCommand) (StaffIdentityOperation, error) {
	result := StaffIdentityOperation{Command: command}
	var err error
	var sourceTime, providerID *string
	var recordedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT d.created_at,r.provider_user_id,r.source_creation_time,r.created_at
 FROM staff_identity_dispatches d LEFT JOIN staff_identity_creations r ON r.command_id=d.command_id
 WHERE d.command_id=$1`, command.ID).
		Scan(&result.DispatchedAt, &providerID, &sourceTime, &recordedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StaffIdentityOperation{}, err
	}
	if err == nil {
		if result.DispatchedAt == nil || (recordedAt == nil && (sourceTime != nil || providerID != nil)) {
			return StaffIdentityOperation{}, ErrConflict
		}
		if recordedAt != nil {
			if sourceTime == nil || providerID == nil || *providerID != result.Command.ProviderUserID {
				return StaffIdentityOperation{}, ErrConflict
			}
			created, err := time.Parse(time.RFC3339Nano, *sourceTime)
			if err != nil {
				return StaffIdentityOperation{}, ErrConflict
			}
			result.Creation = &StaffIdentityCreation{CommandID: command.ID,
				ProviderUserID: *providerID, ProviderCreatedAt: created, RecordedAt: *recordedAt}
		}
	}
	var replaced, closed bool
	err = tx.QueryRow(ctx, `SELECT
          EXISTS(SELECT 1 FROM staff_identity_recovery_decisions WHERE command_id=$1 AND decision='approved' AND requested_action='replace'),
          (SELECT successor_command_id FROM staff_identity_replacements WHERE parent_command_id=$1),
          EXISTS(SELECT 1 FROM staff_identity_recovery_decisions WHERE command_id=$1 AND decision='approved' AND requested_action='close_unresolved')`, command.ID).
		Scan(&replaced, &result.SuccessorCommandID, &closed)
	if err != nil {
		return StaffIdentityOperation{}, err
	}
	if replaced {
		if result.SuccessorCommandID == nil {
			return StaffIdentityOperation{}, ErrConflict
		}
		result.RecoveryState = "superseded"
	} else if result.SuccessorCommandID != nil {
		return StaffIdentityOperation{}, ErrConflict
	} else if closed {
		result.RecoveryState = "closed_unresolved"
	}
	return result, nil
}

func readScopedStaffIdentityCommand(ctx context.Context, tx pgx.Tx, scope StaffIdentityScope) (StaffIdentityCommand, error) {
	if err := lockStaffIdentityScope(ctx, tx, StaffIdentityCommand{OrgID: scope.OrgID, ProductKey: scope.ProductKey, MachineSubject: scope.MachineSubject, ProviderOrgID: scope.ProviderOrgID, ProviderProjectID: scope.ProviderProjectID}); err != nil {
		return StaffIdentityCommand{}, err
	}
	return scanStaffIdentityCommand(tx.QueryRow(ctx, `SELECT `+staffIdentityColumns+`
 FROM staff_identity_commands WHERE id=$1 AND org_id=$2 AND product_key=$3
 AND machine_subject=$4 AND actor_subject=$5 AND provider_org_id=$6 AND provider_project_id=$7`,
		scope.CommandID, scope.OrgID, scope.ProductKey, scope.MachineSubject, scope.ActorSubject, scope.ProviderOrgID, scope.ProviderProjectID))
}
