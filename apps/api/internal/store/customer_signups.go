package store

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	var result CustomerSignup
	if in.ID == uuid.Nil || in.TenantID == uuid.Nil || in.ResourceID.Version() != 7 || in.OwnerSubject != "owner-"+in.TenantID.String() || len(in.ReceiptHash) != 32 || len(in.IntentHash) != 32 || len(in.OwnerEmailHash) != 32 || in.Input.Validate(in.ID) != nil {
		return result, ErrConflict
	}
	raw, err := json.Marshal(in.Input)
	if err != nil {
		return result, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "customer-signup:"+in.ID.String()); err != nil {
			return err
		}
		prior, err := scanCustomerSignup(tx.QueryRow(ctx, `SELECT `+customerSignupColumns+` FROM customer_signups WHERE id=$1 FOR UPDATE`, in.ID))
		if err == nil {
			if subtle.ConstantTimeCompare(prior.ReceiptHash, in.ReceiptHash) != 1 {
				return ErrNotFound
			}
			if subtle.ConstantTimeCompare(prior.IntentHash, in.IntentHash) != 1 || prior.TenantID != in.TenantID || prior.OwnerSubject != in.OwnerSubject {
				return ErrConflict
			}
			result = prior
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		product, err := scanProduct(tx.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE key='helivanta' FOR SHARE`))
		if err != nil {
			return err
		}
		if product.State != "active" || product.Identity != IdentityZitadel {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "customer-email:"+hex.EncodeToString(in.OwnerEmailHash)); err != nil {
			return err
		}
		var reserved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_signups WHERE product_key='helivanta' AND owner_email_hash=$1 AND state<>'existing_account')`, in.OwnerEmailHash).Scan(&reserved); err != nil {
			return err
		}
		state := "recorded"
		if reserved {
			state = "existing_account"
		}
		result, err = scanCustomerSignup(tx.QueryRow(ctx, `INSERT INTO customer_signups(id,product_key,tenant_id,resource_id,owner_subject,receipt_hash,intent_hash,owner_email_hash,input,state,retry_at) VALUES($1,'helivanta',$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+customerSignupColumns, in.ID, in.TenantID, in.ResourceID, in.OwnerSubject, in.ReceiptHash, in.IntentHash, in.OwnerEmailHash, raw, state, in.RetryAt))
		if err != nil {
			return err
		}
		event.ProductKey = "helivanta"
		event.OrgID = in.TenantID.String()
		event.RequestID = in.ID.String()
		event.Action = "customer.signup.requested"
		event.Detail = "{}"
		return appendAudit(ctx, tx, event)
	})
	return result, err
}

const customerSignupColumns = `id,tenant_id,resource_id,owner_subject,receipt_hash,intent_hash,owner_email_hash,input,state,attempts,retry_at,lease_until,error_code`

func scanCustomerSignup(row pgx.Row) (CustomerSignup, error) {
	var result CustomerSignup
	var raw []byte
	err := row.Scan(&result.ID, &result.TenantID, &result.ResourceID, &result.OwnerSubject, &result.ReceiptHash, &result.IntentHash, &result.OwnerEmailHash, &raw, &result.State, &result.Attempts, &result.RetryAt, &result.LeaseUntil, &result.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result.Input); err != nil {
		return CustomerSignup{}, err
	}
	return result, nil
}
