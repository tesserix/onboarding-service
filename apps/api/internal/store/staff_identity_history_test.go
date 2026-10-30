package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStaffIdentityHistoryAuditFailureRollsBackCursorAndPage(t *testing.T) {
	s, c, event := staffIdentityFixture(t)
	if _, err := s.PrepareStaffIdentity(t.Context(), c, event); err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.read"
	if err := s.AppendAudit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ActorSubject: c.ActorSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	event.Action = "staff.identity.history"
	event.Outcome = "INVALID"
	page, err := s.ReadStaffIdentityHistory(t.Context(), scope, "", strings.Repeat("c", 64), 1, event)
	if err == nil || page.Items != nil || page.HasMore || !page.CursorExpiresAt.IsZero() {
		t.Fatal("failed disclosure audit released page/cursor state")
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_history_cursors WHERE command_id=$1`, c.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed audit retained continuation: count=%d err=%v", count, err)
	}
}

func TestStaffIdentityHistoryCursorCleanupIsBoundedAndSkipsLockedRows(t *testing.T) {
	s, c, event := staffIdentityFixture(t)
	if _, err := s.PrepareStaffIdentity(t.Context(), c, event); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO staff_identity_history_cursors(token_hash,command_id,scope_hash,before_seq,created_at,expires_at)
 SELECT lpad(to_hex(i),64,'0'),$1,repeat('a',64),1,now()-interval '2 hours',now()-interval '1 hour'
 FROM generate_series(1,1105) i`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO staff_identity_history_cursors(token_hash,command_id,scope_hash,before_seq,expires_at)
 VALUES(repeat('b',64),$1,repeat('a',64),1,now()+interval '15 minutes')`, c.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PurgeExpiredStaffIdentityHistoryCursors(t.Context())
	if err != nil || removed != 1000 {
		t.Fatalf("cleanup not bounded: removed=%d err=%v", removed, err)
	}
	lock, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `SELECT token_hash FROM staff_identity_history_cursors WHERE expires_at<=now() ORDER BY token_hash LIMIT 1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	removed, err = s.PurgeExpiredStaffIdentityHistoryCursors(ctx)
	if err != nil || removed != 104 {
		t.Fatalf("cleanup did not skip locked expired row: removed=%d err=%v", removed, err)
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.PurgeExpiredStaffIdentityHistoryCursors(t.Context()); err != nil || removed != 1 {
		t.Fatalf("unlocked expired cursor not removed: count=%d err=%v", removed, err)
	}
	var active, commands int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_history_cursors WHERE expires_at>now()`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_commands WHERE id=$1`, c.ID).Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if active != 1 || commands != 1 {
		t.Fatal("cleanup removed active cursor or identity command")
	}
}

func TestStaffIdentityHistoryCursorDatabaseRoleCannotDeleteActiveOrRewriteState(t *testing.T) {
	s, c, event := staffIdentityFixture(t)
	if _, err := s.PrepareStaffIdentity(t.Context(), c, event); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `INSERT INTO staff_identity_history_cursors(token_hash,command_id,scope_hash,before_seq,expires_at)
 VALUES(repeat('a',64),$1,repeat('b',64),1,now()+interval '15 minutes')`, c.ID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`UPDATE staff_identity_history_cursors SET before_seq=before_seq`, `UPDATE staff_identity_history_cursors SET token_hash=token_hash`, `TRUNCATE staff_identity_history_cursors`} {
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `SET LOCAL ROLE onboarding`); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), statement)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Errorf("application rewrote cursor cache: %v", err)
		}
		if err := tx.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SET LOCAL ROLE onboarding`); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(t.Context(), `DELETE FROM staff_identity_history_cursors`)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("application deleted active cursor despite expiry policy: %v", err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO staff_identity_history_cursors(token_hash,command_id,scope_hash,before_seq,created_at,expires_at)
 VALUES(repeat('c',64),$1,repeat('b',64),1,now()-interval '2 hours',now()-interval '1 hour')`, c.ID); err != nil {
		t.Fatal(err)
	}
	tag, err = tx.Exec(t.Context(), `DELETE FROM staff_identity_history_cursors`)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("application could not delete exactly expired cache state: rows=%d err=%v", tag.RowsAffected(), err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var enabled, forced bool
	if err := s.pool.QueryRow(t.Context(), `SELECT relrowsecurity,relforcerowsecurity FROM pg_class WHERE oid='staff_identity_history_cursors'::regclass`).Scan(&enabled, &forced); err != nil || !enabled || !forced {
		t.Fatal("cursor expiry policy is not enforced")
	}
}

func restrictedStaffHistoryStore(t *testing.T) *Store {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("ONBOARDING_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE onboarding`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

func TestStaffIdentityHistoryContinuationAndCleanupWorkAsApplicationRole(t *testing.T) {
	s, c, event := staffIdentityFixture(t)
	if _, err := s.PrepareStaffIdentity(t.Context(), c, event); err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.read"
	if err := s.AppendAudit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	scope := StaffIdentityScope{CommandID: c.ID, OrgID: c.OrgID, ProductKey: c.ProductKey, MachineSubject: c.MachineSubject, ActorSubject: c.ActorSubject, ProviderOrgID: c.ProviderOrgID, ProviderProjectID: c.ProviderProjectID}
	limited := restrictedStaffHistoryStore(t)
	event.Action = "staff.identity.history"
	page, err := limited.ReadStaffIdentityHistory(t.Context(), scope, "", strings.Repeat("c", 64), 1, event)
	if err != nil || !page.HasMore {
		t.Fatalf("application-role first page failed: %v", err)
	}
	page, err = limited.ReadStaffIdentityHistory(t.Context(), scope, strings.Repeat("c", 64), strings.Repeat("d", 64), 1, event)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("application-role continuation failed: %v", err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE staff_identity_history_cursors SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE command_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if removed, err := limited.PurgeExpiredStaffIdentityHistoryCursors(t.Context()); err != nil || removed != 1 {
		t.Fatalf("application-role expired cursor cleanup failed: removed=%d err=%v", removed, err)
	}
}
