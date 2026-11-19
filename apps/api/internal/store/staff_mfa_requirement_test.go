package store

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

func TestStaffMFARequirementFollowsAccountAcrossTenantContexts(t *testing.T) {
	s, command := staffMFAFixture(t)
	read := func(state, generation string) {
		t.Helper()
		out, err := s.StaffMFARecoveryRequirement(t.Context(), command.Request.Target.Subject)
		require.NoError(t, err)
		require.Equal(t, customercontract.StaffMFARecoveryRequirement{State: state, GenerationID: generation}, out)
	}
	read("none", "")
	_, _, err := s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
	require.NoError(t, err)
	read("blocked", command.Request.Intent.CommandID)
	otherTenant := uuid.New()
	_, err = s.CreateOrganization(t.Context(), Organization{ID: otherTenant, ZitadelOrgID: "helivanta-" + otherTenant.String(), Name: "Synthetic legacy membership", CreatedByProduct: "helivanta"})
	require.NoError(t, err)
	require.NoError(t, s.Grant(t.Context(), otherTenant, "helivanta", nil))
	other := command
	other.Request.Target.TenantID = otherTenant.String()
	other.ProviderOrgID = "helivanta-" + otherTenant.String()
	other.Request.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
	_, _, err = s.PrepareStaffMFARecovery(t.Context(), other, staffMFAEvent(other, "prepare"))
	require.ErrorIs(t, err, ErrConflict, "one physical account cannot have concurrent tenant recovery commands")
	_, err = s.pool.Exec(t.Context(), `INSERT INTO staff_mfa_recovery_commands(id,org_id,product_key,machine_subject,actor_subject,target_subject,provider_org_id,provider_project_id,
 employer_kind,employer_id,staff_id,staff_version,link_id,link_version,reason,state)
 SELECT $1,$2,product_key,machine_subject,actor_subject,target_subject,$3,provider_project_id,
 employer_kind,employer_id,staff_id,staff_version,link_id,link_version,reason,'prepared' FROM staff_mfa_recovery_commands WHERE id=$4`,
		other.Request.Intent.CommandID, otherTenant, other.ProviderOrgID, command.Request.Intent.CommandID)
	var conflict *pgconn.PgError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "23505", conflict.Code)
	require.Equal(t, "staff_mfa_recovery_subject_fence", conflict.ConstraintName)
	_, _, err = s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
	require.NoError(t, err)
	read("blocked", command.Request.Intent.CommandID)
	removed := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	require.NoError(t, s.RecordStaffMFARecovery(t.Context(), command, "awaiting_enrollment", "1703", removed, staffMFAEvent(command, "removed")))
	read("enrollment_required", command.Request.Intent.CommandID)
	require.NoError(t, s.CompleteStaffMFARecovery(t.Context(), command, "1704", removed, staffMFAEvent(command, "complete")))
	read("completed", command.Request.Intent.CommandID)
	_, _, err = s.PrepareStaffMFARecovery(t.Context(), other, staffMFAEvent(other, "prepare"))
	require.NoError(t, err)
	read("blocked", other.Request.Intent.CommandID)
	require.ErrorIs(t, s.CompleteStaffMFARecovery(t.Context(), command, "1704", removed, staffMFAEvent(command, "complete")), ErrConflict)
	_, err = s.CancelStaffMFARecovery(t.Context(), other, staffMFAEvent(other, "cancel"))
	require.NoError(t, err)
	read("completed", command.Request.Intent.CommandID)
	separate, err := s.StaffMFARecoveryRequirement(t.Context(), "unrelated-account")
	require.NoError(t, err)
	require.Equal(t, customercontract.StaffMFARecoveryRequirement{State: "none"}, separate)
}

func TestStaffMFARecoveryConcurrentTenantCommandsRetainOneSubjectFence(t *testing.T) {
	s, first := staffMFAFixture(t)
	tenant := uuid.New()
	_, err := s.CreateOrganization(t.Context(), Organization{ID: tenant, ZitadelOrgID: "helivanta-" + tenant.String(), Name: "Synthetic second legacy membership", CreatedByProduct: "helivanta"})
	require.NoError(t, err)
	require.NoError(t, s.Grant(t.Context(), tenant, "helivanta", nil))
	second := first
	second.Request.Target.TenantID = tenant.String()
	second.ProviderOrgID = "helivanta-" + tenant.String()
	second.Request.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, command := range []StaffMFARecoveryCommand{first, second} {
		wg.Go(func() {
			<-start
			_, created, err := s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
			if err == nil && !created {
				t.Error("new tenant command was silently replayed")
			}
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
}

func TestStaffMFARequirementRejectsInvalidSubjectBeforeIO(t *testing.T) {
	var s Store
	for _, subject := range []string{"", "../foreign", "staff:foreign"} {
		out, err := s.StaffMFARecoveryRequirement(t.Context(), subject)
		require.Error(t, err)
		require.Zero(t, out)
	}
}

func TestStaffMFARequirementUnavailableStoreNeverReturnsNoRecovery(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused")
	require.NoError(t, err)
	pool.Close()
	out, err := New(pool).StaffMFARecoveryRequirement(t.Context(), "ordinary-staff")
	require.Error(t, err)
	require.Zero(t, out, "unavailable evidence cannot become an explicit none requirement")
}
