package signupmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// StaffMessage is transient; link tickets and native codes are never retained in diagnostics.
type StaffMessage struct {
	Email         string
	Name          string
	WorkspaceName string
	InvitationID  uuid.UUID
	Ticket        string
	Code          string
	Phase         string
	ExpiresAt     time.Time
}

func (StaffMessage) String() string   { return "StaffMessage{redacted}" }
func (StaffMessage) GoString() string { return "StaffMessage{redacted}" }

var staffBody = template.Must(template.New("staff").Parse(`<h1>{{.Heading}}</h1><p>Hello {{.Name}},</p><p>You have been invited to join {{.Workspace}} on Helivanta.</p><p><a href="{{.Link}}">{{.Heading}}</a></p><p>This link expires at {{.Expiry}}. Ask your organisation administrator for another link if it expires.</p><p>Your organisation administrator will confirm your staff access after account setup.</p>`))

func (c *Client) SendStaff(ctx context.Context, message StaffMessage) (string, error) {
	email, err := mail.ParseAddress(message.Email)
	ticket, ticketErr := base64.RawURLEncoding.DecodeString(message.Ticket)
	if err != nil || email.Name != "" || email.Address != message.Email || len(message.Email) > 200 || ticketErr != nil || len(ticket) != 32 || message.InvitationID.Version() != 7 || message.Code == "" || len(message.Code) > 20 || strings.ContainsFunc(message.Code, unicode.IsControl) || !staffText(message.Name, 400) || !staffText(message.WorkspaceName, 200) || message.ExpiresAt.IsZero() {
		return "", ErrUnavailable
	}
	heading := "Verify your staff email"
	switch message.Phase {
	case "email":
	case "password":
		heading = "Set your staff password"
	default:
		return "", ErrUnavailable
	}
	link := "https://helivanta.app/staff/accept#" + url.Values{"invitation": {message.InvitationID.String()}, "ticket": {message.Ticket}, "phase": {message.Phase}, "code": {message.Code}}.Encode()
	expiry := message.ExpiresAt.UTC().Format("02 Jan 2006 15:04 MST")
	view := struct{ Heading, Name, Workspace, Link, Expiry string }{heading, message.Name, message.WorkspaceName, link, expiry}
	var html bytes.Buffer
	if err := staffBody.Execute(&html, view); err != nil {
		return "", ErrUnavailable
	}
	text := heading + "\n\nHello " + message.Name + ",\n\nYou have been invited to join " + message.WorkspaceName + " on Helivanta.\n\n" + link + "\n\nThis link expires at " + expiry + ". Ask your organisation administrator for another link if it expires.\n\nYour organisation administrator will confirm your staff access after account setup."
	payload, err := json.Marshal(map[string]any{"from": "Helivanta <" + c.from + ">", "to": []string{message.Email}, "subject": heading + " — Helivanta", "html": html.String(), "text": text})
	if err != nil {
		return "", ErrUnavailable
	}
	return c.sendPayloadWithKey(ctx, payload, "helivanta-staff-invitation:"+message.InvitationID.String())
}

func staffText(value string, max int) bool {
	if strings.TrimSpace(value) == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
