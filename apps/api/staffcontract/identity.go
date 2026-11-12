package staffcontract

import (
	"time"

	"github.com/google/uuid"
)

type StaffIdentityReceipt struct {
	SchemaVersion      int        `json:"schema_version"`
	CommandID          uuid.UUID  `json:"command_id"`
	TenantID           uuid.UUID  `json:"tenant_id"`
	ProductKey         string     `json:"product_key"`
	ActorSubject       string     `json:"actor_subject"`
	CommandState       string     `json:"command_state"`
	PreparedAt         time.Time  `json:"prepared_at"`
	UserID             string     `json:"user_id,omitempty"`
	CreatedAt          *time.Time `json:"created_at,omitempty"`
	RecoveryState      string     `json:"recovery_state,omitempty"`
	SuccessorCommandID *uuid.UUID `json:"successor_command_id,omitempty"`
}

type StaffIdentityCurrentState struct {
	State            string    `json:"state"`
	EmailVerified    bool      `json:"email_verified"`
	ObservedAt       time.Time `json:"observed_at"`
	UserRevision     string    `json:"user_revision,omitempty"`
	MetadataRevision string    `json:"metadata_revision,omitempty"`
}

type StaffIdentityObservationReceipt struct {
	Receipt     StaffIdentityReceipt      `json:"receipt"`
	Observation StaffIdentityCurrentState `json:"observation"`
}

type StaffIdentityCreateInput struct {
	ActorSubject string `json:"actor_subject"`
	Email        string `json:"email"`
	GivenName    string `json:"given_name"`
	FamilyName   string `json:"family_name"`
	Reason       string `json:"reason"`
}

func (StaffIdentityCreateInput) String() string { return "[redacted staff identity creation input]" }

func (StaffIdentityCreateInput) GoString() string { return "[redacted staff identity creation input]" }
