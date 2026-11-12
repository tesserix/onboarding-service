package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type recoveryOperatorSource struct {
	verify func(context.Context, string, string, bool) (zitadel.StaffRecoveryOperatorProof, error)
}

func (s recoveryOperatorSource) VerifyStaffRecoveryOperator(ctx context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
	return s.verify(ctx, subject, project, write)
}

func currentRecoveryOperator(subject, project string, write bool) zitadel.StaffRecoveryOperatorProof {
	return zitadel.StaffRecoveryOperatorProof{Subject: subject, OrganizationID: platformOrg, ProjectID: project, GrantID: "TEST-console-grant", Write: write, VerifiedAt: time.Now().UTC()}
}

func TestStaffRecoveryServiceUsesCurrentHumanAndRetainsOriginalCommand(t *testing.T) {
	h := newHarness(t)
	c := preparedStaffIdentity(t, h)
	if _, err := h.fixturePool.Exec(t.Context(), `UPDATE products SET machine_user_id='TEST-current-machine' WHERE key=$1`, c.ProductKey); err != nil {
		t.Fatal(err)
	}
	calls := 0
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		calls++
		if subject != "TEST-current-reviewer" || project != "TEST-console-project" || write {
			t.Error("service fabricated or expanded the operator principal")
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-current-reviewer", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformOperator}}
	got, err := svc.StaffIdentityRecoveryReceipt(t.Context(), p, c.OrgID, c.ID)
	if err != nil || got.CommandID != c.ID || got.ActorSubject != c.ActorSubject || got.CommandState != "prepared" || got.UserID != "" || calls != 2 {
		t.Fatalf("current operator receipt failed: proof_calls=%d err=%v", calls, err)
	}
}

func TestStaffRecoveryServiceDeniesUntrustedCredentialsBeforeSourceIO(t *testing.T) {
	for _, kind := range []string{"legacy", "product", "customer admin", "ordinary user", "roleless", "empty subject"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			c := preparedStaffIdentity(t, h)
			calls := 0
			source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
				calls++
				return currentRecoveryOperator(subject, project, write), nil
			}}
			svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
			p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-current-reviewer", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformAdmin}}
			switch kind {
			case "legacy":
				p.ConsoleAccess = false
			case "product":
				p.Kind = authz.KindProduct
				p.ProductKey = "planning-poker"
				p.Subject = c.MachineSubject
			case "customer admin":
				p.Kind = authz.KindOrgAdmin
				p.OrgID = c.OrgID.String()
			case "ordinary user":
				p.Kind = authz.KindUser
				p.EmailVerified = true
			case "roleless":
				p.Roles = nil
			case "empty subject":
				p.Subject = ""
			}
			got, err := svc.StaffIdentityRecoveryReceipt(t.Context(), p, c.OrgID, c.ID)
			if !errors.Is(err, onboarding.ErrNotFound) || got.CommandID != uuid.Nil || calls != 0 {
				t.Fatalf("untrusted recovery credential reached source/disclosed command: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestStaffRecoveryServiceFailsClosedOnWrongOrWithdrawnCurrentProof(t *testing.T) {
	for _, kind := range []string{"subject", "organization", "project", "missing grant", "write expansion", "stale time", "future time", "outage", "revoked", "revoked after inspection"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			c := preparedStaffIdentity(t, h)
			calls := 0
			source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
				calls++
				proof := currentRecoveryOperator(subject, project, write)
				switch kind {
				case "subject":
					proof.Subject = "TEST-foreign"
				case "organization":
					proof.OrganizationID = "TEST-foreign"
				case "project":
					proof.ProjectID = "TEST-foreign"
				case "missing grant":
					proof.GrantID = ""
				case "write expansion":
					proof.Write = true
				case "stale time":
					proof.VerifiedAt = time.Now().Add(-time.Hour)
				case "future time":
					proof.VerifiedAt = time.Now().Add(time.Hour)
				case "outage":
					return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrIdentityUnavailable
				case "revoked":
					return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
				case "revoked after inspection":
					if calls == 2 {
						return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
					}
				}
				return proof, nil
			}}
			svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
			p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-current-reviewer", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformOperator}}
			want := onboarding.ErrUnavailable
			if kind == "revoked" || kind == "revoked after inspection" {
				want = onboarding.ErrNotFound
			}
			got, err := svc.StaffIdentityRecoveryReceipt(t.Context(), p, c.OrgID, c.ID)
			if !errors.Is(err, want) || got.CommandID != uuid.Nil {
				t.Fatalf("invalid source authority released command: %v", err)
			}
			var count int
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE target=$1 AND action='staff.identity.recovery.read' AND outcome='success'`, c.ID.String()).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed final disclosure retained success: count=%d err=%v", count, err)
			}
		})
	}
}

func TestStaffRecoveryServiceRechecksRegistryAndTenantAfterSourceIO(t *testing.T) {
	for _, kind := range []string{"foreign tenant", "foreign command", "grant withdrawn during final proof", "machine changed during final proof", "org changed during final proof", "source inactive", "source unavailable", "source wrong scope"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			c := preparedStaffIdentity(t, h)
			calls := 0
			h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
				calls++
				proof := identityObservation(org, tenant, project)
				if calls == 2 {
					switch kind {
					case "grant withdrawn during final proof":
						if err := h.store.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
							t.Fatal(err)
						}
					case "machine changed during final proof":
						if _, err := h.fixturePool.Exec(t.Context(), `UPDATE products SET machine_user_id='TEST-rotated' WHERE key=$1`, c.ProductKey); err != nil {
							t.Fatal(err)
						}
					case "org changed during final proof":
						if _, err := h.fixturePool.Exec(t.Context(), `UPDATE organizations SET zitadel_org_id='TEST-remapped' WHERE id=$1`, c.OrgID); err != nil {
							t.Fatal(err)
						}
					}
				}
				switch kind {
				case "source inactive":
					proof.State = "inactive"
				case "source unavailable":
					return zitadel.OrganizationIdentity{}, zitadel.ErrIdentityUnavailable
				case "source wrong scope":
					proof.ProjectID = "TEST-foreign"
				}
				return proof, nil
			}
			source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
				return currentRecoveryOperator(subject, project, write), nil
			}}
			svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
			p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-current-reviewer", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformOperator}}
			tenant, command := c.OrgID, c.ID
			if kind == "foreign tenant" {
				tenant = uuid.New()
			}
			if kind == "foreign command" {
				command = uuid.Must(uuid.NewV7())
			}
			got, err := svc.StaffIdentityRecoveryReceipt(t.Context(), p, tenant, command)
			if err == nil || got.CommandID != uuid.Nil {
				t.Fatalf("changed target scope released command: %v", err)
			}
		})
	}
}

func TestStaffRecoveryHTTPReadUsesCurrentConsoleAuthorityAndMinimizesReceipt(t *testing.T) {
	h := newHarness(t)
	c := preparedStaffIdentity(t, h)
	proofCalls := 0
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		proofCalls++
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{claims: map[string]auth.Claims{
		productToken:   {Subject: c.MachineSubject},
		"legacy-token": {Subject: "TEST-reviewer", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
	}}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"console-reader": {Console: true, Subject: "TEST-reviewer", OrgID: platformOrg, Roles: []string{authz.RolePlatformOperator}},
	}}})
	path := staffIdentityPath(c) + "/recovery"
	rec := h.do(t, http.MethodGet, path, "console-reader", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("console recovery read HTTP %d: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["command_id"] != c.ID.String() || body["tenant_id"] != c.OrgID.String() || body["actor_subject"] != c.ActorSubject || body["command_state"] != "prepared" || proofCalls != 2 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("recovery receipt lost current authority or immutable facts")
	}
	for _, private := range []string{c.Email, c.BindingNonce, c.GivenName, c.FamilyName, c.Reason, c.ProviderUserID, c.MachineSubject, c.ProviderOrgID, c.ProviderProjectID} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("recovery receipt disclosed private input or source configuration")
		}
	}
	for _, token := range []string{productToken, "legacy-token"} {
		if rec := h.do(t, http.MethodGet, path, token, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("non-console recovery read HTTP %d", rec.Code)
		}
	}
	for _, bad := range []struct{ suffix, body string }{{"?actor_subject=TEST-reviewer", ""}, {"?", ""}, {"?unknown=1", ""}, {"", "{}"}} {
		if rec := h.do(t, http.MethodGet, path+bad.suffix, "console-reader", bad.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("asserted scope/body accepted: HTTP %d", rec.Code)
		}
	}
}

func TestStaffRecoveryProposalDerivesMakerAndRetainsExactRetry(t *testing.T) {
	h := newHarness(t)
	c := preparedStaffIdentity(t, h)
	if claimed, err := h.store.ClaimStaffIdentityDispatch(t.Context(), c, audit.Event{ActorType: audit.ActorProduct, ActorID: c.MachineSubject, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.dispatch", Outcome: audit.OutcomeSuccess}); err != nil || !claimed {
		t.Fatalf("fixture dispatch: %v", err)
	}
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		if !write {
			t.Error("recovery proposal used read-only proof")
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-current-maker", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformAdmin}}
	id := uuid.Must(uuid.NewV7())
	t.Cleanup(func() {
		if _, err := h.fixturePool.Exec(context.Background(), `DELETE FROM staff_identity_recovery_requests WHERE id=$1`, id); err != nil {
			t.Error(err)
		}
	})
	in := onboarding.StaffIdentityRecoveryProposal{Action: "close_unresolved", Reason: "Investigated isolated uncertain dispatch", EvidenceReference: "QA:RECOVERY/TEST-1"}
	first, err := svc.ProposeStaffIdentityRecovery(t.Context(), p, c.OrgID, c.ID, id, in)
	if err != nil || first.RequestID != id || first.CommandID != c.ID || first.TenantID != c.OrgID || first.Action != in.Action || first.RecordedAt.IsZero() {
		t.Fatalf("proposal receipt lost immutable request: %v", err)
	}
	second, err := svc.ProposeStaffIdentityRecovery(t.Context(), p, c.OrgID, c.ID, id, in)
	if err != nil || second != first {
		t.Fatalf("proposal retry changed frozen result: %v", err)
	}
	var maker, actor, user string
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT requested_by,original_actor_subject,original_user_id FROM staff_identity_recovery_requests WHERE id=$1`, id).Scan(&maker, &actor, &user); err != nil || maker != p.Subject || actor != c.ActorSubject || user != c.ProviderUserID {
		t.Fatalf("proposal fabricated actor/target: %v", err)
	}
	in.Reason = "Changed proposal"
	if _, err := svc.ProposeStaffIdentityRecovery(t.Context(), p, c.OrgID, c.ID, id, in); !errors.Is(err, onboarding.ErrConflict) {
		t.Fatalf("changed input accepted: %v", err)
	}
}

func TestStaffRecoveryHTTPProposalNeverUsesReplayCacheAuthority(t *testing.T) {
	h := newHarness(t)
	c := preparedStaffIdentity(t, h)
	if claimed, err := h.store.ClaimStaffIdentityDispatch(t.Context(), c, audit.Event{ActorType: audit.ActorProduct, ActorID: c.MachineSubject, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.dispatch", Outcome: audit.OutcomeSuccess}); err != nil || !claimed {
		t.Fatalf("fixture dispatch: %v", err)
	}
	revoked := false
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		if revoked {
			return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"console-maker":  {Console: true, Subject: "TEST-current-maker", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
		"console-reader": {Console: true, Subject: "TEST-current-reader", OrgID: platformOrg, Roles: []string{authz.RolePlatformOperator}},
	}}})
	id := uuid.Must(uuid.NewV7())
	t.Cleanup(func() {
		if _, err := h.fixturePool.Exec(context.Background(), `DELETE FROM staff_identity_recovery_requests WHERE id=$1`, id); err != nil {
			t.Error(err)
		}
	})
	path := staffIdentityPath(c) + "/recovery/requests/" + id.String()
	body := `{"requested_action":"close_unresolved","reason":"Investigated local uncertain dispatch","evidence_reference":"QA:RECOVERY/HTTP-1"}`
	do := func(token, raw string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "TEST-recovery-proposal-"+id.String())
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("console-maker", body); rec.Code != http.StatusOK || decode(t, rec)["request_id"] != id.String() {
		t.Fatalf("proposal HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do("console-reader", body); rec.Code != http.StatusNotFound {
		t.Fatalf("reader proposed recovery: HTTP %d", rec.Code)
	}
	revoked = true
	if rec := do("console-maker", body); rec.Code != http.StatusNotFound || rec.Header().Get("Idempotent-Replay") != "" {
		t.Fatalf("cached response bypassed revoked authority: HTTP %d replay=%q", rec.Code, rec.Header().Get("Idempotent-Replay"))
	}
	var cache int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM idempotency_keys WHERE actor=$1 AND key=$2`, "TEST-current-maker", "TEST-recovery-proposal-"+id.String()).Scan(&cache); err != nil || cache != 0 {
		t.Fatalf("recovery result retained in generic replay cache: %d %v", cache, err)
	}
}

func TestStaffRecoveryHTTPProposalRejectsAssertedAuthorityAndMalformedEvidence(t *testing.T) {
	h := newHarness(t)
	c := preparedStaffIdentity(t, h)
	calls := 0
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		calls++
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	h.router = NewRouter(Deps{Service: svc, Store: h.store, PlatformOrgID: platformOrg, Tokens: stubTokens{}, ConsoleAccessTokens: stubTokens{claims: map[string]auth.Claims{
		"console-maker": {Console: true, Subject: "TEST-current-maker", OrgID: platformOrg, Roles: []string{authz.RolePlatformAdmin}},
	}}})
	base := `{"requested_action":"close_unresolved","reason":"Investigated local dispatch","evidence_reference":"QA:RECOVERY/HTTP-1"}`
	for _, tc := range []struct{ name, raw, suffix string }{
		{"asserted maker", strings.Replace(base, `"reason":`, `"requested_by":"TEST-foreign","reason":`, 1), ""},
		{"asserted reviewer", strings.Replace(base, `"reason":`, `"reviewed_by":"TEST-foreign","reason":`, 1), ""},
		{"credential", strings.Replace(base, `"reason":`, `"password":"PRIVATE","reason":`, 1), ""},
		{"duplicate", strings.Replace(base, `"reason":`, `"reason":"Other","reason":`, 1), ""},
		{"casefold", strings.Replace(base, `"reason":`, `"Reason":"Other","reason":`, 1), ""},
		{"null evidence", strings.Replace(base, `"QA:RECOVERY/HTTP-1"`, `null`, 1), ""},
		{"nested evidence", strings.Replace(base, `"QA:RECOVERY/HTTP-1"`, `{"ref":"QA:RECOVERY/HTTP-1"}`, 1), ""},
		{"blank reason", strings.Replace(base, "Investigated local dispatch", "   ", 1), ""},
		{"control reason", strings.Replace(base, "Investigated local dispatch", `Line\nTwo`, 1), ""},
		{"oversized", strings.Replace(base, "Investigated local dispatch", strings.Repeat("X", 4100), 1), ""},
		{"bad evidence", strings.Replace(base, "QA:RECOVERY/HTTP-1", "PRIVATE evidence body", 1), ""},
		{"missing successor actor", strings.Replace(base, "close_unresolved", "replace", 1), ""},
		{"unexpected successor actor", strings.Replace(base, `"reason":`, `"replacement_actor_subject":"TEST-new-maker","reason":`, 1), ""},
		{"unsupported action", strings.Replace(base, "close_unresolved", "approve", 1), ""},
		{"query override", base, "?requested_by=TEST-foreign"},
		{"empty query", base, "?"},
		{"invalid UTF8", strings.Replace(base, "Investigated local dispatch", string([]byte{0xff}), 1), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := staffIdentityPath(c) + "/recovery/requests/" + uuid.Must(uuid.NewV7()).String() + tc.suffix
			if rec := h.do(t, http.MethodPut, path, "console-maker", tc.raw); rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid proposal accepted: HTTP %d", rec.Code)
			}
		})
	}
	if calls != 0 {
		t.Fatal("invalid proposal reached authority source")
	}
}

func TestStaffRecoveryProposalRetainsCommittedRequestWhenAuthorityWithdrawsBeforeDisclosure(t *testing.T) {
	h := newHarness(t)
	c := preparedStaffIdentity(t, h)
	if claimed, err := h.store.ClaimStaffIdentityDispatch(t.Context(), c, audit.Event{ActorType: audit.ActorProduct, ActorID: c.MachineSubject, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.dispatch", Outcome: audit.OutcomeSuccess}); err != nil || !claimed {
		t.Fatalf("fixture dispatch: %v", err)
	}
	id := uuid.Must(uuid.NewV7())
	t.Cleanup(func() {
		if _, err := h.fixturePool.Exec(context.Background(), `DELETE FROM staff_identity_recovery_requests WHERE id=$1`, id); err != nil {
			t.Error(err)
		}
	})
	calls := 0
	withdraw := true
	source := recoveryOperatorSource{verify: func(_ context.Context, subject, project string, write bool) (zitadel.StaffRecoveryOperatorProof, error) {
		calls++
		if withdraw && calls == 3 {
			return zitadel.StaffRecoveryOperatorProof{}, zitadel.ErrStaffOperatorDenied
		}
		return currentRecoveryOperator(subject, project, write), nil
	}}
	svc := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}), onboarding.WithStaffRecovery("TEST-console-project", source))
	p := authz.Principal{Kind: authz.KindPlatform, Subject: "TEST-current-maker", ExternalOrgID: platformOrg, ConsoleAccess: true, Roles: []string{authz.RolePlatformAdmin}}
	in := onboarding.StaffIdentityRecoveryProposal{Action: "replace", Reason: "Investigated retained uncertain dispatch", EvidenceReference: "QA:RECOVERY/RESTORE-1", ReplacementActorSubject: "TEST-next-initiator"}
	got, err := svc.ProposeStaffIdentityRecovery(t.Context(), p, c.OrgID, c.ID, id, in)
	if !errors.Is(err, onboarding.ErrNotFound) || got.RequestID != uuid.Nil {
		t.Fatalf("withdrawn maker received proposal receipt: %v", err)
	}
	var count int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_recovery_requests WHERE id=$1 AND requested_by=$2`, id, p.Subject).Scan(&count); err != nil || count != 1 {
		t.Fatalf("committed proposal lost after withdrawal: %v", err)
	}
	withdraw = false
	got, err = svc.ProposeStaffIdentityRecovery(t.Context(), p, c.OrgID, c.ID, id, in)
	if err != nil || got.RequestID != id {
		t.Fatalf("restored maker could not recover exact proposal: %v", err)
	}
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_replacements WHERE parent_command_id=$1`, c.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("proposal granted a replacement without independent review")
	}
}
