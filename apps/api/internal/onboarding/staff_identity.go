package onboarding

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// StaffIdentitySource supplies bounded read-only provider observations.
type StaffIdentitySource interface {
	ObserveStaffIdentity(context.Context, zitadel.StaffIdentityInput, time.Time) (zitadel.StaffIdentityObservation, error)
}

// WithStaffIdentity enables the explicitly registered product's identity workflow.
func WithStaffIdentity(product string, source StaffIdentitySource) Option {
	return func(s *Service) {
		s.staffProduct = product
		s.staffSource = source
	}
}

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

func staffIdentityReceipt(op store.StaffIdentityOperation) StaffIdentityReceipt {
	state := "prepared"
	if op.DispatchedAt != nil {
		state = "dispatched"
	}
	result := StaffIdentityReceipt{SchemaVersion: 1, CommandID: op.Command.ID,
		TenantID: op.Command.OrgID, ProductKey: op.Command.ProductKey,
		ActorSubject: op.Command.ActorSubject, CommandState: state, PreparedAt: op.Command.CreatedAt,
		RecoveryState: op.RecoveryState, SuccessorCommandID: op.SuccessorCommandID}
	if op.Creation != nil {
		result.CommandState = "created"
		result.UserID = op.Creation.ProviderUserID
		result.CreatedAt = &op.Creation.ProviderCreatedAt
	}
	return result
}

func (s *Service) staffIdentityScope(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, actor string, action authz.Action) (store.StaffIdentityScope, error) {
	empty := store.StaffIdentityScope{}
	if p.Kind != authz.KindProduct || p.Subject == "" || p.ProductKey == "" {
		s.auditTarget(ctx, p, string(action), tenant.String(), command.String(), audit.OutcomeDenied, nil)
		return empty, ErrNotFound
	}
	if s.staffProduct == "" || s.staffSource == nil {
		return empty, ErrUnavailable
	}
	if p.ProductKey != s.staffProduct {
		s.auditTarget(ctx, p, string(action), tenant.String(), command.String(), audit.OutcomeDenied, nil)
		return empty, ErrNotFound
	}
	if tenant == uuid.Nil || command.Version() != 7 || command.Variant() != uuid.RFC4122 || !enrolSourceID.MatchString(actor) {
		return empty, ErrInvalid
	}
	if err := s.guard(ctx, p, action, tenant.String(), nil); err != nil {
		return empty, err
	}
	proof, err := s.ProductIdentity(ctx, p, tenant)
	if err != nil {
		return empty, err
	}
	if proof.State != "active" {
		s.auditTarget(ctx, p, string(action), tenant.String(), command.String(), audit.OutcomeDenied, nil)
		return empty, ErrNotFound
	}
	return store.StaffIdentityScope{CommandID: command, OrgID: tenant, ProductKey: p.ProductKey,
		MachineSubject: p.Subject, ActorSubject: actor, ProviderOrgID: proof.OrganizationID,
		ProviderProjectID: proof.ProjectID}, nil
}

func staffIdentityEvent(ctx context.Context, p authz.Principal, scope store.StaffIdentityScope, action string, observation *StaffIdentityCurrentState) audit.Event {
	return audit.Event{ActorType: actorType(p), ActorID: p.Subject, ProductKey: p.ProductKey,
		OrgID: scope.OrgID.String(), Target: scope.CommandID.String(), Action: action,
		Outcome: audit.OutcomeSuccess, RequestID: RequestIDFrom(ctx),
		Detail: encodeDetail(struct {
			ActorSubject string                     `json:"actor_subject"`
			Observation  *StaffIdentityCurrentState `json:"observation,omitempty"`
		}{scope.ActorSubject, observation})}
}

func (s *Service) readStaffIdentity(ctx context.Context, p authz.Principal, scope store.StaffIdentityScope, action string, observation *StaffIdentityCurrentState) (store.StaffIdentityOperation, error) {
	event := staffIdentityEvent(ctx, p, scope, action, observation)
	op, err := s.store.ReadStaffIdentity(ctx, scope, event)
	if errors.Is(err, store.ErrNotFound) {
		s.auditTarget(ctx, p, action, scope.OrgID.String(), scope.CommandID.String(), audit.OutcomeDenied, nil)
		return store.StaffIdentityOperation{}, ErrNotFound
	}
	if err != nil {
		return store.StaffIdentityOperation{}, ErrUnavailable
	}
	return op, nil
}

// StaffIdentityReceipt reauthorizes and audits an exact immutable command-state read.
func (s *Service) StaffIdentityReceipt(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, actor string) (StaffIdentityReceipt, error) {
	empty := StaffIdentityReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	scope, err := s.staffIdentityScope(ctx, p, tenant, command, actor, authz.ActionStaffIdentityRead)
	if err != nil {
		return empty, err
	}
	op, err := s.readStaffIdentity(ctx, p, scope, "staff.identity.read", nil)
	if err != nil {
		return empty, err
	}
	return staffIdentityReceipt(op), nil
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

// ObserveStaffIdentity rechecks current source facts and reconciles without redispatch.
func (s *Service) ObserveStaffIdentity(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, actor string) (StaffIdentityObservationReceipt, error) {
	empty := StaffIdentityObservationReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	scope, err := s.staffIdentityScope(ctx, p, tenant, command, actor, authz.ActionStaffIdentityObserve)
	if err != nil {
		return empty, err
	}
	op, err := s.readStaffIdentity(ctx, p, scope, "staff.identity.observe.read", nil)
	if err != nil {
		return empty, err
	}
	if op.DispatchedAt == nil || op.RecoveryState != "" {
		return empty, ErrConflict
	}
	c := op.Command
	var expectedCreated time.Time
	if op.Creation != nil {
		expectedCreated = op.Creation.ProviderCreatedAt
	}
	started := time.Now().UTC()
	proof, sourceErr := s.staffSource.ObserveStaffIdentity(ctx, zitadel.StaffIdentityInput{
		OrganizationID: c.ProviderOrgID, UserID: c.ProviderUserID, Email: c.Email,
		GivenName: c.GivenName, FamilyName: c.FamilyName, BindingNonce: c.BindingNonce,
	}, expectedCreated)
	finished := time.Now().UTC()
	current, err := s.staffIdentityScope(ctx, p, tenant, command, actor, authz.ActionStaffIdentityObserve)
	if err != nil {
		return empty, err
	}
	if current != scope {
		return empty, ErrUnavailable
	}
	observation := StaffIdentityCurrentState{State: "missing", ObservedAt: finished}
	if sourceErr == nil {
		if !validStaffObservation(proof, op, started, finished) {
			return empty, ErrUnavailable
		}
		observation = StaffIdentityCurrentState{State: proof.State, EmailVerified: proof.EmailVerified,
			ObservedAt: proof.ObservedAt, UserRevision: proof.UserRevision, MetadataRevision: proof.MetadataRevision}
		if op.Creation == nil {
			if _, err := s.store.RecordStaffIdentityCreation(ctx, c, proof.CreatedAt,
				staffIdentityEvent(ctx, p, scope, "staff.identity.created", &observation)); err != nil {
				return empty, ErrUnavailable
			}
		}
	} else if !errors.Is(sourceErr, zitadel.ErrIdentityMissing) {
		return empty, ErrUnavailable
	}
	final, err := s.readStaffIdentity(ctx, p, current, "staff.identity.observe", &observation)
	if err != nil {
		return empty, err
	}
	if final.RecoveryState != "" {
		return empty, ErrConflict
	}
	return StaffIdentityObservationReceipt{Receipt: staffIdentityReceipt(final), Observation: observation}, nil
}

func validStaffObservation(proof zitadel.StaffIdentityObservation, op store.StaffIdentityOperation, started, finished time.Time) bool {
	if op.DispatchedAt == nil || proof.UserID != op.Command.ProviderUserID || proof.OrganizationID != op.Command.ProviderOrgID ||
		proof.CreatedAt.IsZero() || proof.CreatedAt.Before(op.DispatchedAt.Add(-zitadel.StaffIdentityClockSkew)) ||
		proof.CreatedAt.After(finished.Add(zitadel.StaffIdentityClockSkew)) ||
		proof.ObservedAt.Before(started) || proof.ObservedAt.After(finished) {
		return false
	}
	if op.Creation != nil && !proof.CreatedAt.Equal(op.Creation.ProviderCreatedAt) {
		return false
	}
	switch proof.State {
	case "active", "inactive", "locked", "initial":
	default:
		return false
	}
	for _, revision := range []string{proof.UserRevision, proof.MetadataRevision} {
		if !positiveRevision(revision) {
			return false
		}
	}
	return true
}
