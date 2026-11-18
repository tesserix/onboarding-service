package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestStaffMFARecoveryApplicationRoleCanRunLifecycleButCannotRewriteHistory(t *testing.T) {
	s, command := staffMFAFixture(t)
	config := s.pool.Config()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE onboarding`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	app := New(pool)
	prepared, new, err := app.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
	require.NoError(t, err)
	require.True(t, new)
	require.Positive(t, prepared.Generation)
	for _, query := range []string{
		`UPDATE staff_mfa_recovery_commands SET reason='rewritten' WHERE id=$1`,
		`DELETE FROM staff_mfa_recovery_commands WHERE id=$1`,
	} {
		_, err = pool.Exec(t.Context(), query, command.Request.Intent.CommandID)
		var denied *pgconn.PgError
		require.ErrorAs(t, err, &denied)
		require.Equal(t, "42501", denied.Code)
	}
	_, err = pool.Exec(t.Context(), `UPDATE staff_mfa_recovery_commands SET state='completed' WHERE id=$1`, command.Request.Intent.CommandID)
	var illegal *pgconn.PgError
	require.ErrorAs(t, err, &illegal)
	require.Equal(t, "23514", illegal.Code)
	_, send, err := app.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
	require.NoError(t, err)
	require.True(t, send)
	removed := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	require.NoError(t, app.RecordStaffMFARecovery(t.Context(), command, "awaiting_enrollment", "1703", removed, staffMFAEvent(command, "removed")))
	require.NoError(t, app.CompleteStaffMFARecovery(t.Context(), command, "1704", time.Now().UTC().Format(time.RFC3339Nano), staffMFAEvent(command, "complete")))
	current, err := app.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "completed", current.State)
	require.Equal(t, removed, current.NativeChangedAt)
}
