package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// StaffIdentityCommand retains private input before any provider creation dispatch.
type StaffIdentityCommand struct {
	ID                uuid.UUID
	OrgID             uuid.UUID
	ProductKey        string
	MachineSubject    string
	ActorSubject      string
	ProviderOrgID     string
	ProviderProjectID string
	ProviderUserID    string
	BindingNonce      string
	Email             string
	GivenName         string
	FamilyName        string
	Reason            string
	CreatedAt         time.Time
}

func (StaffIdentityCommand) String() string { return "[redacted staff identity command]" }

const staffIdentityColumns = `id, org_id, product_key, machine_subject, actor_subject,
 provider_org_id, provider_project_id, provider_user_id, binding_nonce, email,
 given_name, family_name, reason, created_at`

func scanStaffIdentityCommand(row pgx.Row) (StaffIdentityCommand, error) {
	var c StaffIdentityCommand
	err := row.Scan(&c.ID, &c.OrgID, &c.ProductKey, &c.MachineSubject, &c.ActorSubject,
		&c.ProviderOrgID, &c.ProviderProjectID, &c.ProviderUserID, &c.BindingNonce,
		&c.Email, &c.GivenName, &c.FamilyName, &c.Reason, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffIdentityCommand{}, ErrNotFound
	}
	return c, err
}

func lockStaffIdentityScope(ctx context.Context, tx pgx.Tx, c StaffIdentityCommand) error {
	var state, identity, project string
	err := tx.QueryRow(ctx, `SELECT state, identity, zitadel_project_id FROM products
 WHERE key=$1 AND machine_user_id=$2 FOR SHARE`, c.ProductKey, c.MachineSubject).Scan(&state, &identity, &project)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (state != "active" || identity != "zitadel")) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if project != c.ProviderProjectID {
		return ErrConflict
	}
	var org string
	err = tx.QueryRow(ctx, `SELECT state, zitadel_org_id FROM organizations WHERE id=$1 FOR SHARE`, c.OrgID).Scan(&state, &org)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && state != "active") {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if org != c.ProviderOrgID {
		return ErrConflict
	}
	var revoked *time.Time
	err = tx.QueryRow(ctx, `SELECT revoked_at FROM org_products WHERE org_id=$1 AND product_key=$2 FOR SHARE`, c.OrgID, c.ProductKey).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && revoked != nil) {
		return ErrNotFound
	}
	return err
}

// PrepareStaffIdentity atomically retains immutable input and its preparation audit.
func (s *Store) PrepareStaffIdentity(ctx context.Context, c StaffIdentityCommand, event audit.Event) (StaffIdentityCommand, error) {
	var result StaffIdentityCommand
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockStaffIdentityScope(ctx, tx, c); err != nil {
			return err
		}
		inserted, err := tx.Exec(ctx, `INSERT INTO staff_identity_commands
 (id,org_id,product_key,machine_subject,actor_subject,provider_org_id,provider_project_id,
 provider_user_id,binding_nonce,email,given_name,family_name,reason)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(id) DO NOTHING`,
			c.ID, c.OrgID, c.ProductKey, c.MachineSubject, c.ActorSubject, c.ProviderOrgID, c.ProviderProjectID,
			c.ProviderUserID, c.BindingNonce, c.Email, c.GivenName, c.FamilyName, c.Reason)
		if err != nil {
			return err
		}
		result, err = scanStaffIdentityCommand(tx.QueryRow(ctx, `SELECT `+staffIdentityColumns+`
 FROM staff_identity_commands WHERE id=$1 AND org_id=$2 AND product_key=$3`, c.ID, c.OrgID, c.ProductKey))
		if err != nil {
			return err
		}
		if !sameStaffIdentityRequest(result, c) {
			return ErrIdempotencyConflict
		}
		if inserted.RowsAffected() == 0 {
			return requireStaffIdentityContact(ctx, tx, result)
		}
		claim, err := tx.Exec(ctx, `INSERT INTO staff_identity_contact_claims
 (root_command_id,provider_org_id,email,email_key) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			result.ID, result.ProviderOrgID, result.Email, strings.ToLower(result.Email))
		if err != nil {
			return err
		}
		if claim.RowsAffected() != 1 {
			return ErrIdempotencyConflict
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffIdentityCommand{}, err
	}
	return result, nil
}

// ClaimStaffIdentityDispatch commits the sole permission to attempt provider creation.
func (s *Store) ClaimStaffIdentityDispatch(ctx context.Context, c StaffIdentityCommand, event audit.Event) (bool, error) {
	claimed := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockStaffIdentityScope(ctx, tx, c); err != nil {
			return err
		}
		original, err := scanStaffIdentityCommand(tx.QueryRow(ctx, `SELECT `+staffIdentityColumns+`
 FROM staff_identity_commands WHERE id=$1 AND org_id=$2 AND product_key=$3`, c.ID, c.OrgID, c.ProductKey))
		if err != nil {
			return err
		}
		if !sameStaffIdentityCommand(original, c) {
			return ErrConflict
		}
		if err := requireStaffIdentityContact(ctx, tx, original); err != nil {
			return err
		}
		inserted, err := tx.Exec(ctx, `INSERT INTO staff_identity_dispatches(command_id)
 VALUES($1) ON CONFLICT(command_id) DO NOTHING`, c.ID)
		if err != nil {
			return err
		}
		claimed = inserted.RowsAffected() == 1
		if !claimed {
			return nil
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}

func requireStaffIdentityContact(ctx context.Context, tx pgx.Tx, c StaffIdentityCommand) error {
	var claimed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_identity_contact_claims roots
 WHERE roots.provider_org_id=$2 AND roots.email=$3 AND roots.email_key=$4
 AND (roots.root_command_id=$1 OR EXISTS(SELECT 1 FROM staff_identity_replacements lineage
      WHERE lineage.successor_command_id=$1 AND lineage.root_command_id=roots.root_command_id)))
 AND NOT EXISTS(SELECT 1 FROM staff_identity_recovery_decisions
      WHERE command_id=$1 AND decision='approved')`,
		c.ID, c.ProviderOrgID, c.Email, strings.ToLower(c.Email)).Scan(&claimed); err != nil {
		return err
	}
	if !claimed {
		return ErrIdempotencyConflict
	}
	return nil
}

func sameStaffIdentityRequest(a, b StaffIdentityCommand) bool {
	return a.ID == b.ID && a.OrgID == b.OrgID && a.ProductKey == b.ProductKey &&
		a.MachineSubject == b.MachineSubject && a.ActorSubject == b.ActorSubject &&
		a.ProviderOrgID == b.ProviderOrgID && a.ProviderProjectID == b.ProviderProjectID &&
		a.Email == b.Email && a.GivenName == b.GivenName && a.FamilyName == b.FamilyName && a.Reason == b.Reason
}

func sameStaffIdentityCommand(a, b StaffIdentityCommand) bool {
	return sameStaffIdentityRequest(a, b) && a.ProviderUserID == b.ProviderUserID && a.BindingNonce == b.BindingNonce
}

// StaffIdentityCreation is an immutable provider-creation receipt, not current eligibility.
type StaffIdentityCreation struct {
	CommandID         uuid.UUID
	ProviderUserID    string
	ProviderCreatedAt time.Time
	RecordedAt        time.Time
}

// RecordStaffIdentityCreation preserves a dispatched outcome even after entitlement loss.
func (s *Store) RecordStaffIdentityCreation(ctx context.Context, c StaffIdentityCommand, created time.Time, event audit.Event) (StaffIdentityCreation, error) {
	var result StaffIdentityCreation
	if created.IsZero() {
		return result, ErrConflict
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		original, err := scanStaffIdentityCommand(tx.QueryRow(ctx, `SELECT `+staffIdentityColumns+`
 FROM staff_identity_commands WHERE id=$1 AND org_id=$2 AND product_key=$3`, c.ID, c.OrgID, c.ProductKey))
		if err != nil {
			return err
		}
		if !sameStaffIdentityCommand(original, c) {
			return ErrConflict
		}
		var inserted bool
		result, inserted, err = recordStaffIdentityCreation(ctx, tx, original, created)
		if err != nil {
			return err
		}
		if !inserted {
			return nil
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffIdentityCreation{}, err
	}
	return result, nil
}

func recordStaffIdentityCreation(ctx context.Context, tx pgx.Tx, c StaffIdentityCommand, created time.Time) (StaffIdentityCreation, bool, error) {
	var result StaffIdentityCreation
	if err := lockStaffIdentityCommand(ctx, tx, c.ID); err != nil {
		return result, false, err
	}
	inserted, err := tx.Exec(ctx, `INSERT INTO staff_identity_creations
 (command_id,provider_user_id,source_creation_time)
 SELECT command_id,$2,$3 FROM staff_identity_dispatches WHERE command_id=$1
 ON CONFLICT(command_id) DO NOTHING`, c.ID, c.ProviderUserID, created.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return StaffIdentityCreation{}, false, err
	}
	var sourceTime string
	err = tx.QueryRow(ctx, `SELECT command_id,provider_user_id,source_creation_time,created_at
 FROM staff_identity_creations WHERE command_id=$1`, c.ID).
		Scan(&result.CommandID, &result.ProviderUserID, &sourceTime, &result.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffIdentityCreation{}, false, ErrConflict
	}
	if err != nil {
		return StaffIdentityCreation{}, false, err
	}
	result.ProviderCreatedAt, err = time.Parse(time.RFC3339Nano, sourceTime)
	if err != nil {
		return StaffIdentityCreation{}, false, err
	}
	if result.ProviderUserID != c.ProviderUserID || !result.ProviderCreatedAt.Equal(created) {
		return StaffIdentityCreation{}, false, ErrIdempotencyConflict
	}
	return result, inserted.RowsAffected() == 1, nil
}

func lockStaffIdentityCommand(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	// Append-only command ledgers do not grant the UPDATE privilege row locks require.
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('onboarding.staff_identity:' || $1::text,0))`, id.String())
	return err
}
