package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type apiMFASource struct{ calls atomic.Int32 }

func (*apiMFASource) CheckStaffMFARecoveryTarget(context.Context, string, string) error { return nil }
func (*apiMFASource) StaffSecondFactorsConfigured(context.Context, string, string) (bool, error) {
	return true, nil
}
func (s *apiMFASource) RemoveRecoveryFactors(context.Context, string, string) (zitadel.FactorRemovalEvidence, error) {
	s.calls.Add(1)
	return zitadel.FactorRemovalEvidence{Sequence: "1703", ChangedAt: time.Now().UTC()}, nil
}

func mfaHTTPFixture(t *testing.T) (*harness, *apiMFASource, customercontract.StaffMFARecoveryRequest, string) {
	t.Helper()
	source := &apiMFASource{}
	h := newHarness(t, onboarding.WithStaffMFARecovery("helivanta", source))
	require.NoError(t, h.store.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Helivanta", ZitadelProjectID: "proj-helivanta", MachineUserID: "svc-helivanta", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao, Tenancy: store.TenancyOrg, Roles: []string{"helivanta-user"}, DataResidency: "in"}))
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := h.fixturePool.Exec(ctx, `DELETE FROM staff_mfa_recovery_commands WHERE org_id=$1`, tenant)
		require.NoError(t, err)
	})
	_, err := h.store.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: "helivanta-" + tenant.String(), Name: "Synthetic employer", CreatedByProduct: "helivanta"})
	require.NoError(t, err)
	require.NoError(t, h.store.Grant(t.Context(), tenant, "helivanta", nil))
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		return identityObservation(org, tenant, project), nil
	}
	h.tokens.claims["mfa-owner-token"] = auth.Claims{Subject: "synthetic-owner", OrgID: "helivanta-" + tenant.String(), Roles: []string{"ORG_OWNER"}}
	link := uuid.Must(uuid.NewV7()).String()
	in := customercontract.StaffMFARecoveryRequest{ActorSubject: "synthetic-owner", Target: customercontract.StaffMFARecoveryTarget{TenantID: tenant.String(), EmployerKind: "hospital", EmployerID: uuid.NewString(), StaffID: uuid.NewString(), StaffVersion: 1, LinkID: link, LinkVersion: 1, Subject: "synthetic-staff"}, Intent: customercontract.StaffMFAResetInput{CommandID: uuid.Must(uuid.NewV7()).String(), ExpectedLinkID: link, ExpectedLinkVersion: 1, ExpectedStaffVersion: 1, Reason: "Lost device reported in person"}}
	path := "/v1/orgs/" + tenant.String() + "/staff-mfa-recoveries/" + in.Intent.CommandID
	return h, source, in, path
}

func mfaHTTPJSON(t *testing.T, in any) string {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	return string(raw)
}

func TestStaffMFAHTTPPreparationDispatchAndPrivateSnapshot(t *testing.T) {
	h, source, in, path := mfaHTTPFixture(t)
	response := h.do(t, http.MethodPost, path+"/dispatch", helivantaToken, mfaHTTPJSON(t, customercontract.StaffMFADispatchRequest{Request: in, ApplicationFenceID: in.Intent.CommandID}))
	require.Equal(t, http.StatusNotFound, response.Code)
	response = h.do(t, http.MethodPut, path, helivantaToken, mfaHTTPJSON(t, in))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "prepared", decode(t, response)["state"])
	require.Zero(t, source.calls.Load())
	for range 2 {
		response = h.do(t, http.MethodPost, path+"/dispatch", helivantaToken, mfaHTTPJSON(t, customercontract.StaffMFADispatchRequest{Request: in, ApplicationFenceID: in.Intent.CommandID}))
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "awaiting_enrollment", decode(t, response)["state"])
		require.NotContains(t, response.Body.String(), in.Target.Subject)
	}
	require.Equal(t, int32(1), source.calls.Load())
	response = h.do(t, http.MethodGet, path+"?actor_subject="+in.ActorSubject, helivantaToken, "")
	require.Equal(t, http.StatusOK, response.Code)
	var snapshot customercontract.StaffMFARecoverySnapshot
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &snapshot))
	require.Equal(t, in, snapshot.Request)
	response = h.do(t, http.MethodGet, path+"?actor_subject=different-owner", helivantaToken, "")
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestStaffMFAHTTPRejectsOtherProductsAndDirectOwnerTokens(t *testing.T) {
	for _, token := range []string{productToken, otherToken, platformToken, "mfa-owner-token", ""} {
		t.Run(token, func(t *testing.T) {
			h, source, in, path := mfaHTTPFixture(t)
			response := h.do(t, http.MethodPut, path, token, mfaHTTPJSON(t, in))
			require.True(t, response.Code == http.StatusNotFound || response.Code == http.StatusUnauthorized)
			require.Zero(t, source.calls.Load())
			_, err := h.store.CurrentStaffMFARecovery(t.Context(), in.Target.TenantID, in.Target.Subject)
			require.ErrorIs(t, err, store.ErrNotFound)
		})
	}
}

func TestStaffMFAHTTPClosedBodiesAndPathCannotSelectDifferentAccount(t *testing.T) {
	for _, kind := range []string{"unknown_authority", "duplicate_actor", "other_tenant", "other_command", "query", "wrong_fence"} {
		t.Run(kind, func(t *testing.T) {
			h, source, in, path := mfaHTTPFixture(t)
			body := mfaHTTPJSON(t, in)
			method := http.MethodPut
			switch kind {
			case "unknown_authority":
				body = strings.TrimSuffix(body, "}") + `,"approved":true}`
			case "duplicate_actor":
				body = strings.TrimSuffix(body, "}") + `,"actorSubject":"other-owner"}`
			case "other_tenant":
				in.Target.TenantID = uuid.NewString()
				body = mfaHTTPJSON(t, in)
			case "other_command":
				in.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
				body = mfaHTTPJSON(t, in)
			case "query":
				path += "?approved=true"
			case "wrong_fence":
				method = http.MethodPost
				path += "/dispatch"
				body = mfaHTTPJSON(t, customercontract.StaffMFADispatchRequest{Request: in, ApplicationFenceID: uuid.Must(uuid.NewV7()).String()})
			}
			response := h.do(t, method, path, helivantaToken, body)
			require.Equal(t, http.StatusBadRequest, response.Code)
			require.Zero(t, source.calls.Load())
		})
	}
}

func TestStaffMFAHTTPReplayCacheCannotBypassCurrentEntitlement(t *testing.T) {
	h, source, in, path := mfaHTTPFixture(t)
	do := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(mfaHTTPJSON(t, in)))
		request.Header.Set("Authorization", "Bearer "+helivantaToken)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "same-private-reset-intent")
		response := httptest.NewRecorder()
		h.router.ServeHTTP(response, request)
		return response
	}
	require.Equal(t, http.StatusOK, do().Code)
	tenant, err := uuid.Parse(in.Target.TenantID)
	require.NoError(t, err)
	require.NoError(t, h.store.RevokeGrant(t.Context(), tenant, "helivanta"))
	require.Equal(t, http.StatusNotFound, do().Code)
	require.Zero(t, source.calls.Load())
}

func TestStaffMFAHTTPBodyIsStrictAndBoundedBeforeService(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(`null`), []byte(`{}`), []byte(`{"approved":true}`), []byte(strings.Repeat(" ", 8193)), {0xff}} {
		var input customercontract.StaffMFARecoveryRequest
		require.Error(t, staffMFABody(raw, &input))
		require.Zero(t, input)
	}
	link := uuid.Must(uuid.NewV7()).String()
	valid := customercontract.StaffMFARecoveryRequest{ActorSubject: "synthetic-owner", Target: customercontract.StaffMFARecoveryTarget{TenantID: uuid.NewString(), EmployerKind: "hospital", EmployerID: uuid.NewString(), StaffID: uuid.NewString(), StaffVersion: 1, LinkID: link, LinkVersion: 1, Subject: "synthetic-staff"}, Intent: customercontract.StaffMFAResetInput{CommandID: uuid.Must(uuid.NewV7()).String(), ExpectedLinkID: link, ExpectedLinkVersion: 1, ExpectedStaffVersion: 1, Reason: "Lost device"}}
	raw, err := json.Marshal(valid)
	require.NoError(t, err)
	var decoded customercontract.StaffMFARecoveryRequest
	require.NoError(t, staffMFABody(raw, &decoded))
	padded := append(raw, []byte(strings.Repeat(" ", 8193))...)
	require.NoError(t, json.Unmarshal(padded, &decoded))
	decoded = customercontract.StaffMFARecoveryRequest{}
	require.Error(t, staffMFABody(padded, &decoded))
	require.Zero(t, decoded)
}
