package store

import (
	"context"
	"fmt"
	"github.com/google/uuid"
)

// HospitalTenants pages live product entitlements without exposing private contact fields.
func (s *Store) HospitalTenants(ctx context.Context, product string, after uuid.UUID, limit int) ([]Organization, error) {
	if limit < 1 || limit > 101 {
		return nil, fmt.Errorf("invalid page size")
	}
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.name, o.state FROM organizations o
	JOIN org_products g ON g.org_id=o.id
	WHERE g.product_key=$1 AND g.revoked_at IS NULL AND o.id>$2
	ORDER BY o.id LIMIT $3`, product, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list hospital tenants: %w", err)
	}
	defer rows.Close()
	result := []Organization{}
	for rows.Next() {
		var org Organization
		if err = rows.Scan(&org.ID, &org.Name, &org.State); err != nil {
			return nil, err
		}
		result = append(result, org)
	}
	return result, rows.Err()
}
