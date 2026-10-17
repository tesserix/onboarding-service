package onboarding

import (
	"context"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

// HospitalTenants returns a bounded page scoped to the verified product principal.
func (s *Service) HospitalTenants(ctx context.Context, p authz.Principal, after string, limit int) ([]store.Organization, error) {
	if _, err := s.hospitalProduct(ctx, p, p.ProductKey, false); err != nil {
		return nil, err
	}
	cursor := uuid.Nil
	if after != "" {
		parsed, err := uuid.Parse(after)
		if err != nil || parsed == uuid.Nil || parsed.String() != after {
			return nil, ErrInvalid
		}
		cursor = parsed
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return s.store.HospitalTenants(ctx, p.ProductKey, cursor, limit)
}
