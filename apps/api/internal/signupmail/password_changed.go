package signupmail

import (
	"context"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// SendPasswordChanged tells an account holder their password changed, in
// Helivanta's words with a Helivanta link (onboarding#51). It replaces
// Zitadel's instance notice, which the operator turns off per organisation.
// Sent only for a real change (recovery today), never during signup, and
// bound to the operation id so a retried completion cannot mail twice.
func (c *Client) SendPasswordChanged(ctx context.Context, operationID uuid.UUID, email, workspace string, at time.Time) (string, error) {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email || len(email) > 200 || operationID == uuid.Nil || at.IsZero() || strings.TrimSpace(workspace) == "" || len(workspace) > 200 || strings.ContainsFunc(workspace, unicode.IsControl) {
		return "", ErrUnavailable
	}
	framed, err := c.frame(email, content{
		Subject:   "Your password was changed — Helivanta",
		Preheader: "If this wasn't you, reset your password now.",
		Heading:   "Your password was changed",
		Line:      "The password for " + workspace + " was changed on " + at.UTC().Format("02 Jan 2006 at 15:04 MST") + ".",
		Button:    "Reset password",
		URL:       "https://helivanta.app/recover-password",
		Expiry:    "If this was you, there's nothing to do.",
		Note:      "If it wasn't, reset your password now and tell your administrator.",
	})
	if err != nil {
		return "", err
	}
	return c.sendPayloadWithKey(ctx, framed, "helivanta-password-changed:"+operationID.String())
}
