package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func staffHistoryActivity(t *testing.T, h *harness, c store.StaffIdentityCommand, action string) {
	t.Helper()
	if err := h.store.AppendAudit(t.Context(), audit.Event{ActorType: audit.ActorProduct, ActorID: c.MachineSubject, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: action, Outcome: audit.OutcomeSuccess, RequestID: uuid.NewString(), Detail: `{"private":"PRIVATE_HISTORY_DETAIL"}`}); err != nil {
		t.Fatal(err)
	}
}

func staffHistoryPage(t *testing.T, h *harness, c store.StaffIdentityCommand, cursor string, limit int) map[string]any {
	t.Helper()
	path := staffIdentityPath(c) + "/history?actor_subject=" + url.QueryEscape(c.ActorSubject) + fmt.Sprintf("&limit=%d", limit)
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	rec := h.do(t, http.MethodGet, path, productToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("history page: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "PRIVATE_HISTORY_DETAIL") {
		t.Fatal("history returned raw audit detail")
	}
	return decode(t, rec)
}

func TestStaffIdentityHistoryKeysetIsStablePrivateAndRestartSafe(t *testing.T) {
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", staffSource{}))
	c := preparedStaffIdentity(t, h)
	for i := range 4 {
		staffHistoryActivity(t, h, c, fmt.Sprintf("staff.identity.test.%d", i))
	}
	first := staffHistoryPage(t, h, c, "", 2)
	items := first["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["action"] != "staff.identity.test.3" || items[1].(map[string]any)["action"] != "staff.identity.test.2" || first["has_more"] != true {
		t.Fatal("history did not select the newest bounded activity")
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || len(cursor) != 43 || first["cursor_expires_at"] == nil {
		t.Fatal("history did not return an opaque expiring continuation")
	}
	if !strings.HasSuffix(first["cursor_expires_at"].(string), "Z") {
		t.Fatal("cursor expiry is not canonical UTC")
	}
	var stored string
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT token_hash FROM staff_identity_history_cursors WHERE command_id=$1`, c.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(cursor))
	if stored != hex.EncodeToString(digest[:]) || strings.Contains(stored, cursor) {
		t.Fatal("raw continuation token was retained")
	}
	staffHistoryActivity(t, h, c, "staff.identity.after.snapshot")
	second := staffHistoryPage(t, h, c, cursor, 2)
	items = second["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["action"] != "staff.identity.test.1" || items[1].(map[string]any)["action"] != "staff.identity.test.0" || second["cursor_expires_at"] != first["cursor_expires_at"] {
		t.Fatal("page chain included new appends or extended expiry")
	}
	restarted := onboarding.New(h.store, h.identity, authz.New(h.store), nil, onboarding.WithStaffIdentity("planning-poker", staffSource{}))
	page, err := restarted.StaffIdentityHistory(onboarding.WithRequestID(t.Context(), "TEST-history-restart"), authz.Principal{Kind: authz.KindProduct, Subject: c.MachineSubject, ProductKey: c.ProductKey}, c.OrgID, c.ID, c.ActorSubject, cursor, 2)
	if err != nil || len(page.Items) != 2 || page.Items[0].Action != "staff.identity.test.1" || page.Items[1].Action != "staff.identity.test.0" {
		t.Fatalf("replica/restart changed continuation: %v", err)
	}
	last := staffHistoryPage(t, h, c, second["next_cursor"].(string), 2)
	items = last["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["action"] != "staff.identity.prepare" || last["has_more"] != false || last["next_cursor"] != nil {
		t.Fatal("history did not terminate at retained preparation")
	}
	refreshed := staffHistoryPage(t, h, c, "", 100)
	if refreshed["has_more"] != false || len(refreshed["items"].([]any)) <= 5 {
		t.Fatal("refresh did not expose later audited activity")
	}
}

func TestStaffIdentityHistoryNeverMixesForeignAuditActivity(t *testing.T) {
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", staffSource{}))
	c := preparedStaffIdentity(t, h)
	for _, kind := range []string{"org", "product", "actor", "command", "family", "actor type"} {
		e := audit.Event{ActorType: audit.ActorProduct, ActorID: c.MachineSubject, ProductKey: c.ProductKey, OrgID: c.OrgID.String(), Target: c.ID.String(), Action: "staff.identity.FOREIGN_ACTIVITY", Outcome: audit.OutcomeSuccess, RequestID: uuid.NewString()}
		switch kind {
		case "org":
			e.OrgID = uuid.NewString()
		case "product":
			e.ProductKey = "mark8ly"
		case "actor":
			e.ActorID = "svc-other"
		case "command":
			e.Target = uuid.NewString()
		case "family":
			e.Action = "other.FOREIGN_ACTIVITY"
		case "actor type":
			e.ActorType = audit.ActorPlatform
		}
		if err := h.store.AppendAudit(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	page := staffHistoryPage(t, h, c, "", 100)
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["action"] != "staff.identity.prepare" {
		t.Fatal("history included foreign or unsupported activity")
	}
}

func TestStaffIdentityHistoryBadQueryAndScopeCannotDisclosePages(t *testing.T) {
	for _, kind := range []string{"anonymous", "platform", "different product", "wrong actor", "unknown command", "unknown tenant", "withdrawal", "source unavailable", "inactive source", "missing actor", "duplicate actor", "duplicate limit", "unknown query", "zero limit", "huge limit", "noncanonical limit", "empty cursor", "malformed cursor", "GET body"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", staffSource{}))
			c := preparedStaffIdentity(t, h)
			path := staffIdentityPath(c) + "/history?actor_subject=TEST-admin"
			token := productToken
			body := ""
			want := http.StatusNotFound
			switch kind {
			case "anonymous":
				token = ""
				want = http.StatusUnauthorized
			case "platform":
				token = platformToken
			case "different product":
				token = otherToken
			case "wrong actor":
				path = strings.Replace(path, "TEST-admin", "TEST-other", 1)
			case "unknown command":
				path = strings.Replace(path, c.ID.String(), uuid.Must(uuid.NewV7()).String(), 1)
			case "unknown tenant":
				path = strings.Replace(path, c.OrgID.String(), uuid.NewString(), 1)
			case "withdrawal":
				if err := h.store.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
					t.Fatal(err)
				}
			case "source unavailable":
				want = http.StatusServiceUnavailable
				h.identity.verify = func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error) {
					return zitadel.OrganizationIdentity{}, zitadel.ErrIdentityUnavailable
				}
			case "inactive source":
				h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
					v := identityObservation(org, tenant, project)
					v.State = "inactive"
					return v, nil
				}
			default:
				want = http.StatusBadRequest
				switch kind {
				case "missing actor":
					path = staffIdentityPath(c) + "/history"
				case "duplicate actor":
					path += "&actor_subject=TEST-other"
				case "duplicate limit":
					path += "&limit=1&limit=2"
				case "unknown query":
					path += "&product_key=mark8ly"
				case "zero limit":
					path += "&limit=0"
				case "huge limit":
					path += "&limit=101"
				case "noncanonical limit":
					path += "&limit=01"
				case "empty cursor":
					path += "&cursor="
				case "malformed cursor":
					path += "&cursor=NOT-A-CURSOR"
				case "GET body":
					body = `{}`
				}
			}
			rec := h.do(t, http.MethodGet, path, token, body)
			if rec.Code != want || strings.Contains(rec.Body.String(), `"items"`) || strings.Contains(rec.Body.String(), c.Email) {
				t.Fatalf("history denial: status=%d want=%d", rec.Code, want)
			}
		})
	}
}

func TestStaffIdentityHistoryOpaqueCursorCannotBypassScopeExpiryOrRevocation(t *testing.T) {
	for _, kind := range []string{"unknown", "tampered", "expired", "other command", "withdrawal", "source unavailable"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", staffSource{}))
			c := preparedStaffIdentity(t, h)
			staffHistoryActivity(t, h, c, "staff.identity.read")
			first := staffHistoryPage(t, h, c, "", 1)
			cursor := first["next_cursor"].(string)
			target := c
			want := http.StatusBadRequest
			switch kind {
			case "unknown":
				cursor = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
			case "tampered":
				if cursor[0] == 'A' {
					cursor = "B" + cursor[1:]
				} else {
					cursor = "A" + cursor[1:]
				}
			case "expired":
				if _, err := h.fixturePool.Exec(t.Context(), `UPDATE staff_identity_history_cursors SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE command_id=$1`, c.ID); err != nil {
					t.Fatal(err)
				}
			case "other command":
				target.ID = uuid.Must(uuid.NewV7())
				target.ProviderUserID = "TEST-other-history-account"
				target.BindingNonce = "TEST-other-history-nonce"
				target.Email = "other-history@example.invalid"
				e := audit.Event{ActorType: audit.ActorProduct, ActorID: target.MachineSubject, ProductKey: target.ProductKey, OrgID: target.OrgID.String(), Target: target.ID.String(), Action: "staff.identity.prepare", Outcome: audit.OutcomeSuccess, RequestID: uuid.NewString()}
				if _, err := h.store.PrepareStaffIdentity(t.Context(), target, e); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := h.fixturePool.Exec(context.Background(), `DELETE FROM staff_identity_contact_claims WHERE root_command_id=$1`, target.ID); err != nil {
						t.Error(err)
					}
					if _, err := h.fixturePool.Exec(context.Background(), `DELETE FROM staff_identity_commands WHERE id=$1`, target.ID); err != nil {
						t.Error(err)
					}
				})
			case "withdrawal":
				want = http.StatusNotFound
				if err := h.store.RevokeGrant(t.Context(), c.OrgID, c.ProductKey); err != nil {
					t.Fatal(err)
				}
			case "source unavailable":
				want = http.StatusServiceUnavailable
				h.identity.verify = func(context.Context, string, string, string) (zitadel.OrganizationIdentity, error) {
					return zitadel.OrganizationIdentity{}, zitadel.ErrIdentityUnavailable
				}
			}
			rec := h.do(t, http.MethodGet, staffIdentityPath(target)+"/history?actor_subject=TEST-admin&limit=1&cursor="+url.QueryEscape(cursor), productToken, "")
			if rec.Code != want || strings.Contains(rec.Body.String(), `"items"`) || strings.Contains(rec.Body.String(), `"next_cursor"`) {
				t.Fatalf("cursor bypass: status=%d want=%d", rec.Code, want)
			}
		})
	}
}
