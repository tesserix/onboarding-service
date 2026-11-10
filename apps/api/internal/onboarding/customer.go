package onboarding

import (
	"context"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

// StartCustomer durably records server-bound signup intent before identity delivery.
func (s *Service) StartCustomer(ctx context.Context, id uuid.UUID, receiptHash []byte, in customercontract.Signup) (customercontract.Receipt, error) {
	return customercontract.Receipt{}, ErrUnavailable
}
