package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// StaffMFAAdmissionCommand selects only the current acknowledged account recovery.
// The private admission service owns scope authorization and disclosure audit.
func (s *Store) StaffMFAAdmissionCommand(ctx context.Context, subject, generation string) (StaffMFARecoveryCommand, error) {
	id, err := uuid.Parse(generation)
	if !mfaSubject.MatchString(subject) || err != nil || id.String() != generation || id.Version() != 7 || id.Variant() != uuid.RFC4122 {
		return StaffMFARecoveryCommand{}, ErrNotFound
	}
	out, err := scanStaffMFA(s.pool.QueryRow(ctx, `SELECT `+staffMFAColumns+` FROM staff_mfa_recovery_commands WHERE target_subject=$1 AND state<>'cancelled' ORDER BY generation DESC LIMIT 1`, subject))
	if err != nil {
		return StaffMFARecoveryCommand{}, err
	}
	if out.Request.Intent.CommandID != generation || (out.State != "awaiting_enrollment" && out.State != "completed") {
		return StaffMFARecoveryCommand{}, ErrConflict
	}
	return out, nil
}

// AdmitStaffMFARecovery atomically retains first enrollment or audits a fresh
// admission without rewriting completed history. Native proof is verified first.
func (s *Store) AdmitStaffMFARecovery(ctx context.Context, c StaffMFARecoveryCommand, sequence, verifiedAt string, event audit.Event) error {
	if c.Request.Validate() != nil || c.MachineSubject == "" || c.ProviderProjectID == "" || c.ProviderOrgID != "helivanta-"+c.Request.Target.TenantID ||
		event.ActorType != audit.ActorSystem || event.ActorID != audit.HelivantaWorkload || event.ProductKey != "helivanta" ||
		event.OrgID != c.Request.Target.TenantID || event.Target != c.Request.Intent.CommandID || event.Action != "staff.mfa.recovery.admit" ||
		event.Outcome != audit.OutcomeSuccess || event.RequestID == "" || len(event.RequestID) > 200 {
		return ErrConflict
	}
	return s.completeStaffMFARecovery(ctx, c, sequence, verifiedAt, event, true)
}
