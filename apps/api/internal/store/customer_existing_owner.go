package store

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// CustomerSignupForExistingOwner resolves a receipt or an already bound verified owner.
func (s *Store) CustomerSignupForExistingOwner(ctx context.Context, id uuid.UUID, hash []byte, subject string) (CustomerSignup, error) {
	row, err := s.customerSignupByID(ctx, id)
	if err != nil {
		return CustomerSignup{}, err
	}
	if !existingOwnerReceipt(row, hash, subject) {
		return CustomerSignup{}, ErrNotFound
	}
	return row, nil
}

func existingOwnerReceipt(row CustomerSignup, hash []byte, subject string) bool {
	return row.ReceiptActive && ((len(hash) == 32 && subtle.ConstantTimeCompare(row.ReceiptHash, hash) == 1) || (row.ExistingOwnerSubject != "" && row.ExistingOwnerSubject == subject))
}

// ConfirmExistingOwner records native proof and rotates the receipt in one audited transaction.
func (s *Store) ConfirmExistingOwner(ctx context.Context, id uuid.UUID, hash []byte, in customercontract.ExistingOwnerInput) (CustomerSignup, error) {
	var result CustomerSignup
	next, err := hex.DecodeString(in.NextReceiptHash)
	if in.Validate(time.Now().UTC()) != nil || err != nil || len(next) != 32 || len(hash) != 32 || subtle.ConstantTimeCompare(next, hash) == 1 {
		return result, ErrConflict
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row, err := scanCustomerSignup(tx.QueryRow(ctx, `SELECT `+customerSignupColumns+` FROM customer_signups WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if !existingOwnerReceipt(row, hash, in.Subject) {
			return ErrNotFound
		}
		if row.ExistingOwnerSubject == "" {
			if row.State != "existing_account" {
				return ErrConflict
			}
			if _, err = tx.Exec(ctx, `SELECT set_config('app.customer_signup_existing_owner',$1,true)`, in.Subject); err != nil {
				return err
			}
			result, err = scanCustomerSignup(tx.QueryRow(ctx, `UPDATE customer_signups SET existing_owner_subject=$2,existing_owner_auth_time=to_timestamp($3),
    state='recorded',receipt_hash=$4,receipt_verified=true,primary_credential_configured=true,
    receipt_expires_at=now()+interval '7 days',retry_at=now(),updated_at=now()
    WHERE id=$1 RETURNING `+customerSignupColumns, id, in.Subject, in.AuthTime, next))
		} else {
			if row.ExistingOwnerSubject != in.Subject || row.State == "needs_support" {
				return ErrConflict
			}
			result, err = scanCustomerSignup(tx.QueryRow(ctx, `UPDATE customer_signups SET receipt_hash=$2,receipt_expires_at=now()+interval '7 days',updated_at=now()
    WHERE id=$1 RETURNING `+customerSignupColumns, id, next))
		}
		if err != nil {
			return err
		}
		action := "customer.signup.existing_owner.confirmed"
		if row.ExistingOwnerSubject != "" {
			action = "customer.signup.existing_owner.receipt_rotated"
		}
		return appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: in.Subject, ProductKey: "helivanta", OrgID: row.TenantID.String(), Action: action, Outcome: audit.OutcomeSuccess, RequestID: row.ID.String(), Detail: "{}"})
	})
	return result, err
}
