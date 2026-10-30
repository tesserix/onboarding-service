package onboarding

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// StaffRecoverySubjectSource classifies an exact frozen account without mutation.
type StaffRecoverySubjectSource interface {
	InspectStaffRecoverySubject(context.Context, zitadel.StaffIdentityInput, time.Time) (zitadel.StaffRecoverySubjectProof, error)
}

// StaffIdentityRecoverySourceReceipt is a point-in-time observation, not approval.
type StaffIdentityRecoverySourceReceipt struct {
	SchemaVersion   int        `json:"schema_version"`
	CommandID       uuid.UUID  `json:"command_id"`
	TenantID        uuid.UUID  `json:"tenant_id"`
	Disposition     string     `json:"source_disposition"`
	ObservedAt      time.Time  `json:"observed_at"`
	SourceCreatedAt *time.Time `json:"source_created_at,omitempty"`
}

// ObserveStaffIdentityRecovery returns only freshly proved, audited source facts.
func (s *Service) ObserveStaffIdentityRecovery(ctx context.Context, p authz.Principal, tenant, command uuid.UUID) (StaffIdentityRecoverySourceReceipt, error) {
	empty := StaffIdentityRecoverySourceReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	scope, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryRead)
	if err != nil {
		return empty, err
	}
	op, err := s.store.ReadStaffIdentityRecovery(ctx, scope, p.Subject, recoveryReadEvent(ctx, p, scope, "staff.identity.recovery.inspect"))
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	if op.DispatchedAt == nil {
		return empty, ErrConflict
	}
	inspector, ok := s.staffSource.(StaffRecoverySubjectSource)
	if !ok {
		return empty, ErrUnavailable
	}
	c := op.Command
	started := time.Now().UTC()
	proof, err := inspector.InspectStaffRecoverySubject(ctx, zitadel.StaffIdentityInput{OrganizationID: c.ProviderOrgID, UserID: c.ProviderUserID,
		Email: c.Email, GivenName: c.GivenName, FamilyName: c.FamilyName, BindingNonce: c.BindingNonce}, c.CreatedAt)
	if err != nil || proof.UserID != c.ProviderUserID || proof.OrganizationID != c.ProviderOrgID || proof.VerifiedAt.Before(started) || proof.VerifiedAt.After(time.Now().UTC()) {
		return empty, ErrUnavailable
	}
	var born *time.Time
	switch proof.Disposition {
	case "absent":
		if !proof.CreatedAt.IsZero() || proof.ObjectRevision != "" {
			return empty, ErrUnavailable
		}
	case "matching", "removed":
		revision, err := strconv.ParseUint(proof.ObjectRevision, 10, 64)
		if err != nil || revision == 0 || strconv.FormatUint(revision, 10) != proof.ObjectRevision || proof.CreatedAt.IsZero() || proof.CreatedAt.Before(c.CreatedAt.Add(-zitadel.StaffIdentityClockSkew)) || proof.CreatedAt.After(time.Now().Add(zitadel.StaffIdentityClockSkew)) {
			return empty, ErrUnavailable
		}
		at := proof.CreatedAt.UTC()
		born = &at
	default:
		return empty, ErrUnavailable
	}
	current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryRead)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrConflict
	}
	detail, err := json.Marshal(map[string]any{"source_disposition": proof.Disposition, "observed_at": proof.VerifiedAt})
	if err != nil {
		return empty, ErrUnavailable
	}
	event := recoveryReadEvent(ctx, p, current, "staff.identity.recovery.source.observe")
	event.Detail = string(detail)
	if _, err := s.store.ReadStaffIdentityRecovery(ctx, current, p.Subject, event); err != nil {
		return empty, recoveryStoreError(err)
	}
	return StaffIdentityRecoverySourceReceipt{SchemaVersion: 1, CommandID: command, TenantID: tenant, Disposition: proof.Disposition,
		ObservedAt: proof.VerifiedAt, SourceCreatedAt: born}, nil
}
