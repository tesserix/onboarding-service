package signupmail

import (
	"context"
	"encoding/base64"
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

func (c *Client) SendStaff(ctx context.Context, message StaffMessage) (string, error) {
	email, err := mail.ParseAddress(message.Email)
	ticket, ticketErr := base64.RawURLEncoding.DecodeString(message.Ticket)
	if err != nil || email.Name != "" || email.Address != message.Email || len(message.Email) > 200 || ticketErr != nil || len(ticket) != 32 || message.InvitationID.Version() != 7 || message.Code == "" || len(message.Code) > 20 || strings.ContainsFunc(message.Code, unicode.IsControl) || !staffText(message.Name, 400) || !staffText(message.WorkspaceName, 200) || message.ExpiresAt.IsZero() {
		return "", ErrUnavailable
	}
	heading, button, preheader := "You're invited to "+message.WorkspaceName, "Accept invite", "Accept your invite to join "+message.WorkspaceName+" on Helivanta."
	switch message.Phase {
	case "email":
	case "password":
		heading, button, preheader = "Set your password for "+message.WorkspaceName, "Set password", "Set your password to finish joining "+message.WorkspaceName+"."
	default:
		return "", ErrUnavailable
	}
	link := "https://helivanta.app/staff/accept#" + url.Values{"invitation": {message.InvitationID.String()}, "ticket": {message.Ticket}, "phase": {message.Phase}, "code": {message.Code}}.Encode()
	framed, err := c.frame(message.Email, content{
		Subject:   heading + " — Helivanta",
		Preheader: preheader,
		Heading:   heading,
		Greeting:  greeting(message.Name),
		Line:      message.WorkspaceName + " has invited you to Helivanta.",
		Button:    button,
		URL:       link,
		Expiry:    "This link expires " + localTime(message.ExpiresAt) + ". Ask your administrator for another if it has expired.",
		Note:      "Your administrator confirms your access after you join.",
		Closing:   "Not expecting this invite? You can ignore this email.",
	})
	if err != nil {
		return "", err
	}
	return c.sendPayloadWithKey(ctx, framed, "helivanta-staff-invitation:"+message.InvitationID.String())
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
