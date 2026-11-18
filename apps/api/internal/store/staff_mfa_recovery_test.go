package store

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func staffMFAFixture(t *testing.T) (*Store, StaffMFARecoveryCommand) {
	t.Helper()
	s := newStore(t)
	p := seedProduct(t, s, "helivanta")
	tenant := uuid.New()
	org, err := s.CreateOrganization(t.Context(), Organization{ID: tenant, ZitadelOrgID: "helivanta-" + tenant.String(), Name: "Synthetic Staff recovery tenant", CreatedByProduct: p.Key})
	require.NoError(t, err)
	require.NoError(t, s.Grant(t.Context(), org.ID, p.Key, nil))
	command := StaffMFARecoveryCommand{MachineSubject: p.MachineUserID, ProviderOrgID: org.ZitadelOrgID, ProviderProjectID: p.ZitadelProjectID,
		Request: customercontract.StaffMFARecoveryRequest{ActorSubject: "synthetic-admin", Target: customercontract.StaffMFARecoveryTarget{
			TenantID: tenant.String(), EmployerKind: "hospital", EmployerID: uuid.NewString(), StaffID: uuid.NewString(), StaffVersion: 7,
			LinkID: uuid.Must(uuid.NewV7()).String(), LinkVersion: 3, Subject: "synthetic-staff",
		}, Intent: customercontract.StaffMFAResetInput{CommandID: uuid.Must(uuid.NewV7()).String(), ExpectedLinkVersion: 3, ExpectedStaffVersion: 7, Reason: "Lost authenticator reported in person"}},
	}
	command.Request.Intent.ExpectedLinkID = command.Request.Target.LinkID
	return s, command
}

func staffMFAEvent(c StaffMFARecoveryCommand, action string) audit.Event {
	return audit.Event{ActorType: "product", ActorID: c.MachineSubject, ProductKey: "helivanta", OrgID: c.Request.Target.TenantID,
		Action: "staff.mfa.recovery." + action, Target: c.Request.Intent.CommandID, Outcome: audit.OutcomeSuccess, RequestID: c.Request.Intent.CommandID}
}

func TestStaffMFARecoverySerializesOneDispatchAndRetainsUnknownFence(t *testing.T) {
	s, command := staffMFAFixture(t)
	var prepared, dispatched atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, created, err := s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
			if err != nil {
				t.Error(err)
				return
			}
			if created {
				prepared.Add(1)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), prepared.Load())
	for range 8 {
		wg.Go(func() {
			_, send, err := s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
			if err != nil {
				t.Error(err)
				return
			}
			if send {
				dispatched.Add(1)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), dispatched.Load())
	require.NoError(t, s.RecordStaffMFARecovery(t.Context(), command, "unconfirmed", "", "", staffMFAEvent(command, "unconfirmed")))
	read, send, err := s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
	require.NoError(t, err)
	require.False(t, send)
	require.Equal(t, "unconfirmed", read.State)
	other := command
	other.Request.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
	_, _, err = s.PrepareStaffMFARecovery(t.Context(), other, staffMFAEvent(other, "prepare"))
	require.ErrorIs(t, err, ErrConflict)
	require.ErrorIs(t, s.RecordStaffMFARecovery(t.Context(), command, "awaiting_enrollment", "1703", time.Now().UTC().Format(time.RFC3339Nano), staffMFAEvent(command, "removed")), ErrConflict)
}

func TestStaffMFARecoveryCancellationReservesCommandBeforeLatePreparation(t *testing.T) {
	s, command := staffMFAFixture(t)
	cancelled, err := s.CancelStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "cancel"))
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.State)
	read, new, err := s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
	require.NoError(t, err)
	require.False(t, new)
	require.Equal(t, "cancelled", read.State)
	_, _, err = s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStaffMFARecoveryRetainsPreciseEvidenceAndLatestAdmissionGeneration(t *testing.T) {
	s, command := staffMFAFixture(t)
	prepared, _, err := s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
	require.NoError(t, err)
	_, _, err = s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
	require.NoError(t, err)
	_, err = s.CancelStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "cancel"))
	require.ErrorIs(t, err, ErrConflict)
	// A fixed nanosecond tail must survive the actual PostgreSQL round trip.
	removed := time.Now().UTC().Add(-time.Second).Truncate(time.Second).Add(123456789 * time.Nanosecond).Format(time.RFC3339Nano)
	enrolled := time.Now().UTC().Add(-time.Second).Truncate(time.Second).Add(123456790 * time.Nanosecond).Format(time.RFC3339Nano)
	require.NoError(t, s.RecordStaffMFARecovery(t.Context(), command, "awaiting_enrollment", "1703", removed, staffMFAEvent(command, "removed")))
	require.NoError(t, s.CompleteStaffMFARecovery(t.Context(), command, "1704", enrolled, staffMFAEvent(command, "complete")))
	current, err := s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "completed", current.State)
	require.Equal(t, removed, current.NativeChangedAt)
	require.Equal(t, enrolled, current.EnrollmentVerifiedAt)
	require.Equal(t, prepared.Generation, current.Generation)
	_, err = s.CurrentStaffMFARecovery(t.Context(), uuid.NewString(), command.Request.Target.Subject)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, "foreign-account")
	require.ErrorIs(t, err, ErrNotFound)
	newCommand := command
	newCommand.Request.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
	next, _, err := s.PrepareStaffMFARecovery(t.Context(), newCommand, staffMFAEvent(newCommand, "prepare"))
	require.NoError(t, err)
	require.Greater(t, next.Generation, current.Generation)
	require.ErrorIs(t, s.CompleteStaffMFARecovery(t.Context(), command, "1704", enrolled, staffMFAEvent(command, "complete")), ErrConflict)
	_, err = s.CancelStaffMFARecovery(t.Context(), newCommand, staffMFAEvent(newCommand, "cancel"))
	require.NoError(t, err)
	current, err = s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, command.Request.Intent.CommandID, current.Request.Intent.CommandID)
}

func TestStaffMFARecoveryRejectsInvalidCommandsBeforeDatabaseIO(t *testing.T) {
	var s Store
	_, _, err := s.PrepareStaffMFARecovery(t.Context(), StaffMFARecoveryCommand{}, audit.Event{})
	require.ErrorIs(t, err, ErrConflict)
	_, _, err = s.DispatchStaffMFARecovery(t.Context(), StaffMFARecoveryCommand{}, "", audit.Event{})
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.CancelStaffMFARecovery(t.Context(), StaffMFARecoveryCommand{}, audit.Event{})
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.CurrentStaffMFARecovery(t.Context(), "../tenant", "staff")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStaffMFARecoveryPreservesIndependentRequestCorrelation(t *testing.T) {
	s, command := staffMFAFixture(t)
	event := staffMFAEvent(command, "prepare")
	event.RequestID = "independent-prepare-request"
	_, _, err := s.PrepareStaffMFARecovery(t.Context(), command, event)
	require.NoError(t, err)
	event = staffMFAEvent(command, "dispatch")
	event.RequestID = "independent-dispatch-request"
	_, send, err := s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, event)
	require.NoError(t, err)
	require.True(t, send)
	chain, err := s.AuditChain(t.Context())
	require.NoError(t, err)
	require.NoError(t, audit.Verify(chain))
	var seen []string
	for _, entry := range chain {
		if entry.Target == command.Request.Intent.CommandID {
			seen = append(seen, entry.RequestID)
		}
	}
	require.Contains(t, seen, "independent-prepare-request")
	require.Contains(t, seen, "independent-dispatch-request")
}

func TestStaffMFARecoveryRejectsChangedFrozenFactsAndForeignScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*StaffMFARecoveryCommand)
		want   error
	}{
		{"actor", func(c *StaffMFARecoveryCommand) { c.Request.ActorSubject = "different-admin" }, ErrIdempotencyConflict},
		{"reason", func(c *StaffMFARecoveryCommand) { c.Request.Intent.Reason = "Different reason" }, ErrIdempotencyConflict},
		{"staff_generation", func(c *StaffMFARecoveryCommand) {
			c.Request.Target.StaffVersion++
			c.Request.Intent.ExpectedStaffVersion++
		}, ErrIdempotencyConflict},
		{"reviewed_link_generation", func(c *StaffMFARecoveryCommand) {
			c.Request.Target.LinkVersion++
			c.Request.Intent.ExpectedLinkVersion++
		}, ErrIdempotencyConflict},
		{"subject", func(c *StaffMFARecoveryCommand) { c.Request.Target.Subject = "different-account" }, ErrIdempotencyConflict},
		{"independent_scope", func(c *StaffMFARecoveryCommand) { c.Request.Target.EmployerKind = "pharmacy" }, ErrIdempotencyConflict},
		{"foreign_machine", func(c *StaffMFARecoveryCommand) { c.MachineSubject = "different-machine" }, ErrNotFound},
		{"provider_mapping", func(c *StaffMFARecoveryCommand) { c.ProviderProjectID = "changed-native-project" }, ErrConflict},
		{"foreign_tenant", func(c *StaffMFARecoveryCommand) {
			c.Request.Target.TenantID = uuid.NewString()
			c.ProviderOrgID = "helivanta-" + c.Request.Target.TenantID
		}, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, command := staffMFAFixture(t)
			_, _, err := s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
			require.NoError(t, err)
			tc.change(&command)
			_, _, err = s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestStaffMFARecoveryRequiresCorrectFenceAndAtomicAuditAtEveryTransition(t *testing.T) {
	s, command := staffMFAFixture(t)
	event := staffMFAEvent(command, "prepare")
	event.Detail = "{"
	_, send, err := s.PrepareStaffMFARecovery(t.Context(), command, event)
	require.Error(t, err)
	require.False(t, send)
	_, err = s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.ErrorIs(t, err, ErrNotFound)
	_, _, err = s.PrepareStaffMFARecovery(t.Context(), command, staffMFAEvent(command, "prepare"))
	require.NoError(t, err)
	_, send, err = s.DispatchStaffMFARecovery(t.Context(), command, uuid.Must(uuid.NewV7()).String(), staffMFAEvent(command, "dispatch"))
	require.ErrorIs(t, err, ErrConflict)
	require.False(t, send)
	event = staffMFAEvent(command, "dispatch")
	event.Detail = "{"
	_, send, err = s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, event)
	require.Error(t, err)
	require.False(t, send)
	current, err := s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "prepared", current.State)
	_, send, err = s.DispatchStaffMFARecovery(t.Context(), command, command.Request.Intent.CommandID, staffMFAEvent(command, "dispatch"))
	require.NoError(t, err)
	require.True(t, send)
	removed := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	event = staffMFAEvent(command, "removed")
	event.Detail = "{"
	require.Error(t, s.RecordStaffMFARecovery(t.Context(), command, "awaiting_enrollment", "1703", removed, event))
	current, err = s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "processing", current.State)
	require.Empty(t, current.NativeSequence)
	require.NoError(t, s.RecordStaffMFARecovery(t.Context(), command, "awaiting_enrollment", "1703", removed, staffMFAEvent(command, "removed")))
	for _, bad := range []struct{ sequence, when string }{{"1703", removed}, {"1702", removed}, {"01704", removed}, {"1704", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}} {
		require.ErrorIs(t, s.CompleteStaffMFARecovery(t.Context(), command, bad.sequence, bad.when, staffMFAEvent(command, "complete")), ErrConflict)
	}
	event = staffMFAEvent(command, "complete")
	event.Detail = "{"
	enrolled := time.Now().UTC().Format(time.RFC3339Nano)
	require.Error(t, s.CompleteStaffMFARecovery(t.Context(), command, "1704", enrolled, event))
	current, err = s.CurrentStaffMFARecovery(t.Context(), command.Request.Target.TenantID, command.Request.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "awaiting_enrollment", current.State)
	require.NoError(t, s.CompleteStaffMFARecovery(t.Context(), command, "1704", enrolled, staffMFAEvent(command, "complete")))
	chain, err := s.AuditChain(t.Context())
	require.NoError(t, err)
	require.NoError(t, audit.Verify(chain))
}
