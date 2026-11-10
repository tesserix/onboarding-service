package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"time"
)

// CustomerSignup is an immutable submitted command and its mutable delivery state.
type CustomerSignup struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	ResourceID     uuid.UUID
	OwnerSubject   string
	ReceiptHash    []byte
	IntentHash     []byte
	OwnerEmailHash []byte
	Input          customercontract.Signup
	State          string
	Attempts       int
	RetryAt        time.Time
	LeaseUntil     *time.Time
	ErrorCode      string
}

// ReserveCustomerSignup persists intent and its audit before remote provisioning.
func (s *Store) ReserveCustomerSignup(ctx context.Context, in CustomerSignup, event audit.Event) (CustomerSignup, error) {
	return CustomerSignup{}, ErrNotFound
}
