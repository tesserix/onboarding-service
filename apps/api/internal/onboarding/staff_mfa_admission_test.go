package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestStaffMFAAdmissionVerifiesNativeProofBeforeReleasingCurrentGeneration(t *testing.T) {
	for _, mode := range []string{"hospital", "pharmacy", "supplier", "legacy_tenant", "old_primary", "same_second_primary", "wrong_user", "wrong_org", "missing_totp", "unenrolled", "enrollment_changed", "password_changed", "withdrawn", "native_unavailable", "wrong_generation", "promoted_during_proof", "inactive_account", "unverified_email", "moved_owner"} {
		t.Run(mode, func(t *testing.T) {
			removed := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Second).Add(123456700 * time.Nanosecond)
			enrolled := removed.Add(300 * time.Nanosecond)
			svc, st, request := mfaServiceFixture(t, mfaServiceSource{configured: true, remove: func(context.Context, string, string) (zitadel.TOTPRemovalEvidence, error) {
				return zitadel.TOTPRemovalEvidence{Sequence: "1703", ChangedAt: removed}, nil
			}})
			if mode == "pharmacy" || mode == "supplier" {
				request.Target.EmployerKind = mode
			}
			ctx := WithRequestID(t.Context(), uuid.NewString())
			_, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), request)
			require.NoError(t, err)
			dispatched, err := svc.DispatchStaffMFARecovery(ctx, product("helivanta"), request, request.Intent.CommandID)
			require.NoError(t, err)
			require.Equal(t, "awaiting_enrollment", dispatched.State, "native removal must be acknowledged before admission testing")
			loginTenant := request.Target.TenantID
			if mode == "legacy_tenant" {
				id := uuid.New()
				loginTenant = id.String()
				_, err = st.CreateOrganization(ctx, store.Organization{ID: id, ZitadelOrgID: "helivanta-" + loginTenant, Name: "Synthetic legacy login tenant", CreatedByProduct: "helivanta"})
				require.NoError(t, err)
				require.NoError(t, st.Grant(ctx, id, "helivanta", nil))
			}
			input := customercontract.StaffMFAAdmissionInput{Scope: customercontract.StaffMFAAdmissionScope{TenantID: loginTenant, Subject: request.Target.Subject, GenerationID: request.Intent.CommandID, RequestID: uuid.NewString()}, NativeSessionID: "signed-session"}
			if mode == "wrong_generation" {
				input.Scope.GenerationID = uuid.Must(uuid.NewV7()).String()
			}
			var eventReads, nativeWrites atomic.Int32
			var afterSession atomic.Bool
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete || r.Method == http.MethodPut {
					nativeWrites.Add(1)
					w.WriteHeader(500)
					return
				}
				if r.Header.Get("Authorization") != "Bearer synthetic" {
					t.Error("native credential binding lost")
				}
				org := "helivanta-" + request.Target.TenantID
				switch r.URL.Path {
				case "/v2/users/" + request.Target.Subject:
					changed := removed.Add(-time.Second)
					if mode == "password_changed" && afterSession.Load() {
						changed = removed.Add(250 * time.Nanosecond)
					}
					accountState, accountOrg, verified := "USER_STATE_ACTIVE", org, true
					if afterSession.Load() {
						switch mode {
						case "inactive_account":
							accountState = "USER_STATE_INACTIVE"
						case "unverified_email":
							verified = false
						case "moved_owner":
							accountOrg = "other-org"
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": request.Target.Subject, "state": accountState, "details": map[string]string{"resourceOwner": accountOrg}, "human": map[string]any{"passwordChanged": changed.Format(time.RFC3339Nano), "email": map[string]any{"email": "synthetic@example.test", "isVerified": verified}}}})
				case "/management/v1/orgs/me/members/_search":
					if mode == "promoted_during_proof" && afterSession.Load() {
						_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]string{"totalResult": "1", "processedSequence": "1710"}, "result": []any{map[string]any{"userId": request.Target.Subject, "roles": []string{"ORG_OWNER"}}}})
					} else {
						_, _ = w.Write([]byte(`{"details":{"totalResult":"0","processedSequence":"1702"},"result":[]}`))
					}
				case "/admin/v1/events/_search":
					count := eventReads.Add(1)
					sequence, kind := "1704", "user.human.mfa.otp.verified"
					if mode == "unenrolled" {
						kind = "user.human.mfa.otp.added"
					}
					at := enrolled
					if mode == "enrollment_changed" && count > 1 {
						sequence = "1705"
						at = at.Add(time.Nanosecond)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{map[string]any{"sequence": sequence, "creationDate": at.Format(time.RFC3339Nano), "aggregate": map[string]any{"id": request.Target.Subject, "resourceOwner": org, "type": map[string]string{"type": "user"}}, "type": map[string]string{"type": kind}}}})
				case "/v2/sessions/signed-session":
					afterSession.Store(true)
					if mode == "native_unavailable" {
						w.WriteHeader(503)
						return
					}
					if mode == "withdrawn" {
						id, e := uuid.Parse(loginTenant)
						if e != nil {
							t.Error(e)
						} else if e = st.RevokeGrant(r.Context(), id, "helivanta"); e != nil {
							t.Error(e)
						}
					}
					user, userOrg := request.Target.Subject, org
					if mode == "wrong_user" {
						user = "other-user"
					}
					if mode == "wrong_org" {
						userOrg = "other-org"
					}
					primary := removed.Add(200 * time.Nanosecond)
					if mode == "old_primary" {
						primary = removed.Add(-time.Second)
					}
					if mode == "same_second_primary" {
						primary = removed.Add(-time.Nanosecond)
					}
					factors := map[string]any{"user": map[string]any{"id": user, "organizationId": userOrg, "verifiedAt": removed.Add(100 * time.Nanosecond)}, "password": map[string]any{"verifiedAt": primary}, "totp": map[string]any{"verifiedAt": removed.Add(400 * time.Nanosecond)}}
					if mode == "missing_totp" {
						delete(factors, "totp")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"session": map[string]any{"id": "signed-session", "sequence": "1706", "creationDate": removed.Add(100 * time.Nanosecond), "changeDate": removed.Add(time.Second), "expirationDate": time.Now().Add(time.Hour), "factors": factors}})
				default:
					t.Errorf("unexpected native admission request: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			t.Cleanup(native.Close)
			svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)

			receipt, err := svc.AdmitStaffMFASession(ctx, input)
			var want error
			switch mode {
			case "old_primary", "same_second_primary", "wrong_user", "wrong_org", "missing_totp", "unenrolled", "password_changed":
				want = ErrMFAAuthenticationRequired
			case "enrollment_changed", "wrong_generation":
				want = ErrConflict
			case "withdrawn", "inactive_account", "unverified_email", "moved_owner":
				want = ErrNotFound
			case "native_unavailable":
				want = ErrUnavailable
			}
			if want != nil {
				require.ErrorIs(t, err, want)
				require.Zero(t, receipt)
			} else {
				require.NoError(t, err)
				require.Equal(t, input.Scope.GenerationID, receipt.GenerationID)
				_, err = svc.AdmitStaffMFASession(ctx, input)
				require.NoError(t, err, "a completed generation still requires and can prove the current native session")
			}
			row, err := st.CurrentStaffMFARecovery(t.Context(), request.Target.TenantID, request.Target.Subject)
			require.NoError(t, err)
			if want != nil {
				require.Equal(t, "awaiting_enrollment", row.State)
			} else {
				require.Equal(t, "completed", row.State)
				require.Equal(t, "1704", row.EnrollmentSequence)
			}
			require.Zero(t, nativeWrites.Load(), "admission is read-only against the native credential owner")
		})
	}
}

func TestStaffMFAAdmissionInvalidInputCannotReachDependencies(t *testing.T) {
	var svc Service
	out, err := svc.AdmitStaffMFASession(t.Context(), customercontract.StaffMFAAdmissionInput{})
	require.True(t, errors.Is(err, ErrInvalid))
	require.Zero(t, out)
}
