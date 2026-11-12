// Package customercontract owns the customer signup and initial workspace wire contracts.
package customercontract

import (
	"errors"
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid customer signup")

// Kind distinguishes the two independently administered customer models.
type Kind string

const (
	Hospital Kind = "hospital"
	Pharmacy Kind = "pharmacy"
)

func (Kind) Values() []string { return []string{string(Hospital), string(Pharmacy)} }

// Country is the supported customer country, not a legal applicability decision.
type Country string

const India Country = "IN"

func (Country) Values() []string { return []string{string(India)} }

// StateCode is the administrative home-state choice.
type StateCode string

const (
	Maharashtra StateCode = "MH"
	Odisha      StateCode = "OD"
)

func (StateCode) Values() []string { return []string{string(Maharashtra), string(Odisha)} }

// Status is public receipt progress, not a native credential.
type Status string

const (
	NotStarted             Status = "not_started"
	EmailActionRequired    Status = "email_action_required"
	PasswordActionRequired Status = "password_action_required"
	Provisioning           Status = "provisioning"
	Ready                  Status = "ready"
	NeedsSupport           Status = "needs_support"
)

func (Status) Values() []string {
	return []string{string(NotStarted), string(EmailActionRequired), string(PasswordActionRequired), string(Provisioning), string(Ready), string(NeedsSupport)}
}

// Setup is the common initial business-resource intent.
type Setup struct {
	Profile     *OrganizationProfile `json:"profile,omitempty" nullable:"true"`
	Name        string               `json:"name"`
	Kind        Kind                 `json:"kind"`
	CountryCode Country              `json:"countryCode"`
	StateCode   StateCode            `json:"stateCode"`
}

// Signup is submitted intent, never a verified identity or licence.
type Signup struct {
	Setup
	Owner *identity.OrganizationInitialAdmin `json:"owner"`
}

// Receipt contains no native credential or verification code.
type Receipt struct {
	State      Status `json:"state"`
	Name       string `json:"name,omitempty" requireWhen:"ready"`
	Kind       Kind   `json:"kind,omitempty" requireWhen:"ready"`
	TenantID   string `json:"tenantId,omitempty" requireWhen:"ready" format:"uuid"`
	ResourceID string `json:"resourceId,omitempty" requireWhen:"ready" format:"uuid"`
}

// Proof is used only at the native identity boundary, never persisted.
type Proof struct {
	UserID   string `json:"userId"`
	Code     string `json:"code"`
	Password string `json:"password,omitempty"`
}

// Delivery selects a signup already bound by its receipt or native recovery link.
type Delivery struct {
	UserID string `json:"userId,omitempty"`
}

// Workspace is the workload-only command to the selected business owner.
type Workspace struct {
	ExistingOwner  bool   `json:"existingOwner,omitempty"`
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
	if s.Profile != nil && (s.Profile.Validate() != nil || s.Profile.HeadOffice.CountryCode != s.CountryCode || s.Profile.HeadOffice.StateCode != s.StateCode) {
		return ErrInvalid
	}
	if !utf8.ValidString(s.Name) || s.Name == "" || strings.TrimSpace(s.Name) != s.Name || utf8.RuneCountInString(s.Name) > 150 || strings.ContainsFunc(s.Name, unicode.IsControl) {
		return ErrInvalid
	}
	if !slices.Contains(s.Kind.Values(), string(s.Kind)) || !slices.Contains(s.CountryCode.Values(), string(s.CountryCode)) || !slices.Contains(s.StateCode.Values(), string(s.StateCode)) {
		return ErrInvalid
	}
	return nil
}

// Validate refuses ready receipts without the complete initial resource binding.
func (r Receipt) Validate() error {
	if !slices.Contains(r.State.Values(), string(r.State)) {
		return ErrInvalid
	}
	if r.Kind != "" && !slices.Contains(r.Kind.Values(), string(r.Kind)) {
		return ErrInvalid
	}
	value := reflect.ValueOf(r)
	for i := range value.NumField() {
		field := value.Type().Field(i)
		text := value.Field(i).String()
		if field.Tag.Get("requireWhen") == string(r.State) && text == "" {
			return ErrInvalid
		}
		if field.Tag.Get("format") == "uuid" && text != "" {
			id, err := uuid.Parse(text)
			if err != nil || id == uuid.Nil || id.String() != text {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Validate binds the initial resource to the exact new native owner and tenant.
func (w Workspace) Validate() error {
	tenant, err := uuid.Parse(w.TenantID)
	if err != nil || tenant == uuid.Nil || tenant.String() != w.TenantID || w.OrganizationID != "helivanta-"+w.TenantID || (!w.ExistingOwner && w.OwnerSubject != "owner-"+w.TenantID) || (w.ExistingOwner && !nativeSubject.MatchString(w.OwnerSubject)) {
		return ErrInvalid
	}
	resource, err := uuid.Parse(w.ResourceID)
	if err != nil || resource.Version() != 7 || resource.String() != w.ResourceID {
		return ErrInvalid
	}
	return w.Setup.Validate()
}

var nativeSubject = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// SigninInput carries a transient native ID token for receipt-bound continuation.
type SigninInput struct {
	IDToken string `json:"idToken" required:"true" max:"8192" clean:"true"`
}

// Validate bounds a token before the native verifier parses it.
func (in SigninInput) Validate() error {
	if !profileStringsValid(in) {
		return ErrInvalid
	}
	return nil
}

// ExistingOwnerInput is accepted only from the browser application's verified workload.
type ExistingOwnerInput struct {
	Subject         string `json:"subject"`
	AuthTime        int64  `json:"authTime"`
	NextReceiptHash string `json:"nextReceiptHash"`
}

// Validate requires a fresh native authentication and a rotated receipt hash.
func (in ExistingOwnerInput) Validate(now time.Time) error {
	authenticated := time.Unix(in.AuthTime, 0)
	if !nativeSubject.MatchString(in.Subject) || in.AuthTime <= 0 || authenticated.Before(now.Add(-5*time.Minute)) || authenticated.After(now.Add(30*time.Second)) || len(in.NextReceiptHash) != 64 {
		return ErrInvalid
	}
	return nil
}
