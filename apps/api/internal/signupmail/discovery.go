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
	text := "Your Helivanta sign-in code is " + code + ".\n\nEnter this code in the browser where you requested it. It expires after 10 minutes and allows five attempts. This code only lets you choose an account; your password and two-step verification are still required. Up to 3 codes per hour and 10 per 24 hours per email. If you did not request this, ignore this email and do not share the code."
	html := "<!doctype html><html><body><h1>Your Helivanta sign-in code</h1><p>Enter this code in the browser where you requested it:</p><p><strong>" + code + "</strong></p><p>It expires after 10 minutes and allows five attempts. This only lets you choose an account; your password and two-step verification are still required.</p><p>Up to 3 codes per hour and 10 per 24 hours per email. If you did not request this, ignore this email and do not share the code.</p></body></html>"
	payload, err := json.Marshal(map[string]any{"from": "Helivanta <" + c.from + ">", "to": []string{email}, "subject": "Your Helivanta sign-in code", "text": text, "html": html})
	if err != nil {
		return "", ErrUnavailable
	}
	return c.sendPayload(ctx, payload)
}
