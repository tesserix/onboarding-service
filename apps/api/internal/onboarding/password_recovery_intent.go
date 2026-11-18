package onboarding

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"time"
)

// PreparePasswordRecovery retains an exact no-password intent before any password may be sent.
func (s *Service) PreparePasswordRecovery(ctx context.Context, in customercontract.PasswordRecoveryIntent) (customercontract.PasswordRecoveryResult, error) {
	var empty customercontract.PasswordRecoveryResult
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.signupIdentity == nil || s.idp == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
	account, baseline, err := s.passwordRecoveryCandidate(ctx, recipient, org)
	if err != nil {
		return empty, err
	}

	operation, _, err = s.store.PreparePasswordRecoveryCommand(ctx, command, challenge, browser, recipient.TenantID, account.Subject, baseline)
	if errors.Is(err, store.ErrConflict) {
		return empty, ErrConflict
	}
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	return s.passwordRecoveryReceipt(ctx, operation, recipient, org, browser)
}

// CancelPasswordRecovery retains cancellation before acknowledging that a late password is fenced.
func (s *Service) CancelPasswordRecovery(ctx context.Context, in customercontract.PasswordRecoveryIntent) (customercontract.PasswordRecoveryResult, error) {
	// Reserve even a missing intent, so a preparation arriving later cannot revive it.
	prepared, err := s.PreparePasswordRecovery(ctx, in)
	if err != nil {
		return customercontract.PasswordRecoveryResult{}, err
	}
	if prepared.State == "cancelled" {
		return prepared, nil
	}
	if prepared.State != "prepared" {
		return customercontract.PasswordRecoveryResult{}, ErrConflict
	}
	command, _ := uuid.Parse(in.CommandID)
	challenge, _ := uuid.Parse(in.ID)
	browser, _ := hex.DecodeString(in.BrowserHash)
	if err = s.store.CancelPasswordRecoveryCommand(ctx, command, challenge, browser); errors.Is(err, store.ErrConflict) {
		return customercontract.PasswordRecoveryResult{}, ErrConflict
	} else if err != nil {
		return customercontract.PasswordRecoveryResult{}, ErrUnavailable
	}
	status, err := s.ReadPasswordRecovery(ctx, customercontract.PasswordRecoveryRead{ID: in.ID, BrowserHash: in.BrowserHash, CommandID: in.CommandID})
	if err != nil {
		return customercontract.PasswordRecoveryResult{}, err
	}
	return customercontract.PasswordRecoveryResult(status), nil
}

func (s *Service) passwordRecoveryCandidate(ctx context.Context, recipient store.PasswordRecoveryRecipient, org string) (zitadel.RecoveryAccount, time.Time, error) {
	var empty zitadel.RecoveryAccount
	account, err := s.signupIdentity.TenantRecoveryAccount(ctx, org, recipient.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, time.Time{}, ErrNotFound
	}
	if err != nil {
		return empty, time.Time{}, ErrUnavailable
	}
	if account.Locked {
		return empty, time.Time{}, ErrConflict
	}
	credentials, err := s.signupIdentity.RecoveryCredentialState(ctx, org, account.Subject, recipient.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, time.Time{}, ErrNotFound
	}
	if err != nil {
		return empty, time.Time{}, ErrUnavailable
	}
	var baseline time.Time
	if credentials.PasswordConfigured {
		baseline, err = time.Parse(time.RFC3339Nano, credentials.PasswordChangedAt)
		if err != nil {
			return empty, time.Time{}, ErrUnavailable
		}
		if !recipient.VerifiedAt.After(baseline) {
			return empty, time.Time{}, ErrNotFound
		}
	}
	if current, err := s.customerIdentityScope(ctx, recipient.TenantID.String()); err != nil {
		return empty, time.Time{}, err
	} else if current != org {
		return empty, time.Time{}, ErrNotFound
	}
	return account, baseline, nil
}
