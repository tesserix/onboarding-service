package customercontract

import (
	"github.com/google/uuid"
	"unicode/utf8"
)

// PasswordRecoveryRequest selects a native login request, never a browser-supplied tenant.
type PasswordRecoveryRequest struct {
	AuthRequestID string `json:"auth_request_id" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
	RequestID     string `json:"request_id" required:"true" format:"uuid"`
	Email         string `json:"email" required:"true" format:"email" max:"200" clean:"true"`
}

func (in PasswordRecoveryRequest) Validate() error {
	if validateTaggedFields(in, "") != nil || !recoveryCommandID(in.RequestID) || (DiscoveryRequest{Email: in.Email}).Validate() != nil {
		return ErrInvalid
	}
	return nil
}

// PasswordRecoverySubmit contains only the retained command selector and transient new password.
type PasswordRecoverySubmit struct {
	CommandID string `json:"command_id" required:"true" format:"uuid"`
	Password  string `json:"password" required:"true" max:"200"`
}

func (in PasswordRecoverySubmit) Validate() error {
	if !recoveryCommandID(in.CommandID) || in.Password == "" || !utf8.ValidString(in.Password) || utf8.RuneCountInString(in.Password) > 200 {
		return ErrInvalid
	}
	return nil
}
func (PasswordRecoverySubmit) String() string   { return "[redacted password recovery submission]" }
func (PasswordRecoverySubmit) GoString() string { return "[redacted password recovery submission]" }

// PasswordRecoveryCode carries no browser capability or tenant authority.
type PasswordRecoveryCode DiscoveryCode

func (in PasswordRecoveryCode) Validate() error {
	if validateTaggedFields(in, "") != nil {
		return ErrInvalid
	}
	return nil
}
func (PasswordRecoveryCode) String() string   { return "[redacted password recovery code]" }
func (PasswordRecoveryCode) GoString() string { return "[redacted password recovery code]" }

func recoveryCommandID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 7 && id.Variant() == uuid.RFC4122 && id.String() == value
}

// PasswordRecoveryCommand selects a browser intent without a credential payload.
type PasswordRecoveryCommand struct {
	CommandID string `json:"command_id" required:"true" format:"uuid"`
}

func (in PasswordRecoveryCommand) Validate() error {
	if !recoveryCommandID(in.CommandID) {
		return ErrInvalid
	}
	return nil
}
