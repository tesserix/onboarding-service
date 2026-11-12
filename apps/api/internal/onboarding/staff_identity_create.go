package onboarding

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type StaffIdentityCreator interface {
	CreateStaffIdentity(context.Context, zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error)
}

type StaffIdentityCreateInput struct {
	ActorSubject string `json:"actor_subject"`
	Email        string `json:"email"`
	GivenName    string `json:"given_name"`
	FamilyName   string `json:"family_name"`
	Reason       string `json:"reason"`
}

func (StaffIdentityCreateInput) String() string { return "[redacted staff identity creation input]" }

// CreateStaffIdentity persists authority to attempt exactly one role-free creation.
func (s *Service) CreateStaffIdentity(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, in StaffIdentityCreateInput) (StaffIdentityReceipt, error) {
	empty := StaffIdentityReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	scope, err := s.staffIdentityScope(ctx, p, tenant, command, in.ActorSubject, authz.ActionStaffIdentityCreate)
	if err != nil {
		return empty, err
	}
	creator, ok := s.staffSource.(StaffIdentityCreator)
	if !ok {
		return empty, ErrUnavailable
	}
	if !utf8.ValidString(in.Reason) || strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 2000 || strings.ContainsRune(in.Reason, 0) {
		return empty, ErrInvalid
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return empty, ErrUnavailable
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return empty, ErrUnavailable
	}
	input := zitadel.StaffIdentityInput{OrganizationID: scope.ProviderOrgID, UserID: id.String(), Email: in.Email, GivenName: in.GivenName, FamilyName: in.FamilyName, BindingNonce: base64.RawURLEncoding.EncodeToString(nonce[:])}
	if err := input.Validate(); err != nil {
		return empty, ErrInvalid
	}
	c, err := s.store.PrepareStaffIdentity(ctx, store.StaffIdentityCommand{ID: command, OrgID: tenant, ProductKey: p.ProductKey, MachineSubject: p.Subject, ActorSubject: in.ActorSubject, ProviderOrgID: scope.ProviderOrgID, ProviderProjectID: scope.ProviderProjectID, ProviderUserID: input.UserID, BindingNonce: input.BindingNonce, Email: in.Email, GivenName: in.GivenName, FamilyName: in.FamilyName, Reason: in.Reason}, staffIdentityEvent(ctx, p, scope, "staff.identity.prepare", nil))
	if err != nil {
		return empty, s.staffIdentityCreationStoreError(ctx, p, scope, err)
	}
	current, err := s.staffIdentityScope(ctx, p, tenant, command, in.ActorSubject, authz.ActionStaffIdentityCreate)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	claimed, err := s.store.ClaimStaffIdentityDispatch(ctx, c, staffIdentityEvent(ctx, p, scope, "staff.identity.dispatch", nil))
	if err != nil {
		return empty, s.staffIdentityCreationStoreError(ctx, p, scope, err)
	}
	var created zitadel.StaffIdentityCreated
	var sourceErr error
	started := time.Now().UTC()
	if claimed {
		created, sourceErr = creator.CreateStaffIdentity(ctx, zitadel.StaffIdentityInput{OrganizationID: c.ProviderOrgID, UserID: c.ProviderUserID, Email: c.Email, GivenName: c.GivenName, FamilyName: c.FamilyName, BindingNonce: c.BindingNonce})
	}
	finished := time.Now().UTC()
	current, err = s.staffIdentityScope(ctx, p, tenant, command, in.ActorSubject, authz.ActionStaffIdentityCreate)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	action := "staff.identity.create.replay"
	if claimed {
		action = "staff.identity.create.unresolved"
		if sourceErr == nil && created.ID == c.ProviderUserID && !created.CreatedAt.IsZero() && !created.CreatedAt.Before(started.Add(-zitadel.StaffIdentityClockSkew)) && !created.CreatedAt.After(finished.Add(zitadel.StaffIdentityClockSkew)) {
			if _, err := s.store.RecordStaffIdentityCreation(ctx, c, created.CreatedAt, staffIdentityEvent(ctx, p, scope, "staff.identity.created", nil)); err != nil {
				return empty, s.staffIdentityCreationStoreError(ctx, p, scope, err)
			}
			action = "staff.identity.create"
		}
	}
	op, err := s.readStaffIdentity(ctx, p, current, action, nil)
	if err != nil {
		return empty, err
	}
	if op.RecoveryState != "" {
		return empty, ErrConflict
	}
	return staffIdentityReceipt(op), nil
}

func (s *Service) staffIdentityCreationStoreError(ctx context.Context, p authz.Principal, scope store.StaffIdentityScope, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		s.auditTarget(ctx, p, "staff.identity.create", scope.OrgID.String(), scope.CommandID.String(), audit.OutcomeDenied, nil)
		return ErrNotFound
	}
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrIdempotencyConflict) {
		s.auditTarget(ctx, p, "staff.identity.create", scope.OrgID.String(), scope.CommandID.String(), audit.OutcomeDenied, nil)
		return ErrConflict
	}
	return ErrUnavailable
}
