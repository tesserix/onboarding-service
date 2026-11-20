package signupmail

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// onboarding#49: one frame for every Helivanta email. Each sender supplies
// only its fields; the heading, button or code, expiry and closing line are
// rendered by the layout, the text part says the same things, and a code never
// reaches the subject.
func TestEveryEmailRendersThroughTheSharedLayout(t *testing.T) {
	file := t.TempDir() + "/key"
	if err := os.WriteFile(file, []byte("re_synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	type sent struct {
		Subject, HTML, Text string
	}
	var mails []sent
	c, err := New(file, "onboarding@helivanta.app", transport(func(r *http.Request) (*http.Response, error) {
		var body sent
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mails = append(mails, body)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"m"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	staff := staffMessageFixture()
	for name, send := range map[string]func() error{
		"verify": func() error {
			_, e := c.Send(ctx, Message{Email: "o@example.test", Name: "Priya", Organization: "City Hospital", UserID: "owner-1", Code: "code-1", Phase: "email"})
			return e
		},
		"password": func() error {
			_, e := c.Send(ctx, Message{Email: "o@example.test", Name: "Priya", Organization: "City Hospital", UserID: "owner-1", Code: "code-2", Phase: "password"})
			return e
		},
		"sign-in code": func() error { _, e := c.SendDiscovery(ctx, "o@example.test", "12345678"); return e },
		"recovery": func() error {
			_, e := c.SendPasswordRecovery(ctx, uuid.Must(uuid.NewV7()).String(), "o@example.test", "87654321")
			return e
		},
		"staff": func() error { _, e := c.SendStaff(ctx, staff); return e },
		"changed": func() error {
			_, e := c.SendPasswordChanged(ctx, uuid.New(), "o@example.test", "City Hospital", time.Now())
			return e
		},
	} {
		mails = nil
		if err := send(); err != nil || len(mails) != 1 {
			t.Fatalf("%s: %v (%d mails)", name, err, len(mails))
		}
		m := mails[0]
		for _, frame := range []string{"<!doctype html><html lang=\"en\">", ">Helivanta</td>", template.HTMLEscapeString(closing), "Sent by Helivanta &lt;onboarding@helivanta.app&gt;"} {
			if !strings.Contains(m.HTML, frame) {
				t.Fatalf("%s: html lacks the shared frame %q", name, frame)
			}
		}
		if !strings.Contains(m.Text, closing) || strings.Contains(m.Text, "<") {
			t.Fatalf("%s: text part lacks the closing line or carries markup", name)
		}
		if strings.Count(m.HTML, "<h1") != 0 || strings.Contains(m.Text, "per hour") || strings.Contains(m.HTML, "per hour") {
			t.Fatalf("%s: template-local markup or rate limits leaked", name)
		}
		for _, code := range []string{"12345678", "87654321", "code-1", "code-2", staff.Code} {
			if strings.Contains(m.Subject, code) {
				t.Fatalf("%s: code in subject", name)
			}
		}
		// A button's URL, or a code, appears in both parts.
		if strings.Contains(m.HTML, "href=") {
			start := strings.Index(m.HTML, "href=\"") + 6
			link := m.HTML[start : start+strings.Index(m.HTML[start:], "\"")]
			if !strings.Contains(m.Text, strings.ReplaceAll(link, "&amp;", "&")) {
				t.Fatalf("%s: text part lacks the button link", name)
			}
		}
	}
}

func TestLayoutRefusesIncompleteContent(t *testing.T) {
	for name, bad := range map[string]content{
		"no expiry":       {Subject: "s", Heading: "h", Line: "l", Code: "1"},
		"button w/o url":  {Subject: "s", Heading: "h", Line: "l", Button: "Go", Expiry: "e"},
		"neither":         {Subject: "s", Heading: "h", Line: "l", Expiry: "e"},
		"newline subject": {Subject: "s\r\nBcc: x", Heading: "h", Line: "l", Code: "1", Expiry: "e"},
	} {
		if _, _, err := render("onboarding@helivanta.app", bad); err == nil {
			t.Fatalf("%s rendered", name)
		}
	}
}

// hasClosing accepts the shared closing line in either part's encoding.
func hasClosing(part string) bool {
	return strings.Contains(part, closing) || strings.Contains(part, template.HTMLEscapeString(closing))
}
