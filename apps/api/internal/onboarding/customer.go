package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// StartCustomer durably records server-bound signup intent before identity delivery.
func (s *Service) StartCustomer(ctx context.Context, id uuid.UUID, receiptHash []byte, in customercontract.Signup) (customercontract.Receipt, error) {
	if len(receiptHash) != 32 || in.Validate(id) != nil {
		return customercontract.Receipt{}, ErrInvalid
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return customercontract.Receipt{}, ErrInvalid
	}
	intent := sha256.Sum256(raw)
	email := sha256.Sum256([]byte(strings.ToLower(in.Owner.Email)))
	tenant := customerTenantID(id.String())
	resource, err := uuid.NewV7()
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	row, err := s.store.ReserveCustomerSignup(ctx, store.CustomerSignup{
		ID: id, TenantID: tenant, ResourceID: resource, OwnerSubject: "owner-" + tenant.String(),
		ReceiptHash: receiptHash, IntentHash: intent[:], OwnerEmailHash: email[:], Input: in, RetryAt: time.Now().UTC(),
	}, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, Outcome: audit.OutcomeSuccess})
	if err != nil {
		return customercontract.Receipt{}, customerError(err)
	}
	return customerReceipt(row), nil
}

const customerWorkload = "system:serviceaccount:helivanta:helivanta-api"

func customerTenantID(key string) uuid.UUID {
	return hospitalTenantID("helivanta", customerWorkload, key)
}
func hospitalTenantID(product, actor, key string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("hospital-onboarding:"+product+":"+actor+":"+key))
}
func customerError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}
func customerReceipt(row store.CustomerSignup) customercontract.Receipt {
	state := customercontract.Provisioning
	switch row.State {
	case "existing_account", "identity_requested":
		state = customercontract.EmailActionRequired
	case "needs_support":
		state = customercontract.NeedsSupport
	case "ready":
		if row.ReceiptVerified {
			return customercontract.Receipt{State: customercontract.Ready, Name: row.Input.Name, Kind: row.Input.Kind, TenantID: row.TenantID.String(), ResourceID: row.ResourceID.String()}
		}
		state = customercontract.EmailActionRequired
	}
	return customercontract.Receipt{State: state}
}

// Customer returns only the command bound to the presented receipt.
func (s *Service) Customer(ctx context.Context, id uuid.UUID, hash []byte) (customercontract.Receipt, error) {
	row, err := s.store.CustomerSignup(ctx, id, hash)
	if err != nil {
		return customercontract.Receipt{}, customerError(err)
	}
	return customerReceipt(row), nil
}

// WithCustomerIdentity supplies the native code-verification client.
func WithCustomerIdentity(client *zitadel.Client) Option {
	return func(s *Service) { s.signupIdentity = client }
}

// ProveCustomer accepts only native proof for the immutable expected signup owner.
func (s *Service) ProveCustomer(ctx context.Context, in customercontract.Proof, nextHash []byte, password bool) (uuid.UUID, customercontract.Receipt, error) {
	var empty customercontract.Receipt
	if s.signupIdentity == nil {
		return uuid.Nil, empty, ErrUnavailable
	}
	if len(nextHash) != 32 || !hospitalSourceID.MatchString(in.UserID) || in.Code == "" || len(in.Code) > 20 || (!password && in.Password != "") || (password && in.Password == "") {
		return uuid.Nil, empty, ErrInvalid
	}
	row, err := s.store.CustomerSignupBySubject(ctx, in.UserID)
	if err != nil {
		return uuid.Nil, empty, customerError(err)
	}
	if row.Input.Owner == nil || row.OwnerSubject != "owner-"+row.TenantID.String() {
		return uuid.Nil, empty, ErrConflict
	}
	if err = s.store.ConsumeSignupProof(ctx, row.ID, time.Now().UTC()); err != nil {
		return uuid.Nil, empty, customerError(err)
	}
	org := "helivanta-" + row.TenantID.String()
	if _, err = s.signupIdentity.SignupIdentity(ctx, org, row.OwnerSubject, row.Input.Owner.Email); err != nil {
		return uuid.Nil, empty, ErrUnavailable
	}
	if password {
		err = s.signupIdentity.SetSignupPassword(ctx, row.OwnerSubject, in.Code, in.Password)
	} else {
		err = s.signupIdentity.VerifySignupEmail(ctx, row.OwnerSubject, in.Code)
	}
	if errors.Is(err, zitadel.ErrSignupProof) {
		return uuid.Nil, empty, ErrInvalid
	}
	if err != nil {
		return uuid.Nil, empty, ErrUnavailable
	}
	verified, err := s.signupIdentity.SignupIdentity(ctx, org, row.OwnerSubject, row.Input.Owner.Email)
	if err != nil || !verified {
		return uuid.Nil, empty, ErrUnavailable
	}
	if err = s.store.RotateSignupReceipt(ctx, row.ID, nextHash); err != nil {
		return uuid.Nil, empty, customerError(err)
	}
	row.ReceiptVerified = true
	return row.ID, customerReceipt(row), nil
}

// DeliverCustomer requests a bounded native email without disclosing an identity.
func (s *Service) DeliverCustomer(ctx context.Context, id uuid.UUID, hash []byte, in customercontract.Delivery, password bool) (customercontract.Receipt, error) {
	generic := customercontract.Receipt{State: customercontract.EmailActionRequired}
	if s.signupIdentity == nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	var row store.CustomerSignup
	var err error
	if in.UserID != "" {
		if !hospitalSourceID.MatchString(in.UserID) {
			return generic, nil
		}
		row, err = s.store.CustomerSignupBySubject(ctx, in.UserID)
	} else {
		row, err = s.store.CustomerSignup(ctx, id, hash)
	}
	if errors.Is(err, store.ErrNotFound) {
		return generic, nil
	}
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	if row.Input.Owner == nil || row.State == "existing_account" || row.State == "needs_support" {
		return generic, nil
	}
	if err = s.store.ConsumeSignupMail(ctx, row.ID, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return generic, nil
		}
		return customercontract.Receipt{}, ErrUnavailable
	}
	verified, err := s.signupIdentity.SignupIdentity(ctx, "helivanta-"+row.TenantID.String(), row.OwnerSubject, row.Input.Owner.Email)
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	if password && verified {
		err = s.signupIdentity.SendSignupPasswordLink(ctx, row.OwnerSubject)
	} else {
		err = s.signupIdentity.SendSignupEmailLink(ctx, row.OwnerSubject)
	}
	if err != nil {
		return customercontract.Receipt{}, ErrUnavailable
	}
	return generic, nil
}

// DeliverCustomers advances a bounded batch of durable identity deliveries.
func (s *Service) DeliverCustomers(ctx context.Context, now time.Time) error {
	if s.signupIdentity == nil || s.hospitals == nil || s.hospitals.Claims == nil {
		return ErrUnavailable
	}
	rows, err := s.store.LeaseCustomerSignups(ctx, 5, now)
	if err != nil {
		return ErrUnavailable
	}
	for _, row := range rows {
		current, cancel := context.WithTimeout(ctx, 8*time.Second)
		state, code := s.deliverCustomer(current, row)
		cancel()
		retry := now.Add(30 * time.Second)
		if code != "" {
			retry = now.Add(time.Duration((5<<min(row.Attempts, 8))+int(row.ID[0])%5) * time.Second)
		}
		if err = s.store.FinishCustomerSignup(ctx, row, state, code, retry); err != nil && !errors.Is(err, store.ErrConflict) {
			return ErrUnavailable
		}
	}
	return nil
}

func (s *Service) deliverCustomer(ctx context.Context, row store.CustomerSignup) (string, string) {
	if row.Input.Validate(row.ID) != nil || row.TenantID != customerTenantID(row.ID.String()) || row.OwnerSubject != "owner-"+row.TenantID.String() {
		return "needs_support", ""
	}
	if row.State == "recorded" {
		other, err := s.signupIdentity.OtherSignupIdentity(ctx, row.Input.Owner.Email, row.OwnerSubject)
		if err != nil {
			return row.State, "identity_unavailable"
		}
		if other {
			return "existing_account", ""
		}
		_, err = s.hospitals.Claims.Create(ctx, hospitalclaims.Spec{Product: "helivanta", TenantID: row.TenantID.String(), DisplayName: row.Input.Name, InitialAdminUserID: row.OwnerSubject, InitialAdmin: row.Input.Owner})
		if errors.Is(err, hospitalclaims.ErrConflict) {
			return "needs_support", ""
		}
		if err != nil {
			return row.State, "identity_unavailable"
		}
		return "identity_requested", ""
	}
	claim, err := s.hospitals.Claims.Get(ctx, "helivanta", row.TenantID.String())
	if err != nil {
		return row.State, "identity_unavailable"
	}
	if claim.Spec.InitialAdminUserID != row.OwnerSubject || claim.Spec.TenantID != row.TenantID.String() || claim.Spec.Product != "helivanta" {
		return "needs_support", ""
	}
	if !HospitalReady(claim) {
		return row.State, ""
	}
	if err = s.initializeCustomer(ctx, row); err != nil {
		return row.State, "provisioning_unavailable"
	}
	return "ready", ""
}

func (s *Service) initializeCustomer(ctx context.Context, row store.CustomerSignup) error {
	workspace := customercontract.Workspace{TenantID: row.TenantID.String(), OrganizationID: "helivanta-" + row.TenantID.String(), OwnerSubject: row.OwnerSubject, ResourceID: row.ResourceID.String(), Setup: row.Input.Setup}
	if workspace.Validate() != nil {
		return ErrConflict
	}
	product, err := s.store.Product(ctx, "helivanta")
	if err != nil || product.State != "active" {
		return ErrUnavailable
	}
	started := time.Now().UTC()
	org := store.Organization{ID: row.TenantID, ZitadelOrgID: workspace.OrganizationID}
	proof, err := s.idp.VerifyOrganization(ctx, workspace.OrganizationID, workspace.TenantID, product.ZitadelProjectID)
	if err != nil || !currentIdentityProof(proof, org, product, started, time.Now().UTC()) || proof.State != "active" {
		return ErrUnavailable
	}
	verified, err := s.signupIdentity.SignupIdentity(ctx, workspace.OrganizationID, row.OwnerSubject, row.Input.Owner.Email)
	if err != nil || !verified {
		return ErrUnavailable
	}
	members, err := s.idp.OrgMembers(ctx, workspace.OrganizationID)
	if err != nil {
		return ErrUnavailable
	}
	owner := false
	for _, member := range members {
		if member.UserID == row.OwnerSubject {
			for _, role := range member.Roles {
				if role == "ORG_OWNER" {
					owner = true
				}
			}
		}
	}
	if !owner {
		return ErrConflict
	}
	raw, err := json.Marshal(workspace)
	if err != nil {
		return ErrUnavailable
	}
	token, err := os.ReadFile(s.hospitals.HelivantaTokenFile)
	if err != nil || len(token) > 8192 || strings.TrimSpace(string(token)) == "" {
		return ErrUnavailable
	}
	target := strings.TrimSuffix(s.hospitals.HelivantaURL, "/hospital-enrolments") + "/customer-workspaces"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return ErrUnavailable
	}
	request.Header.Set("X-Onboarding-Token", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	if s.hospitals.HTTPClient != nil {
		copy := *s.hospitals.HTTPClient
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	var receipt customercontract.Receipt
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || receipt.Validate() != nil || receipt.State != customercontract.Ready || receipt.TenantID != workspace.TenantID || receipt.ResourceID != workspace.ResourceID || receipt.Name != workspace.Name || receipt.Kind != workspace.Kind {
		return ErrUnavailable
	}
	return nil
}
