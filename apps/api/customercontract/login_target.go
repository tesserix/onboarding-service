package customercontract

import (
	"net/mail"
	"strings"
)

// LoginTargetInput is workload-only; the caller derives tenant from native OIDC.
type LoginTargetInput struct {
	TenantID  string `json:"tenantId" format:"uuid" required:"true"`
	Email     string `json:"email" required:"true" maxLength:"200"`
	RequestID string `json:"requestId" format:"uuid" required:"true"`
}

func (in LoginTargetInput) Validate() error {
	if validateTaggedFields(in, "") != nil {
		return ErrInvalid
	}
	return validLoginEmail(in.Email)
}

func validLoginEmail(email string) error {
	if len(email) > 200 || strings.TrimSpace(email) != email {
		return ErrInvalid
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return ErrInvalid
	}
	return nil
}

// LoginTarget discloses an exact native binding only to the verified workload.
type LoginTarget struct {
	TenantID       string `json:"tenantId" format:"uuid" required:"true"`
	OrganizationID string `json:"organizationId"`
	Subject        string `json:"subject" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
}

func (in LoginTarget) Validate() error {
	if validateTaggedFields(in, "") != nil || in.OrganizationID != "helivanta-"+in.TenantID {
		return ErrInvalid
	}
	return nil
}
