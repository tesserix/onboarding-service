package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// EnrolOrganization preserves source mappings and commits the entitlement with its audit.
func (s *Store) EnrolOrganization(ctx context.Context, org Organization, product Product, event audit.Event) (Organization, error) {
	var result Organization
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		current, err := scanProduct(tx.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE key=$1 FOR SHARE`, product.Key))
		if err != nil {
			return err
		}
		if current.State != "active" || current.Identity != IdentityZitadel || current.ZitadelProjectID != product.ZitadelProjectID || current.MachineUserID != product.MachineUserID {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO organizations(id,zitadel_org_id,name,created_by_product) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, org.ID, org.ZitadelOrgID, org.Name, product.Key); err != nil {
			return err
		}
		result, err = scanOrg(tx.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations o WHERE o.id=$1 FOR UPDATE`, org.ID))
		if errors.Is(err, ErrNotFound) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if result.ZitadelOrgID != org.ZitadelOrgID || result.State != "active" {
			return ErrConflict
		}
		insert, err := tx.Exec(ctx, `INSERT INTO org_products(org_id,product_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, org.ID, product.Key)
		if err != nil {
			return err
		}
		var revoked *time.Time
		if err := tx.QueryRow(ctx, `SELECT revoked_at FROM org_products WHERE org_id=$1 AND product_key=$2 FOR UPDATE`, org.ID, product.Key).Scan(&revoked); err != nil {
			return err
		}
		if revoked != nil {
			return ErrConflict
		}
		if insert.RowsAffected() == 0 {
			return nil
		}
		return appendAudit(ctx, tx, event)
	})
	return result, err
}
