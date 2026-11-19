package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestStaffMFAAdmissionRequiresLatestAccountGenerationAndRealWorkloadAudit(t *testing.T) {
	s, command := staffMFAFixture(t)
	_, _, err := s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
	require.NoError(t, err)
	_, err = s.StaffMFAAdmissionCommand(t.Context(), command.Request.Target.Subject, command.Request.Intent.CommandID)
	require.ErrorIs(t, err, ErrConflict, "preparation is not native removal")
	_, _, err = s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
	require.NoError(t, err)
	removed := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	enrolled := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339Nano)
	require.NoError(t, s.RecordStaffMFARecovery(t.Context(), command, "awaiting_enrollment", "1703", removed, staffMFAEvent(command, "removed")))
	retained, err := s.StaffMFAAdmissionCommand(t.Context(), command.Request.Target.Subject, command.Request.Intent.CommandID)
	require.NoError(t, err)
	require.Equal(t, command.Request, retained.Request)
	_, err = s.StaffMFAAdmissionCommand(t.Context(), "another-subject", command.Request.Intent.CommandID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.StaffMFAAdmissionCommand(t.Context(), command.Request.Target.Subject, uuid.Must(uuid.NewV7()).String())
	require.ErrorIs(t, err, ErrConflict)
	event := audit.Event{ActorType: audit.ActorSystem, ActorID: audit.HelivantaWorkload, ProductKey: "helivanta", OrgID: command.Request.Target.TenantID,
		Action: "staff.mfa.recovery.admit", Target: command.Request.Intent.CommandID, Outcome: audit.OutcomeSuccess, RequestID: uuid.NewString()}
	bad := event
	bad.ActorID = command.MachineSubject
	require.ErrorIs(t, s.AdmitStaffMFARecovery(t.Context(), command, "1704", enrolled, bad), ErrConflict)
	require.NoError(t, s.AdmitStaffMFARecovery(t.Context(), command, "1704", enrolled, event))
	require.NoError(t, s.AdmitStaffMFARecovery(t.Context(), command, "1705", time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), event))
	current, err := s.StaffMFAAdmissionCommand(t.Context(), command.Request.Target.Subject, command.Request.Intent.CommandID)
	require.NoError(t, err)
	require.Equal(t, "completed", current.State)
	require.Equal(t, "1704", current.EnrollmentSequence, "later logins must not rewrite first enrollment history")
	require.Equal(t, enrolled, current.EnrollmentVerifiedAt)
	require.ErrorIs(t, s.AdmitStaffMFARecovery(t.Context(), command, "1703", enrolled, event), ErrConflict)
	newer := command
	newer.Request.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
	_, _, err = s.PrepareStaffMFARecovery(t.Context(), newer, staffMFAEvent(newer, "prepare"))
	require.NoError(t, err)
	require.ErrorIs(t, s.AdmitStaffMFARecovery(t.Context(), command, "1704", enrolled, event), ErrConflict)
}

func TestStaffMFAAdmissionRejectsInvalidInputBeforeIO(t *testing.T) {
	var s Store
	_, err := s.StaffMFAAdmissionCommand(t.Context(), "../foreign", uuid.Must(uuid.NewV7()).String())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.StaffMFAAdmissionCommand(t.Context(), "staff", uuid.NewString())
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, s.AdmitStaffMFARecovery(t.Context(), StaffMFARecoveryCommand{}, "1704", time.Now().UTC().Format(time.RFC3339Nano), audit.Event{}), ErrConflict)
}
