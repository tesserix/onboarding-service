// Package staffcontract owns the Staff invitation wire models.
package staffcontract

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid staff setup request")

func ReceiptStates() []string {
	return []string{"complete", "delivery_unresolved", "proof_unresolved", "already_verified", "consumed", "superseded", "expired", "email_action_required", "password_action_required"}
}

type StaffInvitationReceipt struct {
	State        string     `json:"state"`
	InvitationID *uuid.UUID `json:"invitation_id,omitempty"`
	Phase        string     `json:"phase,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

func (v StaffInvitationReceipt) Validate() error {
	if !slices.Contains(ReceiptStates(), v.State) {
		return ErrInvalid
	}
	if v.InvitationID == nil {
		if v.State != "complete" || v.Phase != "" || v.ExpiresAt != nil {
			return ErrInvalid
		}
		return nil
	}
	if v.InvitationID.Version() != 7 || v.InvitationID.Variant() != uuid.RFC4122 || (v.Phase != "email" && v.Phase != "password") || v.ExpiresAt == nil || v.ExpiresAt.IsZero() {
		return ErrInvalid
	}
	return nil
}

type StaffRecipientInput struct {
	InvitationID uuid.UUID  `json:"invitation_id"`
	Ticket       string     `json:"ticket"`
	Code         string     `json:"code,omitempty"`
	Password     string     `json:"password,omitempty"`
	DeliveryID   *uuid.UUID `json:"delivery_id,omitempty"`
}

func (StaffRecipientInput) String() string   { return "[redacted staff recipient input]" }
func (StaffRecipientInput) GoString() string { return "[redacted staff recipient input]" }

func (v StaffRecipientInput) Validate(action string) error {
	ticket, err := base64.RawURLEncoding.DecodeString(v.Ticket)
	if err != nil || len(ticket) != 32 || base64.RawURLEncoding.EncodeToString(ticket) != v.Ticket || v.InvitationID.Version() != 7 || v.InvitationID.Variant() != uuid.RFC4122 || !utf8.ValidString(v.Password) || utf8.RuneCountInString(v.Password) > 200 || !utf8.ValidString(v.Code) || len(v.Code) > 20 || strings.ContainsFunc(v.Code, unicode.IsControl) {
		return ErrInvalid
	}
	switch action {
	case "status":
		if v.Code != "" || v.Password != "" || v.DeliveryID != nil {
			return ErrInvalid
		}
	case "email":
		if v.Code == "" || v.Password != "" || v.DeliveryID != nil {
			return ErrInvalid
		}
	case "password":
		if v.Code == "" || v.Password == "" || v.DeliveryID != nil {
			return ErrInvalid
		}
	case "resend":
		if v.Code != "" || v.Password != "" || v.DeliveryID == nil || v.DeliveryID.Version() != 7 || v.DeliveryID.Variant() != uuid.RFC4122 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
