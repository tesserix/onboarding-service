package onboarding

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestIdentityMFARecoveryOptInIncludesExactSubjectAcrossLegacyTenants(t *testing.T) {
	for _, mode := range []string{"none", "prepared", "legacy_tenant", "old_caller", "different_subject", "inactive", "withdrawn_during_read"} {
		t.Run(mode, func(t *testing.T) {
			svc, st, request := mfaServiceFixture(t, mfaServiceSource{configured: true})
			ctx := WithRequestID(t.Context(), uuid.NewString())
			if mode != "none" {
				_, err := svc.PrepareStaffMFARecovery(ctx, product("helivanta"), request)
				require.NoError(t, err)
			}
			tenant := request.Target.TenantID
			if mode == "legacy_tenant" {
				id := uuid.New()
				tenant = id.String()
				_, err := st.CreateOrganization(ctx, store.Organization{ID: id, ZitadelOrgID: "helivanta-" + tenant, Name: "Synthetic legacy employer", CreatedByProduct: "helivanta"})
				require.NoError(t, err)
				require.NoError(t, st.Grant(ctx, id, "helivanta", nil))
			}
			subject := request.Target.Subject
			if mode == "different_subject" {
				subject = "separate-native-account"
			}
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v2/users/"+subject, r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				if mode == "withdrawn_during_read" {
					id, err := uuid.Parse(tenant)
					require.NoError(t, err)
					require.NoError(t, st.RevokeGrant(r.Context(), id, "helivanta"))
				}
				state := "USER_STATE_ACTIVE"
				if mode == "inactive" {
					state = "USER_STATE_INACTIVE"
				}
				err := json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": subject, "state": state, "human": map[string]string{"passwordChanged": "2026-11-18T10:11:12Z"}}})
				require.NoError(t, err)
			}))
			t.Cleanup(native.Close)
			svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
			in := customercontract.IdentityProjectionInput{Purpose: "status", TenantID: tenant, Subject: subject, RequestID: uuid.NewString(), IncludeCredentialState: true, IncludeMFARecovery: mode != "old_caller"}
			out, err := svc.CustomerIdentityProjection(ctx, in)
			if mode == "withdrawn_during_read" {
				require.ErrorIs(t, err, ErrNotFound)
				require.Zero(t, out)
				return
			}
			require.NoError(t, err)
			if mode == "old_caller" || mode == "inactive" {
				require.Nil(t, out.MFARecovery)
				return
			}
			require.NotNil(t, out.MFARecovery)
			if mode == "none" || mode == "different_subject" {
				require.Equal(t, customercontract.StaffMFARecoveryRequirement{State: "none"}, *out.MFARecovery)
			} else {
				require.Equal(t, customercontract.StaffMFARecoveryRequirement{State: "blocked", GenerationID: request.Intent.CommandID}, *out.MFARecovery)
			}
			encoded, err := json.Marshal(out)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), request.Intent.Reason)
			require.NotContains(t, string(encoded), request.ActorSubject)
			if mode == "legacy_tenant" {
				require.NotContains(t, string(encoded), request.Target.TenantID)
			}
			chain, err := st.AuditChain(ctx)
			require.NoError(t, err)
			found := false
			for _, event := range chain {
				if event.Action == "customer.identity.read" && event.RequestID == in.RequestID {
					require.Contains(t, event.Detail, `"mfaRecovery":true`)
					found = true
				}
			}
			require.True(t, found, "recovery disclosure requires opt-in audit")
		})
	}
}

func TestIdentityMFARecoveryRequiresCredentialReadBeforeIO(t *testing.T) {
	var svc Service
	out, err := svc.CustomerIdentityProjection(t.Context(), customercontract.IdentityProjectionInput{Purpose: "status", TenantID: uuid.NewString(), Subject: "staff", RequestID: uuid.NewString(), IncludeMFARecovery: true})
	if !errors.Is(err, ErrInvalid) || out != (customercontract.IdentityProjection{}) {
		t.Fatal("invalid recovery read reached dependencies")
	}
}
