package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ONBOARDING_TEST_DSN")
	if dsn == "" {
		t.Skip("ONBOARDING_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// Every test starts from an empty projection. audit_events cannot be
	// deleted by the application role, which is the point, so tests scope
	// their ledger assertions by request id instead of truncating.
	for _, table := range []string{"idps", "domains", "org_products", "organizations", "products", "idempotency_keys"} {
		if _, err := pool.Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return New(pool)
}

func seedProduct(t *testing.T, s *Store, key string) Product {
	t.Helper()
	p := Product{
		Key:              key,
		DisplayName:      key,
		ZitadelProjectID: "proj-" + key,
		MachineUserID:    "svc-" + key,
		Identity:         IdentityZitadel,
		TenantSecrets:    TenantSecretsOpenBao,
		Tenancy:          TenancyOrg,
		Roles:            []string{key + "-user"},
		DataResidency:    "in",
	}
	if err := s.CreateProduct(context.Background(), p); err != nil {
		t.Fatalf("seed product %s: %v", key, err)
	}
	return p
}

func seedOrg(t *testing.T, s *Store, name, product string) Organization {
	t.Helper()
	org, err := s.CreateOrganization(context.Background(), Organization{
		ID:               uuid.New(),
		ZitadelOrgID:     "zorg-" + name,
		Name:             name,
		CreatedByProduct: product,
	})
	if err != nil {
		t.Fatalf("seed org %s: %v", name, err)
	}
	return org
}

func TestProductRoundTripsEveryCapability(t *testing.T) {
	s := newStore(t)
	want := seedProduct(t, s, "planning-poker")

	got, err := s.Product(context.Background(), "planning-poker")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Identity != want.Identity || got.TenantSecrets != want.TenantSecrets ||
		got.Tenancy != want.Tenancy || got.DataResidency != want.DataResidency ||
		len(got.Roles) != 1 || got.Roles[0] != "planning-poker-user" {
		t.Fatalf("capabilities not round-tripped: %+v", got)
	}
}

func TestProductNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Product(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestProductByMachineUserIsHowAProductTokenIsResolved(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "hms")

	got, err := s.ProductByMachineUser(context.Background(), "svc-hms")
	if err != nil || got.Key != "hms" {
		t.Fatalf("want hms, got %+v (%v)", got, err)
	}
}

func TestEntitledIsFalseWithoutAGrant(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "hms")
	org := seedOrg(t, s, "apollo", "hms")

	entitled, err := s.Entitled(context.Background(), org.ID.String(), "hms")
	if err != nil {
		t.Fatal(err)
	}
	if entitled {
		t.Fatal("entitled with no grant row")
	}
}

func TestEntitledIsFalseAfterRevocation(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "hms")
	org := seedOrg(t, s, "apollo", "hms")
	ctx := context.Background()

	if err := s.Grant(ctx, org.ID, "hms", []string{"hms-user"}); err != nil {
		t.Fatal(err)
	}
	if entitled, _ := s.Entitled(ctx, org.ID.String(), "hms"); !entitled {
		t.Fatal("not entitled immediately after grant")
	}

	if err := s.RevokeGrant(ctx, org.ID, "hms"); err != nil {
		t.Fatal(err)
	}
	if entitled, _ := s.Entitled(ctx, org.ID.String(), "hms"); entitled {
		t.Fatal("still entitled after revocation")
	}
}

func TestRegrantAfterRevocationWorks(t *testing.T) {
	s := newStore(t)
	seedProduct(t, s, "hms")
	org := seedOrg(t, s, "apollo", "hms")
	ctx := context.Background()

	if err := s.Grant(ctx, org.ID, "hms", []string{"hms-user"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeGrant(ctx, org.ID, "hms"); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, org.ID, "hms", []string{"hms-user"}); err != nil {
		t.Fatalf("regrant: %v", err)
	}
	if entitled, _ := s.Entitled(ctx, org.ID.String(), "hms"); !entitled {
		t.Fatal("not entitled after regrant")
	}
}

func TestEntitledRejectsAMalformedOrgID(t *testing.T) {
	s := newStore(t)
	if _, err := s.Entitled(context.Background(), "not-a-uuid", "hms"); err == nil {
		t.Fatal("malformed org id accepted")
	}
}

func TestOrganizationsAreListedOnlyForTheGrantedProduct(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedProduct(t, s, "hms")
	seedProduct(t, s, "mark8ly")
	apollo := seedOrg(t, s, "apollo", "hms")
	acme := seedOrg(t, s, "acme", "mark8ly")
	_ = s.Grant(ctx, apollo.ID, "hms", nil)
	_ = s.Grant(ctx, acme.ID, "mark8ly", nil)

	orgs, err := s.OrganizationsForProduct(ctx, "hms")
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 || orgs[0].Name != "apollo" {
		t.Fatalf("hms sees %d orgs: %+v", len(orgs), orgs)
	}
}

func TestOnlyOneOrganizationMayVerifyADomain(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedProduct(t, s, "hms")
	apollo := seedOrg(t, s, "apollo", "hms")
	imposter := seedOrg(t, s, "imposter", "hms")

	if err := s.UpsertDomain(ctx, apollo.ID, "apollo.com", "TXT", "tok-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDomainVerified(ctx, apollo.ID, "apollo.com"); err != nil {
		t.Fatal(err)
	}
	// A second org may claim the same domain, but must not be able to verify it.
	if err := s.UpsertDomain(ctx, imposter.ID, "apollo.com", "TXT", "tok-2"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDomainVerified(ctx, imposter.ID, "apollo.com"); err == nil {
		t.Fatal("two organizations verified the same domain — discovery is now ambiguous")
	}
}

func TestOrganizationByDomainOnlyResolvesVerifiedDomains(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedProduct(t, s, "hms")
	apollo := seedOrg(t, s, "apollo", "hms")
	_ = s.UpsertDomain(ctx, apollo.ID, "apollo.com", "TXT", "tok")

	if _, err := s.OrganizationByDomain(ctx, "apollo.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unverified domain resolved: %v", err)
	}
	if err := s.MarkDomainVerified(ctx, apollo.ID, "apollo.com"); err != nil {
		t.Fatal(err)
	}
	got, err := s.OrganizationByDomain(ctx, "apollo.com")
	if err != nil || got.ID != apollo.ID {
		t.Fatalf("want apollo, got %+v (%v)", got, err)
	}
}

func TestAuditAppendChainsToThePreviousEvent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id := uuid.NewString()

	for _, action := range []string{"org.create", "grant.create", "idp.create"} {
		if err := s.AppendAudit(ctx, audit.Event{
			ActorType: audit.ActorPlatform, ActorID: "sam", Action: action,
			Outcome: audit.OutcomeSuccess, RequestID: id,
		}); err != nil {
			t.Fatalf("append %s: %v", action, err)
		}
	}

	chain, err := s.AuditChain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Verify(chain); err != nil {
		t.Fatalf("ledger does not verify: %v", err)
	}
}

// The chain has one head, so concurrent appends must serialize or two events
// claim the same prev_hash and the ledger stops verifying.
func TestConcurrentAppendsKeepTheChainIntact(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.AppendAudit(ctx, audit.Event{
				ActorType: audit.ActorProduct, ActorID: "svc-hms", ProductKey: "hms",
				Action: "org.read", Outcome: audit.OutcomeSuccess, RequestID: uuid.NewString(),
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}

	chain, err := s.AuditChain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Verify(chain); err != nil {
		t.Fatalf("concurrent appends broke the chain: %v", err)
	}
}

func TestIdempotencyReplaysTheFirstResponse(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	stored, err := s.SaveIdempotent(ctx, "svc-hms", "key-1", "hash-a", 201, []byte(`{"orgId":"1"}`))
	if err != nil || !stored {
		t.Fatalf("first save: stored=%v err=%v", stored, err)
	}

	replay, err := s.Idempotent(ctx, "svc-hms", "key-1", "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	if replay.Status != 201 || string(replay.Body) != `{"orgId":"1"}` {
		t.Fatalf("replay mismatch: %+v", replay)
	}
}

func TestIdempotencyKeyReusedWithADifferentBodyIsRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.SaveIdempotent(ctx, "svc-hms", "key-1", "hash-a", 201, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Idempotent(ctx, "svc-hms", "key-1", "hash-DIFFERENT"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict, got %v", err)
	}
}

func TestIdempotencyKeysAreScopedToTheActor(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.SaveIdempotent(ctx, "svc-hms", "key-1", "hash-a", 201, []byte(`{"secret":"apollo"}`)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Idempotent(ctx, "svc-mark8ly", "key-1", "hash-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("one caller replayed another caller's response: %v", err)
	}
}
