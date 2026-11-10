// Package customercontract owns the customer signup and initial workspace wire contracts.
package customercontract

import (
	"errors"
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid customer signup")

// Setup is the common initial business-resource intent.
type Setup struct {
	Name        string `json:"name"`
	Kind        string `json:"kind" enum:"hospital,pharmacy"`
	CountryCode string `json:"countryCode" enum:"IN"`
	StateCode   string `json:"stateCode" enum:"MH,OD"`
}

// Signup is submitted intent, never a verified identity or licence.
type Signup struct {
	Setup
	Owner *identity.OrganizationInitialAdmin `json:"owner"`
}

// Receipt contains no native credential or verification code.
type Receipt struct {
	State      string `json:"state" enum:"not_started,email_action_required,provisioning,ready,needs_support"`
	Name       string `json:"name,omitempty"`
	Kind       string `json:"kind,omitempty"`
	TenantID   string `json:"tenantId,omitempty"`
	ResourceID string `json:"resourceId,omitempty"`
}

// Proof is used only at the native identity boundary, never persisted.
type Proof struct {
	UserID   string `json:"userId"`
	Code     string `json:"code"`
	Password string `json:"password,omitempty"`
}

// Workspace is the workload-only command to the selected business owner.
type Workspace struct {
	TenantID       string `json:"tenantId"`
	OrganizationID string `json:"organizationId"`
	OwnerSubject   string `json:"ownerSubject"`
	ResourceID     string `json:"resourceId"`
	Setup
}

// Validate checks a signup before any provisioning or data write.
func (s Signup) Validate(requestID uuid.UUID) error {
	if requestID == uuid.Nil || s.Owner == nil || s.Setup.Validate() != nil {
		return ErrInvalid
	}
	spec := identity.ZitadelOrganizationSpec{TenantID: requestID.String(), InitialAdminUserID: "owner-" + requestID.String(), InitialAdmin: s.Owner}
	if spec.ValidateInitialAdmin() != nil {
		return ErrInvalid
	}
	return nil
}

// Validate enforces the supported geographic and business setup before creation.
func (s Setup) Validate() error {
	if !utf8.ValidString(s.Name) || s.Name == "" || strings.TrimSpace(s.Name) != s.Name || utf8.RuneCountInString(s.Name) > 150 || strings.ContainsFunc(s.Name, unicode.IsControl) {
		return ErrInvalid
	}
	value := reflect.ValueOf(s)
	for i := range value.NumField() {
		if choices := value.Type().Field(i).Tag.Get("enum"); choices != "" && !slices.Contains(strings.Split(choices, ","), value.Field(i).String()) {
			return ErrInvalid
		}
	}
	return nil
}
