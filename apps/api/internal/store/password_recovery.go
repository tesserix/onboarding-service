package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

var ErrPasswordRecoveryLimited = errors.New("password recovery delivery limit")

// PasswordRecoveryReservation is the immutable, dedicated mailbox delivery intent.
type PasswordRecoveryReservation struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	BrowserHash []byte
	SourceHash  []byte
	Email       string
	CodeHash    []byte
	CreatedAt   time.Time
}

func (PasswordRecoveryReservation) String() string { return "[redacted password recovery reservation]" }
func (PasswordRecoveryReservation) GoString() string {
	return "[redacted password recovery reservation]"
}

// PasswordRecoveryRecipient is disclosed only after the browser proves its mailbox.
type PasswordRecoveryRecipient struct {
	TenantID   uuid.UUID
	Email      string
	VerifiedAt time.Time
}

func (PasswordRecoveryRecipient) String() string   { return "[redacted password recovery recipient]" }
func (PasswordRecoveryRecipient) GoString() string { return "[redacted password recovery recipient]" }

// ReservePasswordRecovery freezes the command and charges shared budgets before email I/O.
// A repeated or already verified browser command never authorizes another dispatch.
func (s *Store) ReservePasswordRecovery(ctx context.Context, in PasswordRecoveryReservation) (bool, error) {
	if in.ID.Version() != 7 || in.ID.Variant() != uuid.RFC4122 || in.TenantID == uuid.Nil || len(in.BrowserHash) != 32 || len(in.SourceHash) != 32 || len(in.CodeHash) != 32 || in.CreatedAt.IsZero() || in.Email != strings.ToLower(in.Email) || (customercontract.DiscoveryRequest{Email: in.Email}).Validate() != nil {
		return false, ErrConflict
	}
	send := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(8021986)`); err != nil {
			return err
		}
		var tenant uuid.UUID
		var browser, source []byte
		var email string
		err := tx.QueryRow(ctx, `SELECT tenant_id,browser_hash,source_hash,email FROM password_recovery_challenges WHERE id=$1`, in.ID).Scan(&tenant, &browser, &source, &email)
		if err == nil {
			if tenant != in.TenantID || !bytes.Equal(browser, in.BrowserHash) || !bytes.Equal(source, in.SourceHash) || email != in.Email {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var retained bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM password_recovery_delivery_ids WHERE id=$1)`, in.ID).Scan(&retained); err != nil {
			return err
		}
		if retained {
			return nil
		}
		var verified bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM password_recovery_challenges WHERE tenant_id=$1 AND browser_hash=$2 AND email=$3 AND verified_at IS NOT NULL AND expires_at>greatest($4::timestamptz,clock_timestamp()))`, in.TenantID, in.BrowserHash, in.Email, in.CreatedAt).Scan(&verified); err != nil {
			return err
		}
		if verified {
			return nil
		}
		var global, recipientHour, recipientDay, sourceHour, sourceDay int
		err = tx.QueryRow(ctx, `SELECT
   (SELECT count(*) FROM (SELECT 1 FROM password_recovery_challenges WHERE created_at>$1::timestamptz-interval '1 hour' LIMIT 1000) b),
   count(*) FILTER (WHERE email=$2 AND created_at>$1::timestamptz-interval '1 hour'),
   count(*) FILTER (WHERE email=$2),
   count(*) FILTER (WHERE source_hash=$3 AND created_at>$1::timestamptz-interval '1 hour'),
   count(*) FILTER (WHERE source_hash=$3)
   FROM password_recovery_challenges WHERE created_at>$1::timestamptz-interval '24 hours' AND (email=$2 OR source_hash=$3)`, in.CreatedAt, in.Email, in.SourceHash).Scan(&global, &recipientHour, &recipientDay, &sourceHour, &sourceDay)
		if err != nil {
			return err
		}
		if global >= 1000 || recipientHour >= 3 || recipientDay >= 10 || sourceHour >= 10 || sourceDay >= 30 {
			return ErrPasswordRecoveryLimited
		}
		if _, err = tx.Exec(ctx, `INSERT INTO password_recovery_delivery_ids(id,created_at) VALUES($1,$2)`, in.ID, in.CreatedAt); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO password_recovery_challenges(id,tenant_id,browser_hash,source_hash,email,code_hash,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$7::timestamptz+interval '10 minutes')`, in.ID, in.TenantID, in.BrowserHash, in.SourceHash, in.Email, in.CodeHash, in.CreatedAt)
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: "helivanta-api", ProductKey: "helivanta", OrgID: in.TenantID.String(), Action: "account.password_recovery.requested", Outcome: audit.OutcomeSuccess, RequestID: in.ID.String()}); err != nil {
			return err
		}
		send = true
		return nil
	})
	return send && err == nil, err
}

// VerifyPasswordRecovery atomically spends one attempt; successful verification is one use.
func (s *Store) VerifyPasswordRecovery(ctx context.Context, id uuid.UUID, browser, code []byte, now time.Time) error {
	if len(browser) != 32 || len(code) != 32 {
		return ErrNotFound
	}
	var verified bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var tenant uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE password_recovery_challenges SET attempts=attempts+1,updated_at=greatest($4::timestamptz,clock_timestamp()),verified_at=CASE WHEN code_hash=$3 THEN greatest($4::timestamptz,clock_timestamp()) ELSE NULL END WHERE id=$1 AND browser_hash=$2 AND verified_at IS NULL AND attempts<5 AND expires_at>greatest($4::timestamptz,clock_timestamp()) RETURNING tenant_id,verified_at IS NOT NULL`, id, browser, code, now).Scan(&tenant, &verified)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		outcome := audit.OutcomeDenied
		if verified {
			outcome = audit.OutcomeSuccess
		}
		return appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: "helivanta-api", ProductKey: "helivanta", OrgID: tenant.String(), Action: "account.password_recovery.proof", Outcome: outcome, RequestID: id.String()})
	})
	if err == nil && !verified {
		return ErrNotFound
	}
	return err
}

// VerifiedPasswordRecovery reads only the retained, unexpired browser's verified scope.
func (s *Store) VerifiedPasswordRecovery(ctx context.Context, id uuid.UUID, browser []byte, now time.Time) (PasswordRecoveryRecipient, error) {
	var out PasswordRecoveryRecipient
	err := s.pool.QueryRow(ctx, `SELECT tenant_id,email,verified_at FROM password_recovery_challenges WHERE id=$1 AND browser_hash=$2 AND verified_at IS NOT NULL AND expires_at>greatest($3::timestamptz,clock_timestamp())`, id, browser, now).Scan(&out.TenantID, &out.Email, &out.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

// RecordPasswordRecoveryDelivery retains provider acceptance or uncertainty, never inbox delivery.
func (s *Store) RecordPasswordRecoveryDelivery(ctx context.Context, id uuid.UUID, accepted bool) error {
	outcome := "unavailable"
	if accepted {
		outcome = "accepted"
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var tenant uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE password_recovery_challenges SET delivery_outcome=$2,updated_at=clock_timestamp() WHERE id=$1 AND delivery_outcome='reserved' RETURNING tenant_id`, id, outcome).Scan(&tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		result := audit.OutcomeError
		if accepted {
			result = audit.OutcomeSuccess
		}
		return appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: "helivanta-api", ProductKey: "helivanta", OrgID: tenant.String(), Action: "account.password_recovery.delivery", Outcome: result, RequestID: id.String()})
	})
}

// PurgePasswordRecovery erases expired contact/proof data in bounded batches, retaining delivery IDs and audit.
func (s *Store) PurgePasswordRecovery(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `DELETE FROM password_recovery_challenges WHERE id IN (SELECT id FROM password_recovery_challenges WHERE created_at<clock_timestamp()-interval '24 hours' ORDER BY created_at,id LIMIT 5000)`)
	return err
}
