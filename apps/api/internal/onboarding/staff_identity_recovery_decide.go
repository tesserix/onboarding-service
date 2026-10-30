package onboarding

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

// StaffIdentityRecoveryDecisionInput contains the reviewer's immutable decision facts.
type StaffIdentityRecoveryDecisionInput struct {
	Decision           string
	Reason             string
	SuccessorCommandID uuid.UUID
}

func (StaffIdentityRecoveryDecisionInput) String() string {
	return "[redacted staff recovery decision input]"
}

// DecideStaffIdentityRecovery independently reviews fresh source facts without dispatching an account.
func (s *Service) DecideStaffIdentityRecovery(ctx context.Context, p authz.Principal, tenant, command, request uuid.UUID, in StaffIdentityRecoveryDecisionInput) (StaffIdentityRecoveryRequestReceipt, error) {
	empty := StaffIdentityRecoveryRequestReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	scope, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryDecide)
	if err != nil {
		return empty, err
	}
	if request.Version() != 7 || request.Variant() != uuid.RFC4122 || !validStaffRecoveryReason(in.Reason) || (in.Decision != "approved" && in.Decision != "rejected") {
		return empty, ErrInvalid
	}
	event := recoveryReadEvent(ctx, p, scope, "staff.identity.recovery.request.inspect")
	event.Target = request.String()
	review, err := s.store.ReadStaffIdentityRecoveryRequest(ctx, scope, request, p.Subject, event)
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	r := review.Request
	if p.Subject == r.RequestedBy || p.Subject == r.OriginalActorSubject || p.Subject == r.OriginalUserID || p.Subject == r.ReplacementActorSubject {
		return empty, ErrConflict
	}
	needsSuccessor := in.Decision == "approved" && r.Action == "replace"
	if needsSuccessor {
		if in.SuccessorCommandID.Version() != 7 || in.SuccessorCommandID.Variant() != uuid.RFC4122 {
			return empty, ErrInvalid
		}
	} else if in.SuccessorCommandID != uuid.Nil {
		return empty, ErrInvalid
	}
	if d := review.Decision; d != nil {
		if d.ReviewedBy != p.Subject || d.Decision != in.Decision || d.Reason != in.Reason || (review.SuccessorCommandID != nil && *review.SuccessorCommandID != in.SuccessorCommandID) {
			return empty, ErrConflict
		}
		return s.staffRecoveryDecisionReceipt(ctx, p, tenant, command, request, scope)
	}
	var expectedCreation *time.Time
	detail := ""
	if in.Decision == "approved" {
		op, err := s.store.ReadStaffIdentityRecovery(ctx, scope, p.Subject, recoveryReadEvent(ctx, p, scope, "staff.identity.recovery.inspect"))
		if err != nil {
			return empty, recoveryStoreError(err)
		}
		if op.DispatchedAt == nil {
			return empty, ErrConflict
		}
		proof, err := s.inspectStaffRecoverySubject(ctx, op.Command)
		if err != nil {
			return empty, err
		}
		current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryDecide)
		if err != nil {
			return empty, err
		}
		if current != scope {
			return empty, ErrConflict
		}
		raw, err := json.Marshal(map[string]any{"source_disposition": proof.Disposition, "observed_at": proof.VerifiedAt})
		if err != nil {
			return empty, ErrUnavailable
		}
		detail = string(raw)
		if proof.Disposition != "absent" {
			reconciliation := recoveryReadEvent(ctx, p, current, "staff.identity.recovery.reconcile")
			reconciliation.Detail = detail
			if _, err := s.store.ReconcileStaffIdentityCreation(ctx, current, p.Subject, proof.CreatedAt, reconciliation); err != nil {
				return empty, recoveryStoreError(err)
			}
			expectedCreation = &proof.CreatedAt
		}
		if needsSuccessor && proof.Disposition == "matching" {
			return empty, ErrConflict
		}
	}
	var successor *store.StaffIdentitySuccessor
	if needsSuccessor {
		id, err := uuid.NewRandom()
		if err != nil {
			return empty, ErrUnavailable
		}
		var nonce [32]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return empty, ErrUnavailable
		}
		successor = &store.StaffIdentitySuccessor{ID: in.SuccessorCommandID, ProviderUserID: id.String(), BindingNonce: base64.RawURLEncoding.EncodeToString(nonce[:])}
	}
	current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryDecide)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrConflict
	}
	event = recoveryReadEvent(ctx, p, current, "staff.identity.recovery.decide")
	event.Detail = detail
	if _, err := s.store.DecideStaffIdentityRecovery(ctx, current, store.StaffIdentityRecoveryDecision{RequestID: request, ReviewedBy: p.Subject, Decision: in.Decision, Reason: in.Reason}, successor, event, expectedCreation); err != nil {
		return empty, recoveryStoreError(err)
	}
	return s.staffRecoveryDecisionReceipt(ctx, p, tenant, command, request, scope)
}

func (s *Service) staffRecoveryDecisionReceipt(ctx context.Context, p authz.Principal, tenant, command, request uuid.UUID, scope store.StaffIdentityRecoveryScope) (StaffIdentityRecoveryRequestReceipt, error) {
	empty := StaffIdentityRecoveryRequestReceipt{}
	current, err := s.staffRecoveryScope(ctx, p, tenant, command, authz.ActionStaffRecoveryDecide)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrConflict
	}
	event := recoveryReadEvent(ctx, p, current, "staff.identity.recovery.request.read")
	event.Target = request.String()
	review, err := s.store.ReadStaffIdentityRecoveryRequest(ctx, current, request, p.Subject, event)
	if err != nil {
		return empty, recoveryStoreError(err)
	}
	return staffRecoveryRequestReceipt(tenant, review), nil
}
