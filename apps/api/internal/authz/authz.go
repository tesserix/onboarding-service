// Package authz decides what a caller may do. Every decision is computed from
// the caller's own token joined against the entitlement table — never from a
// path parameter. See tesserix-k8s docs/onboarding-api.md § 2.
package authz

import (
	"context"
	"errors"
	"fmt"
)

var ErrDenied = errors.New("authz: denied")

type Kind string

const (
	KindPlatform Kind = "platform"
	KindProduct  Kind = "product"
	KindOrgAdmin Kind = "org_admin"
	KindUser     Kind = "user"
)

const (
	RolePlatformAdmin    = "platform-admin"
	RolePlatformOperator = "platform-operator"
)

type Principal struct {
	Kind          Kind
	Subject       string
	Email         string
	EmailVerified bool
	ConsoleAccess bool // set only from the verified console access-token contract
	ProductKey    string
	OrgID         string // internal platform tenant UUID
	ExternalOrgID string // signed Zitadel organization id
	Roles         []string
}

func (p Principal) hasRole(want string) bool {
	for _, r := range p.Roles {
		if r == want {
			return true
		}
	}
	return false
}

type Action string

const (
	ActionOrgCreate               Action = "org.create"
	ActionOrgEnrol                Action = "org.enrol"
	ActionOrgRead                 Action = "org.read"
	ActionOrgWrite                Action = "org.write"
	ActionOrgDeactivate           Action = "org.deactivate"
	ActionGrantRead               Action = "grant.read"
	ActionGrantWrite              Action = "grant.write"
	ActionDomainRead              Action = "domain.read"
	ActionDomainWrite             Action = "domain.write"
	ActionIDPRead                 Action = "idp.read"
	ActionIDPWrite                Action = "idp.write"
	ActionMemberRead              Action = "member.read"
	ActionMemberWrite             Action = "member.write"
	ActionStaffIdentityRead       Action = "staff.identity.read"
	ActionStaffIdentityHistory    Action = "staff.identity.history"
	ActionStaffIdentityCreate     Action = "staff.identity.create"
	ActionStaffIdentityObserve    Action = "staff.identity.observe"
	ActionStaffRecoveryRead       Action = "staff.identity.recovery.read"
	ActionStaffRecoveryPropose    Action = "staff.identity.recovery.propose"
	ActionStaffRecoveryDecide     Action = "staff.identity.recovery.decide"
	ActionProductRead             Action = "product.read"
	ActionProductWrite            Action = "product.write"
	ActionAuditRead               Action = "audit.read"
	ActionDiscovery               Action = "discovery.resolve"
	ActionRegistryTenantCreate    Action = "registry.tenant.create"
	ActionRegistryCredentialRead  Action = "registry.credential.read"
	ActionRegistryCredentialWrite Action = "registry.credential.write"
)

type rule struct {
	write    bool
	orgScope bool // needs an org id, and entitlement for a product caller
	product  bool // a product machine token may perform it
	orgAdmin bool // a customer's org admin may perform it
	user     bool // a verified signed-in human without a tenant
	console  bool // requires verified console access-token origin
}

// The policy table. A missing entry is a denial, so a new action is closed
// until it is deliberately opened here.
var policy = map[Action]rule{
	ActionOrgCreate:               {write: true, product: true},
	ActionOrgEnrol:                {write: true},
	ActionOrgRead:                 {orgScope: true, product: true, orgAdmin: true},
	ActionOrgWrite:                {write: true, orgScope: true, product: true, orgAdmin: true},
	ActionOrgDeactivate:           {write: true, orgScope: true},
	ActionGrantRead:               {orgScope: true, product: true},
	ActionGrantWrite:              {write: true, orgScope: true, product: true},
	ActionDomainRead:              {orgScope: true, product: true, orgAdmin: true},
	ActionDomainWrite:             {write: true, orgScope: true, product: true, orgAdmin: true},
	ActionIDPRead:                 {orgScope: true, product: true, orgAdmin: true},
	ActionIDPWrite:                {write: true, orgScope: true, product: true, orgAdmin: true},
	ActionMemberRead:              {orgScope: true, product: true, orgAdmin: true},
	ActionMemberWrite:             {write: true, orgScope: true, product: true, orgAdmin: true},
	ActionStaffIdentityRead:       {orgScope: true, product: true},
	ActionStaffIdentityHistory:    {orgScope: true, product: true},
	ActionStaffIdentityCreate:     {write: true, orgScope: true, product: true},
	ActionStaffIdentityObserve:    {write: true, orgScope: true, product: true},
	ActionStaffRecoveryRead:       {orgScope: true, console: true},
	ActionStaffRecoveryPropose:    {write: true, orgScope: true, console: true},
	ActionStaffRecoveryDecide:     {write: true, orgScope: true, console: true},
	ActionProductRead:             {product: true},
	ActionProductWrite:            {write: true},
	ActionAuditRead:               {orgScope: true, product: true, orgAdmin: true},
	ActionDiscovery:               {product: true},
	ActionRegistryTenantCreate:    {write: true, user: true},
	ActionRegistryCredentialRead:  {orgScope: true, orgAdmin: true},
	ActionRegistryCredentialWrite: {write: true, orgScope: true, orgAdmin: true},
}

func AllActions() []Action {
	actions := make([]Action, 0, len(policy))
	for a := range policy {
		actions = append(actions, a)
	}
	return actions
}

// Entitlements is the org_products join: does this org hold a live grant for
// this product?
type Entitlements interface {
	Entitled(ctx context.Context, orgID, productKey string) (bool, error)
}

type Authorizer struct{ entitlements Entitlements }

func New(e Entitlements) *Authorizer { return &Authorizer{entitlements: e} }

// Authorize returns nil, ErrDenied, or a lookup error. Callers must not treat
// a lookup error as a denial to be reported to the user — it is a 503, and
// conflating the two is how a database outage becomes a silent policy change.
func (a *Authorizer) Authorize(ctx context.Context, p Principal, action Action, orgID string) error {
	r, known := policy[action]
	if !known {
		return fmt.Errorf("%w: unknown action %q", ErrDenied, action)
	}
	if r.orgScope && orgID == "" {
		return fmt.Errorf("%w: %s requires an organization", ErrDenied, action)
	}
	if r.console && (p.Kind != KindPlatform || !p.ConsoleAccess || p.Subject == "") {
		return fmt.Errorf("%w: %s requires verified console access", ErrDenied, action)
	}

	switch p.Kind {
	case KindPlatform:
		switch {
		case p.hasRole(RolePlatformAdmin):
			return nil
		case p.hasRole(RolePlatformOperator) && !r.write:
			return nil
		}
		return fmt.Errorf("%w: platform principal lacks a role for %s", ErrDenied, action)

	case KindProduct:
		if !r.product {
			return fmt.Errorf("%w: %s is not available to products", ErrDenied, action)
		}
		if p.ProductKey == "" {
			return fmt.Errorf("%w: product principal has no product key", ErrDenied)
		}
		if !r.orgScope {
			return nil
		}
		entitled, err := a.entitlements.Entitled(ctx, orgID, p.ProductKey)
		if err != nil {
			return fmt.Errorf("authz: entitlement lookup: %w", err)
		}
		if !entitled {
			return fmt.Errorf("%w: %s has no grant on %s", ErrDenied, p.ProductKey, orgID)
		}
		return nil

	case KindOrgAdmin:
		if !r.orgAdmin {
			return fmt.Errorf("%w: %s is not available to organization admins", ErrDenied, action)
		}
		if p.OrgID == "" || p.OrgID != orgID {
			return fmt.Errorf("%w: principal is an admin of %q, not %q", ErrDenied, p.OrgID, orgID)
		}
		return nil

	case KindUser:
		if !r.user || !p.EmailVerified || p.Subject == "" {
			return fmt.Errorf("%w: verified user cannot perform %s", ErrDenied, action)
		}
		return nil
	}
	return fmt.Errorf("%w: unknown principal kind %q", ErrDenied, p.Kind)
}
