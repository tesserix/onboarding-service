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

// helivanta#1777: the invitation's email proof also sets the password, password
// first with a reset code that never leaves the request, from one email.
func TestStaffInvitationEmailProofSetsThePasswordInOneStep(t *testing.T) {
	var verified, configured atomic.Bool
	var mails, passwordWrites, emailProofs atomic.Int32
	var born time.Time
	const emailed, reset, weak, strong = "Emailed+Code/1", "Reset+Code/2", "short", "test-private-password"
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/email/send"):
			_, _ = io.WriteString(w, `{"verificationCode":"`+emailed+`"}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/password_reset"):
			if _, ok := body["returnCode"]; !ok {
				t.Error("the reset code must be returned to the request, never mailed")
			}
			_, _ = io.WriteString(w, `{"verificationCode":"`+reset+`"}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/authentication_methods"):
			if configured.Load() {
				_, _ = io.WriteString(w, `{"details":{"totalResult":"1"},"authMethodTypes":["AUTHENTICATION_METHOD_TYPE_PASSWORD"]}`)
			} else {
				_, _ = io.WriteString(w, `{"details":{"totalResult":"0"}}`)
			}
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/password"):
			passwordWrites.Add(1)
			if verified.Load() {
				t.Error("one step sets the password before the email proof")
			}
			if body["verificationCode"] != reset {
				t.Error("password set with the emailed code instead of the request's own reset code")
			}
			if body["newPassword"].(map[string]any)["password"] == weak {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"code":3,"message":"Errors.User.PasswordComplexityPolicy.MinLength (DOMAIN-HuJf6)","details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"DOMAIN-HuJf6"}]}`)
				return
			}
			configured.Store(true)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/email/verify"):
			emailProofs.Add(1)
			if body["verificationCode"] != emailed {
				w.WriteHeader(400)
				return
			}
			verified.Store(true)
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
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&message)
		for _, line := range strings.Split(message.Text, "\n") {
			if strings.HasPrefix(line, "https://helivanta.app/staff/accept#") {
				link, err := url.Parse(line)
				if err != nil {
					t.Fatal(err)
				}
				if delivered, err = url.ParseQuery(link.EscapedFragment()); err != nil {
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
			born = time.Now().UTC()
			return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: born}, nil
		},
		observe: func(_ context.Context, in zitadel.StaffIdentityInput, _ time.Time) (zitadel.StaffIdentityObservation, error) {
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
	if w := h.do(t, "PUT", staffIdentityPath(c)+"/invitations/"+uuid.Must(uuid.NewV7()).String(), productToken, `{"actor_subject":"TEST-admin"}`); w.Code != 200 {
		t.Fatalf("dispatch: %d %s", w.Code, w.Body)
	}
	email := func(code, password string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"invitation_id": delivered.Get("invitation"), "ticket": delivered.Get("ticket"), "code": code, "password": password})
		return h.do(t, "POST", "/internal/v1/customer-signups/staff-invitations/email", "workload", string(body))
	}
	if w := h.do(t, "POST", "/internal/v1/customer-signups/staff-invitations/status", "workload", `{"invitation_id":"`+delivered.Get("invitation")+`","ticket":"`+delivered.Get("ticket")+`"}`); strings.Contains(w.Body.String(), "subject") {
		t.Fatalf("an incomplete receipt must not name the account: %s", w.Body)
	}
	w := email(emailed, weak)
	if w.Code != 422 || verified.Load() || strings.Contains(w.Body.String(), weak) {
		t.Fatalf("policy refusal: %d %s", w.Code, w.Body)
	}
	if w = email("wrong-code", strong); w.Code != 400 || verified.Load() {
		t.Fatalf("a wrong emailed code verifies nothing: %d %s", w.Code, w.Body)
	}
	for range 2 {
		w = email(emailed, strong)
		got := decode(t, w)
		if w.Code != 200 || got["state"] != "complete" || strings.Contains(w.Body.String(), strong) || strings.Contains(w.Body.String(), reset) {
			t.Fatalf("one step / replay: %d %s", w.Code, w.Body)
		}
		// helivanta#1778: the product server learns the account it just set up.
		if got["subject"] == "" || got["subject"] == nil || got["organization_id"] == nil || got["organization_id"] == "" {
			t.Fatalf("complete recipient receipt must name the native account: %s", w.Body)
		}
	}
	if !verified.Load() || !configured.Load() || mails.Load() != 1 {
		t.Fatalf("one email must verify and set the password (verified=%v configured=%v mails=%d)", verified.Load(), configured.Load(), mails.Load())
	}
	if passwordWrites.Load() != 3 || emailProofs.Load() != 2 {
		t.Fatalf("a completed replay repeated a native write: passwords=%d proofs=%d", passwordWrites.Load(), emailProofs.Load())
	}
	var attempts, consumed int
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT proof_attempts, (consumed_at IS NOT NULL)::int FROM staff_identity_invitation_attempts WHERE command_id=$1`, c.ID).Scan(&attempts, &consumed); err != nil || attempts != 3 || consumed != 1 {
		t.Fatalf("proof budget evidence: attempts=%d consumed=%d err=%v", attempts, consumed, err)
	}
}
