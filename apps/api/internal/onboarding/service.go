// Package onboarding orchestrates the identity plane and the projection. The
// ordering rule is fixed: Zitadel is written first and the local row follows,
// so a crash leaves an orphan in Zitadel — visible and reconcilable — rather
// than a projection row for an organization that cannot log in.
package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

var (
	// ErrNotFound is also the answer to "you may not see this", so the API
	// never confirms that an id exists to a caller with no grant on it.
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("conflict")
	// ErrUnavailable is a dependency failure, never a policy decision.
	ErrUnavailable = errors.New("temporarily unavailable")
)

type Member struct {
	UserID      string   `json:"userId"`
	Email       string   `json:"email,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Roles       []string `json:"roles"`
}

// Identity is the slice of Zitadel this service uses.
type Identity interface {
	VerifyOrganization(ctx context.Context, orgID, tenantID, projectID string) (zitadel.OrganizationIdentity, error)
	CreateOrg(ctx context.Context, name string) (string, error)
	RemoveOrg(ctx context.Context, orgID string) error
	SetOrgMetadata(ctx context.Context, orgID, key, value string) error
	DeactivateOrg(ctx context.Context, orgID string) error
	ReactivateOrg(ctx context.Context, orgID string) error
	CreateHumanUser(ctx context.Context, orgID, email, firstName, lastName string) (string, error)
	AddOrgMember(ctx context.Context, orgID, userID, role string) error
	RemoveOrgMember(ctx context.Context, orgID, userID string) error
	OrgMembers(ctx context.Context, orgID string) ([]Member, error)
	GrantProject(ctx context.Context, projectID, grantedOrgID string, roles []string) error
	CreateProjectGrant(ctx context.Context, projectID, grantedOrgID string, roles []string) (string, error)
	CreateMachineUserInOrg(ctx context.Context, orgID, username, name string) (string, error)
	AddMachineSecret(ctx context.Context, orgID, userID string) (string, error)
	RemoveMachineSecret(ctx context.Context, orgID, userID string) error
	SetUserMetadata(ctx context.Context, orgID, userID string, values map[string][]byte) error
	GrantUserProjectRoles(ctx context.Context, orgID, userID, projectID, projectGrantID string, roles []string) (string, error)
	DeactivateUser(ctx context.Context, orgID, userID string) error
	ReactivateUser(ctx context.Context, orgID, userID string) error
	AddOrgDomain(ctx context.Context, orgID, domain string) error
	RemoveOrgDomain(ctx context.Context, orgID, domain string) error
	GenerateDomainChallenge(ctx context.Context, orgID, domain string) (zitadel.Challenge, error)
	ValidateDomain(ctx context.Context, orgID, domain string) error
	CreateIDP(ctx context.Context, orgID string, spec zitadel.IDPSpec) (string, error)
	DeleteIDP(ctx context.Context, orgID, idpID string) error
}

type Service struct {
	store             *store.Store
	idp               Identity
	authz             *authz.Authorizer
	log               *slog.Logger
	registryProjectID string
	hospitals         *HospitalConfig
}

type Option func(*Service)

func WithRegistryProjectID(projectID string) Option {
	return func(service *Service) { service.registryProjectID = strings.TrimSpace(projectID) }
}

func New(s *store.Store, idp Identity, a *authz.Authorizer, log *slog.Logger, options ...Option) *Service {
	if log == nil {
		log = slog.Default()
	}
	service := &Service{store: s, idp: idp, authz: a, log: log}
	for _, option := range options {
		option(service)
	}
	return service
}

// guard authorizes and audits in one place: a denial is a ledger entry, not
// silence. It returns ErrNotFound for org-scoped denials so the API does not
// confirm that an id exists.
func (s *Service) guard(ctx context.Context, p authz.Principal, action authz.Action, orgID string, detail any) error {
	err := s.authz.Authorize(ctx, p, action, orgID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, authz.ErrDenied) {
		s.log.Error("authorization lookup failed", "action", action, "error", err)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	s.audit(ctx, p, string(action), orgID, audit.OutcomeDenied, detail)
	if orgID != "" {
		return ErrNotFound
	}
	return authz.ErrDenied
}

func (s *Service) audit(ctx context.Context, p authz.Principal, action, orgID string, outcome audit.Outcome, detail any) {
	s.auditTarget(ctx, p, action, orgID, "", outcome, detail)
}

func (s *Service) auditTarget(ctx context.Context, p authz.Principal, action, orgID, target string, outcome audit.Outcome, detail any) {
	event := audit.Event{
		Target:     target,
		ActorType:  actorType(p),
		ActorID:    p.Subject,
		ProductKey: p.ProductKey,
		OrgID:      orgID,
		Action:     action,
		Outcome:    outcome,
		RequestID:  RequestIDFrom(ctx),
		Detail:     encodeDetail(detail),
	}
	// The ledger must not be droppable by cancelling the request that caused
	// the event, so the append outlives the request context.
	if err := s.store.AppendAudit(context.WithoutCancel(ctx), event); err != nil {
		s.log.Error("audit append failed", "action", action, "org", orgID, "error", err)
	}
}

func actorType(p authz.Principal) audit.ActorType {
	switch p.Kind {
	case authz.KindPlatform:
		return audit.ActorPlatform
	case authz.KindProduct:
		return audit.ActorProduct
	case authz.KindOrgAdmin:
		return audit.ActorOrgAdmin
	case authz.KindUser:
		// The existing append-only schema predates self-service users. They have
		// no tenant yet, so system is the only non-fabricated actor class; ActorID
		// still records the verified subject.
		return audit.ActorSystem
	}
	return audit.ActorSystem
}

func encodeDetail(detail any) string {
	if detail == nil {
		return "{}"
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

type ctxKey string

const requestIDKey ctxKey = "request-id"

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		return id
	}
	return "unattributed"
}

// --- organizations -------------------------------------------------------

type CreateOrgInput struct {
	Name         string            `json:"name" binding:"required,min=2,max=200"`
	OwnerEmail   string            `json:"ownerEmail" binding:"required,email"`
	OwnerName    string            `json:"ownerName,omitempty" binding:"omitempty,max=200"`
	Products     []string          `json:"products,omitempty"`
	PrimaryDomai string            `json:"-"`
	BillingEmail string            `json:"billingEmail,omitempty" binding:"omitempty,email"`
	SupportEmail string            `json:"supportEmail,omitempty" binding:"omitempty,email"`
	Plan         string            `json:"plan,omitempty" binding:"omitempty,max=64"`
	Seats        int               `json:"seats,omitempty" binding:"omitempty,min=0,max=1000000"`
	Region       string            `json:"region,omitempty" binding:"omitempty,max=32"`
	ExternalRef  string            `json:"externalRef,omitempty" binding:"omitempty,max=200"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// CreateOrgResult is the org plus whatever failed after it became real. The
// owner invite and the product grants are not rolled back — the org is usable —
// but they must not fail silently either, or the operator is told an org is
// complete when nobody can log into it.
type CreateOrgResult struct {
	store.Organization
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Service) CreateOrganization(ctx context.Context, p authz.Principal, in CreateOrgInput) (CreateOrgResult, error) {
	if err := s.guard(ctx, p, authz.ActionOrgCreate, "", map[string]string{"name": in.Name}); err != nil {
		return CreateOrgResult{}, err
	}

	products, err := s.requestedProducts(ctx, p, in.Products)
	if err != nil {
		return CreateOrgResult{}, err
	}

	// The tenant id is minted here, before Zitadel, because it is what we put
	// into the org metadata and therefore into every token.
	tenantID := uuid.New()

	zitadelOrgID, err := s.idp.CreateOrg(ctx, in.Name)
	if err != nil {
		s.audit(ctx, p, "org.create", "", audit.OutcomeError, map[string]string{"name": in.Name, "stage": "identity"})
		return CreateOrgResult{}, fmt.Errorf("%w: create identity org: %v", ErrUnavailable, err)
	}
	if err := s.idp.SetOrgMetadata(ctx, zitadelOrgID, "tenant_id", tenantID.String()); err != nil {
		s.undoOrg(ctx, zitadelOrgID)
		s.audit(ctx, p, "org.create", "", audit.OutcomeError, map[string]string{"stage": "metadata"})
		return CreateOrgResult{}, fmt.Errorf("%w: set tenant_id: %v", ErrUnavailable, err)
	}

	org, err := s.store.CreateOrganization(ctx, store.Organization{
		ID: tenantID, ZitadelOrgID: zitadelOrgID, Name: in.Name, State: "active",
		CreatedByProduct: p.ProductKey, BillingEmail: in.BillingEmail, SupportEmail: in.SupportEmail,
		Plan: in.Plan, Seats: in.Seats, Region: in.Region, ExternalRef: in.ExternalRef, Labels: in.Labels,
	})
	if err != nil {
		s.undoOrg(ctx, zitadelOrgID)
		s.audit(ctx, p, "org.create", "", audit.OutcomeError, map[string]string{"stage": "projection", "zitadelOrgId": zitadelOrgID})
		return CreateOrgResult{}, fmt.Errorf("record organization: %w", err)
	}

	res := CreateOrgResult{Organization: org}
	if in.OwnerEmail != "" {
		if err := s.inviteOwner(ctx, zitadelOrgID, in.OwnerEmail, in.OwnerName); err != nil {
			s.log.Error("organization created without its owner", "org", org.ID, "error", err)
			res.Warnings = append(res.Warnings, fmt.Sprintf("owner %s was not invited; invite them from the members tab", in.OwnerEmail))
		}
	}
	for _, prod := range products {
		if err := s.attach(ctx, org, prod); err != nil {
			s.log.Error("organization created without a requested grant",
				"org", org.ID, "product", prod.Key, "error", err)
			res.Warnings = append(res.Warnings, fmt.Sprintf("product %s was not granted; add it from the products tab", prod.Key))
		}
	}

	s.audit(ctx, p, "org.create", org.ID.String(), audit.OutcomeSuccess, map[string]any{
		"name": org.Name, "zitadelOrgId": zitadelOrgID, "products": productKeys(products),
		"warnings": res.Warnings,
	})
	return res, nil
}

// undoOrg compensates a create that failed after the identity org existed.
// Left behind, that org has no tenant_id and no row, so it is invisible to the
// console and un-retryable — the name is already taken. Uses WithoutCancel so a
// client disconnect does not strand it.
func (s *Service) undoOrg(ctx context.Context, zitadelOrgID string) {
	if err := s.idp.RemoveOrg(context.WithoutCancel(ctx), zitadelOrgID); err != nil {
		s.log.Error("identity org left behind; remove it by hand",
			"zitadelOrgId", zitadelOrgID, "error", err)
	}
}

// A product caller may only ever attach itself; the body is a request, not an
// authorization.
func (s *Service) requestedProducts(ctx context.Context, p authz.Principal, requested []string) ([]store.Product, error) {
	if p.Kind == authz.KindProduct {
		requested = []string{p.ProductKey}
	}
	products := make([]store.Product, 0, len(requested))
	for _, key := range requested {
		if key == operatorManagedHospitalProduct {
			return nil, errHospitalWorkflow
		}
		prod, err := s.store.Product(ctx, key)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("%w: unknown product %q", ErrInvalid, key)
			}
			return nil, fmt.Errorf("load product %s: %w", key, err)
		}
		products = append(products, prod)
	}
	return products, nil
}

func (s *Service) inviteOwner(ctx context.Context, zitadelOrgID, email, name string) error {
	first, last := splitName(name, email)
	userID, err := s.idp.CreateHumanUser(ctx, zitadelOrgID, email, first, last)
	if err != nil {
		return fmt.Errorf("create owner: %w", err)
	}
	if err := s.idp.AddOrgMember(ctx, zitadelOrgID, userID, "ORG_OWNER"); err != nil {
		return fmt.Errorf("grant owner role: %w", err)
	}
	return nil
}

func (s *Service) attach(ctx context.Context, org store.Organization, prod store.Product) error {
	if err := s.idp.GrantProject(ctx, prod.ZitadelProjectID, org.ZitadelOrgID, prod.Roles); err != nil {
		return fmt.Errorf("grant project %s: %w", prod.Key, err)
	}
	if err := s.store.Grant(ctx, org.ID, prod.Key, prod.Roles); err != nil {
		return fmt.Errorf("record grant %s: %w", prod.Key, err)
	}
	return nil
}

func (s *Service) Organizations(ctx context.Context, p authz.Principal) ([]store.Organization, error) {
	switch p.Kind {
	case authz.KindPlatform:
		if err := s.guard(ctx, p, authz.ActionProductRead, "", nil); err != nil {
			return nil, err
		}
		return s.store.Organizations(ctx)
	case authz.KindProduct:
		return s.store.OrganizationsForProduct(ctx, p.ProductKey)
	case authz.KindOrgAdmin:
		id, err := uuid.Parse(p.OrgID)
		if err != nil {
			return nil, ErrNotFound
		}
		org, err := s.Organization(ctx, p, id)
		if err != nil {
			return nil, err
		}
		return []store.Organization{org}, nil
	}
	return nil, ErrNotFound
}

func (s *Service) Organization(ctx context.Context, p authz.Principal, id uuid.UUID) (store.Organization, error) {
	if err := s.guard(ctx, p, authz.ActionOrgRead, id.String(), nil); err != nil {
		return store.Organization{}, err
	}
	org, err := s.store.Organization(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Organization{}, ErrNotFound
	}
	return org, err
}

func (s *Service) SetOrganizationState(ctx context.Context, p authz.Principal, id uuid.UUID, state string) error {
	if state != "active" && state != "inactive" {
		return fmt.Errorf("%w: state must be active or inactive", ErrInvalid)
	}
	action := "org.deactivate"
	if state == "active" {
		action = "org.reactivate"
	}
	if err := s.guard(ctx, p, authz.ActionOrgDeactivate, id.String(), map[string]string{"state": state}); err != nil {
		return err
	}

	org, err := s.store.Organization(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	if state == "inactive" {
		err = s.idp.DeactivateOrg(ctx, org.ZitadelOrgID)
	} else {
		err = s.idp.ReactivateOrg(ctx, org.ZitadelOrgID)
	}
	if err != nil {
		s.audit(ctx, p, action, id.String(), audit.OutcomeError, nil)
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := s.store.SetOrganizationState(ctx, id, state); err != nil {
		return fmt.Errorf("record state: %w", err)
	}
	s.audit(ctx, p, action, id.String(), audit.OutcomeSuccess, map[string]string{"state": state})
	return nil
}

// --- grants --------------------------------------------------------------

func (s *Service) Grants(ctx context.Context, p authz.Principal, orgID uuid.UUID) ([]store.Grant, error) {
	if err := s.guard(ctx, p, authz.ActionGrantRead, orgID.String(), nil); err != nil {
		return nil, err
	}
	return s.store.Grants(ctx, orgID)
}

func (s *Service) AddGrant(ctx context.Context, p authz.Principal, orgID uuid.UUID, productKey string) error {
	if err := s.guard(ctx, p, authz.ActionGrantWrite, orgID.String(), map[string]string{"product": productKey}); err != nil {
		return err
	}
	if p.Kind == authz.KindProduct && p.ProductKey != productKey {
		return fmt.Errorf("%w: a product may only grant itself", ErrInvalid)
	}

	if productKey == operatorManagedHospitalProduct {
		return errHospitalWorkflow
	}

	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	prod, err := s.store.Product(ctx, productKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: unknown product %q", ErrInvalid, productKey)
		}
		return err
	}
	if err := s.attach(ctx, org, prod); err != nil {
		s.audit(ctx, p, "grant.create", orgID.String(), audit.OutcomeError, map[string]string{"product": productKey})
		return err
	}
	s.audit(ctx, p, "grant.create", orgID.String(), audit.OutcomeSuccess, map[string]string{"product": productKey})
	return nil
}

func (s *Service) RevokeGrant(ctx context.Context, p authz.Principal, orgID uuid.UUID, productKey string) error {
	if err := s.guard(ctx, p, authz.ActionGrantWrite, orgID.String(), map[string]string{"product": productKey}); err != nil {
		return err
	}
	if err := s.store.RevokeGrant(ctx, orgID, productKey); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	s.audit(ctx, p, "grant.revoke", orgID.String(), audit.OutcomeSuccess, map[string]string{"product": productKey})
	return nil
}

// --- domains -------------------------------------------------------------

func (s *Service) Domains(ctx context.Context, p authz.Principal, orgID uuid.UUID) ([]store.Domain, error) {
	if err := s.guard(ctx, p, authz.ActionDomainRead, orgID.String(), nil); err != nil {
		return nil, err
	}
	return s.store.Domains(ctx, orgID)
}

func (s *Service) AddDomain(ctx context.Context, p authz.Principal, orgID uuid.UUID, domain string) (store.Domain, error) {
	domain = normalizeDomain(domain)
	if err := validDomain(domain); err != nil {
		return store.Domain{}, err
	}
	if err := s.guard(ctx, p, authz.ActionDomainWrite, orgID.String(), map[string]string{"domain": domain}); err != nil {
		return store.Domain{}, err
	}

	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Domain{}, ErrNotFound
		}
		return store.Domain{}, err
	}
	if err := s.idp.AddOrgDomain(ctx, org.ZitadelOrgID, domain); err != nil {
		return store.Domain{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	challenge, err := s.idp.GenerateDomainChallenge(ctx, org.ZitadelOrgID, domain)
	if err != nil {
		return store.Domain{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := s.store.UpsertDomain(ctx, orgID, domain, challenge.Type, challenge.Value); err != nil {
		return store.Domain{}, fmt.Errorf("record domain: %w", err)
	}
	s.audit(ctx, p, "domain.add", orgID.String(), audit.OutcomeSuccess, map[string]string{"domain": domain})

	return store.Domain{
		OrgID: orgID, Domain: domain, State: "pending",
		ChallengeType: challenge.Type, ChallengeValue: challenge.Value,
	}, nil
}

// VerifyDomain asks the identity plane to re-check DNS. It never marks a domain
// verified on our own say-so: a verified domain is what discovery resolves on.
func (s *Service) VerifyDomain(ctx context.Context, p authz.Principal, orgID uuid.UUID, domain string) error {
	domain = normalizeDomain(domain)
	if err := s.guard(ctx, p, authz.ActionDomainWrite, orgID.String(), map[string]string{"domain": domain}); err != nil {
		return err
	}
	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	if err := s.idp.ValidateDomain(ctx, org.ZitadelOrgID, domain); err != nil {
		_ = s.store.MarkDomainChecked(ctx, orgID, domain)
		s.audit(ctx, p, "domain.verify", orgID.String(), audit.OutcomeError, map[string]string{"domain": domain})
		return fmt.Errorf("%w: challenge not satisfied for %s", ErrInvalid, domain)
	}
	if err := s.store.MarkDomainVerified(ctx, orgID, domain); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("%w: %s is already verified by another organization", ErrInvalid, domain)
		}
		return fmt.Errorf("record verification: %w", err)
	}
	s.audit(ctx, p, "domain.verify", orgID.String(), audit.OutcomeSuccess, map[string]string{"domain": domain})
	return nil
}

func (s *Service) RemoveDomain(ctx context.Context, p authz.Principal, orgID uuid.UUID, domain string) error {
	domain = normalizeDomain(domain)
	if err := s.guard(ctx, p, authz.ActionDomainWrite, orgID.String(), map[string]string{"domain": domain}); err != nil {
		return err
	}
	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := s.idp.RemoveOrgDomain(ctx, org.ZitadelOrgID, domain); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := s.store.DeleteDomain(ctx, orgID, domain); err != nil {
		return err
	}
	s.audit(ctx, p, "domain.remove", orgID.String(), audit.OutcomeSuccess, map[string]string{"domain": domain})
	return nil
}

// --- identity providers --------------------------------------------------

func (s *Service) IDPs(ctx context.Context, p authz.Principal, orgID uuid.UUID) ([]store.IDP, error) {
	if err := s.guard(ctx, p, authz.ActionIDPRead, orgID.String(), nil); err != nil {
		return nil, err
	}
	return s.store.IDPs(ctx, orgID)
}

// CreateIDP passes the customer's client secret straight through to Zitadel's
// encrypted store. Nothing about it is written here or into the ledger.
func (s *Service) CreateIDP(ctx context.Context, p authz.Principal, orgID uuid.UUID, spec zitadel.IDPSpec) (store.IDP, error) {
	if err := s.guard(ctx, p, authz.ActionIDPWrite, orgID.String(), map[string]string{"type": spec.Type}); err != nil {
		return store.IDP{}, err
	}
	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.IDP{}, ErrNotFound
		}
		return store.IDP{}, err
	}

	idpID, err := s.idp.CreateIDP(ctx, org.ZitadelOrgID, spec)
	if err != nil {
		s.audit(ctx, p, "idp.create", orgID.String(), audit.OutcomeError, map[string]string{"type": spec.Type})
		return store.IDP{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	record := store.IDP{
		OrgID: orgID, ZitadelIDPID: idpID, Type: spec.Type, DisplayName: spec.Name,
		AutoRegister: spec.AutoRegister, State: "active",
	}
	if err := s.store.UpsertIDP(ctx, record); err != nil {
		return store.IDP{}, fmt.Errorf("record idp: %w", err)
	}
	s.audit(ctx, p, "idp.create", orgID.String(), audit.OutcomeSuccess, map[string]string{
		"type": spec.Type, "name": spec.Name, "idpId": idpID,
	})
	return record, nil
}

func (s *Service) DeleteIDP(ctx context.Context, p authz.Principal, orgID uuid.UUID, idpID string) error {
	if err := s.guard(ctx, p, authz.ActionIDPWrite, orgID.String(), map[string]string{"idpId": idpID}); err != nil {
		return err
	}
	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := s.idp.DeleteIDP(ctx, org.ZitadelOrgID, idpID); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := s.store.DeleteIDP(ctx, orgID, idpID); err != nil {
		return err
	}
	s.audit(ctx, p, "idp.delete", orgID.String(), audit.OutcomeSuccess, map[string]string{"idpId": idpID})
	return nil
}

// --- members -------------------------------------------------------------

func (s *Service) Members(ctx context.Context, p authz.Principal, orgID uuid.UUID) ([]Member, error) {
	if err := s.guard(ctx, p, authz.ActionMemberRead, orgID.String(), nil); err != nil {
		return nil, err
	}
	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	members, err := s.idp.OrgMembers(ctx, org.ZitadelOrgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return members, nil
}

func (s *Service) AddMember(ctx context.Context, p authz.Principal, orgID uuid.UUID, email, name, role string) (Member, error) {
	if role == "" {
		role = "ORG_OWNER"
	}
	if err := s.guard(ctx, p, authz.ActionMemberWrite, orgID.String(), map[string]string{"role": role}); err != nil {
		return Member{}, err
	}
	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Member{}, ErrNotFound
		}
		return Member{}, err
	}

	first, last := splitName(name, email)
	userID, err := s.idp.CreateHumanUser(ctx, org.ZitadelOrgID, email, first, last)
	if err != nil {
		return Member{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := s.idp.AddOrgMember(ctx, org.ZitadelOrgID, userID, role); err != nil {
		return Member{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.audit(ctx, p, "member.add", orgID.String(), audit.OutcomeSuccess, map[string]string{"role": role})
	return Member{UserID: userID, Email: email, DisplayName: name, Roles: []string{role}}, nil
}

func (s *Service) RemoveMember(ctx context.Context, p authz.Principal, orgID uuid.UUID, userID string) error {
	if err := s.guard(ctx, p, authz.ActionMemberWrite, orgID.String(), map[string]string{"userId": userID}); err != nil {
		return err
	}
	org, err := s.store.Organization(ctx, orgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := s.idp.RemoveOrgMember(ctx, org.ZitadelOrgID, userID); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.audit(ctx, p, "member.remove", orgID.String(), audit.OutcomeSuccess, map[string]string{"userId": userID})
	return nil
}

// --- discovery -----------------------------------------------------------

type Resolution struct {
	Found      bool     `json:"found"`
	OrgID      string   `json:"orgId,omitempty"`
	OrgName    string   `json:"orgName,omitempty"`
	IDPs       []IDPRef `json:"idps,omitempty"`
	ForceSSO   bool     `json:"forceSso"`
	OrgScopeID string   `json:"orgScopeId,omitempty"`
}

type IDPRef struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"displayName"`
}

// Resolve answers "which org does this email belong to". A miss and a
// no-entitlement are the same answer — an empty one — so the endpoint cannot be
// used to enumerate our customers.
func (s *Service) Resolve(ctx context.Context, p authz.Principal, email string) (Resolution, error) {
	if err := s.guard(ctx, p, authz.ActionDiscovery, "", nil); err != nil {
		return Resolution{}, err
	}
	domain := domainOf(email)
	if domain == "" {
		return Resolution{}, fmt.Errorf("%w: not an email address", ErrInvalid)
	}

	org, err := s.store.OrganizationByDomain(ctx, domain)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Resolution{}, nil
		}
		return Resolution{}, err
	}
	if p.Kind == authz.KindProduct {
		entitled, err := s.store.Entitled(ctx, org.ID.String(), p.ProductKey)
		if err != nil {
			return Resolution{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if !entitled {
			return Resolution{}, nil
		}
	}
	if org.State != "active" {
		return Resolution{}, nil
	}

	idps, err := s.store.IDPs(ctx, org.ID)
	if err != nil {
		return Resolution{}, err
	}
	res := Resolution{Found: true, OrgID: org.ID.String(), OrgName: org.Name, OrgScopeID: org.ZitadelOrgID}
	for _, i := range idps {
		res.IDPs = append(res.IDPs, IDPRef{ID: i.ZitadelIDPID, Type: i.Type, DisplayName: i.DisplayName})
	}
	res.ForceSSO = len(res.IDPs) > 0
	return res, nil
}

// --- products ------------------------------------------------------------

func (s *Service) Products(ctx context.Context, p authz.Principal) ([]store.Product, error) {
	if err := s.guard(ctx, p, authz.ActionProductRead, "", nil); err != nil {
		return nil, err
	}
	return s.store.Products(ctx)
}

func (s *Service) Product(ctx context.Context, p authz.Principal, key string) (store.Product, error) {
	if err := s.guard(ctx, p, authz.ActionProductRead, "", nil); err != nil {
		return store.Product{}, err
	}
	prod, err := s.store.Product(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return store.Product{}, ErrNotFound
	}
	return prod, err
}

func (s *Service) RegisterProduct(ctx context.Context, p authz.Principal, prod store.Product) (store.Product, error) {
	if err := s.guard(ctx, p, authz.ActionProductWrite, "", map[string]string{"key": prod.Key}); err != nil {
		return store.Product{}, err
	}
	if err := validProduct(prod); err != nil {
		return store.Product{}, err
	}
	if err := s.store.CreateProduct(ctx, prod); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.Product{}, fmt.Errorf("%w: product %q already exists", ErrInvalid, prod.Key)
		}
		return store.Product{}, err
	}
	s.audit(ctx, p, "product.register", "", audit.OutcomeSuccess, map[string]any{
		"key": prod.Key, "identity": prod.Identity, "tenantSecrets": prod.TenantSecrets,
	})
	return prod, nil
}

// AcknowledgeProductManifests records that the git counterpart of a product's
// live Zitadel and secret objects has been raised. It changes nothing; the
// ledger entry is the whole point, so a product cannot claim it for itself.
func (s *Service) AcknowledgeProductManifests(ctx context.Context, p authz.Principal, key string) error {
	if err := s.guard(ctx, p, authz.ActionProductWrite, "", map[string]string{"key": key}); err != nil {
		return err
	}
	if _, err := s.store.Product(ctx, key); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	s.auditTarget(ctx, p, "product.manifests.acknowledged", "", key, audit.OutcomeSuccess, nil)
	return nil
}

// --- audit ---------------------------------------------------------------

// Audit answers from the ledger. The filter narrows what is returned; it never
// widens what the principal may see — an unscoped read stays platform-only.
func (s *Service) Audit(ctx context.Context, p authz.Principal, f store.AuditFilter, limit int) ([]audit.Record, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if f.OrgID != nil {
		if err := s.guard(ctx, p, authz.ActionAuditRead, f.OrgID.String(), nil); err != nil {
			return nil, err
		}
		return s.store.AuditSearch(ctx, f, limit)
	}
	if p.Kind != authz.KindPlatform {
		return nil, ErrNotFound
	}
	if err := s.guard(ctx, p, authz.ActionProductRead, "", nil); err != nil {
		return nil, err
	}
	return s.store.AuditSearch(ctx, f, limit)
}

// AuditChain is the whole ledger in order, for chain verification rather than
// for reading.
func (s *Service) AuditChain(ctx context.Context, p authz.Principal) ([]audit.Record, error) {
	if p.Kind != authz.KindPlatform {
		return nil, ErrNotFound
	}
	if err := s.guard(ctx, p, authz.ActionProductRead, "", nil); err != nil {
		return nil, err
	}
	return s.store.AuditChain(ctx)
}

func (s *Service) Ready(ctx context.Context) error { return s.store.Ping(ctx) }

// --- helpers -------------------------------------------------------------

func productKeys(products []store.Product) []string {
	keys := make([]string, 0, len(products))
	for _, p := range products {
		keys = append(keys, p.Key)
	}
	return keys
}

func splitName(name, email string) (first, last string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return email, "-"
	}
	if idx := strings.LastIndex(name, " "); idx > 0 {
		return name[:idx], name[idx+1:]
	}
	return name, "-"
}

func normalizeDomain(d string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(d, ".")))
}

func domainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 {
		return ""
	}
	return normalizeDomain(email[at+1:])
}

func validDomain(d string) error {
	if len(d) < 4 || len(d) > 253 || !strings.Contains(d, ".") || strings.ContainsAny(d, " /:@") {
		return fmt.Errorf("%w: %q is not a domain", ErrInvalid, d)
	}
	return nil
}

func validProduct(p store.Product) error {
	switch {
	case p.Key == "" || len(p.Key) > 64:
		return fmt.Errorf("%w: key is required", ErrInvalid)
	case p.Identity != store.IdentityZitadel && p.Identity != store.IdentityGIP:
		return fmt.Errorf("%w: identity must be zitadel or gip", ErrInvalid)
	case p.TenantSecrets != store.TenantSecretsOpenBao && p.TenantSecrets != store.TenantSecretsNone:
		return fmt.Errorf("%w: tenantSecrets must be openbao or none", ErrInvalid)
	case p.Tenancy != store.TenancyOrg && p.Tenancy != store.TenancyEphemeralOrg:
		return fmt.Errorf("%w: tenancy must be org or ephemeral+org", ErrInvalid)
	}
	return nil
}
