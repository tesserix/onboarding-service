package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// StaffIdentityRecoveryReview is an exact request and its retained decision snapshot.
type StaffIdentityRecoveryReview struct {
	Request            StaffIdentityRecoveryRequest
	Decision           *StaffIdentityRecoveryDecision
	SuccessorCommandID *uuid.UUID
}

func (StaffIdentityRecoveryReview) String() string { return "[redacted staff recovery review]" }

// ReadStaffIdentityRecoveryRequest requires current scope and exact disclosure audit.
func (s *Store) ReadStaffIdentityRecoveryRequest(ctx context.Context, scope StaffIdentityRecoveryScope, request uuid.UUID, operator string, event audit.Event) (StaffIdentityRecoveryReview, error) {
	empty := StaffIdentityRecoveryReview{}
	if request == uuid.Nil || operator == "" || event.ActorType != audit.ActorPlatform || event.ActorID != operator || event.ProductKey != scope.ProductKey || event.OrgID != scope.OrgID.String() || event.Target != request.String() || (event.Action != "staff.identity.recovery.request.read" && event.Action != "staff.identity.recovery.request.inspect") || event.Outcome != audit.OutcomeSuccess {
		return empty, ErrConflict
	}
	var result StaffIdentityRecoveryReview
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		command, err := readStaffIdentityRecoveryTarget(ctx, tx, scope)
		if err != nil {
			return err
		}
		r := &result.Request
		var decisionID *uuid.UUID
		var reviewer, decision, reason *string
		var recorded *time.Time
		err = tx.QueryRow(ctx, `SELECT r.*,d.request_id,d.reviewed_by,d.decision,d.reason,d.created_at,l.successor_command_id
 FROM (SELECT `+staffRecoveryRequestColumns+` FROM staff_identity_recovery_requests WHERE id=$1 AND command_id=$2) r
 LEFT JOIN staff_identity_recovery_decisions d ON d.request_id=r.id AND d.command_id=r.command_id
 LEFT JOIN staff_identity_replacements l ON l.request_id=r.id AND l.parent_command_id=r.command_id`, request, command.ID).
			Scan(&r.ID, &r.CommandID, &r.OriginalActorSubject, &r.OriginalUserID, &r.RequestedBy, &r.Action, &r.ReplacementActorSubject, &r.Reason, &r.EvidenceReference, &r.CreatedAt,
				&decisionID, &reviewer, &decision, &reason, &recorded, &result.SuccessorCommandID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if decisionID != nil {
			if *decisionID != request || reviewer == nil || decision == nil || reason == nil || recorded == nil {
				return ErrConflict
			}
			result.Decision = &StaffIdentityRecoveryDecision{RequestID: request, ReviewedBy: *reviewer, Decision: *decision, Reason: *reason, CreatedAt: recorded.UTC()}
			if (*decision == "approved" && r.Action == "replace") != (result.SuccessorCommandID != nil) {
				return ErrConflict
			}
		} else if reviewer != nil || decision != nil || reason != nil || recorded != nil || result.SuccessorCommandID != nil {
			return ErrConflict
		}
		r.CreatedAt = r.CreatedAt.UTC()
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return empty, err
	}
	return result, nil
}
