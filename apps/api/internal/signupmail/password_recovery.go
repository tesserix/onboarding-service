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
	envelope, err := c.envelope(email, content{
		Subject:   "Reset your password — Helivanta",
		Preheader: "Enter this code to reset your password. It expires in 10 minutes.",
		Heading:   "Reset your password",
		Line:      "Enter this code in the browser where you asked to reset your password.",
		Code:      code,
		Expiry:    "It expires in 10 minutes.",
		Note:      "Resetting your password does not turn off two-step verification. Don't share this code with anyone.",
	})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", ErrUnavailable
	}
	return c.sendPayloadWithKey(ctx, payload, "helivanta-password-recovery-"+id)
}
