package customercontract

import (
	"github.com/google/uuid"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// SecurityState describes native enforcement, rather than a requested change.
type SecurityState string

const (
	SecurityApplied SecurityState = "applied"
	SecurityDrifted SecurityState = "drifted"
	SecurityPending SecurityState = "pending"
)

func (SecurityState) Values() []string {
	return []string{string(SecurityApplied), string(SecurityDrifted), string(SecurityPending)}
}

// SecurityView discloses only the current owner's native MFA enforcement.
type SecurityView struct {
	State        SecurityState `json:"state"`
	TenantID     string        `json:"tenantId" format:"uuid" required:"true"`
	Subject      string        `json:"subject" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
	Revision     string        `json:"revision" required:"true" pattern:"^[a-f0-9]{64}$"`
	RequireMFA   bool          `json:"requireMfa" requireWhen:"applied"`
	ExternalMFA  bool          `json:"externalMfa" requireWhen:"applied"`
	NoGrace      bool          `json:"noGrace" requireWhen:"applied"`
	CanReconcile bool          `json:"canReconcile"`
}

func (v SecurityView) Validate() error {
	if !slices.Contains(v.State.Values(), string(v.State)) {
		return ErrInvalid
	}
	return validateTaggedFields(v, string(v.State))
}

// SecurityInput supplies a stale-state precondition and repeatable refresh nonce.
type SecurityInput struct {
	RequestID        string `json:"requestId" format:"uuid" required:"true"`
	ExpectedRevision string `json:"expectedRevision" pattern:"^[a-f0-9]{64}$" required:"true"`
}

func (v SecurityInput) Validate() error { return validateTaggedFields(v, "") }

// SecurityRequest is accepted only from the separately verified platform workload.
type SecurityRequest struct {
	TenantID         string `json:"tenantId" format:"uuid" required:"true"`
	Subject          string `json:"subject" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
	RequestID        string `json:"requestId" format:"uuid" required:"true"`
	ExpectedRevision string `json:"expectedRevision,omitempty" pattern:"^[a-f0-9]{64}$"`
}

func (v SecurityRequest) Validate() error { return validateTaggedFields(v, "") }

func validateTaggedFields(input any, state string) error {
	value := reflect.ValueOf(input)
	for i := range value.NumField() {
		field := value.Type().Field(i)
		entry := value.Field(i)
		if (field.Tag.Get("required") == "true" || (field.Tag.Get("requireWhen") != "" && field.Tag.Get("requireWhen") == state)) && entry.IsZero() {
			return ErrInvalid
		}
		if entry.Kind() != reflect.String {
			continue
		}
		text := entry.String()
		if text == "" && strings.Contains(field.Tag.Get("json"), ",omitempty") {
			continue
		}
		if field.Tag.Get("format") == "uuid" {
			id, err := uuid.Parse(text)
			if err != nil || id == uuid.Nil || id.String() != text {
				return ErrInvalid
			}
		}
		if pattern := field.Tag.Get("pattern"); pattern != "" {
			valid, err := regexp.MatchString(pattern, text)
			if err != nil || !valid {
				return ErrInvalid
			}
		}
	}
	return nil
}
