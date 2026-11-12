package onboarding

import (
	"context"
	"errors"
	"fmt"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

// ConfigurePlanningPoker binds the reviewed deployment identity without replacing an existing registration.
func (s *Service) ConfigurePlanningPoker(ctx context.Context, projectID, clientID string) error {
	if projectID == "" || clientID == "" {
		return ErrInvalid
	}
	const account = "system:serviceaccount:planning-poker:planning-poker-api"
	existing, err := s.store.Product(ctx, "planning-poker")
	if err == nil {
		if existing.ZitadelProjectID != projectID || existing.OIDCClientID != clientID || existing.MachineUserID != account || existing.Identity != store.IdentityZitadel || existing.State != "active" {
			return fmt.Errorf("%w: planning poker identity binding differs", ErrConflict)
		}
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	_, err = s.RegisterProduct(ctx, authz.Principal{Kind: authz.KindPlatform, Subject: "deployment:planning-poker", Roles: []string{authz.RolePlatformAdmin}}, store.Product{
		Key: "planning-poker", DisplayName: "Planning Poker", ZitadelProjectID: projectID, OIDCClientID: clientID, MachineUserID: account, Hosts: []string{"planningpoker.tesserix.app"}, Identity: store.IdentityZitadel, Tenancy: store.TenancyEphemeralOrg, TenantSecrets: store.TenantSecretsOpenBao, Roles: []string{}, DataResidency: "in", State: "active",
	})
	if err != nil {
		// A concurrent replica may have completed the identical registration.
		existing, e := s.store.Product(ctx, "planning-poker")
		if e == nil && existing.ZitadelProjectID == projectID && existing.OIDCClientID == clientID && existing.MachineUserID == account && existing.Identity == store.IdentityZitadel && existing.State == "active" {
			return nil
		}
	}
	return err
}
