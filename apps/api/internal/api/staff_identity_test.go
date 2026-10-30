package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type staffSource struct {
	observe func(context.Context, zitadel.StaffIdentityInput, time.Time) (zitadel.StaffIdentityObservation, error)
}

func (s staffSource) ObserveStaffIdentity(ctx context.Context, in zitadel.StaffIdentityInput, created time.Time) (zitadel.StaffIdentityObservation, error) {
	if s.observe == nil {
		return zitadel.StaffIdentityObservation{}, zitadel.ErrIdentityUnavailable
	}
	return s.observe(ctx, in, created)
}

func preparedStaffIdentity(t *testing.T, h *harness) store.StaffIdentityCommand {
	t.Helper()
	tenant := identityOrg(t, h)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"staff_identity_creations", "staff_identity_dispatches", "staff_identity_commands"} {
			column := "command_id"
			if table == "staff_identity_commands" {
				column = "id"
			}
			if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+table+" WHERE "+column+"=$1", id); err != nil {
				t.Errorf("remove disposable Staff fixture: %v", err)
			}
		}
	})
	command := store.StaffIdentityCommand{ID: id, OrgID: tenant, ProductKey: "planning-poker",
		MachineSubject: "svc-planning-poker", ActorSubject: "TEST-admin",
		ProviderOrgID: "org-identity", ProviderProjectID: "proj-planning-poker",
		ProviderUserID: "TEST-staff-account", BindingNonce: "TEST-binding-nonce",
		Email: "synthetic@example.invalid", GivenName: "Synthetic", FamilyName: "Staff",
		Reason: "Authorized synthetic Staff identity setup"}
	event := audit.Event{ActorType: audit.ActorProduct, ActorID: command.MachineSubject,
		ProductKey: command.ProductKey, OrgID: tenant.String(), Action: "staff.identity.prepare",
		Target: id.String(), Outcome: audit.OutcomeSuccess, RequestID: id.String()}
	prepared, err := h.store.PrepareStaffIdentity(context.Background(), command, event)
	if err != nil {
		t.Fatal(err)
	}
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		return identityObservation(org, tenant, project), nil
	}
	return prepared
}

func staffIdentityPath(c store.StaffIdentityCommand) string {
	return "/v1/orgs/" + c.OrgID.String() + "/staff-identities/" + c.ID.String()
}

func dispatchedStaffIdentity(t *testing.T, h *harness) store.StaffIdentityCommand {
	t.Helper()
	command := preparedStaffIdentity(t, h)
	event := audit.Event{ActorType: audit.ActorProduct, ActorID: command.MachineSubject, ProductKey: command.ProductKey,
		OrgID: command.OrgID.String(), Target: command.ID.String(), Action: "staff.identity.dispatch",
		Outcome: audit.OutcomeSuccess, RequestID: command.ID.String()}
	if claimed, err := h.store.ClaimStaffIdentityDispatch(context.Background(), command, event); err != nil || !claimed {
		t.Fatalf("synthetic dispatch setup: %v", err)
	}
	return command
}

func TestStaffIdentityReceiptReadIsCurrentScopedAuditedAndMinimized(t *testing.T) {
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", staffSource{}))
	command := preparedStaffIdentity(t, h)
	rec := h.do(t, http.MethodGet, staffIdentityPath(command)+"?actor_subject=TEST-admin", productToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("protected receipt: status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["command_id"] != command.ID.String() || body["tenant_id"] != command.OrgID.String() || body["product_key"] != command.ProductKey || body["command_state"] != "prepared" || body["schema_version"] != float64(1) {
		t.Fatal("receipt does not identify the exact prepared command")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private receipt may be cached")
	}
	for _, private := range []string{command.Email, command.BindingNonce, command.ProviderUserID, command.GivenName, command.Reason} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("prepared receipt disclosed private or unconfirmed identity facts")
		}
	}
	records, err := h.store.AuditSearch(context.Background(), store.AuditFilter{OrgID: &command.OrgID, Action: "staff.identity.read"}, 10)
	if err != nil || len(records) != 1 || records[0].Target != command.ID.String() {
		t.Fatalf("private receipt disclosure lacks audit: count=%d err=%v", len(records), err)
	}
}

func TestStaffIdentityDenialAuditRemainsBoundedAfterCallerCancellation(t *testing.T) {
	h := newHarness(t, onboarding.WithStaffIdentity("mark8ly", staffSource{}))
	command := preparedStaffIdentity(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observer, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	locker, err := h.fixturePool.Acquire(observer)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Release()
	lock, err := locker.Begin(observer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(observer, "LOCK TABLE audit_events IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, staffIdentityPath(command)+"?actor_subject=TEST-admin", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+productToken)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, request)
		finished <- rec
	}()
	for {
		var waiting bool
		if err := h.fixturePool.QueryRow(observer, `SELECT EXISTS (
 SELECT FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, locker.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		runtime.Gosched()
	}
	cancel()
	select {
	case rec := <-finished:
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cancelled denial released an unexpected response: %d", rec.Code)
		}
	case <-time.After(4 * time.Second):
		if err := lock.Rollback(observer); err != nil {
			t.Fatal(err)
		}
		<-finished
		t.Fatal("denial audit waited indefinitely after caller cancellation")
	}
}

func TestStaffIdentityReceiptDeniesWrongScopeAndMalformedReads(t *testing.T) {
	for _, kind := range []string{"unauthenticated", "human", "other product", "wrong configured product", "unconfigured", "wrong actor", "foreign tenant", "unknown command", "withdrawn entitlement", "inactive source", "unavailable source", "missing actor", "repeated actor", "extra query", "malformed query", "GET body", "non-v7 command"} {
		t.Run(kind, func(t *testing.T) {
			configured := "planning-poker"
			if kind == "wrong configured product" {
				configured = "hms"
			}
			if kind == "unconfigured" {
				configured = ""
			}
			h := newHarness(t, onboarding.WithStaffIdentity(configured, staffSource{}))
			command := preparedStaffIdentity(t, h)
			token, body := productToken, ""
			path := staffIdentityPath(command) + "?actor_subject=TEST-admin"
			want := http.StatusNotFound
			switch kind {
			case "unauthenticated":
				token, want = "", http.StatusUnauthorized
			case "human":
				token = platformToken
			case "other product":
				token = otherToken
			case "unconfigured":
				want = http.StatusServiceUnavailable
			case "wrong actor":
				path = staffIdentityPath(command) + "?actor_subject=TEST-other-admin"
			case "foreign tenant":
				path = strings.Replace(path, command.OrgID.String(), uuid.NewString(), 1)
			case "unknown command":
				unknown, err := uuid.NewV7()
				if err != nil {
					t.Fatal(err)
				}
				path = strings.Replace(path, command.ID.String(), unknown.String(), 1)
			case "withdrawn entitlement":
				if err := h.store.RevokeGrant(context.Background(), command.OrgID, command.ProductKey); err != nil {
					t.Fatal(err)
				}
			case "inactive source":
				h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
					proof := identityObservation(org, tenant, project)
					proof.State = "inactive"
					return proof, nil
				}
			case "unavailable source":
				want = http.StatusServiceUnavailable
				h.identity.verify = func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error) {
					return zitadel.OrganizationIdentity{}, zitadel.ErrIdentityUnavailable
				}
			case "missing actor":
				path, want = staffIdentityPath(command), http.StatusBadRequest
			case "repeated actor":
				path, want = path+"&actor_subject=TEST-other-admin", http.StatusBadRequest
			case "extra query":
				path, want = path+"&product_key=hms", http.StatusBadRequest
			case "malformed query":
				path, want = path+"&other=%ZZ", http.StatusBadRequest
			case "GET body":
				body, want = `{}`, http.StatusBadRequest
			case "non-v7 command":
				path, want = strings.Replace(path, command.ID.String(), uuid.NewString(), 1), http.StatusBadRequest
			}
			rec := h.do(t, http.MethodGet, path, token, body)
			if rec.Code != want {
				t.Fatalf("protected receipt denial: status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
			}
			for _, private := range []string{command.Email, command.ProviderUserID, command.BindingNonce, command.Reason} {
				if strings.Contains(rec.Body.String(), private) {
					t.Fatal("denied receipt disclosed private or unconfirmed identity facts")
				}
			}
			if want == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), "no change was made") {
				t.Fatal("unknown command result was reported as no change")
			}
		})
	}
}

func TestStaffIdentityObservationReconcilesOnlyItsDispatchedAccount(t *testing.T) {
	var command store.StaffIdentityCommand
	calls := 0
	source := staffSource{observe: func(_ context.Context, in zitadel.StaffIdentityInput, created time.Time) (zitadel.StaffIdentityObservation, error) {
		calls++
		if in.UserID != command.ProviderUserID || in.OrganizationID != command.ProviderOrgID || in.Email != command.Email || in.BindingNonce != command.BindingNonce || !created.IsZero() {
			t.Error("observation did not use the original immutable command")
		}
		return zitadel.StaffIdentityObservation{UserID: command.ProviderUserID, OrganizationID: command.ProviderOrgID,
			State: "active", EmailVerified: true, CreatedAt: command.CreatedAt,
			ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
	}}
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
	command = dispatchedStaffIdentity(t, h)
	rec := h.do(t, http.MethodPost, staffIdentityPath(command)+"/observations", productToken, `{"actor_subject":"TEST-admin"}`)
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("dispatched account observation: status=%d calls=%d body=%s", rec.Code, calls, rec.Body.String())
	}
	body := decode(t, rec)
	receipt, ok := body["receipt"].(map[string]any)
	if !ok || receipt["command_state"] != "created" || receipt["user_id"] != command.ProviderUserID {
		t.Fatal("matching source proof did not retain a creation receipt")
	}
	observation, ok := body["observation"].(map[string]any)
	if !ok || observation["state"] != "active" || observation["email_verified"] != true || observation["user_revision"] != "101" || observation["metadata_revision"] != "102" {
		t.Fatal("fresh source observation was not returned")
	}
	for _, private := range []string{command.Email, command.BindingNonce, command.GivenName, command.Reason} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("observation disclosed private identity evidence")
		}
	}
	get := h.do(t, http.MethodGet, staffIdentityPath(command)+"?actor_subject=TEST-admin", productToken, "")
	if get.Code != http.StatusOK || decode(t, get)["command_state"] != "created" || calls != 1 {
		t.Fatal("receipt persistence depended on repeating the provider operation")
	}
	records, err := h.store.AuditSearch(context.Background(), store.AuditFilter{OrgID: &command.OrgID, Action: "staff.identity.observe"}, 10)
	if err != nil || len(records) != 1 || !strings.Contains(records[0].Detail, `"user_revision":"101"`) {
		t.Fatalf("observation disclosure lacks source evidence audit: count=%d err=%v", len(records), err)
	}
}

func TestStaffIdentityObservationNeverReplaysCachedAuthority(t *testing.T) {
	var command store.StaffIdentityCommand
	verified, calls := true, 0
	source := staffSource{observe: func(_ context.Context, _ zitadel.StaffIdentityInput, created time.Time) (zitadel.StaffIdentityObservation, error) {
		calls++
		if calls > 1 && !created.Equal(command.CreatedAt) {
			t.Error("retained creation time was not rechecked")
		}
		return zitadel.StaffIdentityObservation{UserID: command.ProviderUserID, OrganizationID: command.ProviderOrgID,
			State: "active", EmailVerified: verified, CreatedAt: command.CreatedAt,
			ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
	}}
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
	command = dispatchedStaffIdentity(t, h)
	observe := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, staffIdentityPath(command)+"/observations", strings.NewReader(`{"actor_subject":"TEST-admin"}`))
		req.Header.Set("Authorization", "Bearer "+productToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "TEST-same-observation-key")
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}
	if rec := observe(); rec.Code != http.StatusOK {
		t.Fatalf("initial observation: %d %s", rec.Code, rec.Body.String())
	}
	verified = false
	rec := observe()
	if rec.Code != http.StatusOK || calls != 2 || decode(t, rec)["observation"].(map[string]any)["email_verified"] != false {
		t.Errorf("observation reused an earlier verification result: status=%d source calls=%d", rec.Code, calls)
	}
	if err := h.store.RevokeGrant(context.Background(), command.OrgID, command.ProductKey); err != nil {
		t.Fatal(err)
	}
	denied := observe()
	if denied.Code != http.StatusNotFound || calls != 2 {
		t.Errorf("cached receipt bypassed withdrawn entitlement: status=%d source calls=%d", denied.Code, calls)
	}
}

func TestStaffIdentityObservationRejectsInvalidProofAndMidflightWithdrawal(t *testing.T) {
	for _, kind := range []string{"wrong account", "wrong owner", "stale observation", "old creation", "unknown state", "missing revision", "source unavailable", "withdrawn during source", "missing account", "inactive account"} {
		t.Run(kind, func(t *testing.T) {
			var command store.StaffIdentityCommand
			var h *harness
			source := staffSource{observe: func(ctx context.Context, _ zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffIdentityObservation, error) {
				proof := zitadel.StaffIdentityObservation{UserID: command.ProviderUserID, OrganizationID: command.ProviderOrgID,
					State: "active", EmailVerified: true, CreatedAt: command.CreatedAt,
					ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}
				switch kind {
				case "wrong account":
					proof.UserID = "TEST-other-account"
				case "wrong owner":
					proof.OrganizationID = "TEST-other-owner"
				case "stale observation":
					proof.ObservedAt = time.Now().Add(-time.Minute)
				case "old creation":
					proof.CreatedAt = command.CreatedAt.Add(-2 * time.Minute)
				case "unknown state":
					proof.State = "unknown"
				case "missing revision":
					proof.MetadataRevision = ""
				case "source unavailable":
					return proof, zitadel.ErrIdentityUnavailable
				case "withdrawn during source":
					if err := h.store.RevokeGrant(ctx, command.OrgID, command.ProductKey); err != nil {
						t.Error(err)
					}
				case "missing account":
					return zitadel.StaffIdentityObservation{}, zitadel.ErrIdentityMissing
				case "inactive account":
					proof.State = "inactive"
				}
				return proof, nil
			}}
			h = newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
			command = dispatchedStaffIdentity(t, h)
			rec := h.do(t, http.MethodPost, staffIdentityPath(command)+"/observations", productToken, `{"actor_subject":"TEST-admin"}`)
			want := http.StatusServiceUnavailable
			if kind == "withdrawn during source" {
				want = http.StatusNotFound
			}
			if kind == "missing account" || kind == "inactive account" {
				want = http.StatusOK
			}
			if rec.Code != want {
				t.Fatalf("observation boundary: status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
			}
			if kind == "missing account" || kind == "inactive account" {
				observation := decode(t, rec)["observation"].(map[string]any)
				state := "missing"
				if kind == "inactive account" {
					state = "inactive"
				}
				if observation["state"] != state {
					t.Fatal("unavailable identity was promoted to active")
				}
			}
			var creations int
			if err := h.fixturePool.QueryRow(context.Background(), `SELECT count(*) FROM staff_identity_creations WHERE command_id=$1`, command.ID).Scan(&creations); err != nil {
				t.Fatal(err)
			}
			if kind != "inactive account" && creations != 0 {
				t.Fatal("invalid or withdrawn proof fabricated a creation receipt")
			}
			if kind == "inactive account" && creations != 1 {
				t.Fatal("confirmed historical creation was confused with current eligibility")
			}
			if rec.Code != http.StatusOK && strings.Contains(rec.Body.String(), command.ProviderUserID) {
				t.Fatal("denied proof disclosed its private provider account")
			}
		})
	}
}

func TestStaffIdentityObservationRequiresClosedActorInputAndDispatch(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"actor_subject":null}`, `{"actor_subject":"TEST-admin","actor_subject":"TEST-admin"}`, `{"Actor_Subject":"TEST-admin"}`, `{"actor_subject":"TEST-admin","email":"other@example.invalid"}`, `{"actor_subject":"TEST-admin"}{}`, `{"actor_subject":"TEST-other-admin"}`, `{"actor_subject":"TEST-admin"}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			source := staffSource{observe: func(context.Context, zitadel.StaffIdentityInput, time.Time) (zitadel.StaffIdentityObservation, error) {
				calls++
				return zitadel.StaffIdentityObservation{}, zitadel.ErrIdentityUnavailable
			}}
			h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
			command := preparedStaffIdentity(t, h)
			want := http.StatusBadRequest
			if body == `{"actor_subject":"TEST-other-admin"}` {
				want = http.StatusNotFound
			}
			if body == `{"actor_subject":"TEST-admin"}` {
				want = http.StatusConflict
			}
			rec := h.do(t, http.MethodPost, staffIdentityPath(command)+"/observations", productToken, body)
			if rec.Code != want || calls != 0 {
				t.Fatalf("invalid observation reached source: status=%d want=%d calls=%d", rec.Code, want, calls)
			}
		})
	}
}
