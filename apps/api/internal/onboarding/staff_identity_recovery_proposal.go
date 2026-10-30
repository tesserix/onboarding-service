package onboarding

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

var staffRecoveryEvidence = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$`)

// StaffIdentityRecoveryProposal contains requested facts, never the maker's identity.
type StaffIdentityRecoveryProposal struct {
	Action, Reason, EvidenceReference, ReplacementActorSubject string
}

func (StaffIdentityRecoveryProposal) String() string { return "[redacted staff recovery proposal]" }

// StaffIdentityRecoveryProposalReceipt proves recording, not approval or eligibility.
type StaffIdentityRecoveryProposalReceipt struct {
	SchemaVersion int       `json:"schema_version"`
	RequestID     uuid.UUID `json:"request_id"`
	CommandID     uuid.UUID `json:"command_id"`
	TenantID      uuid.UUID `json:"tenant_id"`
	Action        string    `json:"requested_action"`
	RecordedAt    time.Time `json:"recorded_at"`
}

func (in StaffIdentityRecoveryProposal) valid() bool {
	if !utf8.ValidString(in.Reason) || strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 2000 || strings.ContainsFunc(in.Reason, unicode.IsControl) || !staffRecoveryEvidence.MatchString(in.EvidenceReference) {
		return false
	}
	return (in.Action == "close_unresolved" && in.ReplacementActorSubject == "") || (in.Action == "replace" && enrolSourceID.MatchString(in.ReplacementActorSubject))
}

// ProposeStaffIdentityRecovery derives the maker from live authority and freezes evidence.
func (s *Service) ProposeStaffIdentityRecovery(ctx context.Context, p authz.Principal, tenant, command, request uuid.UUID, in StaffIdentityRecoveryProposal) (StaffIdentityRecoveryProposalReceipt, error) {
	empty := StaffIdentityRecoveryProposalReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := s.guard(ctx, p, authz.ActionStaffRecoveryPropose, tenant.String(), nil); err != nil {
		return empty, err
	}
	if request.Version() != 7 || request.Variant() != uuid.RFC4122 || !in.valid() {
		return empty, ErrInvalid
	}
	scope, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryPropose)
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
	current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryPropose)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrConflict
	}
	result, err := s.store.ProposeStaffIdentityRecovery(ctx, current, store.StaffIdentityRecoveryRequest{ID: request,
		RequestedBy: p.Subject, Action: in.Action, Reason: in.Reason, EvidenceReference: in.EvidenceReference,
		ReplacementActorSubject: in.ReplacementActorSubject}, recoveryReadEvent(ctx, p, current, "staff.identity.recovery.propose"))
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	final, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryPropose)
	if err != nil {
		return empty, err
	}
	if final != scope {
		return empty, ErrConflict
	}
	if _, err := s.store.ReadStaffIdentityRecovery(ctx, final, p.Subject, recoveryReadEvent(ctx, p, final, "staff.identity.recovery.inspect")); err != nil {
		return empty, recoveryStoreError(err)
	}
	return StaffIdentityRecoveryProposalReceipt{SchemaVersion: 1, RequestID: result.ID, CommandID: result.CommandID, TenantID: tenant, Action: result.Action, RecordedAt: result.CreatedAt}, nil
}
