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
