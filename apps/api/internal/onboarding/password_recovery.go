package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func passwordRecoveryCodeHash(id, browser, code string) []byte {
	sum := sha256.Sum256([]byte("helivanta-password-recovery-v1:" + id + ":" + browser + ":" + code))
	return sum[:]
}

// StartPasswordRecovery reserves a dedicated mailbox challenge before any account lookup.
func (s *Service) StartPasswordRecovery(ctx context.Context, in customercontract.PasswordRecoveryStart) (customercontract.PasswordRecoveryAccepted, error) {
	var empty customercontract.PasswordRecoveryAccepted
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.signupMail == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	value, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return empty, ErrUnavailable
	}
	code := fmt.Sprintf("%08d", value.Int64())
	browser, _ := hex.DecodeString(in.BrowserHash)
	source, _ := hex.DecodeString(in.SourceHash)
	id, _ := uuid.Parse(in.ID)
	tenant, _ := uuid.Parse(in.TenantID)
	send, err := s.store.ReservePasswordRecovery(ctx, store.PasswordRecoveryReservation{ID: id, TenantID: tenant, BrowserHash: browser, SourceHash: source, Email: strings.ToLower(in.Email), CodeHash: passwordRecoveryCodeHash(in.ID, in.BrowserHash, code), CreatedAt: time.Now().UTC()})
	if errors.Is(err, store.ErrPasswordRecoveryLimited) {
		return empty, store.ErrPasswordRecoveryLimited
	}
	if errors.Is(err, store.ErrConflict) {
		return empty, ErrConflict
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	retained, err := s.store.RetainedPasswordRecovery(ctx, id, tenant, browser, strings.ToLower(in.Email))
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	receipt := customercontract.PasswordRecoveryAccepted{State: "check_email", ChallengeID: retained.String()}
	if !send {
		return receipt, nil
	}
	if retained != id {
		if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: tenant.String(), Action: "account.password_recovery.suppressed", Outcome: audit.OutcomeSuccess, RequestID: in.ID}); err != nil {
			return empty, ErrUnavailable
		}
		return receipt, nil
	}
	_, sendErr := s.signupMail.SendPasswordRecovery(ctx, in.ID, in.Email, code)
	// Preserve an uncertain dispatch even when the browser abandons its request.
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer recordCancel()
	recordErr := s.store.RecordPasswordRecoveryDelivery(recordCtx, id, sendErr == nil)
	if sendErr != nil || recordErr != nil {
		return empty, ErrUnavailable
	}
	return receipt, nil
}

// VerifyPasswordRecovery consumes only this browser's purpose-bound mailbox code.
func (s *Service) VerifyPasswordRecovery(ctx context.Context, in customercontract.PasswordRecoveryProof) error {
	if in.Validate() != nil {
		return ErrInvalid
	}
	if s.store == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	browser, _ := hex.DecodeString(in.BrowserHash)
	id, _ := uuid.Parse(in.ID)
	err := s.store.VerifyPasswordRecovery(ctx, id, browser, passwordRecoveryCodeHash(in.ID, in.BrowserHash, in.Code), time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

// ReadPasswordRecovery checks current native eligibility only after mailbox proof.
func (s *Service) ReadPasswordRecovery(ctx context.Context, in customercontract.PasswordRecoveryAccess) (customercontract.PasswordRecoveryStatus, error) {
	var empty customercontract.PasswordRecoveryStatus
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.signupIdentity == nil || s.idp == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	id, _ := uuid.Parse(in.ID)
	browser, _ := hex.DecodeString(in.BrowserHash)
	recipient, err := s.store.VerifiedPasswordRecovery(ctx, id, browser, time.Now().UTC())
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
	operation, commandErr := s.store.LatestPasswordRecoveryCommand(ctx, id, browser)
	if commandErr == nil && operation.State != "refused" {
		result, err := s.passwordRecoveryReceipt(ctx, operation, recipient, org, browser)
		if err != nil {
			return empty, err
		}
		return customercontract.PasswordRecoveryStatus(result), nil
	}
	if commandErr != nil && !errors.Is(commandErr, store.ErrNotFound) {
		return empty, ErrUnavailable
	}
	account, err := s.signupIdentity.TenantRecoveryAccount(ctx, org, recipient.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	current, err := s.customerIdentityScope(ctx, recipient.TenantID.String())
	if err != nil {
		return empty, err
	}
	if current != org {
		return empty, ErrNotFound
	}
	proof, err := s.store.VerifiedPasswordRecovery(ctx, id, browser, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	if proof != recipient {
		return empty, ErrNotFound
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", OrgID: recipient.TenantID.String(), Action: "account.password_recovery.status_read", Outcome: audit.OutcomeSuccess, RequestID: in.ID}); err != nil {
		return empty, ErrUnavailable
	}
	state := "ready"
	if commandErr == nil && operation.State == "refused" {
		state = "refused"
	}
	if account.Locked {
		state = "locked"
	}
	return customercontract.PasswordRecoveryStatus{State: state}, nil
}
