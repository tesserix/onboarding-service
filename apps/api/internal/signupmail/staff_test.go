package signupmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type staffTestPayload struct {
	To      []string `json:"to"`
	HTML    string   `json:"html"`
	Text    string   `json:"text"`
	Subject string   `json:"subject"`
}

func staffMessageFixture() StaffMessage {
	return StaffMessage{Email: "qa@example.test", Name: "QA Staff", WorkspaceName: "Example Hospital", InvitationID: uuid.MustParse("0195e71a-3233-7001-8001-010203040506"), Ticket: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), Code: "TestCode123", Phase: "email", ExpiresAt: time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)}
}

func staffTestClient(t *testing.T, roundTrip http.RoundTripper) *Client {
	t.Helper()
	file := t.TempDir() + "/key"
	if err := os.WriteFile(file, []byte("re_synthetic_key"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := New(file, "onboarding@helivanta.com", roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func staffTestResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-message"}`))}
}

func TestStaffInvitationMailUsesPrivateFragmentAndStableDispatch(t *testing.T) {
	var calls int
	var payload staffTestPayload
	client := staffTestClient(t, transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Idempotency-Key") != "helivanta-staff-invitation:0195e71a-3233-7001-8001-010203040506" {
			t.Error("incorrect provider destination or dispatch identity")
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return staffTestResponse(), nil
	}))
	message := staffMessageFixture()
	id, err := client.SendStaff(context.Background(), message)
	if err != nil || id != "synthetic-message" {
		t.Fatalf("missing provider acceptance: %v", err)
	}
	if calls != 1 || len(payload.To) != 1 || payload.To[0] != message.Email {
		t.Fatal("unexpected delivery")
	}
	start := strings.Index(payload.Text, "https://")
	if start < 0 {
		t.Fatal("missing link")
	}
	parsed, err := url.Parse(strings.Fields(payload.Text[start:])[0])
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "helivanta.app" || parsed.Path != "/staff/accept" || parsed.RawQuery != "" || fragment.Get("ticket") != message.Ticket || fragment.Get("code") != message.Code || fragment.Get("phase") != "email" || fragment.Get("invitation") != message.InvitationID.String() {
		t.Fatal("invalid recipient link")
	}
	if !strings.Contains(payload.Text, "13 Nov 2026 01:00 UTC") || !strings.Contains(payload.Text, message.WorkspaceName) {
		t.Fatal("missing workspace or exact expiry")
	}
}

func TestStaffInvitationMailRejectsMalformedMessagesBeforeDelivery(t *testing.T) {
	var calls int
	client := staffTestClient(t, transport(func(*http.Request) (*http.Response, error) { calls++; return staffTestResponse(), nil }))
	for _, tc := range []struct {
		name   string
		change func(*StaffMessage)
	}{
		{"invalid_ticket", func(m *StaffMessage) { m.Ticket = "short" }},
		{"invalid_code", func(m *StaffMessage) { m.Code = "bad\ncode" }},
		{"wrong_phase", func(m *StaffMessage) { m.Phase = "admin" }},
		{"invalid_id", func(m *StaffMessage) { m.InvitationID = uuid.Nil }},
		{"multiple_recipients", func(m *StaffMessage) { m.Email = "qa@example.test,other@example.test" }},
		{"missing_name", func(m *StaffMessage) { m.Name = "" }},
		{"missing_workspace", func(m *StaffMessage) { m.WorkspaceName = "" }},
		{"missing_expiry", func(m *StaffMessage) { m.ExpiresAt = time.Time{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := staffMessageFixture()
			tc.change(&message)
			if _, err := client.SendStaff(context.Background(), message); err == nil {
				t.Fatal("accepted invalid message")
			}
		})
	}
	if calls != 0 {
		t.Fatal("invalid message reached provider")
	}
}

func TestStaffInvitationMailEscapesNamesAndRedactsDiagnostics(t *testing.T) {
	var payload staffTestPayload
	client := staffTestClient(t, transport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return staffTestResponse(), nil
	}))
	message := staffMessageFixture()
	message.Name = `<script>alert(1)</script>`
	message.WorkspaceName = `<b>Hospital</b>`
	message.Phase = "password"
	if _, err := client.SendStaff(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload.HTML, "<script>") || strings.Contains(payload.HTML, message.WorkspaceName) || !strings.Contains(payload.Subject, "password") {
		t.Fatal("unsafe or wrong template")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		value := fmt.Sprintf(format, message)
		if strings.Contains(value, message.Ticket) || strings.Contains(value, message.Code) || strings.Contains(value, message.Email) {
			t.Fatal("diagnostics expose invitation data")
		}
	}
}

func TestStaffInvitationMailPreservesOpaqueCodeCharacters(t *testing.T) {
	var payload staffTestPayload
	client := staffTestClient(t, transport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return staffTestResponse(), nil
	}))
	message := staffMessageFixture()
	message.Code = "Opaque+Code/==#"
	if _, err := client.SendStaff(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	start := strings.Index(payload.Text, "https://")
	if start < 0 {
		t.Fatal("missing link")
	}
	parsed, err := url.Parse(strings.Fields(payload.Text[start:])[0])
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.EscapedFragment())
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Get("code") != message.Code {
		t.Fatal("recipient would submit a changed native verification code")
	}
}
