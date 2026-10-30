package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// StaffIdentityRecoveryDecision is an immutable independent operator decision.
type StaffIdentityRecoveryDecision struct {
	RequestID  uuid.UUID
	ReviewedBy string
	Decision   string
	Reason     string
	CreatedAt  time.Time
}

func (StaffIdentityRecoveryDecision) String() string {
	return "[redacted staff identity recovery decision]"
}

// StaffIdentitySuccessor contains only the new command's server-owned identifiers.
type StaffIdentitySuccessor struct {
	ID             uuid.UUID
	ProviderUserID string
	BindingNonce   string
}

func (StaffIdentitySuccessor) String() string { return "[redacted staff identity successor]" }

// StaffIdentityRecoveryResult retains a decision and its atomically prepared successor.
type StaffIdentityRecoveryResult struct {
	Decision  StaffIdentityRecoveryDecision
	Successor *StaffIdentityCommand
}

func (StaffIdentityRecoveryResult) String() string {
	return "[redacted staff identity recovery result]"
}

// DecideStaffIdentityRecovery commits an independent decision with its mandatory audit.
func (s *Store) DecideStaffIdentityRecovery(ctx context.Context, scope StaffIdentityRecoveryScope, in StaffIdentityRecoveryDecision, successor *StaffIdentitySuccessor, event audit.Event) (StaffIdentityRecoveryResult, error) {
	var result StaffIdentityRecoveryResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		command, err := readStaffIdentityRecoveryTarget(ctx, tx, scope)
		if err != nil {
			return err
		}
		request, err := scanStaffIdentityRecoveryRequest(tx.QueryRow(ctx, `SELECT `+staffRecoveryRequestColumns+` FROM staff_identity_recovery_requests WHERE id=$1 AND command_id=$2`, in.RequestID, command.ID))
		if err != nil {
			return err
		}
		if event.ActorType != audit.ActorPlatform || event.ActorID != in.ReviewedBy || event.ProductKey != scope.ProductKey || event.OrgID != scope.OrgID.String() || event.Target != scope.CommandID.String() || event.Action != "staff.identity.recovery.decide" {
			return ErrConflict
		}
		if in.ReviewedBy == request.RequestedBy || in.ReviewedBy == request.OriginalActorSubject || in.ReviewedBy == request.OriginalUserID || in.ReviewedBy == request.ReplacementActorSubject {
			return ErrConflict
		}
		needsSuccessor := in.Decision == "approved" && request.Action == "replace"
		if needsSuccessor != (successor != nil) {
			return ErrConflict
		}
		inserted, err := tx.Exec(ctx, `INSERT INTO staff_identity_recovery_decisions
 (request_id,command_id,requested_by,original_actor_subject,original_user_id,requested_action,replacement_actor_subject,reviewed_by,decision,reason)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(request_id) DO NOTHING`, in.RequestID, command.ID, request.RequestedBy, request.OriginalActorSubject, request.OriginalUserID, request.Action, request.ReplacementActorSubject, in.ReviewedBy, in.Decision, in.Reason)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT request_id,reviewed_by,decision,reason,created_at FROM staff_identity_recovery_decisions WHERE request_id=$1 AND command_id=$2`, in.RequestID, command.ID).Scan(&result.Decision.RequestID, &result.Decision.ReviewedBy, &result.Decision.Decision, &result.Decision.Reason, &result.Decision.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if result.Decision.ReviewedBy != in.ReviewedBy || result.Decision.Decision != in.Decision || result.Decision.Reason != in.Reason {
			return ErrIdempotencyConflict
		}
		if needsSuccessor {
			var child StaffIdentityCommand
			if inserted.RowsAffected() == 1 {
				child, err = prepareStaffIdentitySuccessor(ctx, tx, scope, command, request, *successor)
			} else {
				child, err = scanStaffIdentityCommand(tx.QueryRow(ctx, `SELECT `+staffIdentityColumns+` FROM staff_identity_commands
                    WHERE id=(SELECT successor_command_id FROM staff_identity_replacements WHERE request_id=$1 AND parent_command_id=$2)`, in.RequestID, command.ID))
				if errors.Is(err, ErrNotFound) {
					return ErrConflict
				}
				if err == nil && child.ID != successor.ID {
					return ErrIdempotencyConflict
				}
			}
			if err != nil {
				return err
			}
			child.CreatedAt = child.CreatedAt.UTC()
			result.Successor = &child
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" &&
			(pgerr.TableName == "staff_identity_commands" || pgerr.TableName == "staff_identity_recovery_decisions" || pgerr.TableName == "staff_identity_replacements") {
			return StaffIdentityRecoveryResult{}, ErrConflict
		}
		return StaffIdentityRecoveryResult{}, err
	}
	result.Decision.CreatedAt = result.Decision.CreatedAt.UTC()
	return result, nil
}

const staffRecoveryRequestColumns = `id,command_id,original_actor_subject,original_user_id,requested_by,requested_action,replacement_actor_subject,reason,evidence_reference,created_at`

func scanStaffIdentityRecoveryRequest(row pgx.Row) (StaffIdentityRecoveryRequest, error) {
	var r StaffIdentityRecoveryRequest
	err := row.Scan(&r.ID, &r.CommandID, &r.OriginalActorSubject, &r.OriginalUserID, &r.RequestedBy, &r.Action, &r.ReplacementActorSubject, &r.Reason, &r.EvidenceReference, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffIdentityRecoveryRequest{}, ErrNotFound
	}
	return r, err
}

func prepareStaffIdentitySuccessor(ctx context.Context, tx pgx.Tx, scope StaffIdentityRecoveryScope, parent StaffIdentityCommand, request StaffIdentityRecoveryRequest, in StaffIdentitySuccessor) (StaffIdentityCommand, error) {
	var root uuid.UUID
	var parentGeneration int
	err := tx.QueryRow(ctx, `SELECT roots.root_command_id,coalesce(lineage.generation,0)
 FROM staff_identity_contact_claims roots
 LEFT JOIN staff_identity_replacements lineage ON lineage.successor_command_id=$1
 WHERE roots.provider_org_id=$2 AND roots.email_key=$3
 AND (roots.root_command_id=$1 OR lineage.root_command_id=roots.root_command_id)`, parent.ID, parent.ProviderOrgID, strings.ToLower(parent.Email)).Scan(&root, &parentGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffIdentityCommand{}, ErrConflict
	}
	if err != nil {
		return StaffIdentityCommand{}, err
	}
	child := parent
	child.ID = in.ID
	child.MachineSubject = scope.MachineSubject
	child.ActorSubject = request.ReplacementActorSubject
	child.ProviderUserID = in.ProviderUserID
	child.BindingNonce = in.BindingNonce
	child.Reason = request.Reason
	child.CreatedAt = time.Time{}
	err = tx.QueryRow(ctx, `INSERT INTO staff_identity_commands
 (id,org_id,product_key,machine_subject,actor_subject,provider_org_id,provider_project_id,email,given_name,family_name,reason,provider_user_id,binding_nonce)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING created_at`, child.ID, child.OrgID, child.ProductKey, child.MachineSubject, child.ActorSubject, child.ProviderOrgID, child.ProviderProjectID, child.Email, child.GivenName, child.FamilyName, child.Reason, child.ProviderUserID, child.BindingNonce).Scan(&child.CreatedAt)
	if err != nil {
		return StaffIdentityCommand{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO staff_identity_replacements
 (parent_command_id,successor_command_id,root_command_id,request_id,org_id,product_key,provider_org_id,provider_project_id,email,replacement_actor_subject,successor_user_id,generation)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, parent.ID, child.ID, root, request.ID, child.OrgID, child.ProductKey, child.ProviderOrgID, child.ProviderProjectID, child.Email, child.ActorSubject, child.ProviderUserID, parentGeneration+1)
	if err != nil {
		return StaffIdentityCommand{}, err
	}
	return child, nil
}
