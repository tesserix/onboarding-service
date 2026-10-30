package onboarding

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/authz"
)

// StaffIdentityRecoveryRequestReceipt exposes review evidence without private source bindings.
type StaffIdentityRecoveryRequestReceipt struct {
	SchemaVersion           int                                   `json:"schema_version"`
	RequestID               uuid.UUID                             `json:"request_id"`
	CommandID               uuid.UUID                             `json:"command_id"`
	TenantID                uuid.UUID                             `json:"tenant_id"`
	RequestedBy             string                                `json:"requested_by"`
	Action                  string                                `json:"requested_action"`
	ReplacementActorSubject string                                `json:"replacement_actor_subject,omitempty"`
	Reason                  string                                `json:"reason"`
	EvidenceReference       string                                `json:"evidence_reference"`
	RecordedAt              time.Time                             `json:"recorded_at"`
	Decision                *StaffIdentityRecoveryDecisionReceipt `json:"decision,omitempty"`
}

func (StaffIdentityRecoveryRequestReceipt) String() string {
	return "[redacted staff recovery request receipt]"
}

// StaffIdentityRecoveryDecisionReceipt describes only the retained independent decision.
type StaffIdentityRecoveryDecisionReceipt struct {
	Decision           string     `json:"decision"`
	ReviewedBy         string     `json:"reviewed_by"`
	Reason             string     `json:"reason"`
	RecordedAt         time.Time  `json:"recorded_at"`
	SuccessorCommandID *uuid.UUID `json:"successor_command_id,omitempty"`
}

func (StaffIdentityRecoveryDecisionReceipt) String() string {
	return "[redacted staff recovery decision receipt]"
}

// StaffIdentityRecoveryRequest returns an audited, freshly authorized request snapshot.
func (s *Service) StaffIdentityRecoveryRequest(ctx context.Context, p authz.Principal, tenant, command, request uuid.UUID) (StaffIdentityRecoveryRequestReceipt, error) {
	empty := StaffIdentityRecoveryRequestReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := s.guard(ctx, p, authz.ActionStaffRecoveryRead, tenant.String(), nil); err != nil {
		return empty, err
	}
	if request.Version() != 7 || request.Variant() != uuid.RFC4122 {
		return empty, ErrInvalid
	}
	scope, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryRead)
	if err != nil {
		return empty, err
	}
	event := recoveryReadEvent(ctx, p, scope, "staff.identity.recovery.request.inspect")
	event.Target = request.String()
	if _, err := s.store.ReadStaffIdentityRecoveryRequest(ctx, scope, request, p.Subject, event); err != nil {
		return empty, recoveryStoreError(err)
	}
	current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryRead)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrConflict
	}
	event = recoveryReadEvent(ctx, p, current, "staff.identity.recovery.request.read")
	event.Target = request.String()
	review, err := s.store.ReadStaffIdentityRecoveryRequest(ctx, current, request, p.Subject, event)
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	r := review.Request
	result := StaffIdentityRecoveryRequestReceipt{SchemaVersion: 1, RequestID: r.ID, CommandID: r.CommandID, TenantID: tenant,
		RequestedBy: r.RequestedBy, Action: r.Action, ReplacementActorSubject: r.ReplacementActorSubject,
		Reason: r.Reason, EvidenceReference: r.EvidenceReference, RecordedAt: r.CreatedAt}
	if d := review.Decision; d != nil {
		result.Decision = &StaffIdentityRecoveryDecisionReceipt{Decision: d.Decision, ReviewedBy: d.ReviewedBy, Reason: d.Reason, RecordedAt: d.CreatedAt, SuccessorCommandID: review.SuccessorCommandID}
	}
	return result, nil
}
