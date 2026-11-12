package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

type StaffInvitation struct {
	ID, CommandID          uuid.UUID
	Phase                  string
	CreatedAt, ExpiresAt   time.Time
	AcceptedAt, ConsumedAt *time.Time
	PasswordConfigured     bool
	Current                bool
	Superseded             bool
	ProofUncertain         bool
	Scope                  StaffIdentityScope
}

type StaffInvitationRateLimit struct{ RetryAfterSeconds int }

func (StaffInvitationRateLimit) Error() string { return "staff invitation rate limit" }
func (StaffInvitationRateLimit) Unwrap() error { return ErrConflict }

type StaffSetupAcknowledgements struct {
	EmailConfirmed     bool
	PasswordConfigured bool
}

func (s *Store) StaffSetupAcknowledged(ctx context.Context, scope StaffIdentityScope, event audit.Event) (StaffSetupAcknowledgements, error) {
	var complete StaffSetupAcknowledgements
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := invitationCommand(ctx, tx, scope); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_identity_setups WHERE command_id=$1 AND email_confirmed_at IS NOT NULL), EXISTS(SELECT 1 FROM staff_identity_setups WHERE command_id=$1 AND password_configured_at IS NOT NULL)`, scope.CommandID).Scan(&complete.EmailConfirmed, &complete.PasswordConfigured); err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffSetupAcknowledgements{}, err
	}
	return complete, nil
}

func (StaffInvitation) String() string   { return "[redacted staff invitation]" }
func (StaffInvitation) GoString() string { return "[redacted staff invitation]" }

func invitationScope(c StaffIdentityCommand) StaffIdentityScope {
	return StaffIdentityScope{c.ID, c.OrgID, c.ProductKey, c.MachineSubject, c.ActorSubject, c.ProviderOrgID, c.ProviderProjectID}
}

func invitationCommand(ctx context.Context, tx pgx.Tx, scope StaffIdentityScope) (StaffIdentityCommand, error) {
	c, err := readScopedStaffIdentityCommand(ctx, tx, scope)
	if err != nil {
		return c, err
	}
	op, err := readStaffIdentityOperation(ctx, tx, c)
	if err != nil {
		return c, err
	}
	if op.Creation == nil || op.RecoveryState != "" {
		return StaffIdentityCommand{}, ErrConflict
	}
	return c, nil
}

func lockStaffSetup(ctx context.Context, tx pgx.Tx, command uuid.UUID) (bool, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO staff_identity_setups(command_id) VALUES($1) ON CONFLICT DO NOTHING`, command); err != nil {
		return false, err
	}
	var completed bool
	err := tx.QueryRow(ctx, `SELECT password_configured_at IS NOT NULL FROM staff_identity_setups WHERE command_id=$1 FOR UPDATE`, command).Scan(&completed)
	return completed, err
}

const staffInvitationColumns = `id,command_id,phase,created_at,expires_at,accepted_at,consumed_at,proof_uncertain`

func scanStaffInvitation(row pgx.Row) (StaffInvitation, error) {
	var v StaffInvitation
	err := row.Scan(&v.ID, &v.CommandID, &v.Phase, &v.CreatedAt, &v.ExpiresAt, &v.AcceptedAt, &v.ConsumedAt, &v.ProofUncertain)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// StaffInvitationForCommand reauthorizes an administrator's exact delivery receipt.
func (s *Store) StaffInvitationForCommand(ctx context.Context, scope StaffIdentityScope, id uuid.UUID, event audit.Event) (StaffInvitation, error) {
	var v StaffInvitation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := invitationCommand(ctx, tx, scope); err != nil {
			return err
		}
		var err error
		v, err = scanStaffInvitation(tx.QueryRow(ctx, `SELECT `+staffInvitationColumns+` FROM staff_identity_invitation_attempts WHERE id=$1 AND command_id=$2`, id, scope.CommandID))
		if err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffInvitation{}, err
	}
	v.Scope = scope
	return v, nil
}

// PrepareStaffInvitation retains one dispatch before any native-code or mail call.
func (s *Store) PrepareStaffInvitation(ctx context.Context, scope StaffIdentityScope, id uuid.UUID, hash []byte, phase string, event audit.Event) (StaffInvitation, bool, error) {
	var v StaffInvitation
	claimed := false
	if id.Version() != 7 || id.Variant() != uuid.RFC4122 || len(hash) != 32 || (phase != "email" && phase != "password") {
		return v, false, ErrConflict
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := invitationCommand(ctx, tx, scope); err != nil {
			return err
		}
		completed, err := lockStaffSetup(ctx, tx, scope.CommandID)
		if err != nil {
			return err
		}
		v, err = scanStaffInvitation(tx.QueryRow(ctx, `SELECT `+staffInvitationColumns+` FROM staff_identity_invitation_attempts WHERE id=$1 AND command_id=$2`, id, scope.CommandID))
		if err == nil {
			if v.Phase != phase {
				return ErrIdempotencyConflict
			}
			return appendAudit(ctx, tx, event)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if completed {
			return ErrConflict
		}
		var busy bool
		var recent, day int
		if err = tx.QueryRow(ctx, `SELECT lease_until>now() FROM staff_identity_setups WHERE command_id=$1 AND lease_until IS NOT NULL`, scope.CommandID).Scan(&busy); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if busy {
			return ErrConflict
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE phase=$2 AND created_at>now()-interval '60 seconds'),count(*) FROM staff_identity_invitation_attempts WHERE command_id=$1 AND created_at>now()-interval '24 hours'`, scope.CommandID, phase).Scan(&recent, &day); err != nil {
			return err
		}
		if day >= 5 {
			return StaffInvitationRateLimit{86400}
		}
		if recent > 0 {
			return StaffInvitationRateLimit{60}
		}
		v, err = scanStaffInvitation(tx.QueryRow(ctx, `INSERT INTO staff_identity_invitation_attempts(id,command_id,token_hash,phase,expires_at) VALUES($1,$2,$3,$4,now()+interval '30 minutes') RETURNING `+staffInvitationColumns, id, scope.CommandID, hash, phase))
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE staff_identity_setups SET lease_id=$2,lease_until=now()+interval '30 seconds',updated_at=now() WHERE command_id=$1`, scope.CommandID, id); err != nil {
			return err
		}
		claimed = true
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffInvitation{}, false, err
	}
	v.Scope = scope
	return v, claimed, nil
}

// RecordStaffInvitationMail records provider acceptance; it is not inbox delivery.
func (s *Store) RecordStaffInvitationMail(ctx context.Context, scope StaffIdentityScope, id uuid.UUID, provider string, event audit.Event) error {
	if !providerMessageID.MatchString(provider) {
		return ErrConflict
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := invitationCommand(ctx, tx, scope); err != nil {
			return err
		}
		if _, err := lockStaffSetup(ctx, tx, scope.CommandID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE staff_identity_invitation_attempts SET accepted_at=now(),provider_id=$3 WHERE id=$1 AND command_id=$2 AND accepted_at IS NULL`, id, scope.CommandID, provider)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE staff_identity_setups SET lease_id=NULL,lease_until=NULL,updated_at=now() WHERE command_id=$1 AND lease_id=$2`, scope.CommandID, id); err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
}

// StaffInvitationByTicket performs a private capability lookup; callers must audit disclosure.
func (s *Store) StaffInvitationByTicket(ctx context.Context, id uuid.UUID, hash []byte) (StaffInvitation, error) {
	var v StaffInvitation
	if id.Version() != 7 || len(hash) != 32 {
		return v, ErrNotFound
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		v, err = scanStaffInvitation(tx.QueryRow(ctx, `SELECT `+staffInvitationColumns+` FROM staff_identity_invitation_attempts WHERE id=$1 AND token_hash=$2`, id, hash))
		if err != nil {
			return err
		}
		c, err := scanStaffIdentityCommand(tx.QueryRow(ctx, `SELECT `+staffIdentityColumns+` FROM staff_identity_commands WHERE id=$1`, v.CommandID))
		if err != nil {
			return err
		}
		v.Scope = invitationScope(c)
		if _, err = invitationCommand(ctx, tx, v.Scope); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT password_configured_at IS NOT NULL,NOT EXISTS(SELECT 1 FROM staff_identity_invitation_attempts a JOIN staff_identity_invitation_attempts b ON a.command_id=b.command_id AND b.sequence>a.sequence WHERE a.id=$2) FROM staff_identity_setups WHERE command_id=$1`, v.CommandID, id).Scan(&v.PasswordConfigured, &v.Current)
	})
	if err != nil {
		return StaffInvitation{}, err
	}
	v.Superseded = !v.Current
	return v, nil
}

type StaffInvitationProof struct {
	Invitation StaffInvitation
	LeaseID    uuid.UUID
}

func (StaffInvitationProof) String() string   { return "[redacted staff invitation proof]" }
func (StaffInvitationProof) GoString() string { return "[redacted staff invitation proof]" }

// ClaimStaffInvitationProof limits attempts and serializes code mutations per account.
func (s *Store) ClaimStaffInvitationProof(ctx context.Context, id uuid.UUID, hash []byte, event audit.Event) (StaffInvitationProof, error) {
	var proof StaffInvitationProof
	v, err := s.StaffInvitationByTicket(ctx, id, hash)
	if err != nil {
		return proof, err
	}
	lease, err := uuid.NewRandom()
	if err != nil {
		return proof, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := invitationCommand(ctx, tx, v.Scope); err != nil {
			return err
		}
		complete, err := lockStaffSetup(ctx, tx, v.CommandID)
		if err != nil {
			return err
		}
		if complete {
			return ErrConflict
		}
		result, err := tx.Exec(ctx, `UPDATE staff_identity_setups SET lease_id=$2,lease_until=now()+interval '30 seconds',updated_at=now() WHERE command_id=$1 AND (lease_until IS NULL OR lease_until<=now())`, v.CommandID, lease)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		result, err = tx.Exec(ctx, `UPDATE staff_identity_invitation_attempts a SET proof_attempts=proof_attempts+1,proof_uncertain=true WHERE id=$1 AND token_hash=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp() AND proof_attempts<10 AND NOT proof_uncertain AND NOT EXISTS(SELECT 1 FROM staff_identity_invitation_attempts b WHERE b.command_id=a.command_id AND b.sequence>a.sequence)`, id, hash)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return proof, err
	}
	return StaffInvitationProof{v, lease}, nil
}

func (s *Store) FinishStaffInvitationProof(ctx context.Context, proof StaffInvitationProof, success bool, event audit.Event) error {
	v := proof.Invitation
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := invitationCommand(ctx, tx, v.Scope); err != nil {
			return err
		}
		if _, err := lockStaffSetup(ctx, tx, v.CommandID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE staff_identity_setups SET email_confirmed_at=CASE WHEN $4 THEN coalesce(email_confirmed_at,now()) ELSE email_confirmed_at END,password_configured_at=CASE WHEN $3 THEN coalesce(password_configured_at,now()) ELSE password_configured_at END,lease_id=NULL,lease_until=NULL,updated_at=now() WHERE command_id=$1 AND lease_id=$2 AND lease_until>now()`, v.CommandID, proof.LeaseID, success && v.Phase == "password", success && v.Phase == "email")
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE staff_identity_invitation_attempts SET proof_uncertain=false,consumed_at=CASE WHEN $3 THEN coalesce(consumed_at,now()) ELSE consumed_at END WHERE id=$1 AND command_id=$2`, v.ID, v.CommandID, success); err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
}
