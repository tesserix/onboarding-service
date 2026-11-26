// Package signupmail sends native identity links through Helivanta's email
// provider.
//
// Every Helivanta email goes through the shared layout in this file
// (helivanta#1857). The provider transport (resend.go) accepts only a
// framedMail, and only frame below builds one, from a content value rendered
// by layoutHTML; render refuses a subject without the " — Helivanta" suffix.
// TestEveryEmailRendersThroughTheSharedLayout fails CI when a Send method of
// *Client has no entry in its table; guard_test.go fails CI when a file other
// than this one carries HTML or the "html" payload key or
// builds a framedMail, or when a file other than resend.go names the provider.
package signupmail

import (
	"bytes"
	"encoding/json"
	"html/template"
	"strings"
	"time"
)

// content is everything a Helivanta email may say (onboarding#49). Every
// template fills these fields and nothing else; the frame, order and footer
// are shared, and the text part is generated from the same fields so the two
// parts cannot drift. Rate limits and account mechanics are not fields on
// purpose: they are told on refusal, in the product, not in mail.
type content struct {
	Subject string
	// Preheader is the inbox preview line, hidden in the body. Required so the
	// preview never falls back to the header and heading text.
	Preheader string
	Heading   string
	Greeting  string // "Hi Priya," or empty
	Line      string // one sentence of context
	Button    string // button label; needs URL
	URL       string
	Code      string // shown instead of a button
	Expiry    string // "This link expires in 1 hour."
	Note      string // one optional extra sentence
	// Closing tells the reader what to do if they didn't expect the mail, in
	// that mail's terms (helivanta#1858). Security notices leave it empty: their
	// body already says what to do, and "ignore this email" would contradict
	// it. TestEveryEmailRendersThroughTheSharedLayout pins each template's line.
	Closing string
}

// subjectSuffix ends every subject so inboxes group Helivanta mail.
const subjectSuffix = " — Helivanta"

// footerLine says why the mail arrived and who to ask; both parts carry it.
const footerLine = "You're getting this email because of an action on your Helivanta account. Questions? Contact your organisation's administrator."

// layoutHTML is a single-column, table-based frame in the #1834 palette with
// inline styles only, which is what mail clients render reliably. Contrast
// (WCAG): ink #0b1324 on #ffffff 18.5:1; secondary #4a5568 on #ffffff 7.5:1
// and on the #f6f8fb canvas (footer, 13px) 7.1:1; accent #2b4ee6 on #f6f8fb 5.9:1.
var layoutHTML = template.Must(template.New("layout").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light"><title>{{.Subject}}</title></head><body style="margin:0;padding:24px;background:#f6f8fb;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#0b1324">
<div style="display:none;max-height:0;overflow:hidden;mso-hide:all;font-size:1px;line-height:1px;color:#f6f8fb;opacity:0">{{.Preheader}}&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border:1px solid #dbe2ec;border-radius:12px">
<tr><td style="padding:28px 32px 0 32px"><img src="https://helivanta.app/email/helivanta-lockup.png" width="160" height="41" alt="Helivanta" style="display:block;border:0;outline:none;text-decoration:none;height:auto;width:160px"></td></tr>
<tr><td style="padding:20px 32px 0 32px;font-size:22px;font-weight:600;line-height:1.3">{{.Heading}}</td></tr>
{{if .Greeting}}<tr><td style="padding:16px 32px 0 32px;font-size:15px;line-height:1.5">{{.Greeting}}</td></tr>{{end}}
<tr><td style="padding:8px 32px 0 32px;font-size:15px;line-height:1.5">{{.Line}}</td></tr>
{{if .Code}}<tr><td style="padding:20px 32px 0 32px;font-size:28px;font-weight:600;letter-spacing:0.3em;font-family:ui-monospace,SFMono-Regular,Menlo,monospace">{{.Code}}</td></tr>{{end}}
{{if .Button}}<tr><td style="padding:20px 32px 0 32px"><a href="{{.URL}}" style="display:inline-block;background:#0b1324;color:#ffffff;text-decoration:none;font-size:15px;font-weight:600;padding:12px 20px;border-radius:8px">{{.Button}}</a></td></tr>{{end}}
<tr><td style="padding:16px 32px 0 32px;font-size:13px;line-height:1.5;color:#4a5568">{{.Expiry}}</td></tr>
{{if .Note}}<tr><td style="padding:8px 32px 0 32px;font-size:13px;line-height:1.5;color:#4a5568">{{.Note}}</td></tr>{{end}}
{{if .Closing}}<tr><td style="padding:24px 32px 28px 32px;font-size:13px;line-height:1.5;color:#4a5568">{{.Closing}}</td></tr>{{else}}<tr><td style="height:28px;font-size:0;line-height:0">&nbsp;</td></tr>{{end}}
</table>
<p style="margin:16px 0 0 0;max-width:480px;font-size:13px;line-height:1.5;color:#4a5568">{{.Footer}}</p>
<p style="margin:4px 0 0 0;font-size:13px;line-height:1.5;color:#4a5568">Helivanta &middot; <a href="https://helivanta.app" style="color:#2b4ee6;text-decoration:underline">helivanta.app</a></p>
</td></tr></table></body></html>`))

// render produces the HTML and text parts from one content value. The text
// part lists the same fields in the same order; a link appears bare so every
// client can open it.
func render(c content) (string, string, error) {
	if c.Subject == "" || c.Preheader == "" || strings.ContainsAny(c.Preheader, "\r\n") || c.Heading == "" || c.Line == "" || c.Expiry == "" || (c.Button != "") != (c.URL != "") || (c.Button == "" && c.Code == "") || strings.ContainsAny(c.Subject, "\r\n") || !strings.HasSuffix(c.Subject, subjectSuffix) || strings.TrimSpace(strings.TrimSuffix(c.Subject, subjectSuffix)) == "" {
		return "", "", ErrUnavailable
	}
	var html bytes.Buffer
	view := struct {
		content
		Footer string
	}{c, footerLine}
	if err := layoutHTML.Execute(&html, view); err != nil {
		return "", "", ErrUnavailable
	}
	parts := []string{c.Heading}
	if c.Greeting != "" {
		parts = append(parts, c.Greeting)
	}
	parts = append(parts, c.Line)
	if c.Code != "" {
		parts = append(parts, c.Code)
	}
	if c.URL != "" {
		// The bare link on its own line: plain-text clients linkify it and
		// readers can copy it without the label.
		parts = append(parts, c.Button+":", c.URL)
	}
	parts = append(parts, c.Expiry)
	if c.Note != "" {
		parts = append(parts, c.Note)
	}
	if c.Closing != "" {
		parts = append(parts, c.Closing)
	}
	parts = append(parts, footerLine, "Helivanta · https://helivanta.app")
	return html.String(), strings.Join(parts, "\n\n"), nil
}

// framedMail is the only value the transport sends. Its payload is set only
// by frame, so a sender cannot post a body it built itself; guard_test.go
// fails if another file constructs one or reads its payload.
type framedMail struct{ payload []byte }

// frame renders body through the shared layout into the provider payload.
func (c *Client) frame(to string, body content) (framedMail, error) {
	html, text, err := render(body)
	if err != nil {
		return framedMail{}, err
	}
	payload, err := json.Marshal(map[string]any{"from": "Helivanta <" + c.from + ">", "to": []string{to}, "subject": body.Subject, "html": html, "text": text})
	if err != nil {
		return framedMail{}, ErrUnavailable
	}
	return framedMail{payload: payload}, nil
}

// ist is India Standard Time. India keeps no daylight saving, so the fixed
// offset is exact; tenants carry no timezone yet (helivanta#1858).
var ist = time.FixedZone("IST", 5*60*60+30*60)

// localTime reads "26 Nov 2026 at 14:05 IST (08:35 UTC)". The bracket names the
// UTC date too when it falls on another day, so it never implies the wrong one.
func localTime(at time.Time) string {
	local, utc := at.In(ist), at.UTC()
	bracket := utc.Format("15:04")
	if utc.Day() != local.Day() {
		bracket = utc.Format("02 Jan 15:04")
	}
	return local.Format("02 Jan 2006 at 15:04 MST") + " (" + bracket + " UTC)"
}

func greeting(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "Hi " + name + ","
}
