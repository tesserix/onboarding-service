package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

const (
	maxRegistryCredentials = 10
	rotationOverlap        = time.Hour
	maxCredentialLifetime  = 90 * 24 * time.Hour
)

var (
	registrySlugPattern   = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
	credentialNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
	registryKinds         = map[string]bool{
		"Agent": true, "MCPServer": true, "Tool": true, "Skill": true,
		"Prompt": true, "Workflow": true, "Blueprint": true, "Dataset": true,
		"EvalSuite": true,
	}
)

var registryProjectRoles = []string{"registry.reader", "registry.publisher", "registry.deleter"}

type RegistryTenantInput struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type RegistryCredentialInput struct {
	Name         string   `json:"name"`
	Scopes       []string `json:"scopes"`
	LifetimeDays int      `json:"lifetime_days"`
	Namespaces   []string `json:"namespaces"`
	Kinds        []string `json:"kinds"`
}

type RegistryCredentialSecret struct {
	Credential   store.RegistryCredential `json:"credential"`
	ClientSecret string                   `json:"client_secret"`
}

func (s *Service) OnboardRegistryTenant(
	ctx context.Context,
	p authz.Principal,
	input RegistryTenantInput,
) (store.RegistryTenant, error) {
	input.Slug = strings.TrimSpace(input.Slug)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if !registrySlugPattern.MatchString(input.Slug) || len(input.DisplayName) < 1 || len(input.DisplayName) > 100 {
		return store.RegistryTenant{}, fmt.Errorf("%w: slug or display_name is invalid", ErrInvalid)
	}
	if s.registryProjectID == "" {
		return store.RegistryTenant{}, fmt.Errorf("%w: Registry project is not configured", ErrUnavailable)
	}
	if err := s.guard(ctx, p, authz.ActionRegistryTenantCreate, "", map[string]string{"slug": input.Slug}); err != nil {
		return store.RegistryTenant{}, err
	}

	if existing, err := s.store.RegistryTenantByOwner(ctx, p.Subject); err == nil {
		if existing.Slug != input.Slug {
			return store.RegistryTenant{}, fmt.Errorf("%w: this identity already owns a different Registry workspace", ErrConflict)
		}
		if err := s.idp.SetUserMetadata(ctx, p.ExternalOrgID, p.Subject,
			map[string][]byte{"registry_tenant": []byte(existing.ZitadelOrgID)}); err != nil {
			return store.RegistryTenant{}, fmt.Errorf("%w: reconcile Registry tenant selection", ErrUnavailable)
		}
		return existing, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.RegistryTenant{}, fmt.Errorf("%w: read Registry tenant projection", ErrUnavailable)
	}

	tenantID := uuid.New()
	zitadelOrgID, err := s.idp.CreateOrg(ctx, input.DisplayName)
	if err != nil {
		s.audit(ctx, p, "registry.tenant.create", "", audit.OutcomeError, map[string]string{"stage": "organization"})
		return store.RegistryTenant{}, fmt.Errorf("%w: create Registry organization", ErrUnavailable)
	}
	fail := func(stage string, cause error) (store.RegistryTenant, error) {
		s.undoOrg(ctx, zitadelOrgID)
		s.audit(ctx, p, "registry.tenant.create", "", audit.OutcomeError, map[string]string{"stage": stage})
		return store.RegistryTenant{}, fmt.Errorf("%w: create Registry tenant at %s: %v", ErrUnavailable, stage, cause)
	}
	if err := s.idp.SetOrgMetadata(ctx, zitadelOrgID, "tenant_id", tenantID.String()); err != nil {
		return fail("metadata", err)
	}
	if err := s.idp.AddOrgMember(ctx, zitadelOrgID, p.Subject, "ORG_OWNER"); err != nil {
		return fail("owner", err)
	}
	projectGrantID, err := s.idp.CreateProjectGrant(ctx, s.registryProjectID, zitadelOrgID, registryProjectRoles)
	if err != nil || projectGrantID == "" {
		if err == nil {
			err = errors.New("empty project grant id")
		}
		return fail("project_grant", err)
	}
	if _, err := s.idp.GrantUserProjectRoles(ctx, zitadelOrgID, p.Subject,
		s.registryProjectID, projectGrantID, registryProjectRoles); err != nil {
		return fail("owner_roles", err)
	}
	if err := s.idp.SetUserMetadata(ctx, p.ExternalOrgID, p.Subject,
		map[string][]byte{"registry_tenant": []byte(zitadelOrgID)}); err != nil {
		return fail("tenant_selection", err)
	}

	tenant, err := s.store.CreateRegistryTenant(ctx, store.Organization{
		ID: tenantID, ZitadelOrgID: zitadelOrgID, Name: input.DisplayName, State: "active",
	}, store.RegistryTenant{
		TenantID: tenantID, ZitadelOrgID: zitadelOrgID, OwnerSubject: p.Subject,
		Slug: input.Slug, Namespace: input.Slug, ProjectGrantID: projectGrantID, State: "ready",
	})
	if err != nil {
		s.undoOrg(ctx, zitadelOrgID)
		if errors.Is(err, store.ErrConflict) {
			if existing, findErr := s.store.RegistryTenantByOwner(ctx, p.Subject); findErr == nil && existing.Slug == input.Slug {
				return existing, nil
			}
			// A conflicting owner already has authoritative metadata. Only clear
			// ours when the conflict came from another unique field, such as slug.
			if _, findErr := s.store.RegistryTenantByOwner(ctx, p.Subject); errors.Is(findErr, store.ErrNotFound) {
				s.clearRegistryTenantSelection(ctx, p)
			}
			return store.RegistryTenant{}, fmt.Errorf("%w: workspace slug or owner is already registered", ErrConflict)
		}
		s.clearRegistryTenantSelection(ctx, p)
		return store.RegistryTenant{}, fmt.Errorf("%w: record Registry tenant projection", ErrUnavailable)
	}
	s.audit(ctx, p, "registry.tenant.create", tenant.TenantID.String(), audit.OutcomeSuccess,
		map[string]string{"slug": tenant.Slug, "zitadelOrgId": tenant.ZitadelOrgID})
	return tenant, nil
}

func (s *Service) clearRegistryTenantSelection(ctx context.Context, p authz.Principal) {
	if err := s.idp.SetUserMetadata(context.WithoutCancel(ctx), p.ExternalOrgID, p.Subject,
		map[string][]byte{"registry_tenant": {}}); err != nil {
		s.log.Warn("could not clear rolled-back Registry tenant selection", "subject", p.Subject, "error", err)
	}
}

func (s *Service) RegistryCredentials(ctx context.Context, p authz.Principal) ([]store.RegistryCredential, error) {
	tenant, err := s.registryTenantForActor(ctx, p, authz.ActionRegistryCredentialRead)
	if err != nil {
		return nil, err
	}
	credentials, err := s.store.RegistryCredentials(ctx, tenant.TenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: list Registry credentials", ErrUnavailable)
	}
	return credentials, nil
}

func (s *Service) CreateRegistryCredential(
	ctx context.Context,
	p authz.Principal,
	input RegistryCredentialInput,
) (RegistryCredentialSecret, error) {
	tenant, err := s.registryTenantForActor(ctx, p, authz.ActionRegistryCredentialWrite)
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	input, err = validateRegistryCredentialInput(input)
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	count, err := s.store.CountLiveRegistryCredentials(ctx, tenant.TenantID)
	if err != nil {
		return RegistryCredentialSecret{}, fmt.Errorf("%w: count Registry credentials", ErrUnavailable)
	}
	if count >= maxRegistryCredentials {
		return RegistryCredentialSecret{}, fmt.Errorf("%w: at most %d active credentials are allowed", ErrConflict, maxRegistryCredentials)
	}

	credential := store.RegistryCredential{
		ID: uuid.New(), TenantID: tenant.TenantID, Name: input.Name,
		Scopes: input.Scopes, Namespaces: input.Namespaces, Kinds: input.Kinds,
		CreatedBy: p.Subject, ExpiresAt: time.Now().UTC().Add(time.Duration(input.LifetimeDays) * 24 * time.Hour),
		Status: "active",
	}
	credential, secret, err := s.provisionRegistryCredential(ctx, tenant, credential)
	if err != nil {
		s.audit(ctx, p, "registry.credential.create", tenant.TenantID.String(), audit.OutcomeError,
			map[string]string{"stage": "identity"})
		return RegistryCredentialSecret{}, err
	}
	created, err := s.store.CreateRegistryCredential(ctx, credential)
	if err != nil {
		s.rollbackRegistryCredential(ctx, tenant.ZitadelOrgID, credential.ZitadelUserID)
		return RegistryCredentialSecret{}, fmt.Errorf("%w: record Registry credential", ErrUnavailable)
	}
	s.auditTarget(ctx, p, "registry.credential.create", tenant.TenantID.String(), created.ID.String(),
		audit.OutcomeSuccess, map[string]any{"scopes": created.Scopes, "expiresAt": created.ExpiresAt})
	return RegistryCredentialSecret{Credential: created, ClientSecret: secret}, nil
}

func (s *Service) RotateRegistryCredential(
	ctx context.Context,
	p authz.Principal,
	credentialID uuid.UUID,
) (RegistryCredentialSecret, error) {
	tenant, err := s.registryTenantForActor(ctx, p, authz.ActionRegistryCredentialWrite)
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	prior, err := s.store.RegistryCredential(ctx, tenant.TenantID, credentialID)
	if errors.Is(err, store.ErrNotFound) {
		return RegistryCredentialSecret{}, ErrNotFound
	}
	if err != nil {
		return RegistryCredentialSecret{}, fmt.Errorf("%w: read Registry credential", ErrUnavailable)
	}
	if prior.Status != "active" || !prior.ExpiresAt.After(time.Now()) {
		return RegistryCredentialSecret{}, fmt.Errorf("%w: only an active, unexpired credential can be rotated", ErrConflict)
	}
	lifetime := prior.ExpiresAt.Sub(prior.CreatedAt)
	if lifetime <= 0 || lifetime > maxCredentialLifetime {
		lifetime = 30 * 24 * time.Hour
	}
	replacement := store.RegistryCredential{
		ID: uuid.New(), TenantID: tenant.TenantID, Name: prior.Name,
		Scopes: prior.Scopes, Namespaces: prior.Namespaces, Kinds: prior.Kinds,
		CreatedBy: p.Subject, ExpiresAt: time.Now().UTC().Add(lifetime), Status: "active",
	}
	replacement, secret, err := s.provisionRegistryCredential(ctx, tenant, replacement)
	if err != nil {
		return RegistryCredentialSecret{}, err
	}
	cutover := time.Now().UTC().Add(rotationOverlap)
	created, err := s.store.RotateRegistryCredential(ctx, prior.ID, replacement, cutover)
	if err != nil {
		s.rollbackRegistryCredential(ctx, tenant.ZitadelOrgID, replacement.ZitadelUserID)
		if errors.Is(err, store.ErrConflict) {
			return RegistryCredentialSecret{}, fmt.Errorf("%w: credential was already rotated", ErrConflict)
		}
		return RegistryCredentialSecret{}, fmt.Errorf("%w: record Registry credential rotation", ErrUnavailable)
	}
	s.auditTarget(ctx, p, "registry.credential.rotate", tenant.TenantID.String(), prior.ID.String(),
		audit.OutcomeSuccess, map[string]any{"replacementId": created.ID, "cutoverAt": cutover})
	return RegistryCredentialSecret{Credential: created, ClientSecret: secret}, nil
}

func (s *Service) RevokeRegistryCredential(ctx context.Context, p authz.Principal, credentialID uuid.UUID) error {
	tenant, err := s.registryTenantForActor(ctx, p, authz.ActionRegistryCredentialWrite)
	if err != nil {
		return err
	}
	credential, err := s.store.RegistryCredential(ctx, tenant.TenantID, credentialID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: read Registry credential", ErrUnavailable)
	}
	if credential.Status == "revoked" || credential.Status == "expired" {
		return nil
	}
	if err := s.idp.RemoveMachineSecret(ctx, tenant.ZitadelOrgID, credential.ZitadelUserID); err != nil {
		return fmt.Errorf("%w: revoke Registry credential", ErrUnavailable)
	}
	if err := s.idp.DeactivateUser(ctx, tenant.ZitadelOrgID, credential.ZitadelUserID); err != nil {
		// Secret removal already prevents further token exchange. Persist the
		// revocation and let the reaper retry the defense-in-depth deactivation.
		s.log.Warn("Registry credential secret removed but user deactivation failed",
			"credential", credential.ID, "error", err)
	}
	if err := s.store.RevokeRegistryCredential(ctx, tenant.TenantID, credential.ID); err != nil {
		return fmt.Errorf("%w: record Registry credential revocation", ErrUnavailable)
	}
	s.auditTarget(ctx, p, "registry.credential.revoke", tenant.TenantID.String(), credential.ID.String(),
		audit.OutcomeSuccess, nil)
	return nil
}

func (s *Service) ReapRegistryCredentials(ctx context.Context, limit int) error {
	if limit < 1 || limit > 1000 {
		return fmt.Errorf("%w: reaper limit must be between 1 and 1000", ErrInvalid)
	}
	now := time.Now().UTC()
	items, err := s.store.RegistryCredentialsToReap(ctx, now, limit)
	if err != nil {
		return err
	}
	var failed int
	for _, item := range items {
		if err := s.idp.RemoveMachineSecret(ctx, item.ZitadelOrgID, item.Credential.ZitadelUserID); err != nil {
			failed++
			s.log.Warn("could not remove expired Registry credential secret", "credential", item.Credential.ID, "error", err)
			continue
		}
		if err := s.idp.DeactivateUser(ctx, item.ZitadelOrgID, item.Credential.ZitadelUserID); err != nil {
			s.log.Warn("could not deactivate expired Registry credential user", "credential", item.Credential.ID, "error", err)
		}
		status := "revoked"
		if !item.Credential.ExpiresAt.After(now) {
			status = "expired"
		}
		if err := s.store.MarkRegistryCredentialReaped(ctx, item.Credential.ID, status); err != nil {
			failed++
			s.log.Warn("could not mark Registry credential reaped", "credential", item.Credential.ID, "error", err)
			continue
		}
		s.auditTarget(ctx, authz.Principal{Kind: authz.KindUser, Subject: "credential-reaper"},
			"registry.credential.reap", item.Credential.TenantID.String(), item.Credential.ID.String(),
			audit.OutcomeSuccess, map[string]string{"status": status})
	}
	if failed > 0 {
		return fmt.Errorf("%w: %d Registry credentials could not be reaped", ErrUnavailable, failed)
	}
	return nil
}

func (s *Service) registryTenantForActor(
	ctx context.Context,
	p authz.Principal,
	action authz.Action,
) (store.RegistryTenant, error) {
	tenantID, err := uuid.Parse(p.OrgID)
	if err != nil {
		return store.RegistryTenant{}, ErrNotFound
	}
	if err := s.guard(ctx, p, action, tenantID.String(), nil); err != nil {
		return store.RegistryTenant{}, err
	}
	tenant, err := s.store.RegistryTenant(ctx, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return store.RegistryTenant{}, ErrNotFound
	}
	if err != nil {
		return store.RegistryTenant{}, fmt.Errorf("%w: read Registry tenant", ErrUnavailable)
	}
	if tenant.State != "ready" || tenant.ZitadelOrgID != p.ExternalOrgID {
		return store.RegistryTenant{}, ErrNotFound
	}
	return tenant, nil
}

func (s *Service) provisionRegistryCredential(
	ctx context.Context,
	tenant store.RegistryTenant,
	credential store.RegistryCredential,
) (store.RegistryCredential, string, error) {
	username := "registry-" + tenant.Slug + "-" + strings.ReplaceAll(credential.ID.String()[:13], "-", "")
	userID, err := s.idp.CreateMachineUserInOrg(ctx, tenant.ZitadelOrgID, username, credential.Name)
	if err != nil {
		return store.RegistryCredential{}, "", fmt.Errorf("%w: create Registry service user", ErrUnavailable)
	}
	credential.ZitadelUserID = userID
	credential.ClientID = username
	secret, err := s.idp.AddMachineSecret(ctx, tenant.ZitadelOrgID, userID)
	if err != nil || secret == "" {
		s.rollbackRegistryCredential(ctx, tenant.ZitadelOrgID, userID)
		return store.RegistryCredential{}, "", fmt.Errorf("%w: create Registry client secret", ErrUnavailable)
	}
	namespaces, _ := json.Marshal(credential.Namespaces)
	kinds, _ := json.Marshal(credential.Kinds)
	if err := s.idp.SetUserMetadata(ctx, tenant.ZitadelOrgID, userID, map[string][]byte{
		"registry_namespaces": namespaces,
		"registry_kinds":      kinds,
	}); err != nil {
		s.rollbackRegistryCredential(ctx, tenant.ZitadelOrgID, userID)
		return store.RegistryCredential{}, "", fmt.Errorf("%w: set Registry credential restrictions", ErrUnavailable)
	}
	roles := rolesForRegistryScopes(credential.Scopes)
	if _, err := s.idp.GrantUserProjectRoles(ctx, tenant.ZitadelOrgID, userID,
		s.registryProjectID, tenant.ProjectGrantID, roles); err != nil {
		s.rollbackRegistryCredential(ctx, tenant.ZitadelOrgID, userID)
		return store.RegistryCredential{}, "", fmt.Errorf("%w: grant Registry credential roles", ErrUnavailable)
	}
	return credential, secret, nil
}

func (s *Service) rollbackRegistryCredential(ctx context.Context, orgID, userID string) {
	rollback := context.WithoutCancel(ctx)
	if err := s.idp.RemoveMachineSecret(rollback, orgID, userID); err != nil {
		s.log.Warn("could not remove rolled-back Registry credential secret", "user", userID, "error", err)
	}
	if err := s.idp.DeactivateUser(rollback, orgID, userID); err != nil {
		s.log.Warn("could not deactivate rolled-back Registry credential user", "user", userID, "error", err)
	}
}

func validateRegistryCredentialInput(input RegistryCredentialInput) (RegistryCredentialInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !credentialNamePattern.MatchString(input.Name) {
		return RegistryCredentialInput{}, fmt.Errorf("%w: credential name is invalid", ErrInvalid)
	}
	if input.LifetimeDays < 1 || input.LifetimeDays > 90 {
		return RegistryCredentialInput{}, fmt.Errorf("%w: lifetime_days must be between 1 and 90", ErrInvalid)
	}
	var err error
	input.Scopes, err = uniqueSorted(input.Scopes, 3, func(value string) bool {
		return value == "registry:read" || value == "registry:publish" || value == "registry:delete"
	})
	if err != nil || len(input.Scopes) == 0 {
		return RegistryCredentialInput{}, fmt.Errorf("%w: scopes contain an unsupported or duplicate value", ErrInvalid)
	}
	input.Namespaces, err = uniqueSorted(input.Namespaces, 20, registrySlugPattern.MatchString)
	if err != nil || len(input.Namespaces) == 0 {
		return RegistryCredentialInput{}, fmt.Errorf("%w: namespaces must contain 1-20 unique DNS labels", ErrInvalid)
	}
	input.Kinds, err = uniqueSorted(input.Kinds, len(registryKinds), func(value string) bool { return registryKinds[value] })
	if err != nil || len(input.Kinds) == 0 {
		return RegistryCredentialInput{}, fmt.Errorf("%w: kinds contain an unsupported or duplicate value", ErrInvalid)
	}
	return input, nil
}

func uniqueSorted(values []string, max int, valid func(string) bool) ([]string, error) {
	if len(values) > max {
		return nil, errors.New("too many values")
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !valid(value) || seen[value] {
			return nil, errors.New("invalid or duplicate value")
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func rolesForRegistryScopes(scopes []string) []string {
	roles := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		switch scope {
		case "registry:read":
			roles = append(roles, "registry.reader")
		case "registry:publish":
			roles = append(roles, "registry.publisher")
		case "registry:delete":
			roles = append(roles, "registry.deleter")
		}
	}
	return roles
}
