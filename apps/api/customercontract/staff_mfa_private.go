package customercontract

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

// StaffMFADispatchRequest is an authenticated product assertion that IAM committed
// the matching application revocation. The browser must never supply this body.
type StaffMFADispatchRequest struct {
	Request            StaffMFARecoveryRequest `json:"request" required:"true"`
	ApplicationFenceID string                  `json:"applicationFenceId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
}

func (in StaffMFADispatchRequest) Validate() error {
	if validateTaggedFields(in, "") != nil || in.Request.Validate() != nil || in.ApplicationFenceID != in.Request.Intent.CommandID {
		return ErrInvalid
	}
	return nil
}
func (StaffMFADispatchRequest) String() string   { return "[redacted staff MFA dispatch request]" }
func (StaffMFADispatchRequest) GoString() string { return "[redacted staff MFA dispatch request]" }
func (in *StaffMFADispatchRequest) UnmarshalJSON(raw []byte) error {
	type wire StaffMFADispatchRequest
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFADispatchRequest(out).Validate() != nil {
		return ErrInvalid
	}
	*in = StaffMFADispatchRequest(out)
	return nil
}

// StaffMFARecoverySnapshot is private to the workload that initiated the command.
type StaffMFARecoverySnapshot struct {
	Request StaffMFARecoveryRequest `json:"request" required:"true"`
	Receipt StaffMFARecoveryReceipt `json:"receipt" required:"true"`
}

func (in StaffMFARecoverySnapshot) Validate() error {
	if in.Request.Validate() != nil || in.Receipt.Validate() != nil || in.Request.Intent.CommandID != in.Receipt.CommandID {
		return ErrInvalid
	}
	return nil
}
func (StaffMFARecoverySnapshot) String() string   { return "[redacted staff MFA recovery snapshot]" }
func (StaffMFARecoverySnapshot) GoString() string { return "[redacted staff MFA recovery snapshot]" }
func (in *StaffMFARecoverySnapshot) UnmarshalJSON(raw []byte) error {
	type wire StaffMFARecoverySnapshot
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFARecoverySnapshot(out).Validate() != nil {
		return ErrInvalid
	}
	*in = StaffMFARecoverySnapshot(out)
	return nil
}
