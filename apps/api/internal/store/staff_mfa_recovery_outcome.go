package store

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// RecordStaffMFARecovery preserves dispatched outcomes even after entitlement loss.
func (s *Store) RecordStaffMFARecovery(ctx context.Context, c StaffMFARecoveryCommand, state, sequence, changed string, event audit.Event) error {
	action := "unconfirmed"
	if state == "awaiting_enrollment" {
		action = "removed"
	}
	if err := staffMFAShape(c, event, action); err != nil {
		return err
	}
	if state != "awaiting_enrollment" && state != "unconfirmed" {
		return ErrConflict
	}
	if state == "awaiting_enrollment" {
		if !staffMFAEvidence(sequence, changed) {
			return ErrConflict
		}
	} else if sequence != "" || changed != "" {
		return ErrConflict
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		original, err := readExactStaffMFA(ctx, tx, c)
		if err != nil {
			return err
		}
		if original.State == state && original.NativeSequence == sequence && original.NativeChangedAt == changed {
			return appendAudit(ctx, tx, event)
		}
		if original.State != "processing" {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `UPDATE staff_mfa_recovery_commands SET state=$3,native_sequence=nullif($4,''),native_changed_at=nullif($5,''),updated_at=clock_timestamp() WHERE id=$1 AND org_id=$2 AND state='processing'`, c.Request.Intent.CommandID, c.Request.Target.TenantID, state, sequence, changed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		return appendAudit(ctx, tx, event)
	})
}

// CompleteStaffMFARecovery records enrollment for the current acknowledged generation.
// The consumer must first prove the exact fresh native session against its cutoffs.
func (s *Store) CompleteStaffMFARecovery(ctx context.Context, c StaffMFARecoveryCommand, sequence, verifiedAt string, event audit.Event) error {
	if err := staffMFAShape(c, event, "complete"); err != nil {
		return err
	}
	return s.completeStaffMFARecovery(ctx, c, sequence, verifiedAt, event, false)
}

func (s *Store) completeStaffMFARecovery(ctx context.Context, c StaffMFARecoveryCommand, sequence, verifiedAt string, event audit.Event, admission bool) error {
	if !staffMFAEvidence(sequence, verifiedAt) {
		return ErrConflict
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockStaffMFA(ctx, tx, c); err != nil {
			return err
		}
		original, err := readExactStaffMFA(ctx, tx, c)
		if err != nil {
			return err
		}
		var currentID string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM staff_mfa_recovery_commands WHERE target_subject=$1 AND state<>'cancelled' ORDER BY generation DESC LIMIT 1`, c.Request.Target.Subject).Scan(&currentID); err != nil {
			return err
		}
		if currentID != c.Request.Intent.CommandID {
			return ErrConflict
		}
		if original.State == "completed" && original.EnrollmentSequence == sequence && original.EnrollmentVerifiedAt == verifiedAt {
			return appendAudit(ctx, tx, event)
		}
		if admission && original.State == "completed" {
			prior, err := strconv.ParseUint(original.EnrollmentSequence, 10, 64)
			if err != nil {
				return ErrConflict
			}
			current, err := strconv.ParseUint(sequence, 10, 64)
			if err != nil || current <= prior {
				return ErrConflict
			}
			before, err := time.Parse(time.RFC3339Nano, original.EnrollmentVerifiedAt)
			if err != nil {
				return ErrConflict
			}
			after, err := time.Parse(time.RFC3339Nano, verifiedAt)
			if err != nil || after.Before(before) {
				return ErrConflict
			}
			return appendAudit(ctx, tx, event)
		}
		if original.State != "awaiting_enrollment" {
			return ErrConflict
		}
		native, err := strconv.ParseUint(original.NativeSequence, 10, 64)
		if err != nil {
			return ErrConflict
		}
		enrolled, err := strconv.ParseUint(sequence, 10, 64)
		if err != nil || enrolled <= native {
			return ErrConflict
		}
		changed, err := time.Parse(time.RFC3339Nano, original.NativeChangedAt)
		if err != nil {
			return ErrConflict
		}
		verified, err := time.Parse(time.RFC3339Nano, verifiedAt)
		if err != nil || verified.Before(changed) {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `UPDATE staff_mfa_recovery_commands SET state='completed',enrollment_sequence=$3,enrollment_verified_at=$4,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND org_id=$2 AND state='awaiting_enrollment'`, c.Request.Intent.CommandID, c.Request.Target.TenantID, sequence, verifiedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		return appendAudit(ctx, tx, event)
	})
}

func staffMFAEvidence(sequence, instant string) bool {
	n, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != sequence {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, instant)
	return err == nil && at.Unix() > 0 && !at.After(time.Now()) && at.UTC().Format(time.RFC3339Nano) == instant
}

// CurrentStaffMFARecovery retains completed admission requirements and ignores cancellations.
// The service reauthorizes scope and audits the surrounding identity/admission read.
func (s *Store) CurrentStaffMFARecovery(ctx context.Context, tenant, subject string) (StaffMFARecoveryCommand, error) {
	id, err := uuid.Parse(tenant)
	if err != nil || id == uuid.Nil || id.String() != tenant || subject == "" || len(subject) > 200 {
		return StaffMFARecoveryCommand{}, ErrNotFound
	}
	return scanStaffMFA(s.pool.QueryRow(ctx, `SELECT `+staffMFAColumns+` FROM staff_mfa_recovery_commands WHERE org_id=$1 AND target_subject=$2 AND state<>'cancelled' ORDER BY generation DESC LIMIT 1`, id, subject))
}
