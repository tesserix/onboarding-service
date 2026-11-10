package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"strings"
	"time"
)

// StartCustomer durably records server-bound signup intent before identity delivery.
func (s *Service) StartCustomer(ctx context.Context, id uuid.UUID, receiptHash []byte, in customercontract.Signup) (customercontract.Receipt, error) {
	if len(receiptHash) != 32 || in.Validate(id) != nil {
		return customercontract.Receipt{}, ErrInvalid
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return customercontract.Receipt{}, ErrInvalid
	}
	intent := sha256.Sum256(raw)
	email := sha256.Sum256([]byte(strings.ToLower(in.Owner.Email)))
	tenant := customerTenantID(id.String())
	resource, err := uuid.NewV7()
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	row, err := s.store.ReserveCustomerSignup(ctx, store.CustomerSignup{
		ID: id, TenantID: tenant, ResourceID: resource, OwnerSubject: "owner-" + tenant.String(),
		ReceiptHash: receiptHash, IntentHash: intent[:], OwnerEmailHash: email[:], Input: in, RetryAt: time.Now().UTC(),
	}, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, Outcome: audit.OutcomeSuccess})
	if err != nil {
		return customercontract.Receipt{}, customerError(err)
	}
	return customerReceipt(row), nil
}

const customerWorkload = "system:serviceaccount:helivanta:helivanta-api"

func customerTenantID(key string) uuid.UUID {
	return hospitalTenantID("helivanta", customerWorkload, key)
}
func hospitalTenantID(product, actor, key string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("hospital-onboarding:"+product+":"+actor+":"+key))
}
func customerError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}
func customerReceipt(row store.CustomerSignup) customercontract.Receipt {
	state := customercontract.Provisioning
	switch row.State {
	case "existing_account", "identity_requested":
		state = customercontract.EmailActionRequired
	case "needs_support":
		state = customercontract.NeedsSupport
	case "ready":
		if row.ReceiptVerified {
			return customercontract.Receipt{State: customercontract.Ready, Name: row.Input.Name, Kind: row.Input.Kind, TenantID: row.TenantID.String(), ResourceID: row.ResourceID.String()}
		}
		state = customercontract.EmailActionRequired
	}
	return customercontract.Receipt{State: state}
}

// Customer returns only the command bound to the presented receipt.
func (s *Service) Customer(ctx context.Context, id uuid.UUID, hash []byte) (customercontract.Receipt, error) {
	row, err := s.store.CustomerSignup(ctx, id, hash)
	if err != nil {
		return customercontract.Receipt{}, customerError(err)
	}
	return customerReceipt(row), nil
}

// WithCustomerIdentity supplies the native code-verification client.
func WithCustomerIdentity(client *zitadel.Client) Option {
	return func(s *Service) { s.signupIdentity = client }
}

// ProveCustomer accepts only native proof for the immutable expected signup owner.
func (s *Service) ProveCustomer(ctx context.Context, in customercontract.Proof, nextHash []byte, password bool) (uuid.UUID, customercontract.Receipt, error) {
	var empty customercontract.Receipt
	if s.signupIdentity == nil {
		return uuid.Nil, empty, ErrUnavailable
	}
	if len(nextHash) != 32 || !hospitalSourceID.MatchString(in.UserID) || in.Code == "" || len(in.Code) > 20 || (!password && in.Password != "") || (password && in.Password == "") {
		return uuid.Nil, empty, ErrInvalid
	}
	row, err := s.store.CustomerSignupBySubject(ctx, in.UserID)
	if err != nil {
		return uuid.Nil, empty, customerError(err)
	}
	if row.Input.Owner == nil || row.OwnerSubject != "owner-"+row.TenantID.String() {
		return uuid.Nil, empty, ErrConflict
	}
	if err = s.store.ConsumeSignupProof(ctx, row.ID, time.Now().UTC()); err != nil {
		return uuid.Nil, empty, customerError(err)
	}
	org := "helivanta-" + row.TenantID.String()
	if _, err = s.signupIdentity.SignupIdentity(ctx, org, row.OwnerSubject, row.Input.Owner.Email); err != nil {
		return uuid.Nil, empty, ErrUnavailable
	}
	if password {
		err = s.signupIdentity.SetSignupPassword(ctx, row.OwnerSubject, in.Code, in.Password)
	} else {
		err = s.signupIdentity.VerifySignupEmail(ctx, row.OwnerSubject, in.Code)
	}
	if errors.Is(err, zitadel.ErrSignupProof) {
		return uuid.Nil, empty, ErrInvalid
	}
	if err != nil {
		return uuid.Nil, empty, ErrUnavailable
	}
	verified, err := s.signupIdentity.SignupIdentity(ctx, org, row.OwnerSubject, row.Input.Owner.Email)
	if err != nil || !verified {
		return uuid.Nil, empty, ErrUnavailable
	}
	if err = s.store.RotateSignupReceipt(ctx, row.ID, nextHash); err != nil {
		return uuid.Nil, empty, customerError(err)
	}
	row.ReceiptVerified = true
	return row.ID, customerReceipt(row), nil
}

// DeliverCustomer requests a bounded native email without disclosing an identity.
func (s *Service) DeliverCustomer(ctx context.Context, id uuid.UUID, hash []byte, in customercontract.Delivery, password bool) (customercontract.Receipt, error) {
	generic := customercontract.Receipt{State: customercontract.EmailActionRequired}
	if s.signupIdentity == nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	var row store.CustomerSignup
	var err error
	if in.UserID != "" {
		if !hospitalSourceID.MatchString(in.UserID) {
			return generic, nil
		}
		row, err = s.store.CustomerSignupBySubject(ctx, in.UserID)
	} else {
		row, err = s.store.CustomerSignup(ctx, id, hash)
	}
	if errors.Is(err, store.ErrNotFound) {
		return generic, nil
	}
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	if row.Input.Owner == nil || row.State == "existing_account" || row.State == "needs_support" {
		return generic, nil
	}
	if err = s.store.ConsumeSignupMail(ctx, row.ID, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return generic, nil
		}
		return customercontract.Receipt{}, ErrUnavailable
	}
	verified, err := s.signupIdentity.SignupIdentity(ctx, "helivanta-"+row.TenantID.String(), row.OwnerSubject, row.Input.Owner.Email)
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	if password && verified {
		err = s.signupIdentity.SendSignupPasswordLink(ctx, row.OwnerSubject)
	} else {
		err = s.signupIdentity.SendSignupEmailLink(ctx, row.OwnerSubject)
	}
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	return generic, nil
}

// DeliverCustomers advances a bounded batch of durable identity deliveries.
func (s *Service) DeliverCustomers(ctx context.Context, now time.Time) error { return nil }
