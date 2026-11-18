package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// PasswordRecoveryOperation retains a native command outcome without its credential payload.
type PasswordRecoveryOperation struct {
	ID, ChallengeID, TenantID          uuid.UUID
	Subject, State, NativeSequence     string
	BaselineChangedAt, NativeChangedAt *time.Time
	CreatedAt                          time.Time
}

const recoveryCommandColumns = `id,challenge_id,tenant_id,subject,state,native_sequence,baseline_changed_at,native_changed_at,created_at`

func scanRecoveryOperation(row pgx.Row) (PasswordRecoveryOperation, error) {
	var out PasswordRecoveryOperation
	var sequence *string
	err := row.Scan(&out.ID, &out.ChallengeID, &out.TenantID, &out.Subject, &out.State, &sequence, &out.BaselineChangedAt, &out.NativeChangedAt, &out.CreatedAt)
	if sequence != nil {
		out.NativeSequence = *sequence
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

// PasswordRecoveryCommand is readable only with the original unexpired verified browser proof.
func (s *Store) PasswordRecoveryCommand(ctx context.Context, id, challenge uuid.UUID, browser []byte) (PasswordRecoveryOperation, error) {
	return scanRecoveryOperation(s.pool.QueryRow(ctx, `SELECT `+recoveryCommandColumns+` FROM password_recovery_commands WHERE id=$1 AND challenge_id=$2 AND EXISTS(SELECT 1 FROM password_recovery_challenges c WHERE c.id=$2 AND c.tenant_id=password_recovery_commands.tenant_id AND c.browser_hash=$3 AND c.verified_at IS NOT NULL AND c.expires_at>clock_timestamp())`, id, challenge, browser))
}

// ReservePasswordRecoveryCommand serializes native writes before leaving the database boundary.
func (s *Store) ReservePasswordRecoveryCommand(ctx context.Context, id, challenge uuid.UUID, browser []byte, tenant uuid.UUID, subject string, baseline time.Time) (PasswordRecoveryOperation, bool, error) {
	var out PasswordRecoveryOperation
	send := false
	if id.Version() != 7 || id.Variant() != uuid.RFC4122 || len(browser) != 32 || tenant == uuid.Nil || subject == "" {
		return out, false, ErrConflict
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,8021987))`, tenant.String()+":"+subject); err != nil {
			return err
		}
		var proofTenant uuid.UUID
		var verified time.Time
		err := tx.QueryRow(ctx, `SELECT tenant_id,verified_at FROM password_recovery_challenges WHERE id=$1 AND browser_hash=$2 AND verified_at IS NOT NULL AND expires_at>clock_timestamp() FOR UPDATE`, challenge, browser).Scan(&proofTenant, &verified)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && proofTenant != tenant {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out, err = scanRecoveryOperation(tx.QueryRow(ctx, `SELECT `+recoveryCommandColumns+` FROM password_recovery_commands WHERE id=$1`, id))
		if err == nil {
			if out.ChallengeID != challenge || out.TenantID != tenant || out.Subject != subject {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if !baseline.IsZero() && !verified.After(baseline) {
			return ErrNotFound
		}
		var nativeBaseline *time.Time
		if !baseline.IsZero() {
			nativeBaseline = &baseline
		}
		var fenced bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM password_recovery_commands WHERE tenant_id=$1 AND subject=$2 AND (state IN ('processing','applied','unconfirmed') OR (state='completed' AND ($3::timestamptz IS NULL OR native_changed_at>$3))))`, tenant, subject, nativeBaseline).Scan(&fenced); err != nil {
			return err
		}
		if fenced {
			return ErrConflict
		}
		var attempts int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM password_recovery_commands WHERE challenge_id=$1 LIMIT 5) b`, challenge).Scan(&attempts); err != nil {
			return err
		}
		if attempts >= 5 {
			return ErrConflict
		}
		out, err = scanRecoveryOperation(tx.QueryRow(ctx, `INSERT INTO password_recovery_commands(id,challenge_id,tenant_id,subject,baseline_changed_at) VALUES($1,$2,$3,$4,$5) RETURNING `+recoveryCommandColumns, id, challenge, tenant, subject, nativeBaseline))
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: "helivanta-api", ProductKey: "helivanta", OrgID: tenant.String(), Action: "account.password_recovery.admitted", Outcome: audit.OutcomeSuccess, RequestID: id.String()}); err != nil {
			return err
		}
		send = true
		return nil
	})
	return out, send && err == nil, err
}

// RecordPasswordRecoveryCommand commits acknowledged evidence or a retained uncertain/refused outcome.
func (s *Store) RecordPasswordRecoveryCommand(ctx context.Context, id uuid.UUID, state, sequence string, changed time.Time) error {
	var nativeSequence *string
	var nativeChanged *time.Time
	expected := "processing"
	switch state {
	case "applied", "completed":
		n, err := strconv.ParseUint(sequence, 10, 64)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != sequence || !changed.After(time.Unix(0, 0)) {
			return ErrConflict
		}
		nativeSequence = &sequence
		nativeChanged = &changed
		if state == "completed" {
			expected = "applied"
		}
	case "refused", "unconfirmed":
		if sequence != "" || !changed.IsZero() {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var tenant uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE password_recovery_commands SET state=$2,native_sequence=$3,native_changed_at=$4,updated_at=clock_timestamp() WHERE id=$1 AND state=$5 AND ($5<>'applied' OR (native_sequence=$3 AND native_changed_at=$4)) RETURNING tenant_id`, id, state, nativeSequence, nativeChanged, expected).Scan(&tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		outcome := audit.OutcomeSuccess
		if state == "unconfirmed" {
			outcome = audit.OutcomeError
		}
		if state == "refused" {
			outcome = audit.OutcomeDenied
		}
		return appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: "helivanta-api", ProductKey: "helivanta", OrgID: tenant.String(), Action: "account.password_recovery." + state, Outcome: outcome, RequestID: id.String()})
	})
}

// LatestPasswordRecoveryCommand restores a receipt only for its verified browser capability.
func (s *Store) LatestPasswordRecoveryCommand(ctx context.Context, challenge uuid.UUID, browser []byte) (PasswordRecoveryOperation, error) {
	return scanRecoveryOperation(s.pool.QueryRow(ctx, `SELECT `+recoveryCommandColumns+` FROM password_recovery_commands WHERE challenge_id=$1 AND EXISTS(SELECT 1 FROM password_recovery_challenges c WHERE c.id=$1 AND c.tenant_id=password_recovery_commands.tenant_id AND c.browser_hash=$2 AND c.verified_at IS NOT NULL AND c.expires_at>clock_timestamp()) ORDER BY created_at DESC,id DESC LIMIT 1`, challenge, browser))
}
