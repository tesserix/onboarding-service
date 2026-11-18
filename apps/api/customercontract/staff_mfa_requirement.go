package customercontract

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

// StaffMFARecoveryRequirement is private account-wide admission state, never source authority.
type StaffMFARecoveryRequirement struct {
	State        string `json:"state" required:"true" enum:"none,blocked,enrollment_required,completed"`
	GenerationID string `json:"generationId,omitempty" format:"uuid" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$" requireWhen:"blocked,enrollment_required,completed" forbidWhen:"none"`
}

func (v StaffMFARecoveryRequirement) Validate() error {
	if validateTaggedFields(v, v.State) != nil {
		return ErrInvalid
	}
	switch v.State {
	case "none", "blocked", "enrollment_required", "completed":
	default:
		return ErrInvalid
	}
	return nil
}

func (v *StaffMFARecoveryRequirement) UnmarshalJSON(raw []byte) error {
	type wire StaffMFARecoveryRequirement
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFARecoveryRequirement(out).Validate() != nil {
		return ErrInvalid
	}
	if out.State == "none" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields["generationId"] != nil {
			return ErrInvalid
		}
	}
	*v = StaffMFARecoveryRequirement(out)
	return nil
}
