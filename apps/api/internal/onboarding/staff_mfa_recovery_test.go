package onboarding

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type mfaServiceIdentity struct{ *fakeIdentity }

func (m mfaServiceIdentity) VerifyOrganization(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
	return zitadel.OrganizationIdentity{SchemaVersion: 1, TenantID: tenant, OrganizationID: org, ProjectID: project, GrantID: "synthetic-project-grant", State: "active", VerifiedAt: time.Now().UTC(), SourceRevisions: zitadel.IdentityRevisions{Organization: "1", Metadata: "2", Project: "3", Grant: "4"}}, nil
}

type mfaServiceSource struct {
	check      func(context.Context, string, string) error
	configured bool
	remove     func(context.Context, string, string) (zitadel.FactorRemovalEvidence, error)
}

func (m mfaServiceSource) CheckStaffMFARecoveryTarget(ctx context.Context, org, user string) error {
	if m.check != nil {
		return m.check(ctx, org, user)
	}
	return nil
}
func (m mfaServiceSource) StaffSecondFactorsConfigured(context.Context, string, string) (bool, error) {
	return m.configured, nil
}
func (m mfaServiceSource) RemoveRecoveryFactors(ctx context.Context, org, user string) (zitadel.FactorRemovalEvidence, error) {
	if m.remove == nil {
		return zitadel.FactorRemovalEvidence{}, zitadel.ErrIdentityUnavailable
	}
	return m.remove(ctx, org, user)
}

func mfaServiceFixture(t *testing.T, source mfaServiceSource) (*Service, *store.Store, customercontract.StaffMFARecoveryRequest) {
	t.Helper()
	_, identity, st, pool := newServiceWithFixturePool(t)
	seedProduct(t, st, "helivanta")
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, `DELETE FROM staff_mfa_recovery_commands WHERE org_id=$1`, tenant)
		require.NoError(t, err)
	})
	_, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: "helivanta-" + tenant.String(), Name: "Synthetic employer", CreatedByProduct: "helivanta"})
	require.NoError(t, err)
	require.NoError(t, st.Grant(t.Context(), tenant, "helivanta", nil))
	link := uuid.Must(uuid.NewV7()).String()
	input := customercontract.StaffMFARecoveryRequest{ActorSubject: "synthetic-owner", Target: customercontract.StaffMFARecoveryTarget{
		TenantID: tenant.String(), EmployerKind: "hospital", EmployerID: uuid.NewString(), StaffID: uuid.NewString(), StaffVersion: 7, LinkID: link, LinkVersion: 3, Subject: "synthetic-staff",
	}, Intent: customercontract.StaffMFAResetInput{CommandID: uuid.Must(uuid.NewV7()).String(), ExpectedLinkID: link, ExpectedLinkVersion: 3, ExpectedStaffVersion: 7, Reason: "Lost authenticator reported in person"}}
	svc := New(st, mfaServiceIdentity{identity}, authz.New(st), nil, WithStaffMFARecovery("helivanta", source))
	return svc, st, input
}

func TestStaffMFAServicePersistsFenceBeforeOneNativeDispatch(t *testing.T) {
	var calls atomic.Int32
	source := mfaServiceSource{configured: true}
	svc, st, input := mfaServiceFixture(t, source)
	svc.staffMFASource = mfaServiceSource{configured: true, remove: func(ctx context.Context, org, user string) (zitadel.FactorRemovalEvidence, error) {
		calls.Add(1)
		require.Equal(t, "helivanta-"+input.Target.TenantID, org)
		require.Equal(t, input.Target.Subject, user)
		row, err := st.CurrentStaffMFARecovery(ctx, input.Target.TenantID, user)
		require.NoError(t, err)
		require.Equal(t, "processing", row.State)
		require.NotNil(t, row.ApplicationFenceID)
		require.Equal(t, input.Intent.CommandID, row.ApplicationFenceID.String())
		return zitadel.FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().UTC()}, nil
	}}
	ctx := WithRequestID(t.Context(), uuid.NewString())
	prepared, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), input)
	require.NoError(t, err)
	require.Equal(t, "prepared", prepared.State)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			receipt, err := svc.DispatchStaffMFARecovery(ctx, product("helivanta"), input, input.Intent.CommandID)
			if err != nil {
				t.Error(err)
				return
			}
			if receipt.State != "processing" && receipt.State != "awaiting_enrollment" {
				t.Error("unexpected command state", receipt.State)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	row, err := st.CurrentStaffMFARecovery(ctx, input.Target.TenantID, input.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "awaiting_enrollment", row.State)
}

func TestStaffMFAServiceUnknownNativeOutcomeNeverRedispatches(t *testing.T) {
	var calls atomic.Int32
	svc, _, input := mfaServiceFixture(t, mfaServiceSource{configured: true, remove: func(context.Context, string, string) (zitadel.FactorRemovalEvidence, error) {
		calls.Add(1)
		return zitadel.FactorRemovalEvidence{}, context.DeadlineExceeded
	}})
	ctx := WithRequestID(t.Context(), uuid.NewString())
	_, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), input)
	require.NoError(t, err)
	for range 2 {
		receipt, err := svc.DispatchStaffMFARecovery(ctx, product("helivanta"), input, input.Intent.CommandID)
		require.NoError(t, err)
		require.Equal(t, "unconfirmed", receipt.State)
	}
	require.Equal(t, int32(1), calls.Load())
	_, err = svc.CancelStaffMFARecovery(ctx, product("helivanta"), input)
	require.ErrorIs(t, err, ErrConflict)
}

func TestStaffMFAServiceCancellationAndUnenrolledTargetsNeverWrite(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(map[bool]string{true: "explicit_cancel", false: "no_configured_factor"}[cancel], func(t *testing.T) {
			var calls atomic.Int32
			svc, _, input := mfaServiceFixture(t, mfaServiceSource{configured: false, remove: func(context.Context, string, string) (zitadel.FactorRemovalEvidence, error) {
				calls.Add(1)
				return zitadel.FactorRemovalEvidence{}, errors.New("unexpected write")
			}})
			ctx := WithRequestID(t.Context(), uuid.NewString())
			_, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), input)
			require.NoError(t, err)
			if cancel {
				receipt, err := svc.CancelStaffMFARecovery(ctx, product("helivanta"), input)
				require.NoError(t, err)
				require.Equal(t, "cancelled", receipt.State)
				_, err = svc.DispatchStaffMFARecovery(ctx, product("helivanta"), input, input.Intent.CommandID)
				require.ErrorIs(t, err, ErrConflict)
			} else {
				receipt, err := svc.DispatchStaffMFARecovery(ctx, product("helivanta"), input, input.Intent.CommandID)
				require.NoError(t, err)
				require.Equal(t, "cancelled", receipt.State)
			}
			require.Zero(t, calls.Load())
		})
	}
}

func TestStaffMFAServiceInvalidInputCannotReachStoreOrNative(t *testing.T) {
	var svc Service
	_, err := svc.PrepareStaffMFARecovery(t.Context(), product("helivanta"), customercontract.StaffMFARecoveryRequest{})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestStaffMFAServiceReadIsExactAndNeverPrepares(t *testing.T) {
	svc, st, in := mfaServiceFixture(t, mfaServiceSource{configured: true})
	ctx := WithRequestID(t.Context(), uuid.NewString())
	tenant, err := uuid.Parse(in.Target.TenantID)
	require.NoError(t, err)
	id, err := uuid.Parse(in.Intent.CommandID)
	require.NoError(t, err)
	_, err = svc.ReadStaffMFARecovery(ctx, product("helivanta"), tenant, id, in.ActorSubject)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = st.CurrentStaffMFARecovery(ctx, in.Target.TenantID, in.Target.Subject)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.PrepareStaffMFARecovery(ctx, product("helivanta"), in)
	require.NoError(t, err)
	read, err := svc.ReadStaffMFARecovery(ctx, product("helivanta"), tenant, id, in.ActorSubject)
	require.NoError(t, err)
	require.Equal(t, in, read.Request)
	require.Equal(t, "prepared", read.Receipt.State)
	_, err = svc.ReadStaffMFARecovery(ctx, product("helivanta"), tenant, id, "different-admin")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStaffMFAServiceProtectedAndUnavailableTargetsNeverPrepare(t *testing.T) {
	for _, tc := range []struct {
		name            string
		sourceErr, want error
	}{
		{"protected", zitadel.ErrRecoveryTargetIneligible, ErrNotFound},
		{"unavailable", zitadel.ErrIdentityUnavailable, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, st, in := mfaServiceFixture(t, mfaServiceSource{configured: true, check: func(context.Context, string, string) error { return tc.sourceErr }})
			ctx := WithRequestID(t.Context(), uuid.NewString())
			_, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), in)
			require.ErrorIs(t, err, tc.want)
			_, err = st.CurrentStaffMFARecovery(ctx, in.Target.TenantID, in.Target.Subject)
			require.ErrorIs(t, err, store.ErrNotFound)
		})
	}
}

func TestStaffMFAServiceRetainsOutcomeWhenEntitlementIsWithdrawnDuringNativeIO(t *testing.T) {
	svc, st, in := mfaServiceFixture(t, mfaServiceSource{configured: true})
	ctx := WithRequestID(t.Context(), uuid.NewString())
	_, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), in)
	require.NoError(t, err)
	svc.staffMFASource = mfaServiceSource{configured: true, remove: func(ctx context.Context, _, _ string) (zitadel.FactorRemovalEvidence, error) {
		tenant, err := uuid.Parse(in.Target.TenantID)
		require.NoError(t, err)
		require.NoError(t, st.RevokeGrant(ctx, tenant, "helivanta"))
		return zitadel.FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().UTC()}, nil
	}}
	receipt, err := svc.DispatchStaffMFARecovery(ctx, product("helivanta"), in, in.Intent.CommandID)
	require.ErrorIs(t, err, ErrNotFound)
	require.Zero(t, receipt)
	retained, err := st.CurrentStaffMFARecovery(t.Context(), in.Target.TenantID, in.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "awaiting_enrollment", retained.State)
	require.Equal(t, "1703", retained.NativeSequence)
}

func TestStaffMFAServiceChecksWithdrawalBeforeDispatchAndRefusesImplicitPrepare(t *testing.T) {
	var calls atomic.Int32
	svc, st, in := mfaServiceFixture(t, mfaServiceSource{configured: true})
	ctx := WithRequestID(t.Context(), uuid.NewString())
	_, err := svc.DispatchStaffMFARecovery(ctx, product("helivanta"), in, in.Intent.CommandID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.PrepareStaffMFARecovery(ctx, product("helivanta"), in)
	require.NoError(t, err)
	svc.staffMFASource = mfaServiceSource{configured: true, check: func(ctx context.Context, _, _ string) error {
		tenant, err := uuid.Parse(in.Target.TenantID)
		require.NoError(t, err)
		require.NoError(t, st.RevokeGrant(ctx, tenant, "helivanta"))
		return nil
	}, remove: func(context.Context, string, string) (zitadel.FactorRemovalEvidence, error) {
		calls.Add(1)
		return zitadel.FactorRemovalEvidence{}, errors.New("unexpected")
	}}
	_, err = svc.DispatchStaffMFARecovery(ctx, product("helivanta"), in, in.Intent.CommandID)
	require.ErrorIs(t, err, ErrNotFound)
	require.Zero(t, calls.Load())
	row, err := st.CurrentStaffMFARecovery(t.Context(), in.Target.TenantID, in.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "prepared", row.State)
}

func TestStaffMFAServiceRetainsNativeAcknowledgementAfterCallerDisconnect(t *testing.T) {
	svc, st, in := mfaServiceFixture(t, mfaServiceSource{configured: true})
	ctx, stop := context.WithCancel(WithRequestID(t.Context(), uuid.NewString()))
	defer stop()
	_, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), in)
	require.NoError(t, err)
	svc.staffMFASource = mfaServiceSource{configured: true, remove: func(context.Context, string, string) (zitadel.FactorRemovalEvidence, error) {
		stop()
		return zitadel.FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().UTC()}, nil
	}}
	receipt, err := svc.DispatchStaffMFARecovery(ctx, product("helivanta"), in, in.Intent.CommandID)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, receipt)
	row, err := st.CurrentStaffMFARecovery(t.Context(), in.Target.TenantID, in.Target.Subject)
	require.NoError(t, err)
	require.Equal(t, "awaiting_enrollment", row.State)
}
