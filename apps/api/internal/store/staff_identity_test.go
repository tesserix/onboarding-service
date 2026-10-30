package store

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func staffIdentityFixture(t *testing.T) (*Store, StaffIdentityCommand, audit.Event) {
	t.Helper()
	s := newStore(t)
	p := seedProduct(t, s, "hms")
	o := seedOrg(t, s, "staff-identity", p.Key)
	ctx := context.Background()
	if err := s.Grant(ctx, o.ID, p.Key, nil); err != nil {
		t.Fatal(err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"staff_identity_creations", "staff_identity_dispatches", "staff_identity_commands"} {
			column := "command_id"
			if table == "staff_identity_commands" {
				column = "id"
			}
			if _, err := s.pool.Exec(context.Background(), "DELETE FROM "+table+" WHERE "+column+"=$1", id); err != nil {
				t.Errorf("remove disposable identity fixture: %v", err)
			}
		}
	})
	command := StaffIdentityCommand{
		ID: id, OrgID: o.ID, ProductKey: p.Key, MachineSubject: p.MachineUserID,
		ActorSubject: "TEST-admin", ProviderOrgID: o.ZitadelOrgID, ProviderProjectID: p.ZitadelProjectID,
		ProviderUserID: "TEST-prepared-user", BindingNonce: "TEST-binding-nonce",
		Email: "synthetic@example.invalid", GivenName: "Synthetic", FamilyName: "Staff",
		Reason: "Authorized synthetic Staff account preparation",
	}
	event := audit.Event{ActorType: "product", ActorID: p.MachineUserID, ProductKey: p.Key, OrgID: o.ID.String(), Action: "staff.identity.prepare", Target: id.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	return s, command, event
}

func TestStaffIdentityDispatchRejectsChangedFactsAndRevokedScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *Store, *StaffIdentityCommand)
		want   error
	}{
		{"actor changed", func(_ *testing.T, _ *Store, c *StaffIdentityCommand) { c.ActorSubject = "TEST-other-admin" }, ErrConflict},
		{"provider target changed", func(_ *testing.T, _ *Store, c *StaffIdentityCommand) { c.ProviderUserID = "TEST-other-user" }, ErrConflict},
		{"contact changed", func(_ *testing.T, _ *Store, c *StaffIdentityCommand) { c.Email = "other@example.invalid" }, ErrConflict},
		{"correlation changed", func(_ *testing.T, _ *Store, c *StaffIdentityCommand) { c.BindingNonce = "TEST-other-binding-nonce" }, ErrConflict},
		{"foreign tenant", func(_ *testing.T, _ *Store, c *StaffIdentityCommand) { c.OrgID = uuid.New() }, ErrNotFound},
		{"foreign machine", func(_ *testing.T, _ *Store, c *StaffIdentityCommand) { c.MachineSubject = "TEST-other-machine" }, ErrNotFound},
		{"grant withdrawn", func(t *testing.T, s *Store, c *StaffIdentityCommand) {
			if err := s.RevokeGrant(context.Background(), c.OrgID, c.ProductKey); err != nil {
				t.Fatal(err)
			}
		}, ErrNotFound},
		{"source mapping changed", func(t *testing.T, s *Store, c *StaffIdentityCommand) {
			if _, err := s.pool.Exec(context.Background(), `UPDATE organizations SET zitadel_org_id='TEST-new-org' WHERE id=$1`, c.OrgID); err != nil {
				t.Fatal(err)
			}
		}, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, command, event := staffIdentityFixture(t)
			ctx := context.Background()
			prepared, err := s.PrepareStaffIdentity(ctx, command, event)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, s, &prepared)
			event.Action = "staff.identity.dispatch"
			claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, event)
			if claimed || !errors.Is(err, tc.want) {
				t.Fatalf("invalid dispatch: claimed=%t err=%v", claimed, err)
			}
			var count int
			if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM staff_identity_dispatches WHERE command_id=$1`, command.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("denied claim retained dispatch: count=%d err=%v", count, err)
			}
		})
	}
}

func TestStaffIdentityLedgerCannotBeRewrittenByApplicationRole(t *testing.T) {
	s, command, event := staffIdentityFixture(t)
	ctx := context.Background()
	prepared, err := s.PrepareStaffIdentity(ctx, command, event)
	if err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.dispatch"
	if claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, event); err != nil || !claimed {
		t.Fatalf("dispatch: %v", err)
	}
	event.Action = "staff.identity.created"
	if _, err := s.RecordStaffIdentityCreation(ctx, prepared, time.Now().UTC(), event); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"staff_identity_commands", "staff_identity_dispatches", "staff_identity_creations"} {
		for _, command := range []string{"UPDATE " + table + " SET created_at=now()", "DELETE FROM " + table, "TRUNCATE " + table} {
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE onboarding"); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, command)
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.Code != "42501" {
				t.Errorf("application rewrote %s: %v", table, err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if fmt.Sprint(prepared) != "[redacted staff identity command]" {
		t.Fatal("private command input can escape in logs")
	}
}

func TestStaffIdentityDispatchAuditFailureRollsBackClaim(t *testing.T) {
	s, command, event := staffIdentityFixture(t)
	ctx := context.Background()
	prepared, err := s.PrepareStaffIdentity(ctx, command, event)
	if err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.dispatch"
	invalid := event
	invalid.Outcome = "TEST-invalid-outcome"
	if claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, invalid); err == nil || claimed {
		t.Fatal("failed audit committed the creation dispatch")
	}
	if claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, event); err != nil || !claimed {
		t.Fatalf("rolled-back dispatch was not safely claimable: claimed=%t err=%v", claimed, err)
	}
}

func TestStaffIdentityDispatchRechecksEntitlementAfterDatabaseLockWait(t *testing.T) {
	s, command, event := staffIdentityFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prepared, err := s.PrepareStaffIdentity(ctx, command, event)
	if err != nil {
		t.Fatal(err)
	}
	locker, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Release()
	lock, err := locker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(ctx, `SELECT key FROM products WHERE key=$1 FOR UPDATE`, command.ProductKey); err != nil {
		t.Fatal(err)
	}
	type result struct {
		claimed bool
		err     error
	}
	finished := make(chan result, 1)
	event.Action = "staff.identity.dispatch"
	go func() {
		claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, event)
		finished <- result{claimed, err}
	}()
	for {
		var waiting bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (
 SELECT FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, locker.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatalf("observe actual dispatch lock wait: %v", err)
		}
		if waiting {
			break
		}
		runtime.Gosched()
	}
	if err := s.RevokeGrant(ctx, command.OrgID, command.ProductKey); err != nil {
		t.Fatal(err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-finished:
		if got.claimed || !errors.Is(got.err, ErrNotFound) {
			t.Fatalf("withdrawn entitlement obtained dispatch: claimed=%t err=%v", got.claimed, got.err)
		}
	case <-ctx.Done():
		t.Fatal("dispatch did not finish after releasing its blocker")
	}
	var claims int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM staff_identity_dispatches WHERE command_id=$1`, command.ID).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("withdrawn entitlement retained a claim: count=%d err=%v", claims, err)
	}
}

func TestStaffIdentityReceiptRequiresDispatchAndAtomicAudit(t *testing.T) {
	s, command, event := staffIdentityFixture(t)
	ctx := context.Background()
	invalid := event
	invalid.Outcome = "TEST-invalid-outcome"
	if _, err := s.PrepareStaffIdentity(ctx, command, invalid); err == nil {
		t.Fatal("preparation succeeded without audit")
	}
	var commands int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM staff_identity_commands WHERE id=$1`, command.ID).Scan(&commands); err != nil || commands != 0 {
		t.Fatalf("preparation audit failure retained input: count=%d err=%v", commands, err)
	}
	prepared, err := s.PrepareStaffIdentity(ctx, command, event)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 30, 10, 0, 0, 123456789, time.UTC)
	event.Action = "staff.identity.created"
	if _, err := s.RecordStaffIdentityCreation(ctx, prepared, created, event); !errors.Is(err, ErrConflict) {
		t.Fatalf("undispatched account obtained receipt: %v", err)
	}
	dispatch := event
	dispatch.Action = "staff.identity.dispatch"
	if claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, dispatch); err != nil || !claimed {
		t.Fatalf("dispatch: %v", err)
	}
	forged := prepared
	forged.ProviderUserID = "TEST-other-provider-user"
	if _, err := s.RecordStaffIdentityCreation(ctx, forged, created, event); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed provider account obtained receipt: %v", err)
	}
	invalid = event
	invalid.Outcome = "TEST-invalid-outcome"
	if _, err := s.RecordStaffIdentityCreation(ctx, prepared, created, invalid); err == nil {
		t.Fatal("creation receipt succeeded without audit")
	}
	var receipts int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM staff_identity_creations WHERE command_id=$1`, command.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("receipt audit failure retained success: count=%d err=%v", receipts, err)
	}
	if err := s.RevokeGrant(ctx, command.OrgID, command.ProductKey); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecordStaffIdentityCreation(ctx, prepared, created, event)
	if err != nil || got.CommandID != prepared.ID {
		t.Fatalf("provider outcome was lost after entitlement withdrawal: %v", err)
	}
	if claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, dispatch); claimed || !errors.Is(err, ErrNotFound) {
		t.Fatalf("creation receipt restored withdrawn dispatch authority: %v", err)
	}
}

func TestStaffIdentityCreationReceiptReplaysWithoutTimestampPrecisionLoss(t *testing.T) {
	s, command, event := staffIdentityFixture(t)
	ctx := context.Background()
	prepared, err := s.PrepareStaffIdentity(ctx, command, event)
	if err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.dispatch"
	if claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, event); err != nil || !claimed {
		t.Fatalf("dispatch claim: claimed=%t err=%v", claimed, err)
	}
	event.Action = "staff.identity.created"
	created := time.Date(2026, 10, 30, 10, 0, 0, 123456789, time.UTC)
	got, err := s.RecordStaffIdentityCreation(ctx, prepared, created, event)
	if err != nil || got.CommandID != command.ID || got.ProviderUserID != command.ProviderUserID || !got.ProviderCreatedAt.Equal(created) || got.RecordedAt.IsZero() {
		t.Fatalf("creation receipt did not retain exact source facts: err=%v", err)
	}
	replayed, err := s.RecordStaffIdentityCreation(ctx, prepared, created, event)
	if err != nil || !replayed.RecordedAt.Equal(got.RecordedAt) || !replayed.ProviderCreatedAt.Equal(created) {
		t.Fatalf("exact receipt replay changed source facts: %v", err)
	}
	if _, err := s.RecordStaffIdentityCreation(ctx, prepared, created.Add(time.Nanosecond), event); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed creation time did not conflict: %v", err)
	}
	records, err := s.AuditSearch(ctx, AuditFilter{RequestID: command.ID.String(), Action: event.Action}, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("creation audit is not exactly once: count=%d err=%v", len(records), err)
	}
}

func TestStaffIdentityPreparationRetainsImmutableFactsAndExactReplay(t *testing.T) {
	s, command, event := staffIdentityFixture(t)
	ctx := context.Background()
	id := command.ID
	got, err := s.PrepareStaffIdentity(ctx, command, event)
	if err != nil || got.ID != id || got.Email != command.Email || got.ProviderUserID != command.ProviderUserID || got.CreatedAt.IsZero() {
		t.Fatalf("preparation was not retained: id=%s err=%v", got.ID, err)
	}
	retry := command
	retry.ProviderUserID = "TEST-unused-retry-candidate"
	retry.BindingNonce = "TEST-unused-retry-nonce"
	replayed, err := s.PrepareStaffIdentity(ctx, retry, event)
	if err != nil || replayed.ProviderUserID != got.ProviderUserID || replayed.BindingNonce != got.BindingNonce || !replayed.CreatedAt.Equal(got.CreatedAt) {
		t.Fatalf("exact retry replaced immutable creation facts: %v", err)
	}
	changed := command
	changed.Email = "changed@example.invalid"
	if _, err := s.PrepareStaffIdentity(ctx, changed, event); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed input did not conflict: %v", err)
	}
	records, err := s.AuditSearch(ctx, AuditFilter{RequestID: id.String()}, 10)
	if err != nil || len(records) != 1 || records[0].Action != "staff.identity.prepare" {
		t.Fatalf("preparation audit is not exactly once: count=%d err=%v", len(records), err)
	}
}

func TestStaffIdentityDispatchIsClaimedOnceAcrossConcurrentRequests(t *testing.T) {
	s, command, event := staffIdentityFixture(t)
	ctx := context.Background()
	id := command.ID
	prepared, err := s.PrepareStaffIdentity(ctx, command, event)
	if err != nil {
		t.Fatal(err)
	}
	event.Action = "staff.identity.dispatch"
	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			<-start
			claimed, err := s.ClaimStaffIdentityDispatch(ctx, prepared, event)
			if err != nil {
				t.Errorf("dispatch claim: %v", err)
			}
			if claimed {
				winners.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("creation could dispatch %d times", winners.Load())
	}
	records, err := s.AuditSearch(ctx, AuditFilter{RequestID: id.String(), Action: event.Action}, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("dispatch audit is not exactly once: count=%d err=%v", len(records), err)
	}
}
