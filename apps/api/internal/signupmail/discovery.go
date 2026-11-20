package signupmail

import (
	"context"
	"encoding/json"
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
	envelope, err := c.envelope(email, content{
		Subject: "Your Helivanta sign-in code",
		Heading: "Your sign-in code",
		Line:    "Enter this code in the browser where you asked for it.",
		Code:    code,
		Expiry:  "It expires in 10 minutes.",
		Note:    "Don't share this code with anyone.",
	})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", ErrUnavailable
	}
	return c.sendPayload(ctx, payload)
}
