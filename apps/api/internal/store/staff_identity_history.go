package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

var ErrStaffHistoryCursor = errors.New("invalid staff history cursor")

type StaffIdentityHistoryItem struct {
	Action     string        `json:"action"`
	Outcome    audit.Outcome `json:"outcome"`
	OccurredAt time.Time     `json:"occurred_at"`
}

type StaffIdentityHistoryPage struct {
	Items           []StaffIdentityHistoryItem
	HasMore         bool
	CursorExpiresAt time.Time
}

func (s *Store) ReadStaffIdentityHistory(ctx context.Context, scope StaffIdentityScope, cursorHash, nextHash string, limit int, event audit.Event) (StaffIdentityHistoryPage, error) {
	empty := StaffIdentityHistoryPage{}
	if limit < 1 || limit > 100 || !staffHistoryHash(nextHash) || (cursorHash != "" && !staffHistoryHash(cursorHash)) {
		return empty, ErrStaffHistoryCursor
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		return empty, err
	}
	digest := sha256.Sum256(raw)
	scopeHash := hex.EncodeToString(digest[:])
	var result StaffIdentityHistoryPage
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := readScopedStaffIdentityCommand(ctx, tx, scope); err != nil {
			return err
		}
		var before int64
		var expiry time.Time
		if cursorHash != "" {
			err := tx.QueryRow(ctx, `SELECT before_seq,expires_at FROM staff_identity_history_cursors
 WHERE token_hash=$1 AND command_id=$2 AND scope_hash=$3 AND expires_at>now() FOR SHARE`, cursorHash, scope.CommandID, scopeHash).Scan(&before, &expiry)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrStaffHistoryCursor
			}
			if err != nil {
				return err
			}
		}
		query := `SELECT seq,action,outcome,occurred_at FROM audit_events
 WHERE org_id=$1 AND product_key=$2 AND actor_type='product' AND actor_id=$3 AND target=$4
 AND action LIKE 'staff.identity.%'`
		args := []any{scope.OrgID, scope.ProductKey, scope.MachineSubject, scope.CommandID.String()}
		if before > 0 {
			query += " AND seq<$5"
			args = append(args, before)
		}
		args = append(args, limit+1)
		query += fmt.Sprintf(" ORDER BY seq DESC LIMIT $%d", len(args))
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		type historyRow struct {
			sequence int64
			item     StaffIdentityHistoryItem
		}
		records, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (historyRow, error) {
			var result historyRow
			err := row.Scan(&result.sequence, &result.item.Action, &result.item.Outcome, &result.item.OccurredAt)
			result.item.OccurredAt = result.item.OccurredAt.UTC()
			return result, err
		})
		if err != nil {
			return err
		}
		result.HasMore = len(records) > limit
		if result.HasMore {
			records = records[:limit]
		}
		result.Items = make([]StaffIdentityHistoryItem, 0, len(records))
		for _, record := range records {
			result.Items = append(result.Items, record.item)
		}
		if result.HasMore {
			position := records[len(records)-1].sequence
			if expiry.IsZero() {
				err = tx.QueryRow(ctx, `INSERT INTO staff_identity_history_cursors(token_hash,command_id,scope_hash,before_seq,expires_at)
 VALUES($1,$2,$3,$4,now()+interval '15 minutes') RETURNING expires_at`, nextHash, scope.CommandID, scopeHash, position).Scan(&result.CursorExpiresAt)
			} else {
				err = tx.QueryRow(ctx, `INSERT INTO staff_identity_history_cursors(token_hash,command_id,scope_hash,before_seq,expires_at)
 VALUES($1,$2,$3,$4,$5) RETURNING expires_at`, nextHash, scope.CommandID, scopeHash, position, expiry).Scan(&result.CursorExpiresAt)
			}
			if err != nil {
				return err
			}
			result.CursorExpiresAt = result.CursorExpiresAt.UTC()
		}
		return appendAudit(ctx, tx, event)
	})
	if err != nil {
		return empty, err
	}
	return result, nil
}

func staffHistoryHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) PurgeExpiredStaffIdentityHistoryCursors(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `WITH expired AS (
 SELECT token_hash FROM staff_identity_history_cursors WHERE expires_at<=now()
 ORDER BY expires_at,token_hash LIMIT 1000 FOR UPDATE SKIP LOCKED
 ) DELETE FROM staff_identity_history_cursors c USING expired e WHERE c.token_hash=e.token_hash`)
	return tag.RowsAffected(), err
}
