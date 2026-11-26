package signupmail

import (
	"context"
	"net/mail"
)

// SendDiscovery sends a transient mailbox code without disclosing account existence.
func (c *Client) SendDiscovery(ctx context.Context, email, code string) (string, error) {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email || len(email) > 200 || len(code) != 8 {
		return "", ErrUnavailable
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return "", ErrUnavailable
		}
	}
	// The code is never in the subject: subjects show in notifications and
	// previews that a shared screen can read.
	framed, err := c.frame(email, content{
		Subject:   "Your sign-in code — Helivanta",
		Preheader: "Enter this code to sign in. It expires in 10 minutes.",
		Heading:   "Your sign-in code",
		Line:      "Enter this code in the browser where you asked for it.",
		Code:      code,
		Expiry:    "It expires in 10 minutes.",
		Note:      "Don't share this code with anyone.",
		Closing:   "Didn't ask for this code? You can ignore this email.",
	})
	if err != nil {
		return "", err
	}
	return c.sendPayload(ctx, framed)
}
