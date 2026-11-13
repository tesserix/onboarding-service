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
	if decodeStringFields(raw, keys...) != nil || json.Unmarshal(raw, &in) != nil || in.Validate(action) != nil {
		return StaffRecipientInput{}, ErrInvalid
	}
	return in, nil
}

// DecodeStaffContact rejects caller-supplied authority before the product binds its verified actor.
func DecodeStaffContact(raw []byte) (StaffIdentityCreateInput, error) {
	var in StaffIdentityCreateInput
	if decodeStringFields(raw, "email", "given_name", "family_name", "reason") != nil || json.Unmarshal(raw, &in) != nil {
		return StaffIdentityCreateInput{}, ErrInvalid
	}
	return in, nil
}

func decodeStringFields(raw []byte, keys ...string) error {
	if len(raw) > 4096 || !utf8.Valid(raw) {
		return ErrInvalid
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	seen := make(map[string]bool, len(keys))
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return ErrInvalid
	}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || seen[key] {
			return ErrInvalid
		}
		seen[key] = true
		var value string
		if d.Decode(&value) != nil || value == "" {
			return ErrInvalid
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') || len(seen) != len(keys) || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
