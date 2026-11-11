package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"regexp"
)

var providerMessageID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// AcceptCustomerEmail records only the current worker's first provider acceptance.
func (s *Store) AcceptCustomerEmail(ctx context.Context, row CustomerSignup, providerID string) error {
	if row.LeaseID == nil || !providerMessageID.MatchString(providerID) {
		return ErrConflict
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE customer_signups SET email_accepted_at=now(),email_provider_id=$3,updated_at=now()
WHERE id=$1 AND lease_id=$2 AND lease_until>now() AND state='identity_requested' AND existing_owner_subject='' AND email_accepted_at IS NULL`, row.ID, *row.LeaseID, providerID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		return appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: row.OwnerSubject, ProductKey: "helivanta", OrgID: row.TenantID.String(), Action: "customer.signup.email_accepted", Outcome: audit.OutcomeSuccess, RequestID: row.ID.String(), Target: providerID, Detail: "{}"})
	})
}
