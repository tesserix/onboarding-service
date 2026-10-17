package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"k8s.io/apimachinery/pkg/api/meta"
)

type HospitalConfig struct {
	Claims             *hospitalclaims.Client
	Product            string
	HelivantaURL       string
	HelivantaTokenFile string
	HTTPClient         *http.Client
}

func WithHospitals(config HospitalConfig) Option { return func(s *Service) { s.hospitals = &config } }

const operatorManagedHospitalProduct = "helivanta"

var errHospitalWorkflow = fmt.Errorf("%w: use /v1/hospital-onboardings for Helivanta", ErrInvalid)

var hospitalSourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

type StartHospitalInput struct {
	Product             string `json:"product"`
	DisplayName         string `json:"displayName"`
	InitialAdminUserID  string `json:"initialAdminUserId"`
	TenantID            string `json:"tenantId,omitempty"`
	OrganizationID      string `json:"organizationId,omitempty"`
	PreserveLoginPolicy bool   `json:"preserveLoginPolicy,omitempty"`
}

func (s *Service) hospitalProduct(ctx context.Context, p authz.Principal, key string, write bool) (store.Product, error) {
	if s.hospitals == nil || s.hospitals.Claims == nil || key != s.hospitals.Product {
		return store.Product{}, ErrNotFound
	}
	switch p.Kind {
	case authz.KindProduct:
		if p.ProductKey != key {
			return store.Product{}, ErrNotFound
		}
	case authz.KindPlatform:
		allowed := false
		for _, role := range p.Roles {
			if role == authz.RolePlatformAdmin || (!write && role == authz.RolePlatformOperator) {
				allowed = true
			}
		}
		if !allowed {
			return store.Product{}, ErrNotFound
		}
	default:
		return store.Product{}, ErrNotFound
	}
	if p.Subject == "" {
		return store.Product{}, ErrNotFound
	}
	product, e := s.store.Product(ctx, key)
	if e != nil {
		return store.Product{}, ErrNotFound
	}
	if product.State != "active" || product.Identity != store.IdentityZitadel {
		return store.Product{}, ErrConflict
	}
	return product, nil
}
func (s *Service) StartHospital(ctx context.Context, p authz.Principal, in StartHospitalInput, idempotencyKey string) (hospitalclaims.Result, error) {
	var empty hospitalclaims.Result
	if _, err := s.hospitalProduct(ctx, p, in.Product, true); err != nil {
		return empty, err
	}
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 200 || strings.TrimSpace(in.DisplayName) == "" || len(in.DisplayName) > 200 || !hospitalSourceID.MatchString(in.InitialAdminUserID) {
		return empty, ErrInvalid
	}
	tenant := uuid.NewSHA1(uuid.NameSpaceURL, []byte("hospital-onboarding:"+in.Product+":"+p.Subject+":"+idempotencyKey))
	adoption := in.TenantID != "" || in.OrganizationID != "" || in.PreserveLoginPolicy
	if adoption {
		if p.Kind != authz.KindPlatform {
			return empty, ErrNotFound
		}
		var err error
		tenant, err = uuid.Parse(in.TenantID)
		if err != nil || tenant == uuid.Nil || tenant.String() != in.TenantID || !hospitalSourceID.MatchString(in.OrganizationID) {
			return empty, ErrInvalid
		}
	}
	spec := hospitalclaims.Spec{Product: in.Product, TenantID: tenant.String(), DisplayName: strings.TrimSpace(in.DisplayName), OrganizationID: in.OrganizationID, PreserveLoginPolicy: in.PreserveLoginPolicy, InitialAdminUserID: in.InitialAdminUserID}
	if e := s.store.AppendAudit(ctx, audit.Event{ActorType: actorType(p), ActorID: p.Subject, ProductKey: in.Product, OrgID: tenant.String(), Action: "hospital.onboarding.requested", Outcome: audit.OutcomeSuccess, RequestID: RequestIDFrom(ctx), Detail: encodeDetail(map[string]string{"product": in.Product})}); e != nil {
		return empty, ErrUnavailable
	}
	result, err := s.hospitals.Claims.Create(ctx, spec)
	if errors.Is(err, hospitalclaims.ErrConflict) {
		return empty, ErrConflict
	}
	if err != nil {
		return empty, ErrUnavailable
	}

	return result, nil
}
func (s *Service) Hospital(ctx context.Context, p authz.Principal, product, tenantID string) (hospitalclaims.Result, error) {
	if _, err := s.hospitalProduct(ctx, p, product, false); err != nil {
		return hospitalclaims.Result{}, err
	}
	tenant, err := uuid.Parse(tenantID)
	if err != nil || tenant == uuid.Nil || tenant.String() != tenantID {
		return hospitalclaims.Result{}, ErrNotFound
	}
	result, err := s.hospitals.Claims.Get(ctx, product, tenantID)
	if err != nil {
		return hospitalclaims.Result{}, ErrNotFound
	}
	if result.Spec.Product != product || result.Spec.TenantID != tenantID {
		return hospitalclaims.Result{}, ErrNotFound
	}
	return result, nil
}

type CompleteHospitalInput struct {
	Product            string `json:"product"`
	TenantID           string `json:"tenantId"`
	OrganizationID     string `json:"organizationId"`
	ProjectID          string `json:"projectId"`
	DisplayName        string `json:"displayName"`
	InitialAdminUserID string `json:"initialAdminUserId"`
}

// CompleteHospital is called only by the authenticated organization controller.
func (s *Service) CompleteHospital(ctx context.Context, in CompleteHospitalInput) error {
	if s.hospitals == nil || s.hospitals.Claims == nil || in.Product != s.hospitals.Product {
		return ErrNotFound
	}
	tenant, e := uuid.Parse(in.TenantID)
	if e != nil || tenant == uuid.Nil || tenant.String() != in.TenantID || !hospitalSourceID.MatchString(in.OrganizationID) {
		return ErrInvalid
	}
	claim, e := s.hospitals.Claims.Get(ctx, in.Product, in.TenantID)
	if e != nil {
		return ErrNotFound
	}
	orgID := claim.Spec.OrganizationID
	if orgID == "" {
		orgID = claim.Spec.Product + "-" + claim.Spec.TenantID
	}
	if claim.Spec.Product != in.Product || claim.Spec.TenantID != in.TenantID || orgID != in.OrganizationID || claim.Spec.InitialAdminUserID != in.InitialAdminUserID || claim.Spec.DisplayName != in.DisplayName {
		return ErrConflict
	}
	product, e := s.store.Product(ctx, in.Product)
	if e != nil {
		return ErrUnavailable
	}
	if product.State != "active" || product.Identity != store.IdentityZitadel || product.ZitadelProjectID != in.ProjectID {
		return ErrConflict
	}
	org := store.Organization{ID: tenant, ZitadelOrgID: in.OrganizationID, Name: in.DisplayName, CreatedByProduct: in.Product}
	started := time.Now().UTC()
	proof, e := s.idp.VerifyOrganization(ctx, org.ZitadelOrgID, in.TenantID, in.ProjectID)
	if e != nil || !currentIdentityProof(proof, org, product, started, time.Now().UTC()) || proof.State != "active" {
		return ErrUnavailable
	}
	actor := "system:serviceaccount:identity-operator:zitadel-operator"
	_, e = s.store.EnrolOrganization(ctx, org, product, audit.Event{ActorType: audit.ActorSystem, ActorID: actor, ProductKey: in.Product, OrgID: in.TenantID, Action: "hospital.enrol", Outcome: audit.OutcomeSuccess, RequestID: in.TenantID, Detail: encodeDetail(map[string]any{"identity": proof})})
	if e != nil {
		return ErrConflict
	}
	token, e := os.ReadFile(s.hospitals.HelivantaTokenFile)
	if e != nil || len(token) > 8192 || strings.TrimSpace(string(token)) == "" {
		return ErrUnavailable
	}
	payload, _ := json.Marshal(map[string]any{"allowBootstrap": !claim.Status.Activated, "tenantId": in.TenantID, "organizationId": in.OrganizationID, "initialAdminUserId": in.InitialAdminUserID})
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, s.hospitals.HelivantaURL, bytes.NewReader(payload))
	if e != nil {
		return ErrUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := &http.Client{Timeout: 10 * time.Second}
	if s.hospitals.HTTPClient != nil {
		copy := *s.hospitals.HTTPClient
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, e := client.Do(request)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusConflict {
		return ErrConflict
	}
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	var result struct {
		TenantID       string `json:"tenantId"`
		OrganizationID string `json:"organizationId"`
		State          string `json:"state"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&result) != nil || result.TenantID != in.TenantID || result.OrganizationID != in.OrganizationID || result.State != "complete" {
		return ErrUnavailable
	}
	if !HospitalReady(claim) {
		if e := s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: actor, ProductKey: in.Product, OrgID: in.TenantID, Action: "hospital.activation.complete", Outcome: audit.OutcomeSuccess, RequestID: in.TenantID, Detail: encodeDetail(map[string]string{"organizationId": in.OrganizationID, "initialAdminUserId": in.InitialAdminUserID})}); e != nil {
			return ErrUnavailable
		}
	}
	return nil
}

// HospitalReady requires a current controller result and a durable archive receipt.
func HospitalReady(result hospitalclaims.Result) bool {
	return result.Status.Activated && result.Generation == result.Status.ObservedGeneration && result.Status.OrganizationID != "" && result.Status.ArchiveGeneration != "" && meta.IsStatusConditionTrue(result.Status.Conditions, "Ready")
}
