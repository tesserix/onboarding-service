package signupmail

import (
	"context"
	"encoding/json"
	"net/mail"

	"github.com/google/uuid"
)

// SendPasswordRecovery delivers a dedicated mailbox proof under its retained delivery ID.
func (c *Client) SendPasswordRecovery(ctx context.Context, id, email, code string) (string, error) {
	delivery, err := uuid.Parse(id)
	address, addressErr := mail.ParseAddress(email)
	if err != nil || delivery.Version() != 7 || delivery.Variant() != uuid.RFC4122 || delivery.String() != id || addressErr != nil || address.Name != "" || address.Address != email || len(email) > 200 || len(code) != 8 {
		return "", ErrUnavailable
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return "", ErrUnavailable
		}
	}
	notice := "Enter this code in the browser where you requested password recovery. It expires after 10 minutes and allows five attempts. Up to 3 codes per hour and 10 per 24 hours per email. After verification, no further mail is sent for this challenge. Recovery does not disable two-step verification. If you did not request this, ignore this email and do not share the code."
	payload, err := json.Marshal(map[string]any{
		"from": "Helivanta <" + c.from + ">", "to": []string{email}, "subject": "Recover your Helivanta password",
		"text": "Your Helivanta password recovery code is " + code + ".\n\n" + notice,
		"html": "<!doctype html><html><body><h1>Recover your Helivanta password</h1><p>Your recovery code:</p><p><strong>" + code + "</strong></p><p>" + notice + "</p></body></html>",
	})
	if err != nil {
		return "", ErrUnavailable
	}
	return c.sendPayloadWithKey(ctx, payload, "helivanta-password-recovery-"+id)
}
