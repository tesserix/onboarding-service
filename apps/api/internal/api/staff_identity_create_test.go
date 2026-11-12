package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func staffCreationFixture(t *testing.T, source staffSource) (*harness, store.StaffIdentityCommand, string) {
	t.Helper()
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", source))
	command := store.StaffIdentityCommand{ID: uuid.Must(uuid.NewV7()), OrgID: identityOrg(t, h), ActorSubject: "TEST-admin", Email: "synthetic@example.invalid", GivenName: "Synthetic", FamilyName: "Staff", Reason: "Authorized synthetic Staff identity creation"}
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		return identityObservation(org, tenant, project), nil
	}
	t.Cleanup(func() {
		for _, item := range []struct{ table, column string }{{"staff_identity_creations", "command_id"}, {"staff_identity_dispatches", "command_id"}, {"staff_identity_contact_claims", "root_command_id"}, {"staff_identity_commands", "id"}} {
			if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+item.table+" WHERE "+item.column+"=$1", command.ID); err != nil {
				t.Error(err)
			}
		}
	})
	raw, err := json.Marshal(map[string]string{"actor_subject": command.ActorSubject, "email": command.Email, "given_name": command.GivenName, "family_name": command.FamilyName, "reason": command.Reason})
	if err != nil {
		t.Fatal(err)
	}
	return h, command, string(raw)
}

func TestStaffIdentityCreationPreparesReservesAndRecordsNewCommand(t *testing.T) {
	var calls atomic.Int32
	var sent zitadel.StaffIdentityInput
	source := staffSource{create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		calls.Add(1)
		sent = in
		return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().UTC()}, nil
	}}
	h, c, body := staffCreationFixture(t, source)
	rec := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("new command: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if calls.Load() != 1 || sent.UserID == "" || sent.UserID == c.ID.String() || len(sent.BindingNonce) < 32 || sent.OrganizationID != "org-identity" || sent.Email != c.Email {
		t.Fatal("new command did not freeze a role-free server-selected identity")
	}
	got := decode(t, rec)
	if got["command_state"] != "created" || got["user_id"] != sent.UserID || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("created receipt lost binding or cache protection")
	}
	for _, private := range []string{c.Email, c.Reason, sent.BindingNonce, c.GivenName} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("creation receipt disclosed private command input")
		}
	}
	for _, table := range []string{"staff_identity_commands", "staff_identity_contact_claims", "staff_identity_dispatches", "staff_identity_creations"} {
		var count int
		if err := h.fixturePool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("creation persistence %s: count=%d err=%v", table, count, err)
		}
	}
	replay := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
	if replay.Code != http.StatusOK || replay.Body.String() != rec.Body.String() || calls.Load() != 1 {
		t.Fatal("retry did not recover immutable creation receipt without provider I/O")
	}
	changed := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, strings.Replace(body, c.Reason, "Different authorized reason", 1))
	if changed.Code != http.StatusConflict || calls.Load() != 1 {
		t.Fatal("changed command input bypassed immutable creation")
	}
	other := c
	other.ID = uuid.Must(uuid.NewV7())
	duplicate := h.do(t, http.MethodPut, staffIdentityPath(other), productToken, strings.Replace(body, c.Email, "SYNTHETIC@EXAMPLE.INVALID", 1))
	if duplicate.Code != http.StatusConflict || calls.Load() != 1 {
		t.Fatal("new command ID bypassed contact reservation")
	}
}

func TestStaffIdentityCreationUncertainResponseNeverRedispatches(t *testing.T) {
	var calls atomic.Int32
	for _, kind := range []string{"timeout", "invalid identity", "invalid creation time"} {
		t.Run(kind, func(t *testing.T) {
			calls.Store(0)
			source := staffSource{create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
				calls.Add(1)
				switch kind {
				case "timeout":
					return zitadel.StaffIdentityCreated{}, context.DeadlineExceeded
				case "invalid identity":
					return zitadel.StaffIdentityCreated{ID: "other", CreatedAt: time.Now().UTC()}, nil
				default:
					return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().Add(-24 * time.Hour)}, nil
				}
			}}
			h, c, body := staffCreationFixture(t, source)
			for range 2 {
				rec := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
				if rec.Code != http.StatusAccepted {
					t.Fatalf("uncertain command: status=%d", rec.Code)
				}
				got := decode(t, rec)
				if got["command_state"] != "dispatched" || got["user_id"] != nil || got["created_at"] != nil {
					t.Fatal("uncertain creation fabricated a confirmed identity")
				}
			}
			if calls.Load() != 1 {
				t.Fatal("uncertain creation was redispatched")
			}
		})
	}
}

func TestStaffIdentityCreationConcurrentReplaysDispatchOnce(t *testing.T) {
	var calls atomic.Int32
	source := staffSource{create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		calls.Add(1)
		return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().UTC()}, nil
	}}
	h, c, body := staffCreationFixture(t, source)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			rec := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
			if rec.Code != http.StatusOK && rec.Code != http.StatusAccepted {
				t.Errorf("concurrent command: status=%d", rec.Code)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent replays dispatched %d provider creates", calls.Load())
	}
	final := h.do(t, http.MethodGet, staffIdentityPath(c)+"?actor_subject=TEST-admin", productToken, "")
	if final.Code != http.StatusOK || decode(t, final)["command_state"] != "created" {
		t.Fatal("winning creation receipt was not retained")
	}
}

func TestStaffIdentityCreationClosedBodyAndInvalidInputNeverDispatch(t *testing.T) {
	for _, kind := range []string{"role", "verified", "password", "duplicate", "case alias", "missing reason", "long name", "nul reason", "invalid utf8", "query"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			h, c, body := staffCreationFixture(t, staffSource{create: func(context.Context, zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
				calls.Add(1)
				return zitadel.StaffIdentityCreated{}, nil
			}})
			path := staffIdentityPath(c)
			switch kind {
			case "role":
				body = strings.TrimSuffix(body, "}") + `,"roles":["ORG_OWNER"]}`
			case "verified":
				body = strings.TrimSuffix(body, "}") + `,"is_verified":true}`
			case "password":
				body = strings.TrimSuffix(body, "}") + `,"password":"TEST-password"}`
			case "duplicate":
				body = strings.TrimSuffix(body, "}") + `,"actor_\u0073ubject":"TEST-other"}`
			case "case alias":
				body = strings.Replace(body, "actor_subject", "Actor_Subject", 1)
			case "missing reason":
				body = strings.Replace(body, `,"reason":"`+c.Reason+`"`, "", 1)
			case "long name":
				body = strings.Replace(body, c.GivenName, strings.Repeat("क", 201), 1)
			case "nul reason":
				body = strings.Replace(body, c.Reason, `bad\u0000reason`, 1)
			case "invalid utf8":
				body = strings.Replace(body, c.GivenName, string([]byte{0xff}), 1)
			case "query":
				path += "?actor_subject=TEST-other"
			}
			rec := h.do(t, http.MethodPut, path, productToken, body)
			if rec.Code != http.StatusBadRequest || calls.Load() != 0 {
				t.Fatalf("invalid input dispatched: status=%d calls=%d", rec.Code, calls.Load())
			}
			var count int
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_commands`).Scan(&count); err != nil || count != 0 {
				t.Fatal("invalid input retained command facts")
			}
		})
	}
}

func TestStaffIdentityCreationReplayKeyCannotBypassWithdrawnEntitlement(t *testing.T) {
	var calls atomic.Int32
	source := staffSource{create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		calls.Add(1)
		return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().UTC()}, nil
	}}
	h, c, body := staffCreationFixture(t, source)
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, staffIdentityPath(c), strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+productToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "TEST-same-staff-create")
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}
	if rec := do(); rec.Code != http.StatusOK {
		t.Fatalf("first command: %d", rec.Code)
	}
	if err := h.store.RevokeGrant(t.Context(), c.OrgID, "planning-poker"); err != nil {
		t.Fatal(err)
	}
	rec := do()
	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatal("generic replay cache disclosed a creation receipt after entitlement withdrawal")
	}
	if calls.Load() != 1 || strings.Contains(rec.Body.String(), `"user_id"`) {
		t.Fatal("withdrawn caller obtained private identity or caused another create")
	}
}

func TestStaffIdentityCreationLostResponseReconcilesWithoutCreatingAgain(t *testing.T) {
	var calls atomic.Int32
	var sent zitadel.StaffIdentityInput
	var born time.Time
	source := staffSource{
		create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
			calls.Add(1)
			sent = in
			born = time.Now().UTC()
			return zitadel.StaffIdentityCreated{}, context.DeadlineExceeded
		},
		observe: func(_ context.Context, in zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffIdentityObservation, error) {
			if in != sent {
				t.Error("reconciliation changed the frozen identity")
			}
			return zitadel.StaffIdentityObservation{UserID: in.UserID, OrganizationID: in.OrganizationID, State: "initial", EmailVerified: false, CreatedAt: born, ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
		},
	}
	h, c, body := staffCreationFixture(t, source)
	for range 2 {
		rec := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
		if rec.Code != http.StatusAccepted || decode(t, rec)["command_state"] != "dispatched" {
			t.Fatal("lost response did not retain uncertain dispatch")
		}
	}
	reconciled := h.do(t, http.MethodPost, staffIdentityPath(c)+"/observations", productToken, `{"actor_subject":"TEST-admin"}`)
	receipt, ok := decode(t, reconciled)["receipt"].(map[string]any)
	if reconciled.Code != http.StatusOK || !ok || receipt["command_state"] != "created" || !strings.Contains(reconciled.Body.String(), `"email_verified":false`) {
		t.Fatalf("read-only reconciliation: status=%d body=%s", reconciled.Code, reconciled.Body.String())
	}
	replay := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
	if replay.Code != http.StatusOK || decode(t, replay)["user_id"] != sent.UserID || calls.Load() != 1 {
		t.Fatal("reconciled command created another account")
	}
}

func TestStaffIdentityCreationWithdrawalDuringProviderCallRetainsUncertainty(t *testing.T) {
	var calls atomic.Int32
	var h *harness
	var c store.StaffIdentityCommand
	source := staffSource{create: func(ctx context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
		calls.Add(1)
		if err := h.store.RevokeGrant(ctx, c.OrgID, "planning-poker"); err != nil {
			t.Error(err)
		}
		return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: time.Now().UTC()}, nil
	}}
	var body string
	h, c, body = staffCreationFixture(t, source)
	rec := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), `"user_id"`) {
		t.Fatalf("withdrawn creation disclosed receipt: status=%d", rec.Code)
	}
	if err := h.store.Grant(t.Context(), c.OrgID, "planning-poker", []string{"planning-poker-user"}); err != nil {
		t.Fatal(err)
	}
	replay := h.do(t, http.MethodPut, staffIdentityPath(c), productToken, body)
	if replay.Code != http.StatusAccepted || decode(t, replay)["command_state"] != "dispatched" || calls.Load() != 1 {
		t.Fatal("withdrawn midflight create was repeated or silently confirmed")
	}
}

func TestStaffIdentityCreationScopeDenialCannotPrepareOrDispatch(t *testing.T) {
	for _, kind := range []string{"anonymous", "platform", "different product", "inactive source", "source unavailable", "withdrawn entitlement"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			h, c, body := staffCreationFixture(t, staffSource{create: func(context.Context, zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
				calls.Add(1)
				return zitadel.StaffIdentityCreated{}, nil
			}})
			token := productToken
			want := http.StatusNotFound
			switch kind {
			case "anonymous":
				token = ""
				want = http.StatusUnauthorized
			case "platform":
				token = platformToken
			case "different product":
				token = otherToken
			case "withdrawn entitlement":
				if err := h.store.RevokeGrant(t.Context(), c.OrgID, "planning-poker"); err != nil {
					t.Fatal(err)
				}
			case "inactive source":
				h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
					v := identityObservation(org, tenant, project)
					v.State = "inactive"
					return v, nil
				}
			case "source unavailable":
				want = http.StatusServiceUnavailable
				h.identity.verify = func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error) {
					return zitadel.OrganizationIdentity{}, zitadel.ErrIdentityUnavailable
				}
			}
			rec := h.do(t, http.MethodPut, staffIdentityPath(c), token, body)
			if rec.Code != want || calls.Load() != 0 {
				t.Fatalf("scope denial dispatched: status=%d calls=%d", rec.Code, calls.Load())
			}
			var count int
			if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_commands`).Scan(&count); err != nil || count != 0 {
				t.Fatal("unauthorized scope retained command facts")
			}
		})
	}
}
