package store

import (
	"context"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

var mfaSubject = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// StaffMFARecoveryRequirement reads account-wide state without disclosing employer sources.
// Only the authorized identity-status service may disclose this private projection.
func (s *Store) StaffMFARecoveryRequirement(ctx context.Context, subject string) (customercontract.StaffMFARecoveryRequirement, error) {
	var out customercontract.StaffMFARecoveryRequirement
	if !mfaSubject.MatchString(subject) {
		return out, ErrNotFound
	}
	var state string
	err := s.pool.QueryRow(ctx, `SELECT id::text,state FROM staff_mfa_recovery_commands
 WHERE target_subject=$1 AND state<>'cancelled' ORDER BY generation DESC LIMIT 1`, subject).Scan(&out.GenerationID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return customercontract.StaffMFARecoveryRequirement{State: "none"}, nil
	}
	if err != nil {
		return customercontract.StaffMFARecoveryRequirement{}, err
	}
	switch state {
	case "prepared", "processing", "unconfirmed":
		out.State = "blocked"
	case "awaiting_enrollment":
		out.State = "enrollment_required"
	case "completed":
		out.State = "completed"
	default:
		return customercontract.StaffMFARecoveryRequirement{}, ErrConflict
	}
	if out.Validate() != nil {
		return customercontract.StaffMFARecoveryRequirement{}, ErrConflict
	}
	return out, nil
}
