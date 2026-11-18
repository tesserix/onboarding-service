package customercontract

import (
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

// StaffMFAResetInput selects a reviewed Staff link, never a browser-chosen account.
type StaffMFAResetInput struct {
	CommandID            string `json:"commandId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
	ExpectedLinkID       string `json:"expectedLinkId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
	ExpectedLinkVersion  int64  `json:"expectedLinkVersion" required:"true" minValue:"1" maxValue:"9007199254740991"`
	ExpectedStaffVersion int64  `json:"expectedStaffVersion" required:"true" minValue:"1" maxValue:"9007199254740991"`
	Reason               string `json:"reason" required:"true" clean:"true" max:"500"`
}

func (in StaffMFAResetInput) Validate() error {
	if validateTaggedFields(in, "") != nil || !profileStringsValid(in) || !recoveryCommandID(in.CommandID) || !recoveryCommandID(in.ExpectedLinkID) {
		return ErrInvalid
	}
	return nil
}

func (StaffMFAResetInput) String() string   { return "[redacted staff MFA reset intent]" }
func (StaffMFAResetInput) GoString() string { return "[redacted staff MFA reset intent]" }

// StaffMFARecoveryTarget is resolved by Workforce from the current reviewed link.
// Its wire shape is not proof of employment, account ownership or reset authority.
type StaffMFARecoveryTarget struct {
	TenantID     string `json:"tenantId" format:"uuid" required:"true"`
	EmployerKind string `json:"employerKind" enum:"hospital,pharmacy,supplier" required:"true"`
	EmployerID   string `json:"employerId" format:"uuid" required:"true"`
	StaffID      string `json:"staffId" format:"uuid" required:"true"`
	StaffVersion int64  `json:"staffVersion" required:"true" minValue:"1" maxValue:"9007199254740991"`
	LinkID       string `json:"linkId" format:"uuid" required:"true" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
	LinkVersion  int64  `json:"linkVersion" required:"true" minValue:"1" maxValue:"9007199254740991"`
	Subject      string `json:"subject" pattern:"^[A-Za-z0-9_-]{1,200}$" required:"true"`
}

func (in StaffMFARecoveryTarget) Validate() error {
	if validateTaggedFields(in, "") != nil || !recoveryCommandID(in.LinkID) ||
		(in.EmployerKind != "hospital" && in.EmployerKind != "pharmacy" && in.EmployerKind != "supplier") {
		return ErrInvalid
	}
	return nil
}

// StaffMFARecoveryRequest crosses only the authenticated product-workload boundary.
type StaffMFARecoveryRequest struct {
	ActorSubject string                 `json:"actorSubject" pattern:"^[A-Za-z0-9_-]{1,200}$" required:"true"`
	Target       StaffMFARecoveryTarget `json:"target" required:"true"`
	Intent       StaffMFAResetInput     `json:"intent" required:"true"`
}

func (in StaffMFARecoveryRequest) Validate() error {
	if validateTaggedFields(in, "") != nil || in.Target.Validate() != nil || in.Intent.Validate() != nil ||
		in.ActorSubject == in.Target.Subject || in.Intent.ExpectedLinkID != in.Target.LinkID ||
		in.Intent.ExpectedLinkVersion != in.Target.LinkVersion || in.Intent.ExpectedStaffVersion != in.Target.StaffVersion {
		return ErrInvalid
	}
	return nil
}

func (StaffMFARecoveryRequest) String() string   { return "[redacted staff MFA recovery request]" }
func (StaffMFARecoveryRequest) GoString() string { return "[redacted staff MFA recovery request]" }

func (in *StaffMFAResetInput) UnmarshalJSON(raw []byte) error {
	type wire StaffMFAResetInput
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || StaffMFAResetInput(value).Validate() != nil {
		return ErrInvalid
	}
	*in = StaffMFAResetInput(value)
	return nil
}

func (in *StaffMFARecoveryTarget) UnmarshalJSON(raw []byte) error {
	type wire StaffMFARecoveryTarget
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || StaffMFARecoveryTarget(value).Validate() != nil {
		return ErrInvalid
	}
	*in = StaffMFARecoveryTarget(value)
	return nil
}

func (in *StaffMFARecoveryRequest) UnmarshalJSON(raw []byte) error {
	type wire StaffMFARecoveryRequest
	var value wire
	if !utf8.Valid(raw) || strictProjectionObject(raw, reflect.TypeFor[wire]()) != nil || json.Unmarshal(raw, &value) != nil || StaffMFARecoveryRequest(value).Validate() != nil {
		return ErrInvalid
	}
	*in = StaffMFARecoveryRequest(value)
	return nil
}
