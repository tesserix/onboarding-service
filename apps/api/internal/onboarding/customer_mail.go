package onboarding

import (
	"context"
	"errors"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"time"
)

// WithCustomerMail supplies the product-scoped native-code delivery client.
func WithCustomerMail(client *signupmail.Client) Option {
	return func(s *Service) { s.signupMail = client }
}

func (s *Service) deliverInitialCustomerEmail(ctx context.Context, row store.CustomerSignup) error {
	if row.EmailAcceptedAt != nil || row.ExistingOwnerSubject != "" || row.ReceiptVerified {
		return nil
	}
	if s.signupMail == nil || s.signupIdentity == nil || row.Input.Owner == nil || row.LeaseID == nil {
		return ErrUnavailable
	}
	verified, err := s.signupIdentity.SignupIdentity(ctx, "helivanta-"+row.TenantID.String(), row.OwnerSubject, row.Input.Owner.Email)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	if verified {
		return nil
	}
	if err = s.store.ConsumeSignupMail(ctx, row.ID, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil
		}
		return ErrUnavailable
	}
	id, err := s.sendCustomerEmail(ctx, row, false)
	if err != nil {
		return ErrUnavailable
	}
	return s.store.AcceptCustomerEmail(ctx, row, id)
}

func (s *Service) sendCustomerEmail(ctx context.Context, row store.CustomerSignup, password bool) (string, error) {
	if s.signupMail == nil || row.Input.Owner == nil || row.ExistingOwnerSubject != "" {
		return "", ErrUnavailable
	}
	code, err := s.signupIdentity.SignupCode(ctx, row.OwnerSubject, password)
	if err != nil {
		return "", ErrUnavailable
	}
	phase := "email"
	if password {
		phase = "password"
	}
	return s.signupMail.Send(ctx, signupmail.Message{Email: row.Input.Owner.Email, Name: row.Input.Owner.GivenName, Organization: row.Input.Name, UserID: row.OwnerSubject, Code: code, Phase: phase})
}
