package onboarding

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

// CustomerLoginTargets resolves every eligible account behind an email for the
// verified API workload, under the same eligibility as discovery choices.
func (s *Service) CustomerLoginTargets(ctx context.Context, in customercontract.LoginTargetsInput) (out customercontract.LoginTargets, failure error) {
	requestCtx, started, phase := ctx, time.Now(), "dependencies"
	defer func() { s.recordAccountLookupFailure(requestCtx, phase, started, failure) }()

	empty := customercontract.LoginTargets{Targets: []customercontract.LoginCandidate{}}
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.idp == nil || s.signupIdentity == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	phase = "identity_accounts"
	accounts, err := s.signupIdentity.DiscoveryAccounts(ctx, in.Email)
	if err != nil {
		return empty, ErrUnavailable
	}
	targets := []customercontract.LoginCandidate{}
	for _, account := range accounts {
		phase = "tenant_scope"
		orgID, err := s.customerIdentityScope(ctx, account.TenantID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return empty, ErrUnavailable
		}
		phase = "organization_display"
		tenant, _ := uuid.Parse(account.TenantID)
		org, err := s.store.Organization(ctx, tenant)
		if err != nil {
			return empty, ErrUnavailable
		}
		targets = append(targets, customercontract.LoginCandidate{TenantID: account.TenantID, OrganizationID: orgID, Subject: account.Subject, Name: org.Name, Account: in.Email})
	}
	result := customercontract.LoginTargets{Targets: targets}
	if len(targets) > customercontract.MaxLoginTargets {
		// Fail closed to an empty list: no binding the caller must not use leaves this service.
		result = customercontract.LoginTargets{Targets: []customercontract.LoginCandidate{}, Truncated: true}
	}
	sort.Slice(result.Targets, func(i, j int) bool {
		if result.Targets[i].Name == result.Targets[j].Name {
			return result.Targets[i].TenantID < result.Targets[j].TenantID
		}
		return result.Targets[i].Name < result.Targets[j].Name
	})
	phase = "response_conformance"
	if result.Validate() != nil {
		return empty, ErrUnavailable
	}
	phase = "audit"
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", Action: "customer.login.targets_read", Outcome: audit.OutcomeSuccess, RequestID: in.RequestID}); err != nil {
		return empty, ErrUnavailable
	}
	return result, nil
}
