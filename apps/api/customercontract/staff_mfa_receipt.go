package customercontract

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

type StaffMFARecoveryReceipt struct {
	CommandID string `json:"commandId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
	State     string `json:"state" required:"true" enum:"prepared,processing,awaiting_enrollment,unconfirmed,cancelled,completed"`
}

func (in StaffMFARecoveryReceipt) Validate() error {
	if validateTaggedFields(in, "") != nil {
		return ErrInvalid
	}
	switch in.State {
	case "prepared", "processing", "awaiting_enrollment", "unconfirmed", "cancelled", "completed":
		return nil
	default:
		return ErrInvalid
	}
}

func (in *StaffMFARecoveryReceipt) UnmarshalJSON(raw []byte) error {
	type wire StaffMFARecoveryReceipt
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFARecoveryReceipt(out).Validate() != nil {
		return ErrInvalid
	}
	*in = StaffMFARecoveryReceipt(out)
	return nil
}
