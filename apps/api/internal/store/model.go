package store

import (
	"time"

	"github.com/google/uuid"
)

type Identity string

const (
	IdentityZitadel Identity = "zitadel"
	IdentityGIP     Identity = "gip"
)

type TenantSecrets string

const (
	TenantSecretsOpenBao TenantSecrets = "openbao"
	TenantSecretsNone    TenantSecrets = "none"
)

type Tenancy string

const (
	TenancyOrg          Tenancy = "org"
	TenancyEphemeralOrg Tenancy = "ephemeral+org"
)

// Product is a product's capability record: the two questions CLAUDE.md
// requires before any product is built, plus what the onboarding wizard needs
// to emit its git snippets.
type Product struct {
	Key              string        `json:"key"`
	DisplayName      string        `json:"displayName"`
	ZitadelProjectID string        `json:"zitadelProjectId"`
	MachineUserID    string        `json:"-"`
	OIDCClientID     string        `json:"oidcClientId,omitempty"`
	Hosts            []string      `json:"hosts"`
	Identity         Identity      `json:"identity"`
	TenantSecrets    TenantSecrets `json:"tenantSecrets"`
	Tenancy          Tenancy       `json:"tenancy"`
	Roles            []string      `json:"roles"`
	DataResidency    string        `json:"dataResidency"`
	State            string        `json:"state"`
	CreatedAt        time.Time     `json:"createdAt"`
}

// Organization.ID is the tenant_id: generated here, written to Zitadel org
// metadata, carried in the token, stored on the product's own tenant row.
type Organization struct {
	ID               uuid.UUID         `json:"id"`
	ZitadelOrgID     string            `json:"zitadelOrgId"`
	Name             string            `json:"name"`
	PrimaryDomain    string            `json:"primaryDomain,omitempty"`
	State            string            `json:"state"`
	CreatedByProduct string            `json:"createdByProduct,omitempty"`
	BillingEmail     string            `json:"billingEmail,omitempty"`
	SupportEmail     string            `json:"supportEmail,omitempty"`
	Plan             string            `json:"plan,omitempty"`
	Seats            int               `json:"seats,omitempty"`
	Region           string            `json:"region,omitempty"`
	ExternalRef      string            `json:"externalRef,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
}

type Grant struct {
	OrgID      uuid.UUID  `json:"orgId"`
	ProductKey string     `json:"productKey"`
	Roles      []string   `json:"roles"`
	GrantedAt  time.Time  `json:"grantedAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type Domain struct {
	OrgID          uuid.UUID  `json:"orgId"`
	Domain         string     `json:"domain"`
	State          string     `json:"state"`
	ChallengeType  string     `json:"challengeType,omitempty"`
	ChallengeValue string     `json:"challengeValue,omitempty"`
	LastCheckedAt  *time.Time `json:"lastCheckedAt,omitempty"`
	VerifiedAt     *time.Time `json:"verifiedAt,omitempty"`
}

// IDP carries no secret. The client secret goes to Zitadel's encrypted
// eventstore and, where a copy is genuinely needed, to OpenBao.
type IDP struct {
	OrgID        uuid.UUID `json:"orgId"`
	ZitadelIDPID string    `json:"id"`
	Type         string    `json:"type"`
	DisplayName  string    `json:"displayName"`
	AutoRegister bool      `json:"autoRegister"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
}

type IdempotentResponse struct {
	Status int
	Body   []byte
}

// RegistryTenant is the local projection of a self-service Registry
// workspace. ZitadelOrgID is the external tenant id carried in signed tokens;
// TenantID is the internal platform UUID shared with organizations.
type RegistryTenant struct {
	TenantID       uuid.UUID `json:"-"`
	ZitadelOrgID   string    `json:"id"`
	OwnerSubject   string    `json:"-"`
	Slug           string    `json:"slug"`
	Namespace      string    `json:"namespace"`
	ProjectGrantID string    `json:"-"`
	State          string    `json:"state"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// RegistryCredential contains projection metadata only. Client secrets and
// their verifiers are deliberately absent; Zitadel returns the secret once.
type RegistryCredential struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      uuid.UUID  `json:"-"`
	ZitadelUserID string     `json:"-"`
	ClientID      string     `json:"client_id"`
	Name          string     `json:"name"`
	Scopes        []string   `json:"scopes"`
	Namespaces    []string   `json:"namespaces"`
	Kinds         []string   `json:"kinds"`
	CreatedBy     string     `json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	Status        string     `json:"status"`
	RotatedFrom   *uuid.UUID `json:"rotated_from,omitempty"`
	CutoverAt     *time.Time `json:"cutover_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
}

type RegistryCredentialReap struct {
	Credential   RegistryCredential
	ZitadelOrgID string
}
