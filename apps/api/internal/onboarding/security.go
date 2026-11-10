package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"time"
)

func securityView(in customercontract.SecurityRequest, claim hospitalclaims.Result, proof zitadel.OwnerSecurityProof) customercontract.SecurityView {
	raw, _ := json.Marshal(struct {
		Tenant, Subject, Version, Request string
		Proof                             zitadel.OwnerSecurityProof
	}{in.TenantID, in.Subject, claim.ResourceVersion, claim.SecurityRequest, proof})
	sum := sha256.Sum256(raw)
	state := customercontract.SecurityDrifted
	if proof.RequireMFA && proof.ExternalMFA && proof.NoGrace {
		state = customercontract.SecurityApplied
	} else if claim.SecurityRequest != "" {
		state = customercontract.SecurityPending
	}
	return customercontract.SecurityView{State: state, TenantID: in.TenantID, Subject: in.Subject, Revision: hex.EncodeToString(sum[:]), RequireMFA: proof.RequireMFA, ExternalMFA: proof.ExternalMFA, NoGrace: proof.NoGrace, CanReconcile: !claim.Spec.PreserveLoginPolicy}
}

// OwnerSecurity is workload-only; the native organization is resolved from the enrolled tenant.
func (s *Service) OwnerSecurity(ctx context.Context, in customercontract.SecurityRequest, apply bool) (customercontract.SecurityView, error) {
	var empty customercontract.SecurityView
	if in.Validate() != nil || apply && in.ExpectedRevision == "" {
		return empty, ErrInvalid
	}
	native := s.signupIdentity
	if native == nil || s.hospitals == nil || s.hospitals.Claims == nil || s.store == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	tenant, _ := uuid.Parse(in.TenantID)
	org, err := s.store.Organization(ctx, tenant)
	if err != nil {
		return empty, ErrUnavailable
	}
	if org.State != "active" || org.CreatedByProduct != "helivanta" {
		return empty, authz.ErrDenied
	}
	product, err := s.store.Product(ctx, "helivanta")
	if err != nil {
		return empty, ErrUnavailable
	}
	if product.State != "active" {
		return empty, authz.ErrDenied
	}
	grants, err := s.store.Grants(ctx, tenant)
	if err != nil {
		return empty, ErrUnavailable
	}
	enrolled := false
	for _, grant := range grants {
		if grant.ProductKey == "helivanta" && grant.RevokedAt == nil {
			enrolled = true
		}
	}
	if !enrolled {
		return empty, authz.ErrDenied
	}
	binding, err := s.idp.VerifyOrganization(ctx, org.ZitadelOrgID, in.TenantID, product.ZitadelProjectID)
	if err != nil {
		return empty, ErrUnavailable
	}
	if binding.State != "active" {
		return empty, authz.ErrDenied
	}
	proof, err := native.OwnerSecurity(ctx, org.ZitadelOrgID, in.Subject)
	if errors.Is(err, zitadel.ErrIdentityMissing) {
		return empty, authz.ErrDenied
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	claim, err := s.hospitals.Claims.Get(ctx, "helivanta", in.TenantID)
	if err != nil {
		return empty, ErrUnavailable
	}
	if claim.Spec.Product != "helivanta" || claim.Spec.TenantID != in.TenantID || claim.Status.OrganizationID != org.ZitadelOrgID || claim.Status.ProjectID != product.ZitadelProjectID || !HospitalReady(claim) {
		return empty, ErrUnavailable
	}
	view := securityView(in, claim, proof)
	action := "owner.security.read"
	if apply {
		action = "owner.security.reconcile"
		if !view.CanReconcile {
			return empty, authz.ErrDenied
		}
		if view.Revision != in.ExpectedRevision && claim.SecurityRequest != in.RequestID {
			return empty, ErrConflict
		}
	}
	// Audit must commit before either sensitive disclosure or a policy request.
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorOrgAdmin, ActorID: in.Subject, ProductKey: "helivanta", OrgID: in.TenantID, Action: action, Target: in.TenantID, Outcome: audit.OutcomeSuccess, RequestID: in.RequestID}); err != nil {
		return empty, ErrUnavailable
	}
	if !apply {
		return view, nil
	}
	claim, err = s.hospitals.Claims.RequestSecurityReconcile(ctx, claim, in.RequestID)
	if errors.Is(err, hospitalclaims.ErrConflict) {
		return empty, ErrConflict
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	// A successful Kubernetes write is not proof the native policy has been applied.
	view = securityView(in, claim, proof)
	view.State = customercontract.SecurityPending
	return view, nil
}
