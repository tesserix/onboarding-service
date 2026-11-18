package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// StaffMFARecoveryCommand freezes reviewed source facts before a native reset.
type StaffMFARecoveryCommand struct {
	Request                                          customercontract.StaffMFARecoveryRequest
	MachineSubject, ProviderOrgID, ProviderProjectID string
	Generation                                       int64
	State, NativeSequence, NativeChangedAt           string
	EnrollmentSequence, EnrollmentVerifiedAt         string
	ApplicationFenceID                               *uuid.UUID
	DispatchedAt, CompletedAt                        *time.Time
	CreatedAt, UpdatedAt                             time.Time
}

func (StaffMFARecoveryCommand) String() string   { return "[redacted staff MFA recovery command]" }
func (StaffMFARecoveryCommand) GoString() string { return "[redacted staff MFA recovery command]" }

const staffMFAColumns = `id,generation,org_id,machine_subject,actor_subject,target_subject,provider_org_id,provider_project_id,
 employer_kind,employer_id,staff_id,staff_version,link_id,link_version,reason,state,application_fence_id,dispatched_at,
 coalesce(native_sequence,''),coalesce(native_changed_at,''),coalesce(enrollment_sequence,''),coalesce(enrollment_verified_at,''),completed_at,created_at,updated_at`

func scanStaffMFA(row pgx.Row) (StaffMFARecoveryCommand, error) {
	var out StaffMFARecoveryCommand
	var id, org, employer, staff, link uuid.UUID
	err := row.Scan(&id, &out.Generation, &org, &out.MachineSubject, &out.Request.ActorSubject, &out.Request.Target.Subject,
		&out.ProviderOrgID, &out.ProviderProjectID, &out.Request.Target.EmployerKind, &employer, &staff, &out.Request.Target.StaffVersion,
		&link, &out.Request.Target.LinkVersion, &out.Request.Intent.Reason, &out.State, &out.ApplicationFenceID, &out.DispatchedAt,
		&out.NativeSequence, &out.NativeChangedAt, &out.EnrollmentSequence, &out.EnrollmentVerifiedAt, &out.CompletedAt, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffMFARecoveryCommand{}, ErrNotFound
	}
	if err != nil {
		return StaffMFARecoveryCommand{}, err
	}
	out.Request.Intent.CommandID = id.String()
	out.Request.Target.TenantID = org.String()
	out.Request.Target.EmployerID = employer.String()
	out.Request.Target.StaffID = staff.String()
	out.Request.Target.LinkID = link.String()
	out.Request.Intent.ExpectedLinkID = link.String()
	out.Request.Intent.ExpectedLinkVersion = out.Request.Target.LinkVersion
	out.Request.Intent.ExpectedStaffVersion = out.Request.Target.StaffVersion
	return out, nil
}

func staffMFAShape(c StaffMFARecoveryCommand, event audit.Event, action string) error {
	if c.Request.Validate() != nil || c.MachineSubject == "" || c.ProviderProjectID == "" || c.ProviderOrgID != "helivanta-"+c.Request.Target.TenantID ||
		event.ActorType != audit.ActorProduct || event.ActorID != c.MachineSubject || event.ProductKey != "helivanta" ||
		event.OrgID != c.Request.Target.TenantID || event.Target != c.Request.Intent.CommandID || event.RequestID != c.Request.Intent.CommandID ||
		event.Outcome != audit.OutcomeSuccess || event.Action != "staff.mfa.recovery."+action {
		return ErrConflict
	}
	return nil
}

func lockStaffMFA(ctx context.Context, tx pgx.Tx, c StaffMFARecoveryCommand) error {
	tenant, err := uuid.Parse(c.Request.Target.TenantID)
	if err != nil {
		return ErrConflict
	}
	if err = lockStaffIdentityScope(ctx, tx, StaffIdentityCommand{OrgID: tenant, ProductKey: "helivanta", MachineSubject: c.MachineSubject,
		ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,8021987))`, c.Request.Target.TenantID+":"+c.Request.Target.Subject)
	return err
}

func sameStaffMFA(a, b StaffMFARecoveryCommand) bool {
	return a.Request == b.Request && a.MachineSubject == b.MachineSubject && a.ProviderOrgID == b.ProviderOrgID && a.ProviderProjectID == b.ProviderProjectID
}

func readExactStaffMFA(ctx context.Context, tx pgx.Tx, c StaffMFARecoveryCommand) (StaffMFARecoveryCommand, error) {
	out, err := scanStaffMFA(tx.QueryRow(ctx, `SELECT `+staffMFAColumns+` FROM staff_mfa_recovery_commands WHERE id=$1 AND org_id=$2 FOR UPDATE`, c.Request.Intent.CommandID, c.Request.Target.TenantID))
	if err == nil && !sameStaffMFA(out, c) {
		return StaffMFARecoveryCommand{}, ErrIdempotencyConflict
	}
	return out, err
}

// PrepareStaffMFARecovery retains a single account intent and audit atomically.
func (s *Store) PrepareStaffMFARecovery(ctx context.Context, c StaffMFARecoveryCommand, event audit.Event) (StaffMFARecoveryCommand, bool, error) {
	if err := staffMFAShape(c, event, "prepare"); err != nil {
		return StaffMFARecoveryCommand{}, false, err
	}
	var out StaffMFARecoveryCommand
	created := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockStaffMFA(ctx, tx, c); err != nil {
			return err
		}
		var err error
		out, err = readExactStaffMFA(ctx, tx, c)
		if err == nil {
			return appendAudit(ctx, tx, event)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var fenced bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_mfa_recovery_commands WHERE org_id=$1 AND target_subject=$2 AND state IN ('prepared','processing','awaiting_enrollment','unconfirmed'))`, c.Request.Target.TenantID, c.Request.Target.Subject).Scan(&fenced); err != nil {
			return err
		}
		if fenced {
			return ErrConflict
		}
		out, created, err = insertStaffMFA(ctx, tx, c, "prepared")
		if err != nil {
			return err
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffMFARecoveryCommand{}, false, err
	}
	return out, created, nil
}

func insertStaffMFA(ctx context.Context, tx pgx.Tx, c StaffMFARecoveryCommand, state string) (StaffMFARecoveryCommand, bool, error) {
	r := c.Request
	tag, err := tx.Exec(ctx, `INSERT INTO staff_mfa_recovery_commands(id,org_id,product_key,machine_subject,actor_subject,target_subject,provider_org_id,provider_project_id,
 employer_kind,employer_id,staff_id,staff_version,link_id,link_version,reason,state)
 VALUES($1,$2,'helivanta',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(id) DO NOTHING`,
		r.Intent.CommandID, r.Target.TenantID, c.MachineSubject, r.ActorSubject, r.Target.Subject, c.ProviderOrgID, c.ProviderProjectID,
		r.Target.EmployerKind, r.Target.EmployerID, r.Target.StaffID, r.Target.StaffVersion, r.Target.LinkID, r.Target.LinkVersion, r.Intent.Reason, state)
	if err != nil {
		return StaffMFARecoveryCommand{}, false, err
	}
	out, err := readExactStaffMFA(ctx, tx, c)
	return out, tag.RowsAffected() == 1, err
}

// DispatchStaffMFARecovery claims one irreversible native write after app revocation.
func (s *Store) DispatchStaffMFARecovery(ctx context.Context, c StaffMFARecoveryCommand, fenceID string, event audit.Event) (StaffMFARecoveryCommand, bool, error) {
	if err := staffMFAShape(c, event, "dispatch"); err != nil || fenceID != c.Request.Intent.CommandID {
		return StaffMFARecoveryCommand{}, false, ErrConflict
	}
	var out StaffMFARecoveryCommand
	send := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockStaffMFA(ctx, tx, c); err != nil {
			return err
		}
		var err error
		out, err = readExactStaffMFA(ctx, tx, c)
		if err != nil {
			return err
		}
		if out.State == "cancelled" {
			return ErrConflict
		}
		if out.State == "prepared" {
			out, err = scanStaffMFA(tx.QueryRow(ctx, `UPDATE staff_mfa_recovery_commands SET state='processing',application_fence_id=id,dispatched_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND org_id=$2 AND state='prepared' RETURNING `+staffMFAColumns, c.Request.Intent.CommandID, c.Request.Target.TenantID))
			if err != nil {
				return err
			}
			send = true
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffMFARecoveryCommand{}, false, err
	}
	return out, send, nil
}

// CancelStaffMFARecovery reserves missing IDs so late preparation cannot revive them.
func (s *Store) CancelStaffMFARecovery(ctx context.Context, c StaffMFARecoveryCommand, event audit.Event) (StaffMFARecoveryCommand, error) {
	if err := staffMFAShape(c, event, "cancel"); err != nil {
		return StaffMFARecoveryCommand{}, err
	}
	var out StaffMFARecoveryCommand
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockStaffMFA(ctx, tx, c); err != nil {
			return err
		}
		var err error
		out, err = readExactStaffMFA(ctx, tx, c)
		if errors.Is(err, ErrNotFound) {
			out, _, err = insertStaffMFA(ctx, tx, c, "cancelled")
		}
		if err != nil {
			return err
		}
		if out.State != "prepared" && out.State != "cancelled" {
			return ErrConflict
		}
		if out.State == "prepared" {
			out, err = scanStaffMFA(tx.QueryRow(ctx, `UPDATE staff_mfa_recovery_commands SET state='cancelled',updated_at=clock_timestamp() WHERE id=$1 AND org_id=$2 AND state='prepared' RETURNING `+staffMFAColumns, c.Request.Intent.CommandID, c.Request.Target.TenantID))
			if err != nil {
				return err
			}
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return StaffMFARecoveryCommand{}, err
	}
	return out, nil
}
