package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// AppendIdentityReadAudit reauthorizes the exact registry binding after native
// I/O and commits its sensitive-read audit while the authority rows are locked.
func (s *Store) AppendIdentityReadAudit(ctx context.Context, org Organization, product Product, event audit.Event) error {
	if event.OrgID != org.ID.String() || event.ProductKey != product.Key {
		return ErrConflict
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Match enrolment's lock order; no native request holds these locks.
		currentProduct, err := scanProduct(tx.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE key=$1 FOR SHARE`, product.Key))
		if err != nil {
			return err
		}
		if currentProduct.State != "active" || currentProduct.Identity != IdentityZitadel || currentProduct.ZitadelProjectID != product.ZitadelProjectID || currentProduct.MachineUserID != product.MachineUserID {
			return ErrConflict
		}
		currentOrg, err := scanOrg(tx.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations o WHERE o.id=$1 FOR SHARE`, org.ID))
		if err != nil {
			return err
		}
		if currentOrg.State != "active" || currentOrg.ZitadelOrgID != org.ZitadelOrgID || currentOrg.CreatedByProduct != org.CreatedByProduct || currentOrg.Name != org.Name {
			return ErrConflict
		}
		var revoked *time.Time
		if err = tx.QueryRow(ctx, `SELECT revoked_at FROM org_products WHERE org_id=$1 AND product_key=$2 FOR SHARE`, org.ID, product.Key).Scan(&revoked); err != nil {
			return err
		}
		if revoked != nil {
			return ErrConflict
		}
		return appendAudit(ctx, tx, event)
	})
}
