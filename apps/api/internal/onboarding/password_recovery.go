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
	"github.com/tesserix/onboarding-service/api/internal/store"
)

func passwordRecoveryCodeHash(id, browser, code string) []byte {
	sum := sha256.Sum256([]byte("helivanta-password-recovery-v1:" + id + ":" + browser + ":" + code))
	return sum[:]
}

// StartPasswordRecovery reserves a dedicated mailbox challenge before any account lookup.
func (s *Service) StartPasswordRecovery(ctx context.Context, in customercontract.PasswordRecoveryStart) error {
	if in.Validate() != nil {
		return ErrInvalid
	}
	if s.store == nil || s.signupMail == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	value, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return ErrUnavailable
	}
	code := fmt.Sprintf("%08d", value.Int64())
	browser, _ := hex.DecodeString(in.BrowserHash)
	source, _ := hex.DecodeString(in.SourceHash)
	id, _ := uuid.Parse(in.ID)
	tenant, _ := uuid.Parse(in.TenantID)
	send, err := s.store.ReservePasswordRecovery(ctx, store.PasswordRecoveryReservation{ID: id, TenantID: tenant, BrowserHash: browser, SourceHash: source, Email: strings.ToLower(in.Email), CodeHash: passwordRecoveryCodeHash(in.ID, in.BrowserHash, code), CreatedAt: time.Now().UTC()})
	if errors.Is(err, store.ErrPasswordRecoveryLimited) {
		return store.ErrPasswordRecoveryLimited
	}
	if errors.Is(err, store.ErrConflict) {
		return ErrConflict
	}
	if err != nil {
		return ErrUnavailable
	}
	if !send {
		return nil
	}
	_, sendErr := s.signupMail.SendPasswordRecovery(ctx, in.ID, in.Email, code)
	// Preserve an uncertain dispatch even when the browser abandons its request.
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer recordCancel()
	recordErr := s.store.RecordPasswordRecoveryDelivery(recordCtx, id, sendErr == nil)
	if sendErr != nil || recordErr != nil {
		return ErrUnavailable
	}
	return nil
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
