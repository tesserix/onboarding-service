package customercontract

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

// StaffMFAAdmissionRead is private to the verified HMS login workload.
type StaffMFAAdmissionRead struct {
	TenantID     string `json:"tenantId" format:"uuid" required:"true"`
	Subject      string `json:"subject" pattern:"^[A-Za-z0-9_-]{1,200}$" required:"true"`
	GenerationID string `json:"generationId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
	RequestID    string `json:"requestId" format:"uuid" required:"true"`
}

func (v StaffMFAAdmissionRead) Validate() error { return validateTaggedFields(v, "") }
func (StaffMFAAdmissionRead) String() string    { return "[redacted staff MFA admission scope]" }
func (StaffMFAAdmissionRead) GoString() string  { return "[redacted staff MFA admission scope]" }
func (v *StaffMFAAdmissionRead) UnmarshalJSON(raw []byte) error {
	type wire StaffMFAAdmissionRead
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFAAdmissionRead(out).Validate() != nil {
		return ErrInvalid
	}
	*v = StaffMFAAdmissionRead(out)
	return nil
}

// StaffMFAAdmissionSource discloses only the exact source HMS must reauthorize.
type StaffMFAAdmissionSource struct {
	GenerationID string                 `json:"generationId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
	Target       StaffMFARecoveryTarget `json:"target" required:"true"`
}

func (v StaffMFAAdmissionSource) Validate() error {
	if validateTaggedFields(v, "") != nil || v.Target.Validate() != nil {
		return ErrInvalid
	}
	return nil
}
func (StaffMFAAdmissionSource) String() string   { return "[redacted staff MFA admission source]" }
func (StaffMFAAdmissionSource) GoString() string { return "[redacted staff MFA admission source]" }
func (v *StaffMFAAdmissionSource) UnmarshalJSON(raw []byte) error {
	type wire StaffMFAAdmissionSource
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFAAdmissionSource(out).Validate() != nil {
		return ErrInvalid
	}
	*v = StaffMFAAdmissionSource(out)
	return nil
}

// StaffMFAAdmissionInput asserts HMS revalidated Target and verified NativeSessionID
// from the signed native ID token. Neither authority assertion comes from a browser.
type StaffMFAAdmissionInput struct {
	Scope           StaffMFAAdmissionRead  `json:"scope" required:"true"`
	Target          StaffMFARecoveryTarget `json:"target" required:"true"`
	NativeSessionID string                 `json:"nativeSessionId" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
}

func (v StaffMFAAdmissionInput) Validate() error {
	if validateTaggedFields(v, "") != nil || v.Scope.Validate() != nil || v.Target.Validate() != nil || v.Target.Subject != v.Scope.Subject {
		return ErrInvalid
	}
	return nil
}
func (StaffMFAAdmissionInput) String() string   { return "[redacted staff MFA admission input]" }
func (StaffMFAAdmissionInput) GoString() string { return "[redacted staff MFA admission input]" }
func (v *StaffMFAAdmissionInput) UnmarshalJSON(raw []byte) error {
	type wire StaffMFAAdmissionInput
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFAAdmissionInput(out).Validate() != nil {
		return ErrInvalid
	}
	*v = StaffMFAAdmissionInput(out)
	return nil
}

// StaffMFAAdmissionReceipt contains only the generation HMS may admit after its own rechecks.
type StaffMFAAdmissionReceipt struct {
	GenerationID string `json:"generationId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
}

func (v StaffMFAAdmissionReceipt) Validate() error { return validateTaggedFields(v, "") }
func (v *StaffMFAAdmissionReceipt) UnmarshalJSON(raw []byte) error {
	type wire StaffMFAAdmissionReceipt
	var out wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &out) != nil || StaffMFAAdmissionReceipt(out).Validate() != nil {
		return ErrInvalid
	}
	*v = StaffMFAAdmissionReceipt(out)
	return nil
}
