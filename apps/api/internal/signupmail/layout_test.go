package signupmail

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
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
	staffPassword := staffMessageFixture()
	staffPassword.Phase = "password"
	type mail struct {
		name               string
		send               func() error
		subject, preheader string
		code               string // shown instead of a button, or empty
	}
	// Keyed by the *Client method that sends it: every exported Send method
	// must appear here, so a new email cannot skip these frame checks.
	cases := map[string][]mail{
		"Send": {
			{"verify", func() error {
				_, e := c.Send(ctx, Message{Email: "o@example.test", Name: "Priya", Organization: "City Hospital", UserID: "owner-1", Code: "code-1", Phase: "email"})
				return e
			}, "Verify your email for City Hospital — Helivanta", "Confirm your email to finish setting up City Hospital.", ""},
			{"password", func() error {
				_, e := c.Send(ctx, Message{Email: "o@example.test", Name: "Priya", Organization: "City Hospital", UserID: "owner-1", Code: "code-2", Phase: "password"})
				return e
			}, "Set your password — Helivanta", "Set a password to finish setting up City Hospital.", ""},
		},
		"SendDiscovery": {{"sign-in code", func() error { _, e := c.SendDiscovery(ctx, "o@example.test", "12345678"); return e },
			"Your sign-in code — Helivanta", "Enter this code to sign in. It expires in 10 minutes.", "12345678"}},
		"SendPasswordRecovery": {{"recovery", func() error {
			_, e := c.SendPasswordRecovery(ctx, uuid.Must(uuid.NewV7()).String(), "o@example.test", "87654321")
			return e
		}, "Reset your password — Helivanta", "Enter this code to reset your password. It expires in 10 minutes.", "87654321"}},
		"SendStaff": {
			{"staff", func() error { _, e := c.SendStaff(ctx, staff); return e },
				"You're invited to Example Hospital — Helivanta", "Accept your invite to join Example Hospital on Helivanta.", ""},
			{"staff password", func() error { _, e := c.SendStaff(ctx, staffPassword); return e },
				"Set your password for Example Hospital — Helivanta", "Set your password to finish joining Example Hospital.", ""},
		},
		"SendPasswordChanged": {{"changed", func() error {
			_, e := c.SendPasswordChanged(ctx, uuid.New(), "o@example.test", "City Hospital", time.Now())
			return e
		}, "Your password was changed — Helivanta", "If this wasn't you, reset your password now.", ""}},
	}
	client := reflect.TypeOf(c)
	for i := range client.NumMethod() {
		if method := client.Method(i).Name; strings.HasPrefix(method, "Send") && len(cases[method]) == 0 {
			t.Fatalf("(*Client).%s sends email but has no entry in TestEveryEmailRendersThroughTheSharedLayout: add its mails to the cases table in layout_test.go", method)
		}
	}
	for method := range cases {
		if _, ok := client.MethodByName(method); !ok || !strings.HasPrefix(method, "Send") {
			t.Fatalf("cases table names %q, which is not a Send method of *Client", method)
		}
	}
	for _, want := range slices.Concat(slices.Collect(maps.Values(cases))...) {
		name := want.name
		mails = nil
		if err := want.send(); err != nil || len(mails) != 1 {
			t.Fatalf("%s: %v (%d mails)", name, err, len(mails))
		}
		m := mails[0]
		if m.Subject != want.subject || !strings.HasSuffix(m.Subject, " — Helivanta") {
			t.Fatalf("%s: subject %q", name, m.Subject)
		}
		// helivanta#1857: head block, lockup, fluid card, accessible footer.
		for _, frame := range []string{
			`<!doctype html><html lang="en"><head><meta charset="utf-8">`,
			`<meta name="viewport" content="width=device-width, initial-scale=1">`,
			`<meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light">`,
			"<title>" + template.HTMLEscapeString(want.subject) + "</title></head>",
			`<img src="https://helivanta.app/email/helivanta-lockup.png" width="160" height="41" alt="Helivanta" style="display:block;border:0;outline:none;text-decoration:none;height:auto;width:160px">`,
			`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border:1px solid #dbe2ec;`,
			"background:#f6f8fb", "color:#0b1324",
			template.HTMLEscapeString(closing),
			`color:#4a5568">` + template.HTMLEscapeString(footerLine) + "</p>",
			`Helivanta &middot; <a href="https://helivanta.app" style="color:#2b4ee6`,
		} {
			if !strings.Contains(m.HTML, frame) {
				t.Fatalf("%s: html lacks the shared frame %q", name, frame)
			}
		}
		if strings.Count(m.HTML, "<img") != 1 || strings.Contains(m.HTML, `width="480"`) || strings.Contains(m.HTML, "onboarding@helivanta") {
			t.Fatalf("%s: extra image, fixed-width card or sender address in the body", name)
		}
		for _, slate := range []string{"#f8fafc", "#0f172a", "#1e293b", "#e2e8f0", "#475569", "#64748b", "#94a3b8"} {
			if strings.Contains(strings.ToLower(m.HTML), slate) {
				t.Fatalf("%s: old slate colour %s", name, slate)
			}
		}
		// The preheader is the first thing in the body, hidden, exact, padded so
		// clients don't pull body text into the preview, and before the header.
		hidden := `<div style="display:none;max-height:0;overflow:hidden;mso-hide:all;font-size:1px;line-height:1px;color:#f6f8fb;opacity:0">` + template.HTMLEscapeString(want.preheader) + strings.Repeat("&zwnj;&nbsp;", 40) + "</div>"
		body := strings.Index(m.HTML, "<body")
		if body < 0 || !strings.HasPrefix(m.HTML[body+strings.Index(m.HTML[body:], ">")+1:], "\n"+hidden+"\n<table") {
			t.Fatalf("%s: preheader is not the hidden first element of the body", name)
		}
		if strings.Contains(m.Text, want.preheader) {
			t.Fatalf("%s: preheader leaked into the text part", name)
		}
		if !strings.HasSuffix(m.Text, closing+"\n\n"+footerLine+"\n\nHelivanta · https://helivanta.app") || strings.Contains(m.Text, "<") || strings.Contains(m.Text, "&zwnj;") || strings.Contains(m.Text, "&middot;") {
			t.Fatalf("%s: text part lacks the closing and footer lines or carries markup", name)
		}
		if strings.Count(m.HTML, "<h1") != 0 || strings.Contains(m.Text, "per hour") || strings.Contains(m.HTML, "per hour") {
			t.Fatalf("%s: template-local markup or rate limits leaked", name)
		}
		for _, code := range []string{"12345678", "87654321", "code-1", "code-2", staff.Code} {
			if strings.Contains(m.Subject, code) || strings.Contains(want.preheader, code) {
				t.Fatalf("%s: code in subject or preheader", name)
			}
		}
		if want.code != "" && !strings.Contains(m.Text, "\n\n"+want.code+"\n\n") {
			t.Fatalf("%s: text part lacks the code on its own line", name)
		}
		// A button's URL, or a code, appears in both parts.
		if want.code == "" {
			start := strings.Index(m.HTML, "<a href=\"") + 9
			link := m.HTML[start : start+strings.Index(m.HTML[start:], "\"")]
			if !strings.Contains(m.HTML, `" style="display:inline-block;background:#0b1324;color:#ffffff;`) || link == "https://helivanta.app" || !strings.Contains(m.Text, strings.ReplaceAll(link, "&amp;", "&")) {
				t.Fatalf("%s: text part lacks the button link", name)
			}
		}
	}
}

// helivanta#1857: the frame's text colours meet WCAG AA (4.5:1) on the
// surface they sit on; the 13px footer #4a5568 on the #f6f8fb canvas is 7.07:1.
func TestFrameColoursMeetAAContrast(t *testing.T) {
	luminance := func(hex string) float64 {
		var rgb [3]float64
		for i := range rgb {
			v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
			c := float64(v) / 255
			if c <= 0.04045 {
				rgb[i] = c / 12.92
			} else {
				rgb[i] = math.Pow((c+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
	}
	for _, pair := range [][2]string{{"#0b1324", "#ffffff"}, {"#ffffff", "#0b1324"}, {"#4a5568", "#ffffff"}, {"#4a5568", "#f6f8fb"}, {"#2b4ee6", "#f6f8fb"}} {
		hi, lo := luminance(pair[0]), luminance(pair[1])
		if hi < lo {
			hi, lo = lo, hi
		}
		if ratio := (hi + 0.05) / (lo + 0.05); ratio < 4.5 {
			t.Fatalf("%s on %s is %.2f:1", pair[0], pair[1], ratio)
		}
	}
}

func TestLayoutRefusesIncompleteContent(t *testing.T) {
	if _, _, err := render(content{Subject: "s — Helivanta", Preheader: "p", Heading: "h", Line: "l", Code: "1", Expiry: "e"}); err != nil {
		t.Fatal("complete content refused")
	}
	for name, bad := range map[string]content{
		"no expiry":         {Subject: "s — Helivanta", Preheader: "p", Heading: "h", Line: "l", Code: "1"},
		"button w/o url":    {Subject: "s — Helivanta", Preheader: "p", Heading: "h", Line: "l", Button: "Go", Expiry: "e"},
		"neither":           {Subject: "s — Helivanta", Preheader: "p", Heading: "h", Line: "l", Expiry: "e"},
		"newline subject":   {Subject: "s\r\nBcc: x — Helivanta", Preheader: "p", Heading: "h", Line: "l", Code: "1", Expiry: "e"},
		"no preheader":      {Subject: "s — Helivanta", Heading: "h", Line: "l", Code: "1", Expiry: "e"},
		"newline preheader": {Subject: "s — Helivanta", Preheader: "p\nq", Heading: "h", Line: "l", Code: "1", Expiry: "e"},
		"return preheader":  {Subject: "s — Helivanta", Preheader: "p\rq", Heading: "h", Line: "l", Code: "1", Expiry: "e"},
	} {
		if _, _, err := render(bad); err == nil {
			t.Fatalf("%s rendered", name)
		}
	}
}

// hasClosing accepts the shared closing line in either part's encoding.
func hasClosing(part string) bool {
	return strings.Contains(part, closing) || strings.Contains(part, template.HTMLEscapeString(closing))
}
