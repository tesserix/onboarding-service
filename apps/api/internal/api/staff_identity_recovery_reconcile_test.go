package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestStaffRecoveryReconcileHTTPRequiresFreshWriteAuthorityAndPreservesOriginalBinding(t *testing.T) {
	h, c, _, _ := recoveryReviewFixture(t)
	if _, err := h.fixturePool.Exec(t.Context(), `UPDATE products SET machine_user_id='TEST-current-machine' WHERE key=$1`, c.ProductKey); err != nil {
		t.Fatal(err)
	}
	calls := 0
	revoked := false
	born := c.CreatedAt.UTC().Add(time.Nanosecond)
	subject := recoverySubjectSource{staffSource: staffSource{create: func(context.Context, zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		t.Fatal("reconciliation dispatched a new account")
		return zitadel.StaffIdentityCreated{}, nil
	}}, inspect: func(_ context.Context, in zitadel.StaffIdentityInput, prepared time.Time) (zitadel.StaffRecoverySubjectProof, error) {
		calls++
		if in.UserID != c.ProviderUserID || in.BindingNonce != c.BindingNonce || in.Email != c.Email || in.OrganizationID != c.ProviderOrgID || !prepared.Equal(c.CreatedAt) {
			t.Error("reconciliation changed frozen source facts")
		}
		return zitadel.StaffRecoverySubjectProof{UserID: in.UserID, OrganizationID: in.OrganizationID, Disposition: "matching", CreatedAt: born, VerifiedAt: time.Now().UTC(), ObjectRevision: "3"}, nil
	}}
	operator := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		if !write {
			t.Error("reconciliation used read-only proof")
		}
		if revoked {
			return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", subject), onboarding.WithStaffRecovery("TEST-console-project", operator))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"console-admin":  {Console: true, Subject: "TEST-operator", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
		"console-reader": {Console: true, Subject: "TEST-reader", OrgID: platformOrg, Roles: []string{authz.RolePlatformOperator}},
	}}})
	path := staffIdentityPath(c) + "/recovery/reconciliations"
	do := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "TEST-reconcile-"+c.ID.String())
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("console-reader"); rec.Code != http.StatusNotFound || calls != 0 {
		t.Fatalf("reader reconciled source: HTTP %d", rec.Code)
	}
	for i := 0; i < 2; i++ {
		rec := do("console-admin")
		if rec.Code != http.StatusOK {
			t.Fatalf("reconcile HTTP %d: %s", rec.Code, rec.Body.String())
		}
		body := decode(t, rec)
		if body["command_id"] != c.ID.String() || body["command_state"] != "created" || body["actor_subject"] != c.ActorSubject || body["user_id"] != c.ProviderUserID || body["created_at"] != born.Format(time.RFC3339Nano) || rec.Header().Get("Idempotent-Replay") != "" {
			t.Fatal("reconciliation changed historical receipt or bypassed fresh authority")
		}
	}
	if calls != 2 {
		t.Fatal("reconciliation retry did not inspect fresh source")
	}
	var machine, actor, user, nonce string
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT machine_subject,actor_subject,provider_user_id,binding_nonce FROM staff_identity_commands WHERE id=$1`, c.ID).Scan(&machine, &actor, &user, &nonce); err != nil || machine != c.MachineSubject || actor != c.ActorSubject || user != c.ProviderUserID || nonce != c.BindingNonce {
		t.Fatal("reconciliation rewrote original command")
	}
	revoked = true
	if rec := do("console-admin"); rec.Code != http.StatusNotFound {
		t.Fatalf("cached receipt bypassed revoked authority: HTTP %d", rec.Code)
	}
}
