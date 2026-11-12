package staffcontract

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// DecodeRecipient preserves closed-body semantics across the private HTTP handoff.
func DecodeRecipient(raw []byte, action string) (StaffRecipientInput, error) {
	var in StaffRecipientInput
	if len(raw) > 4096 || !utf8.Valid(raw) {
		return in, ErrInvalid
	}
	keys := []string{"invitation_id", "ticket"}
	switch action {
	case "status":
	case "email":
		keys = append(keys, "code")
	case "password":
		keys = append(keys, "code", "password")
	case "resend":
		keys = append(keys, "delivery_id")
	default:
		return in, ErrInvalid
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	seen := make(map[string]bool, len(keys))
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return in, ErrInvalid
	}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return in, ErrInvalid
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || seen[key] {
			return in, ErrInvalid
		}
		seen[key] = true
		var value string
		if d.Decode(&value) != nil || value == "" {
			return in, ErrInvalid
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') || len(seen) != len(keys) || d.Decode(new(any)) != io.EOF || json.Unmarshal(raw, &in) != nil || in.Validate(action) != nil {
		return StaffRecipientInput{}, ErrInvalid
	}
	return in, nil
}
