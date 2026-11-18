package customercontract

import (
	"github.com/google/uuid"
	"unicode/utf8"
)

// PasswordRecoveryStart binds a dedicated mailbox challenge to one tenant.
// TenantID is supplied only by the workload's validated native auth request.
type PasswordRecoveryStart struct {
	ID          string `json:"id" format:"uuid" required:"true"`
	TenantID    string `json:"tenantId" format:"uuid" required:"true"`
	BrowserHash string `json:"browserHash" required:"true" pattern:"^[a-f0-9]{64}$"`
	SourceHash  string `json:"sourceHash" required:"true" pattern:"^[a-f0-9]{64}$"`
	Email       string `json:"email" format:"email" required:"true" max:"200" clean:"true"`
}

func (in PasswordRecoveryStart) Validate() error {
	tenant, err := uuid.Parse(in.TenantID)
	if err != nil || tenant == uuid.Nil || tenant.String() != in.TenantID ||
		(PasswordRecoveryAccess{in.ID, in.BrowserHash}).Validate() != nil || !discoveryHash(in.SourceHash) ||
		(DiscoveryRequest{Email: in.Email}).Validate() != nil {
		return ErrInvalid
	}
	return nil
}

// PasswordRecoveryAccess addresses only the retained browser-bound recovery.
type PasswordRecoveryAccess struct {
	ID          string `json:"id" format:"uuid" required:"true"`
	BrowserHash string `json:"browserHash" required:"true" pattern:"^[a-f0-9]{64}$"`
}

func (in PasswordRecoveryAccess) Validate() error {
	id, err := uuid.Parse(in.ID)
	if err != nil || id.Version() != 7 || id.Variant() != uuid.RFC4122 || id.String() != in.ID || !discoveryHash(in.BrowserHash) {
		return ErrInvalid
	}
	return nil
}

// PasswordRecoveryProof verifies the dedicated mailbox challenge, never a login.
type PasswordRecoveryProof struct {
	ID          string `json:"id" format:"uuid" required:"true"`
	BrowserHash string `json:"browserHash" required:"true" pattern:"^[a-f0-9]{64}$"`
	Code        string `json:"code" required:"true" pattern:"^[0-9]{8}$"`
}

func (in PasswordRecoveryProof) Validate() error {
	if (PasswordRecoveryAccess{in.ID, in.BrowserHash}).Validate() != nil ||
		DiscoveryProof(in).Validate() != nil {
		return ErrInvalid
	}
	return nil
}

// PasswordRecoveryPassword is a private native handoff, never persisted or logged.
type PasswordRecoveryPassword struct {
	ID          string `json:"id" format:"uuid" required:"true"`
	BrowserHash string `json:"browserHash" required:"true" pattern:"^[a-f0-9]{64}$"`
	Password    string `json:"password" required:"true" max:"200"`
}

func (in PasswordRecoveryPassword) Validate() error {
	if (PasswordRecoveryAccess{in.ID, in.BrowserHash}).Validate() != nil || in.Password == "" ||
		!utf8.ValidString(in.Password) || utf8.RuneCountInString(in.Password) > 200 {
		return ErrInvalid
	}
	return nil
}

func (PasswordRecoveryStart) String() string      { return "[redacted password recovery request]" }
func (PasswordRecoveryStart) GoString() string    { return "[redacted password recovery request]" }
func (PasswordRecoveryAccess) String() string     { return "[redacted password recovery capability]" }
func (PasswordRecoveryAccess) GoString() string   { return "[redacted password recovery capability]" }
func (PasswordRecoveryProof) String() string      { return "[redacted password recovery proof]" }
func (PasswordRecoveryProof) GoString() string    { return "[redacted password recovery proof]" }
func (PasswordRecoveryPassword) String() string   { return "[redacted password recovery password]" }
func (PasswordRecoveryPassword) GoString() string { return "[redacted password recovery password]" }
