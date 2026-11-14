package customercontract

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IdentityProjectionInput is accepted only from the verified product workload.
type IdentityProjectionInput struct {
	Purpose           string `json:"purpose" required:"true" enum:"status,membership,profile"`
	TenantID          string `json:"tenantId" format:"uuid" required:"true"`
	Subject           string `json:"subject" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
	RequestID         string `json:"requestId" format:"uuid" required:"true"`
	IncludeTenantName bool   `json:"includeTenantName,omitempty"`
}

func (v IdentityProjectionInput) Validate() error {
	if v.IncludeTenantName && v.Purpose != "profile" {
		return ErrInvalid
	}
	if v.Purpose != "status" && v.Purpose != "membership" && v.Purpose != "profile" {
		return ErrInvalid
	}
	return validateTaggedFields(v, "")
}

// IdentityProfile is returned only for an explicit current-user profile read.
type IdentityProfile struct {
	DisplayName string `json:"displayName" required:"true" max:"200" clean:"true"`
	Email       string `json:"email" required:"true" max:"200" clean:"true" format:"email"`
}

func (v IdentityProfile) Validate() error {
	if validateTaggedFields(v, "") != nil || utf8.RuneCountInString(v.DisplayName) > 200 || len(v.Email) > 200 || strings.TrimSpace(v.DisplayName) != v.DisplayName || strings.ContainsFunc(v.DisplayName, unicode.IsControl) {
		return ErrInvalid
	}
	address, err := mail.ParseAddress(v.Email)
	if err != nil || address.Address != v.Email {
		return ErrInvalid
	}
	return nil
}

// IdentityProjection separates current-state checks from new-grant authority.
// OrganizationID identifies the authorized tenant scope, not a foreign user's org.
type IdentityProjection struct {
	Purpose        string           `json:"purpose" required:"true" enum:"status,membership,profile"`
	TenantID       string           `json:"tenantId" format:"uuid" required:"true"`
	OrganizationID string           `json:"organizationId" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
	Subject        string           `json:"subject" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
	State          string           `json:"state" required:"true" enum:"active,inactive"`
	Admission      string           `json:"admission" required:"true" enum:"unassessed,eligible,ineligible"`
	Profile        *IdentityProfile `json:"profile,omitempty"`
	TenantName     string           `json:"tenantName,omitempty" max:"200" clean:"true"`
}

func (v IdentityProjection) Validate() error {
	if validateTaggedFields(v, "") != nil {
		return ErrInvalid
	}
	// Self reads may use a retained, owner-verified native organisation. New
	// grants still require a fresh tenant-owned organisation, and the reserved
	// deterministic namespace must never name another tenant.
	if v.OrganizationID != "helivanta-"+v.TenantID && (v.Purpose == "membership" || strings.HasPrefix(v.OrganizationID, "helivanta-")) {
		return ErrInvalid
	}
	if v.State != "active" && v.State != "inactive" {
		return ErrInvalid
	}
	if v.Admission != "eligible" && v.Admission != "ineligible" && v.Admission != "unassessed" {
		return ErrInvalid
	}
	if v.Admission == "eligible" && v.State != "active" {
		return ErrInvalid
	}
	if v.Purpose != "status" && v.Purpose != "membership" && v.Purpose != "profile" {
		return ErrInvalid
	}
	if (v.Purpose == "membership") != (v.Admission != "unassessed") {
		return ErrInvalid
	}
	if v.Profile != nil && (v.Purpose != "profile" || v.State != "active" || v.Profile.Validate() != nil) {
		return ErrInvalid
	}
	if v.TenantName != "" && (v.Purpose != "profile" || v.State != "active" || v.Profile == nil || !utf8.ValidString(v.TenantName) || utf8.RuneCountInString(v.TenantName) > 200 || strings.TrimSpace(v.TenantName) != v.TenantName || strings.ContainsFunc(v.TenantName, unicode.IsControl)) {
		return ErrInvalid
	}
	return nil
}
