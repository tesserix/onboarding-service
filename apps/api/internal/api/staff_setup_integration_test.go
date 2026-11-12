package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type staffMailTransport func(*http.Request) (*http.Response, error)

func (f staffMailTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStaffSetupAPIsDeliverOnlyToRecipientAndDoNotReplaySuccessfulProofs(t *testing.T) {
	var verified, configured atomic.Bool
	var nativeCreates, nativeProofs, mails atomic.Int32
	var born time.Time
	var frozen zitadel.StaffIdentityInput
	const nativeCode = "Opaque+Code/==#"
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/email/send") || strings.HasSuffix(r.URL.Path, "/password_reset")):
			_, _ = io.WriteString(w, `{"verificationCode":"`+nativeCode+`"}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/authentication_methods"):
			if configured.Load() {
				_, _ = io.WriteString(w, `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`)
			} else {
				_, _ = io.WriteString(w, `{"details":{"totalResult":"0"}}`)
			}
		case r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/email/verify") || strings.HasSuffix(r.URL.Path, "/password")):
			nativeProofs.Add(1)
			var proof map[string]any
			if json.NewDecoder(r.Body).Decode(&proof) != nil || proof["verificationCode"] != nativeCode {
				w.WriteHeader(400)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/email/verify") {
				verified.Store(true)
			} else {
				if !verified.Load() {
					t.Error("password mutation before contact proof")
				}
				configured.Store(true)
			}
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected native setup command %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(native.Close)
	key := t.TempDir() + "/resend-key"
	if err := os.WriteFile(key, []byte("re_test_only"), 0600); err != nil {
		t.Fatal(err)
	}
	var delivered url.Values
	sender, err := signupmail.New(key, "onboarding@helivanta.com", staffMailTransport(func(r *http.Request) (*http.Response, error) {
		mails.Add(1)
		var message struct {
			Text string   `json:"text"`
			To   []string `json:"to"`
		}
		if json.NewDecoder(r.Body).Decode(&message) != nil || len(message.To) != 1 || message.To[0] != "synthetic@example.invalid" {
			t.Error("delivery did not use frozen recipient")
		}
		for _, line := range strings.Split(message.Text, "\n") {
			if strings.HasPrefix(line, "https://helivanta.app/staff/accept#") {
				link, err := url.Parse(line)
				if err != nil {
					t.Fatal(err)
				}
				delivered, err = url.ParseQuery(link.EscapedFragment())
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"test-accepted"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	source := staffSource{
		create: func(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
			nativeCreates.Add(1)
			born = time.Now().UTC()
			frozen = in
			return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: born}, nil
		},
		observe: func(_ context.Context, in zitadel.StaffIdentityInput, created time.Time) (zitadel.StaffIdentityObservation, error) {
			if in != frozen || !created.Equal(born) {
				t.Error("native observation lost frozen identity")
			}
			state := "initial"
			if verified.Load() {
				state = "active"
			}
			return zitadel.StaffIdentityObservation{UserID: in.UserID, OrganizationID: in.OrganizationID, State: state, EmailVerified: verified.Load(), CreatedAt: born, ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
		},
	}
	h, c, create := staffCreationFixture(t, source, onboarding.WithCustomerIdentity(zitadel.New(native.URL, zitadel.StaticToken("test-token"), "test-platform", nil)), onboarding.WithCustomerMail(sender))
	if w := h.do(t, "PUT", staffIdentityPath(c), productToken, create); w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	id := uuid.Must(uuid.NewV7())
	path := staffIdentityPath(c) + "/invitations/" + id.String()
	for range 2 {
		w := h.do(t, "PUT", path, productToken, `{"actor_subject":"TEST-admin"}`)
		if w.Code != 200 || decode(t, w)["state"] != "email_action_required" {
			t.Fatalf("dispatch: %d %s", w.Code, w.Body)
		}
		for _, secret := range []string{nativeCode, delivered.Get("ticket"), "synthetic@example.invalid"} {
			if secret != "" && strings.Contains(w.Body.String(), secret) {
				t.Fatal("administrator received recipient capability or contact")
			}
		}
	}
	if mails.Load() != 1 || nativeCreates.Load() != 1 || delivered.Get("code") != nativeCode {
		t.Fatal("replay delivered twice or changed opaque code")
	}
	call := func(action, code, password string) *httptest.ResponseRecorder {
		payload := map[string]string{"invitation_id": delivered.Get("invitation"), "ticket": delivered.Get("ticket")}
		if action == "resend" {
			payload["delivery_id"] = uuid.Must(uuid.NewV7()).String()
		}
		if code != "" {
			payload["code"] = code
		}
		if password != "" {
			payload["password"] = password
		}
		body, _ := json.Marshal(payload)
		return h.do(t, "POST", "/internal/v1/customer-signups/staff-invitations/"+action, "workload", string(body))
	}
	if w := call("email", "wrong-code", ""); w.Code != 400 {
		t.Fatalf("wrong proof: %d %s", w.Code, w.Body)
	}
	for range 2 {
		if w := call("email", nativeCode, ""); w.Code != 200 || decode(t, w)["state"] != "already_verified" {
			t.Fatalf("verify/replay: %d %s", w.Code, w.Body)
		}
	}
	if nativeProofs.Load() != 2 || mails.Load() != 1 {
		t.Fatal("verified replay repeated proof or mail")
	}
	if w := call("resend", "", ""); w.Code != 200 || decode(t, w)["state"] != "password_action_required" {
		t.Fatalf("password delivery: %d %s", w.Code, w.Body)
	}
	for range 2 {
		if w := call("password", nativeCode, "test-private-password"); w.Code != 200 || decode(t, w)["state"] != "complete" {
			t.Fatalf("password/replay: %d %s", w.Code, w.Body)
		}
	}
	if nativeProofs.Load() != 3 || mails.Load() != 2 {
		t.Fatal("configured replay changed credentials or mailed again")
	}
	if w := call("resend", "", ""); w.Code != 200 || decode(t, w)["state"] != "complete" || mails.Load() != 2 {
		t.Fatal("configured account received another link")
	}
	var management int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT count(*) FROM staff_identity_invitation_attempts WHERE command_id=$1`, c.ID).Scan(&management); err != nil || management != 2 {
		t.Fatal("delivery evidence lost")
	}
}
