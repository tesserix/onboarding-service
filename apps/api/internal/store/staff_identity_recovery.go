package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// StaffIdentityRecoveryScope binds an operator command to the current product registry.
type StaffIdentityRecoveryScope struct {
	CommandID         uuid.UUID
	OrgID             uuid.UUID
	ProductKey        string
	MachineSubject    string
	ProviderOrgID     string
	ProviderProjectID string
}

// StaffIdentityRecoveryRequest retains an immutable request without approving recovery.
type StaffIdentityRecoveryRequest struct {
	ID                      uuid.UUID
	CommandID               uuid.UUID
	OriginalActorSubject    string
	OriginalUserID          string
	RequestedBy             string
	Action                  string
	ReplacementActorSubject string
	Reason                  string
	EvidenceReference       string
	CreatedAt               time.Time
}

func (StaffIdentityRecoveryRequest) String() string {
	return "[redacted staff identity recovery request]"
}

// ProposeStaffIdentityRecovery retains exact request facts and their mandatory audit.
func (s *Store) ProposeStaffIdentityRecovery(ctx context.Context, scope StaffIdentityRecoveryScope, in StaffIdentityRecoveryRequest, event audit.Event) (StaffIdentityRecoveryRequest, error) {
	var result StaffIdentityRecoveryRequest
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		command, err := readStaffIdentityRecoveryTarget(ctx, tx, scope)
		if err != nil {
			return err
		}
		if event.ActorType != audit.ActorPlatform || event.ActorID != in.RequestedBy ||
			event.ProductKey != scope.ProductKey || event.OrgID != scope.OrgID.String() ||
			event.Target != scope.CommandID.String() || event.Action != "staff.identity.recovery.propose" || event.Outcome != audit.OutcomeSuccess {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_identity_recovery_requests
 (id,command_id,original_actor_subject,original_user_id,requested_by,requested_action,replacement_actor_subject,reason,evidence_reference)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO NOTHING`, in.ID, command.ID, command.ActorSubject, command.ProviderUserID, in.RequestedBy, in.Action, in.ReplacementActorSubject, in.Reason, in.EvidenceReference); err != nil {
			return err
		}
		result, err = scanStaffIdentityRecoveryRequest(tx.QueryRow(ctx, `SELECT `+staffRecoveryRequestColumns+` FROM staff_identity_recovery_requests WHERE id=$1 AND command_id=$2`, in.ID, command.ID))
		if err != nil {
			return err
		}
		if result.RequestedBy != in.RequestedBy || result.Action != in.Action || result.ReplacementActorSubject != in.ReplacementActorSubject || result.Reason != in.Reason || result.EvidenceReference != in.EvidenceReference {
			return ErrIdempotencyConflict
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffIdentityRecoveryRequest{}, err
	}
	result.CreatedAt = result.CreatedAt.UTC()
	return result, nil
}

func readStaffIdentityRecoveryTarget(ctx context.Context, tx pgx.Tx, scope StaffIdentityRecoveryScope) (StaffIdentityCommand, error) {
	if err := lockStaffIdentityScope(ctx, tx, StaffIdentityCommand{OrgID: scope.OrgID, ProductKey: scope.ProductKey, MachineSubject: scope.MachineSubject, ProviderOrgID: scope.ProviderOrgID, ProviderProjectID: scope.ProviderProjectID}); err != nil {
		return StaffIdentityCommand{}, err
	}
	return scanStaffIdentityCommand(tx.QueryRow(ctx, `SELECT `+staffIdentityColumns+` FROM staff_identity_commands
 WHERE id=$1 AND org_id=$2 AND product_key=$3 AND provider_org_id=$4 AND provider_project_id=$5`, scope.CommandID, scope.OrgID, scope.ProductKey, scope.ProviderOrgID, scope.ProviderProjectID))
}
