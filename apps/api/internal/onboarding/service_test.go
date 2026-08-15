package onboarding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// fakeIdentity stands in for Zitadel. It records what it was asked so the tests
// can assert the ordering rule the design depends on: Zitadel is written first
// and the projection follows.
type fakeIdentity struct {
	mu       sync.Mutex
	calls    []string
	metadata map[string]string
	members  map[string][]Member
	failOn   string
	nextID   int
}

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{metadata: map[string]string{}, members: map[string][]Member{}}
}

func (f *fakeIdentity) record(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	if f.failOn == name {
		return errors.New("zitadel is unavailable")
	}
	return nil
}

func (f *fakeIdentity) did(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (f *fakeIdentity) CreateOrg(context.Context, string) (string, error) {
	if err := f.record("CreateOrg"); err != nil {
		return "", err
	}
	f.nextID++
	return fmt.Sprintf("zorg-fake-%d", f.nextID), nil
}

func (f *fakeIdentity) SetOrgMetadata(_ context.Context, orgID, key, value string) error {
	if err := f.record("SetOrgMetadata"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metadata[orgID+"/"+key] = value
	return nil
}

func (f *fakeIdentity) DeactivateOrg(context.Context, string) error {
	return f.record("DeactivateOrg")
}
func (f *fakeIdentity) ReactivateOrg(context.Context, string) error {
	return f.record("ReactivateOrg")
}

func (f *fakeIdentity) CreateHumanUser(_ context.Context, orgID, email, _, _ string) (string, error) {
	if err := f.record("CreateHumanUser"); err != nil {
		return "", err
	}
	return "user-" + email, nil
}

func (f *fakeIdentity) AddOrgMember(_ context.Context, orgID, userID, role string) error {
	if err := f.record("AddOrgMember"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[orgID] = append(f.members[orgID], Member{UserID: userID, Roles: []string{role}})
	return nil
}

func (f *fakeIdentity) RemoveOrgMember(context.Context, string, string) error {
	return f.record("RemoveOrgMember")
}

func (f *fakeIdentity) OrgMembers(_ context.Context, orgID string) ([]Member, error) {
	if err := f.record("OrgMembers"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.members[orgID], nil
}

func (f *fakeIdentity) GrantProject(context.Context, string, string, []string) error {
	return f.record("GrantProject")
}

func (f *fakeIdentity) AddOrgDomain(context.Context, string, string) error {
	return f.record("AddOrgDomain")
}
func (f *fakeIdentity) RemoveOrgDomain(context.Context, string, string) error {
	return f.record("RemoveOrgDomain")
}

func (f *fakeIdentity) GenerateDomainChallenge(_ context.Context, _, domain string) (zitadel.Challenge, error) {
	if err := f.record("GenerateDomainChallenge"); err != nil {
		return zitadel.Challenge{}, err
	}
	return zitadel.Challenge{Type: "TXT", Name: "_zitadel-challenge." + domain, Value: "tok"}, nil
}

func (f *fakeIdentity) ValidateDomain(context.Context, string, string) error {
	return f.record("ValidateDomain")
}

func (f *fakeIdentity) CreateIDP(_ context.Context, _ string, spec zitadel.IDPSpec) (string, error) {
	if err := f.record("CreateIDP"); err != nil {
		return "", err
	}
	return "idp-fake", nil
}

func (f *fakeIdentity) DeleteIDP(context.Context, string, string) error {
	return f.record("DeleteIDP")
}

func newService(t *testing.T) (*Service, *fakeIdentity, *store.Store) {
	t.Helper()
	dsn := os.Getenv("ONBOARDING_TEST_DSN")
	if dsn == "" {
		t.Skip("ONBOARDING_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"idps", "domains", "org_products", "organizations", "products", "idempotency_keys"} {
		if _, err := pool.Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	st := store.New(pool)
	id := newFakeIdentity()
	return New(st, id, authz.New(st), nil), id, st
}

func seedProduct(t *testing.T, s *store.Store, key string) {
	t.Helper()
	err := s.CreateProduct(context.Background(), store.Product{
		Key: key, DisplayName: key, ZitadelProjectID: "proj-" + key, MachineUserID: "svc-" + key,
		Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsOpenBao,
		Tenancy: store.TenancyOrg, Roles: []string{key + "-user"}, DataResidency: "in",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func platform() authz.Principal {
	return authz.Principal{Kind: authz.KindPlatform, Subject: "sam", Roles: []string{authz.RolePlatformAdmin}}
}

func product(key string) authz.Principal {
	return authz.Principal{Kind: authz.KindProduct, Subject: "svc-" + key, ProductKey: key}
}

func TestCreatingAnOrgWritesIdentityFirstThenTheProjection(t *testing.T) {
	svc, id, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")

	org, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{
		Name: "Apollo", OwnerEmail: "cto@apollo.com", Products: []string{"planning-poker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if org.ZitadelOrgID != "zorg-fake-1" {
		t.Fatalf("no identity org recorded: %+v", org)
	}
	// tenant_id must reach the token, and it is the projection's own id.
	if got := id.metadata[org.ZitadelOrgID+"/tenant_id"]; got != org.ID.String() {
		t.Fatalf("tenant_id metadata %q, want %q", got, org.ID)
	}
	if !id.did("GrantProject") {
		t.Fatal("requested product was not granted in the identity plane")
	}
	entitled, err := st.Entitled(ctx, org.ID.String(), "planning-poker")
	if err != nil || !entitled {
		t.Fatalf("projection not entitled: %v %v", entitled, err)
	}
}

func TestOwnerIsInvitedAsOrgOwner(t *testing.T) {
	svc, id, st := newService(t)
	seedProduct(t, st, "planning-poker")

	org, err := svc.CreateOrganization(context.Background(), platform(), CreateOrgInput{
		Name: "Apollo", OwnerEmail: "cto@apollo.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	members := id.members[org.ZitadelOrgID]
	if len(members) != 1 || members[0].UserID != "user-cto@apollo.com" {
		t.Fatalf("owner not added: %+v", members)
	}
}

// A product self-serving an org may only ever attach itself. Trusting the body
// would let one product grant a competitor's project into a customer's org.
func TestAProductMayOnlyGrantItself(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")
	seedProduct(t, st, "mark8ly")

	org, err := svc.CreateOrganization(ctx, product("planning-poker"), CreateOrgInput{
		Name: "Apollo", OwnerEmail: "cto@apollo.com", Products: []string{"planning-poker", "mark8ly"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if entitled, _ := st.Entitled(ctx, org.ID.String(), "mark8ly"); entitled {
		t.Fatal("a product granted another product's project into a customer org")
	}
	if entitled, _ := st.Entitled(ctx, org.ID.String(), "planning-poker"); !entitled {
		t.Fatal("the calling product was not granted")
	}
}

func TestAnOrgIsInvisibleToAProductWithoutAGrant(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")
	seedProduct(t, st, "mark8ly")

	org, err := svc.CreateOrganization(ctx, product("planning-poker"), CreateOrgInput{
		Name: "Apollo", OwnerEmail: "cto@apollo.com", Products: []string{"planning-poker"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Organization(ctx, product("mark8ly"), org.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound so the id is not confirmed, got %v", err)
	}
}

func TestEveryMutationLandsInTheLedger(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")

	org, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Apollo", OwnerEmail: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetOrganizationState(ctx, platform(), org.ID, "inactive"); err != nil {
		t.Fatal(err)
	}

	chain, err := st.AuditChain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Verify(chain); err != nil {
		t.Fatalf("ledger broken: %v", err)
	}
	var seen []string
	for _, r := range chain {
		seen = append(seen, r.Action)
	}
	for _, want := range []string{"org.create", "org.deactivate"} {
		if !contains(seen, want) {
			t.Fatalf("%s not in the ledger: %v", want, seen)
		}
	}
}

// A denial is evidence, not silence.
func TestADeniedCallIsAudited(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")
	org, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Apollo", OwnerEmail: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}

	// A product cannot deactivate a customer's org, whatever its grants.
	if err := svc.SetOrganizationState(ctx, product("planning-poker"), org.ID, "inactive"); err == nil {
		t.Fatal("a product deactivated an organization")
	}
	chain, _ := st.AuditChain(ctx)
	for _, r := range chain {
		if r.Action == "org.deactivate" && r.Outcome == audit.OutcomeDenied {
			return
		}
	}
	t.Fatal("the denial left no trace in the ledger")
}

func TestADomainIsOnlyVerifiedAfterTheIdentityPlaneConfirmsIt(t *testing.T) {
	svc, id, st := newService(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Apollo", OwnerEmail: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddDomain(ctx, platform(), org.ID, "apollo.com"); err != nil {
		t.Fatal(err)
	}

	id.failOn = "ValidateDomain"
	if err := svc.VerifyDomain(ctx, platform(), org.ID, "apollo.com"); err == nil {
		t.Fatal("verification succeeded while the DNS challenge was unmet")
	}
	if _, err := st.OrganizationByDomain(ctx, "apollo.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("an unverified domain became resolvable")
	}

	id.failOn = ""
	if err := svc.VerifyDomain(ctx, platform(), org.ID, "apollo.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.OrganizationByDomain(ctx, "apollo.com"); err != nil {
		t.Fatalf("verified domain does not resolve: %v", err)
	}
}

// Discovery must not become an enumeration oracle: an unknown domain is an
// empty answer, not an error that distinguishes it from a known one.
func TestDiscoveryOfAnUnknownDomainRevealsNothing(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")

	got, err := svc.Resolve(ctx, product("planning-poker"), "someone@nowhere.example")
	if err != nil {
		t.Fatalf("unknown domain returned an error: %v", err)
	}
	if got.Found {
		t.Fatalf("resolved an unknown domain: %+v", got)
	}
}

func TestDiscoveryReturnsTheOrgOnlyToAnEntitledProduct(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")
	seedProduct(t, st, "mark8ly")

	org, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{
		Name: "Apollo", OwnerEmail: "a@b.com", Products: []string{"planning-poker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddDomain(ctx, platform(), org.ID, "apollo.com"); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyDomain(ctx, platform(), org.ID, "apollo.com"); err != nil {
		t.Fatal(err)
	}

	entitled, err := svc.Resolve(ctx, product("planning-poker"), "cto@apollo.com")
	if err != nil || !entitled.Found || entitled.OrgID != org.ID.String() {
		t.Fatalf("entitled product could not resolve its own customer: %+v %v", entitled, err)
	}
	other, err := svc.Resolve(ctx, product("mark8ly"), "cto@apollo.com")
	if err != nil {
		t.Fatal(err)
	}
	if other.Found {
		t.Fatal("a product resolved a customer it has no grant on")
	}
}

func TestAnIDPSecretIsNeverProjected(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Apollo", OwnerEmail: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}

	idp, err := svc.CreateIDP(ctx, platform(), org.ID, zitadel.IDPSpec{
		Type: "oidc", Name: "Okta", Issuer: "https://apollo.okta.com",
		ClientID: "abc", ClientSecret: "super-secret-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if idp.ZitadelIDPID != "idp-fake" {
		t.Fatalf("idp id %q", idp.ZitadelIDPID)
	}

	var dump strings.Builder
	stored, err := st.IDPs(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range stored {
		dump.WriteString(i.DisplayName + i.Type + i.ZitadelIDPID)
	}
	chain, _ := st.AuditChain(ctx)
	for _, r := range chain {
		dump.WriteString(r.Detail)
	}
	if strings.Contains(dump.String(), "super-secret-value") {
		t.Fatal("the customer's IdP secret was written to our own database")
	}
}

func TestAProductSeesOnlyItsOwnCustomers(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")
	seedProduct(t, st, "mark8ly")

	if _, err := svc.CreateOrganization(ctx, product("planning-poker"), CreateOrgInput{Name: "Apollo", OwnerEmail: "a@b.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateOrganization(ctx, product("mark8ly"), CreateOrgInput{Name: "Acme", OwnerEmail: "c@d.com"}); err != nil {
		t.Fatal(err)
	}

	orgs, err := svc.Organizations(ctx, product("planning-poker"))
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 || orgs[0].Name != "Apollo" {
		t.Fatalf("planning-poker sees %d orgs: %+v", len(orgs), orgs)
	}

	all, err := svc.Organizations(ctx, platform())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("platform sees %d orgs, want 2", len(all))
	}
}

func TestAFailedIdentityWriteLeavesNoProjectionRow(t *testing.T) {
	svc, id, st := newService(t)
	ctx := context.Background()
	id.failOn = "CreateOrg"

	if _, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Apollo", OwnerEmail: "a@b.com"}); err == nil {
		t.Fatal("org creation reported success while the identity plane failed")
	}
	orgs, err := st.Organizations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 0 {
		t.Fatalf("projection holds %d orgs that do not exist in the identity plane", len(orgs))
	}
}

func TestOrgAdminCannotReachAnotherOrg(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	mine, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Apollo", OwnerEmail: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Acme", OwnerEmail: "c@d.com"})
	if err != nil {
		t.Fatal(err)
	}
	_ = st

	admin := authz.Principal{Kind: authz.KindOrgAdmin, Subject: "u1", OrgID: mine.ID.String()}
	if _, err := svc.Organization(ctx, admin, theirs.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org admin read another organization: %v", err)
	}
	if _, err := svc.Organization(ctx, admin, mine.ID); err != nil {
		t.Fatalf("org admin cannot read their own organization: %v", err)
	}
}

func TestUnknownOrgIsNotFound(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Organization(context.Background(), platform(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// The wizard's "I've raised the PR" checkbox is only worth anything if it is
// recorded: it is the claim that the live objects have a git counterpart.
func TestAcknowledgingProductManifestsIsPlatformOnlyAndLandsInTheLedger(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	seedProduct(t, st, "planning-poker")

	if err := svc.AcknowledgeProductManifests(ctx, product("planning-poker"), "planning-poker"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("a product acknowledged its own manifests: %v", err)
	}
	if err := svc.AcknowledgeProductManifests(ctx, platform(), "planning-poker"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AcknowledgeProductManifests(ctx, platform(), "no-such-product"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown product was acknowledged: %v", err)
	}

	events, err := svc.Audit(ctx, platform(), store.AuditFilter{Action: "product.manifests.acknowledged"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Target != "planning-poker" {
		t.Fatalf("acknowledgement not in the ledger: %+v", events)
	}
}
