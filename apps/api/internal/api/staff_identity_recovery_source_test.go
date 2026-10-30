package api

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type recoverySubjectSource struct {
	staffSource
	inspect func(context.Context, zitadel.StaffIdentityInput, time.Time) (zitadel.StaffRecoverySubjectProof, error)
}

func (s recoverySubjectSource) InspectStaffRecoverySubject(ctx context.Context, in zitadel.StaffIdentityInput, at time.Time) (zitadel.StaffRecoverySubjectProof, error) {
	return s.inspect(ctx, in, at)
}

func TestStaffRecoverySourceViewUsesFrozenFactsAndNeverCachesCurrentAuthority(t *testing.T) {
	h, c, _, _ := recoveryReviewFixture(t)
	disposition := "absent"
	revoked := false
	calls := 0
	subjectSource := recoverySubjectSource{staffSource: staffSource{create: func(context.Context, zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		t.Fatal("source observation created an account")
		return zitadel.StaffIdentityCreated{}, nil
	}}, inspect: func(_ context.Context, in zitadel.StaffIdentityInput, prepared time.Time) (zitadel.StaffRecoverySubjectProof, error) {
		calls++
		if in.UserID != c.ProviderUserID || in.OrganizationID != c.ProviderOrgID || in.Email != c.Email || in.GivenName != c.GivenName || in.FamilyName != c.FamilyName || in.BindingNonce != c.BindingNonce || !prepared.Equal(c.CreatedAt) {
			t.Error("source view accepted caller-supplied target facts")
		}
		proof := zitadel.StaffRecoverySubjectProof{UserID: in.UserID, OrganizationID: in.OrganizationID, Disposition: disposition, VerifiedAt: time.Now().UTC()}
		if disposition != "absent" {
			proof.CreatedAt = c.CreatedAt
			proof.ObjectRevision = "3"
		}
		return proof, nil
	}}
	operator := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		if revoked {
			return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", subjectSource), onboarding.WithStaffRecovery("TEST-console-project", operator))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"console-reader": {Console: true, Subject: "TEST-reader", OrgID: platformOrg, Roles: []string{authz.RolePlatformOperator}},
	}}})
	path := staffIdentityPath(c) + "/recovery/observations"
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer console-reader")
		req.Header.Set("Idempotency-Key", "TEST-source-view-"+c.ID.String())
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}
	rec := do()
	if rec.Code != http.StatusOK || decode(t, rec)["source_disposition"] != "absent" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("source view HTTP %d: %s", rec.Code, rec.Body.String())
	}
	for _, private := range []string{c.Email, c.ProviderUserID, c.ProviderOrgID, c.BindingNonce, c.MachineSubject} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("source view disclosed private binding")
		}
	}
	disposition = "matching"
	rec = do()
	if rec.Code != http.StatusOK || decode(t, rec)["source_disposition"] != "matching" || calls != 2 || rec.Header().Get("Idempotent-Replay") != "" {
		t.Fatalf("source view replay hid changed facts: HTTP %d calls=%d", rec.Code, calls)
	}
	for _, bad := range []struct{ suffix, body string }{{"?source_disposition=absent", ""}, {"?", ""}, {"", "{}"}, {"", `{"actor_subject":"TEST-reader"}`}} {
		if rec := h.do(t, http.MethodPost, path+bad.suffix, "console-reader", bad.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("asserted observation input accepted: HTTP %d", rec.Code)
		}
	}
	if calls != 2 {
		t.Fatal("invalid observation reached source")
	}
	revoked = true
	if rec = do(); rec.Code != http.StatusNotFound {
		t.Fatalf("cached source view bypassed revoked authority: HTTP %d", rec.Code)
	}
	var creations, decisions, replacements int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM staff_identity_creations WHERE command_id=$1),(SELECT count(*) FROM staff_identity_recovery_decisions WHERE command_id=$1),(SELECT count(*) FROM staff_identity_replacements WHERE parent_command_id=$1)`, c.ID).Scan(&creations, &decisions, &replacements); err != nil || creations != 0 || decisions != 0 || replacements != 0 {
		t.Fatal("source view fabricated a receipt, approval or successor")
	}
}

func TestStaffRecoverySourceViewRejectsUnboundStaleAndWithdrawnProof(t *testing.T) {
	for _, kind := range []string{"wrong account", "wrong organization", "unknown disposition", "stale proof", "future proof", "absent with birth", "absent with revision", "missing birth", "ancient birth", "missing revision", "noncanonical revision", "source failure", "authority revoked after source", "entitlement revoked after source"} {
		t.Run(kind, func(t *testing.T) {
			h, c, _, _ := recoveryReviewFixture(t)
			revoked := false
			subject := recoverySubjectSource{inspect: func(_ context.Context, in zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffRecoverySubjectProof, error) {
				proof := zitadel.StaffRecoverySubjectProof{UserID: in.UserID, OrganizationID: in.OrganizationID, Disposition: "matching", CreatedAt: c.CreatedAt, VerifiedAt: time.Now().UTC(), ObjectRevision: "3"}
				switch kind {
				case "wrong account":
					proof.UserID = "TEST-other"
				case "wrong organization":
					proof.OrganizationID = "TEST-other"
				case "unknown disposition":
					proof.Disposition = "eligible"
				case "stale proof":
					proof.VerifiedAt = proof.VerifiedAt.Add(-time.Hour)
				case "future proof":
					proof.VerifiedAt = proof.VerifiedAt.Add(time.Hour)
				case "absent with birth":
					proof.Disposition = "absent"
					proof.ObjectRevision = ""
				case "absent with revision":
					proof.Disposition = "absent"
					proof.CreatedAt = time.Time{}
				case "missing birth":
					proof.CreatedAt = time.Time{}
				case "ancient birth":
					proof.CreatedAt = c.CreatedAt.Add(-time.Hour)
				case "missing revision":
					proof.ObjectRevision = ""
				case "noncanonical revision":
					proof.ObjectRevision = "03"
				case "source failure":
					return zitadel.StaffRecoverySubjectProof{}, zitadel.ErrIdentityUnavailable
				case "authority revoked after source":
					revoked = true
				case "entitlement revoked after source":
					if err := h.store.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
						t.Fatal(err)
					}
				}
				return proof, nil
			}}
			operator := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
				if revoked {
					return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
				}
				return currentRecoveryOperator(subject, project, write), nil
			}}
			svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", subject), onboarding.WithStaffRecovery("TEST-console-project", operator))
			p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-reader", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformOperator}}
			got, err := svc.ObserveStaffIdentityRecovery(t.Context(), p, c.OrgID, c.ID)
			want := onboarding.ErrUnavailable
			if strings.Contains(kind, "revoked after source") {
				want = onboarding.ErrNotFound
			}
			if !errors.Is(err, want) || got.CommandID != uuid.Nil || got.Disposition != "" || got.SourceCreatedAt != nil {
				t.Fatalf("invalid source proof released facts: %v", err)
			}
			var count int
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE target=$1 AND action='staff.identity.recovery.source.observe' AND outcome='success'`, c.ID.String()).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed observation retained success audit: %d %v", count, err)
			}
		})
	}
}
