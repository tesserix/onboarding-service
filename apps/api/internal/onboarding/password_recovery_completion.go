package onboarding

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// CompletePasswordRecovery admits one retained native write and never replays it on retry.
func (s *Service) CompletePasswordRecovery(ctx context.Context, in customercontract.PasswordRecoveryPassword) (customercontract.PasswordRecoveryResult, error) {
	var empty customercontract.PasswordRecoveryResult
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.signupIdentity == nil || s.idp == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command, _ := uuid.Parse(in.CommandID)
	challenge, _ := uuid.Parse(in.ID)
	browser, _ := hex.DecodeString(in.BrowserHash)
	recipient, err := s.store.VerifiedPasswordRecovery(ctx, challenge, browser, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	org, err := s.customerIdentityScope(ctx, recipient.TenantID.String())
	if err != nil {
		return empty, err
	}
	operation, err := s.store.PasswordRecoveryCommand(ctx, command, challenge, browser)
	if err == nil {
		return s.passwordRecoveryReceipt(ctx, operation, recipient, org, browser)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return empty, ErrUnavailable
	}
	account, err := s.signupIdentity.TenantRecoveryAccount(ctx, org, recipient.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if account.Locked {
		return empty, ErrConflict
	}
	credentials, err := s.signupIdentity.RecoveryCredentialState(ctx, org, account.Subject, recipient.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	var baseline time.Time
	if credentials.PasswordConfigured {
		baseline, err = time.Parse(time.RFC3339Nano, credentials.PasswordChangedAt)
		if err != nil {
			return empty, ErrUnavailable
		}
		if !recipient.VerifiedAt.After(baseline) {
			return empty, ErrNotFound
		}
	}
	if current, err := s.customerIdentityScope(ctx, recipient.TenantID.String()); err != nil {
		return empty, err
	} else if current != org {
		return empty, ErrNotFound
	}
	operation, dispatch, err := s.store.ReservePasswordRecoveryCommand(ctx, command, challenge, browser, recipient.TenantID, account.Subject, baseline)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if errors.Is(err, store.ErrConflict) {
		return empty, ErrConflict
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if !dispatch {
		return s.passwordRecoveryReceipt(ctx, operation, recipient, org, browser)
	}
	code, err := s.signupIdentity.SignupCode(ctx, account.Subject, true)
	if err != nil {
		return s.retainPasswordRecoveryOutcome(ctx, operation, recipient, org, browser, "unconfirmed", zitadel.PasswordChangeEvidence{})
	}
	// The native code is transient; reauthorize before the credential-changing boundary.
	if current, err := s.customerIdentityScope(ctx, recipient.TenantID.String()); err != nil || current != org {
		_, recordErr := s.retainPasswordRecoveryOutcome(ctx, operation, recipient, org, browser, "unconfirmed", zitadel.PasswordChangeEvidence{})
		if err != nil {
			return empty, err
		}
		if recordErr != nil {
			return empty, recordErr
		}
		return empty, ErrNotFound
	}
	if proof, err := s.store.VerifiedPasswordRecovery(ctx, challenge, browser, time.Now().UTC()); err != nil || proof != recipient {
		if _, recordErr := s.retainPasswordRecoveryOutcome(ctx, operation, recipient, org, browser, "unconfirmed", zitadel.PasswordChangeEvidence{}); recordErr != nil && !errors.Is(recordErr, ErrNotFound) {
			return empty, recordErr
		}
		if errors.Is(err, store.ErrNotFound) || err == nil {
			return empty, ErrNotFound
		}
		return empty, ErrUnavailable
	}
	evidence, err := s.signupIdentity.SetRecoveryPassword(ctx, org, account.Subject, code, in.Password)
	state := "applied"
	if errors.Is(err, zitadel.ErrRecoveryPasswordPolicy) {
		state = "refused"
	} else if err != nil || !evidence.ChangedAt.After(baseline) {
		state = "unconfirmed"
	}
	if state != "applied" {
		evidence = zitadel.PasswordChangeEvidence{}
	}
	return s.retainPasswordRecoveryOutcome(ctx, operation, recipient, org, browser, state, evidence)
}

func (s *Service) retainPasswordRecoveryOutcome(ctx context.Context, op store.PasswordRecoveryOperation, recipient store.PasswordRecoveryRecipient, org string, browser []byte, state string, evidence zitadel.PasswordChangeEvidence) (customercontract.PasswordRecoveryResult, error) {
	// Keep acknowledged or uncertain native outcomes even after a browser disconnect.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.store.RecordPasswordRecoveryCommand(recordCtx, op.ID, state, evidence.Sequence, evidence.ChangedAt); err != nil {
		return customercontract.PasswordRecoveryResult{}, ErrUnavailable
	}
	op.State = state
	op.NativeSequence = evidence.Sequence
	if !evidence.ChangedAt.IsZero() {
		op.NativeChangedAt = &evidence.ChangedAt
	}
	return s.passwordRecoveryReceipt(ctx, op, recipient, org, browser)
}

func (s *Service) passwordRecoveryReceipt(ctx context.Context, op store.PasswordRecoveryOperation, recipient store.PasswordRecoveryRecipient, org string, browser []byte) (customercontract.PasswordRecoveryResult, error) {
	var empty customercontract.PasswordRecoveryResult
	if op.TenantID != recipient.TenantID {
		return empty, ErrNotFound
	}
	if op.State == "processing" && time.Since(op.CreatedAt) > 30*time.Second {
		if err := s.store.RecordPasswordRecoveryCommand(ctx, op.ID, "unconfirmed", "", time.Time{}); err != nil {
			return empty, ErrUnavailable
		}
		op.State = "unconfirmed"
	}
	if op.State == "applied" {
		credentials, err := s.signupIdentity.RecoveryCredentialState(ctx, org, op.Subject, recipient.Email)
		if err == nil && credentials.PasswordConfigured && op.NativeChangedAt != nil {
			cutoff, err := time.Parse(time.RFC3339Nano, credentials.PasswordChangedAt)
			if err == nil && !cutoff.Before(*op.NativeChangedAt) {
				if err = s.store.RecordPasswordRecoveryCommand(ctx, op.ID, "completed", op.NativeSequence, *op.NativeChangedAt); err != nil {
					return empty, ErrUnavailable
				}
				op.State = "completed"
			}
		}
	}
	current, err := s.customerIdentityScope(ctx, recipient.TenantID.String())
	if err != nil {
		return empty, err
	}
	if current != org {
		return empty, ErrNotFound
	}
	proof, err := s.store.VerifiedPasswordRecovery(ctx, op.ChallengeID, browser, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if proof != recipient {
		return empty, ErrNotFound
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: op.TenantID.String(), Action: "account.password_recovery.receipt_read", Outcome: audit.OutcomeSuccess, RequestID: op.ID.String()}); err != nil {
		return empty, ErrUnavailable
	}
	return customercontract.PasswordRecoveryResult{State: op.State}, nil
}
