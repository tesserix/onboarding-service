package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"github.com/tesserix/onboarding-service/api/staffcontract"
)

type StaffInvitationReceipt = staffcontract.StaffInvitationReceipt
type StaffRecipientInput = staffcontract.StaffRecipientInput

func staffInvitationError(err error) error {
	var limited store.StaffInvitationRateLimit
	if errors.As(err, &limited) {
		return limited
	}
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrIdempotencyConflict) {
		return ErrConflict
	}
	return ErrUnavailable
}

func (s *Service) staffSetupSource(ctx context.Context, p authz.Principal, scope store.StaffIdentityScope) (store.StaffIdentityOperation, zitadel.StaffIdentityObservation, bool, error) {
	var proof zitadel.StaffIdentityObservation
	if s.signupIdentity == nil || s.staffSource == nil {
		return store.StaffIdentityOperation{}, proof, false, ErrUnavailable
	}
	op, err := s.readStaffIdentity(ctx, p, scope, "staff.invitation.source", nil)
	if err != nil {
		return op, proof, false, err
	}
	if op.Creation == nil || op.RecoveryState != "" {
		return op, proof, false, ErrConflict
	}
	c := op.Command
	started := time.Now().UTC()
	proof, err = s.staffSource.ObserveStaffIdentity(ctx, zitadel.StaffIdentityInput{OrganizationID: c.ProviderOrgID, UserID: c.ProviderUserID, Email: c.Email, GivenName: c.GivenName, FamilyName: c.FamilyName, BindingNonce: c.BindingNonce}, op.Creation.ProviderCreatedAt)
	if err != nil || !validStaffObservation(proof, op, started, time.Now().UTC()) {
		return op, proof, false, ErrUnavailable
	}
	if proof.State != "active" && proof.State != "initial" {
		return op, proof, false, ErrNotFound
	}
	configured := false
	completed, err := s.store.StaffSetupCompleted(ctx, scope, staffIdentityEvent(ctx, p, scope, "staff.invitation.completion", nil))
	if err != nil {
		return op, proof, false, staffInvitationError(err)
	}
	if proof.EmailVerified {
		configured, err = s.signupIdentity.StaffPasswordConfigured(ctx, c.ProviderOrgID, c.ProviderUserID)
		if err != nil {
			return op, proof, false, ErrUnavailable
		}
	}
	current, err := s.staffIdentityScope(ctx, p, scope.OrgID, scope.CommandID, scope.ActorSubject, authz.ActionStaffIdentityRead)
	if err != nil {
		return op, proof, false, err
	}
	if current != scope {
		return op, proof, false, ErrUnavailable
	}
	return op, proof, configured || completed, nil
}

func (s *Service) DeliverStaffInvitation(ctx context.Context, p authz.Principal, tenant, command, id uuid.UUID, actor string) (StaffInvitationReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	scope, err := s.staffIdentityScope(ctx, p, tenant, command, actor, authz.ActionStaffIdentityCreate)
	if err != nil {
		return StaffInvitationReceipt{}, err
	}
	return s.deliverStaffInvitation(ctx, p, scope, id)
}

func (s *Service) deliverStaffInvitation(ctx context.Context, p authz.Principal, scope store.StaffIdentityScope, id uuid.UUID) (StaffInvitationReceipt, error) {
	var empty StaffInvitationReceipt
	if id.Version() != 7 || id.Variant() != uuid.RFC4122 {
		return empty, ErrInvalid
	}
	op, source, configured, err := s.staffSetupSource(ctx, p, scope)
	if err != nil {
		return empty, err
	}
	if configured {
		return StaffInvitationReceipt{State: "complete"}, nil
	}
	event := staffIdentityEvent(ctx, p, scope, "staff.invitation.read", nil)
	if prior, err := s.store.StaffInvitationForCommand(ctx, scope, id, event); err == nil {
		return invitationReceipt(prior, source.EmailVerified, configured), nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, staffInvitationError(err)
	}
	if s.signupMail == nil {
		return empty, ErrUnavailable
	}
	phase := "email"
	if source.EmailVerified {
		phase = "password"
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return empty, ErrUnavailable
	}
	hash := sha256.Sum256(raw[:])
	event.Action = "staff.invitation.dispatch"
	row, claimed, err := s.store.PrepareStaffInvitation(ctx, scope, id, hash[:], phase, event)
	if err != nil {
		return empty, staffInvitationError(err)
	}
	if !claimed {
		return invitationReceipt(row, source.EmailVerified, configured), nil
	}
	code, err := s.signupIdentity.SignupCode(ctx, op.Command.ProviderUserID, phase == "password")
	if err != nil {
		return empty, ErrUnavailable
	}
	org, err := s.store.Organization(ctx, scope.OrgID)
	if err != nil {
		return empty, ErrUnavailable
	}
	c := op.Command
	provider, err := s.signupMail.SendStaff(ctx, signupmail.StaffMessage{Email: c.Email, Name: c.GivenName, WorkspaceName: org.Name, InvitationID: id, Ticket: base64.RawURLEncoding.EncodeToString(raw[:]), Code: code, Phase: phase, ExpiresAt: row.ExpiresAt})
	if err != nil {
		return empty, ErrUnavailable
	}
	current, err := s.staffIdentityScope(ctx, p, scope.OrgID, scope.CommandID, scope.ActorSubject, authz.ActionStaffIdentityCreate)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	event.Action = "staff.invitation.accepted"
	if err = s.store.RecordStaffInvitationMail(ctx, scope, id, provider, event); err != nil {
		return empty, staffInvitationError(err)
	}
	row.AcceptedAt = new(time.Time)
	*row.AcceptedAt = time.Now().UTC()
	return invitationReceipt(row, source.EmailVerified, false), nil
}

func invitationReceipt(v store.StaffInvitation, verified, configured bool) StaffInvitationReceipt {
	r := StaffInvitationReceipt{State: "delivery_unresolved", InvitationID: &v.ID, Phase: v.Phase, ExpiresAt: &v.ExpiresAt}
	switch {
	case configured || v.PasswordConfigured:
		r.State = "complete"
	case v.Phase == "email" && verified:
		r.State = "already_verified"
	case v.ProofUncertain:
		r.State = "proof_unresolved"
	case v.ConsumedAt != nil:
		r.State = "consumed"
	case !v.ExpiresAt.After(time.Now().UTC()):
		r.State = "expired"
	case v.AcceptedAt != nil:
		r.State = v.Phase + "_action_required"
	}
	return r
}

func (s *Service) staffRecipient(ctx context.Context, in StaffRecipientInput) (store.StaffInvitation, authz.Principal, []byte, error) {
	var empty store.StaffInvitation
	var p authz.Principal
	ticket, err := base64.RawURLEncoding.DecodeString(in.Ticket)
	if err != nil || len(ticket) != 32 || base64.RawURLEncoding.EncodeToString(ticket) != in.Ticket {
		return empty, p, nil, ErrNotFound
	}
	hash := sha256.Sum256(ticket)
	v, err := s.store.StaffInvitationByTicket(ctx, in.InvitationID, hash[:])
	if err != nil {
		return empty, p, nil, staffInvitationError(err)
	}
	p = authz.Principal{Kind: authz.KindProduct, Subject: v.Scope.MachineSubject, ProductKey: v.Scope.ProductKey}
	current, err := s.staffIdentityScope(ctx, p, v.Scope.OrgID, v.CommandID, v.Scope.ActorSubject, authz.ActionStaffIdentityRead)
	if err != nil {
		return empty, p, nil, err
	}
	if current != v.Scope {
		return empty, p, nil, ErrUnavailable
	}
	return v, p, hash[:], nil
}

// StaffRecipient handles status, proof and bounded resend without adopting caller IDs.
func (s *Service) StaffRecipient(ctx context.Context, in StaffRecipientInput, action string) (StaffInvitationReceipt, error) {
	var empty StaffInvitationReceipt
	if in.Validate(action) != nil {
		return empty, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	v, p, hash, err := s.staffRecipient(ctx, in)
	if err != nil {
		return empty, err
	}
	op, source, configured, err := s.staffSetupSource(ctx, p, v.Scope)
	if err != nil {
		return empty, err
	}
	event := staffIdentityEvent(ctx, p, v.Scope, "staff.recipient."+action, nil)
	event.ActorType = audit.ActorSystem
	event.ActorID = "staff-recipient-capability"
	if err = s.store.AppendAudit(ctx, event); err != nil {
		return empty, ErrUnavailable
	}
	r := invitationReceipt(v, source.EmailVerified, configured)
	if action == "status" || r.State == "complete" || action == "email" && r.State == "already_verified" {
		return r, nil
	}
	if action == "resend" {
		return s.deliverStaffInvitation(ctx, p, v.Scope, *in.DeliveryID)
	}
	if action != v.Phase || action != "email" && action != "password" || r.State == "expired" || v.ConsumedAt != nil || !v.Current {
		return empty, ErrConflict
	}
	if in.Code == "" || len(in.Code) > 20 || action == "email" && in.Password != "" || action == "password" && (in.Password == "" || !source.EmailVerified) {
		return empty, ErrInvalid
	}
	proof, err := s.store.ClaimStaffInvitationProof(ctx, v.ID, hash, event)
	if err != nil {
		return empty, staffInvitationError(err)
	}
	if action == "password" {
		err = s.signupIdentity.SetSignupPassword(ctx, op.Command.ProviderUserID, in.Code, in.Password)
	} else {
		err = s.signupIdentity.VerifySignupEmail(ctx, op.Command.ProviderUserID, in.Code)
	}
	sourceErr := err
	// A lost response is reconciled against native facts before any proof is repeated.
	_, after, hasPassword, observeErr := s.staffSetupSource(ctx, p, v.Scope)
	success := observeErr == nil && after.EmailVerified && (action == "email" || hasPassword)
	if observeErr != nil || !success && !errors.Is(sourceErr, zitadel.ErrSignupProof) {
		return empty, ErrUnavailable
	}
	event.Action = "staff.recipient." + action + ".rejected"
	if success {
		event.Action = "staff.recipient." + action + ".accepted"
	}
	if err = s.store.FinishStaffInvitationProof(ctx, proof, success, event); err != nil {
		return empty, staffInvitationError(err)
	}
	if !success {
		if errors.Is(sourceErr, zitadel.ErrSignupProof) {
			return empty, ErrInvalid
		}
		return empty, ErrUnavailable
	}
	return invitationReceipt(v, after.EmailVerified, hasPassword), nil
}
