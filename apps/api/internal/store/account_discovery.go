package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"time"
)

var ErrDiscoveryLimited = errors.New("discovery delivery limit")

// ReserveDiscovery serializes delivery budgets across replicas before provider I/O.
func (s *Store) ReserveDiscovery(ctx context.Context, id uuid.UUID, browser, source []byte, email string, code []byte, now time.Time) error {
	if id.Version() != 7 || len(browser) != 32 || len(source) != 32 || len(code) != 32 || email == "" {
		return ErrConflict
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(8021985)`); err != nil {
			return err
		}
		var global, recipientHour, recipientDay, sourceHour, sourceDay int
		err := tx.QueryRow(ctx, `SELECT
   (SELECT count(*) FROM (SELECT 1 FROM account_discovery_challenges WHERE created_at>$1::timestamptz-interval '1 hour' LIMIT 1000) b),
   count(*) FILTER (WHERE email=$2 AND created_at>$1::timestamptz-interval '1 hour'),
   count(*) FILTER (WHERE email=$2),
   count(*) FILTER (WHERE source_hash=$3 AND created_at>$1::timestamptz-interval '1 hour'),
   count(*) FILTER (WHERE source_hash=$3)
   FROM account_discovery_challenges WHERE created_at>$1::timestamptz-interval '24 hours' AND (email=$2 OR source_hash=$3)`, now, email, source).Scan(&global, &recipientHour, &recipientDay, &sourceHour, &sourceDay)
		if err != nil {
			return err
		}
		if global >= 1000 || recipientHour >= 3 || recipientDay >= 10 || sourceHour >= 10 || sourceDay >= 30 {
			return ErrDiscoveryLimited
		}
		if _, err = tx.Exec(ctx, `INSERT INTO account_discovery_challenges(id,browser_hash,source_hash,email,code_hash,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$6,$6::timestamptz+interval '10 minutes')`, id, browser, source, email, code, now); err != nil {
			return err
		}
		return appendAudit(ctx, tx, audit.Event{ActorType: audit.ActorSystem, ActorID: "helivanta-api", ProductKey: "helivanta", Action: "account.discovery.requested", Outcome: audit.OutcomeSuccess, RequestID: id.String()})
	})
}

// VerifyDiscovery consumes an attempt atomically; a successful proof cannot replay.
func (s *Store) VerifyDiscovery(ctx context.Context, id uuid.UUID, browser, code []byte, now time.Time) error {
	var verified bool
	err := s.pool.QueryRow(ctx, `UPDATE account_discovery_challenges SET attempts=attempts+1,updated_at=$4,verified_at=CASE WHEN code_hash=$3 THEN $4 ELSE NULL END WHERE id=$1 AND browser_hash=$2 AND verified_at IS NULL AND attempts<5 AND expires_at>$4 RETURNING verified_at IS NOT NULL`, id, browser, code, now).Scan(&verified)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !verified {
		return ErrNotFound
	}
	return err
}

// DiscoveryEmail permits list recovery only for the browser's verified, unexpired proof.
func (s *Store) DiscoveryEmail(ctx context.Context, id uuid.UUID, browser []byte, now time.Time) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx, `SELECT email FROM account_discovery_challenges WHERE id=$1 AND browser_hash=$2 AND verified_at IS NOT NULL AND expires_at>$3`, id, browser, now).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return email, err
}

// PurgeDiscovery removes expired personal data in bounded batches, preserving audit history.
func (s *Store) PurgeDiscovery(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM account_discovery_challenges WHERE id IN (SELECT id FROM account_discovery_challenges WHERE created_at<clock_timestamp()-interval '24 hours' ORDER BY created_at LIMIT 5000)`)
	return err
}
