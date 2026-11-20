package signupmail

import (
	"bytes"
	"html/template"
	"strings"
)

// content is everything a Helivanta email may say (onboarding#49). Every
// template fills these fields and nothing else; the frame, order and footer
// are shared, and the text part is generated from the same fields so the two
// parts cannot drift. Rate limits and account mechanics are not fields on
// purpose: they are told on refusal, in the product, not in mail.
type content struct {
	Subject  string
	Heading  string
	Greeting string // "Hi Priya," or empty
	Line     string // one sentence of context
	Button   string // button label; needs URL
	URL      string
	Code     string // shown instead of a button
	Expiry   string // "This link expires in 1 hour."
	Note     string // one optional extra sentence
}

const closing = "Didn't ask for this? You can ignore this email."

// layoutHTML is a single-column, table-based frame in the app's slate palette
// with inline styles only, which is what mail clients render reliably.
var layoutHTML = template.Must(template.New("layout").Parse(`<!doctype html><html lang="en"><body style="margin:0;padding:24px;background:#f8fafc;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#0f172a">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">
<table role="presentation" width="480" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border:1px solid #e2e8f0;border-radius:12px">
<tr><td style="padding:28px 32px 0 32px;font-size:18px;font-weight:600;letter-spacing:-0.01em">Helivanta</td></tr>
<tr><td style="padding:20px 32px 0 32px;font-size:22px;font-weight:600;line-height:1.3">{{.Heading}}</td></tr>
{{if .Greeting}}<tr><td style="padding:16px 32px 0 32px;font-size:15px;line-height:1.5">{{.Greeting}}</td></tr>{{end}}
<tr><td style="padding:8px 32px 0 32px;font-size:15px;line-height:1.5">{{.Line}}</td></tr>
{{if .Code}}<tr><td style="padding:20px 32px 0 32px;font-size:28px;font-weight:600;letter-spacing:0.3em;font-family:ui-monospace,SFMono-Regular,Menlo,monospace">{{.Code}}</td></tr>{{end}}
{{if .Button}}<tr><td style="padding:20px 32px 0 32px"><a href="{{.URL}}" style="display:inline-block;background:#1e293b;color:#ffffff;text-decoration:none;font-size:15px;font-weight:600;padding:12px 20px;border-radius:8px">{{.Button}}</a></td></tr>{{end}}
<tr><td style="padding:16px 32px 0 32px;font-size:13px;line-height:1.5;color:#475569">{{.Expiry}}</td></tr>
{{if .Note}}<tr><td style="padding:8px 32px 0 32px;font-size:13px;line-height:1.5;color:#475569">{{.Note}}</td></tr>{{end}}
<tr><td style="padding:24px 32px 28px 32px;font-size:13px;line-height:1.5;color:#64748b">{{.Closing}}</td></tr>
</table>
<p style="margin:16px 0 0 0;font-size:12px;color:#94a3b8">Sent by Helivanta &lt;{{.From}}&gt;</p>
</td></tr></table></body></html>`))

// render produces the HTML and text parts from one content value. The text
// part lists the same fields in the same order; a link appears bare so every
// client can open it.
func render(from string, c content) (string, string, error) {
	if c.Subject == "" || c.Heading == "" || c.Line == "" || c.Expiry == "" || (c.Button != "") != (c.URL != "") || (c.Button == "" && c.Code == "") || strings.ContainsAny(c.Subject, "\r\n") {
		return "", "", ErrUnavailable
	}
	var html bytes.Buffer
	view := struct {
		content
		Closing, From string
	}{c, closing, from}
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
	parts = append(parts, closing)
	return html.String(), strings.Join(parts, "\n\n"), nil
}

// envelope is the provider payload every sender builds the same way.
func (c *Client) envelope(to string, body content) (map[string]any, error) {
	html, text, err := render(c.from, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{"from": "Helivanta <" + c.from + ">", "to": []string{to}, "subject": body.Subject, "html": html, "text": text}, nil
}

func greeting(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return "Hi " + name + ","
}
