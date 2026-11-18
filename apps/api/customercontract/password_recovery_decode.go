package customercontract

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

// UnmarshalJSON rejects ambiguous or incomplete recovery authority at the wire boundary.
func (in *PasswordRecoveryStart) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryStart
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryStart(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryStart(value)
	return nil
}

// UnmarshalJSON rejects ambiguous or incomplete recovery authority at the wire boundary.
func (in *PasswordRecoveryAccess) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryAccess
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryAccess(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryAccess(value)
	return nil
}

// UnmarshalJSON rejects ambiguous or incomplete recovery authority at the wire boundary.
func (in *PasswordRecoveryProof) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryProof
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryProof(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryProof(value)
	return nil
}

// UnmarshalJSON rejects ambiguous or incomplete recovery authority at the wire boundary.
func (in *PasswordRecoveryPassword) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryPassword
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryPassword(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryPassword(value)
	return nil
}

// UnmarshalJSON rejects ambiguous delivery receipt bindings at the private boundary.
func (in *PasswordRecoveryAccepted) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryAccepted
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryAccepted(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryAccepted(value)
	return nil
}

// UnmarshalJSON rejects ambiguous browser recovery inputs.
func (in *PasswordRecoveryRequest) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryRequest
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryRequest(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryRequest(value)
	return nil
}

// UnmarshalJSON rejects ambiguous browser recovery inputs.
func (in *PasswordRecoverySubmit) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoverySubmit
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoverySubmit(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoverySubmit(value)
	return nil
}

// UnmarshalJSON rejects ambiguous browser recovery inputs.
func (in *PasswordRecoveryCode) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryCode
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryCode(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryCode(value)
	return nil
}

// UnmarshalJSON rejects invented or ambiguous recovery outcomes.
func (in *PasswordRecoveryStatus) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryStatus
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryStatus(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryStatus(value)
	return nil
}

// UnmarshalJSON rejects invented or ambiguous recovery outcomes.
func (in *PasswordRecoveryResult) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryResult
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryResult(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryResult(value)
	return nil
}

// UnmarshalJSON refuses ambiguous recovery command selectors.
func (in *PasswordRecoveryIntent) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryIntent
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryIntent(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryIntent(value)
	return nil
}

// UnmarshalJSON refuses ambiguous recovery command selectors.
func (in *PasswordRecoveryRead) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryRead
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryRead(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryRead(value)
	return nil
}

// UnmarshalJSON refuses ambiguous recovery command selectors.
func (in *PasswordRecoveryCommand) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryCommand
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryCommand(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryCommand(value)
	return nil
}

// UnmarshalJSON rejects ambiguous cancellation selectors.
func (in *PasswordRecoveryCancellation) UnmarshalJSON(raw []byte) error {
	type wire PasswordRecoveryCancellation
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || PasswordRecoveryCancellation(value).Validate() != nil {
		return ErrInvalid
	}
	*in = PasswordRecoveryCancellation(value)
	return nil
}
