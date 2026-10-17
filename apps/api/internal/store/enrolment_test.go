package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
)

func TestEnrolmentAuditFailureRollsBackRegistryAndEntitlement(t *testing.T) {
	s := newStore(t)
	p := seedProduct(t, s, "pharmacy")
	org := Organization{ID: uuid.New(), ZitadelOrgID: "existing-org", Name: "Synthetic pharmacy"}
	_, err := s.EnrolOrganization(context.Background(), org, p, audit.Event{ActorType: "invalid", ActorID: "test-admin", RequestID: uuid.NewString(), OrgID: org.ID.String(), ProductKey: p.Key, Action: "org.enrol", Outcome: audit.OutcomeSuccess})
	if err == nil {
		t.Fatal("invalid audit did not reject enrolment")
	}
	if _, err := s.Organization(context.Background(), org.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed audit left an organisation behind")
	}
	grants, err := s.Grants(context.Background(), org.ID)
	if err != nil || len(grants) != 0 {
		t.Fatal("failed audit left an entitlement behind")
	}
}

func TestConcurrentEnrolmentCreatesOneEntitlementAndOneAudit(t *testing.T) {
	s := newStore(t)
	p := seedProduct(t, s, "pharmacy")
	org := Organization{ID: uuid.New(), ZitadelOrgID: "existing-org", Name: "Synthetic pharmacy"}
	event := audit.Event{ActorType: audit.ActorPlatform, ActorID: "test-admin", RequestID: uuid.NewString(), OrgID: org.ID.String(), ProductKey: p.Key, Action: "org.enrol", Outcome: audit.OutcomeSuccess}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, err := s.EnrolOrganization(context.Background(), org, p, event)
			if err != nil || got.ID != org.ID {
				t.Errorf("concurrent enrolment lost its binding: %v", err)
			}
		})
	}
	wg.Wait()
	grants, err := s.Grants(context.Background(), org.ID)
	if err != nil || len(grants) != 1 {
		t.Fatal("enrolment duplicated entitlements")
	}
	events, err := s.AuditChain(context.Background())
	if err != nil || audit.Verify(events) != nil {
		t.Fatal("audit chain failed verification")
	}
	count := 0
	for _, e := range events {
		if e.OrgID == org.ID.String() && e.Action == "org.enrol" && e.Outcome == audit.OutcomeSuccess {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one successful enrolment audit, got %d", count)
	}
}

func TestEnrolmentRechecksProductAfterSourceObservation(t *testing.T) {
	for _, kind := range []string{"state", "project", "machine", "identity"} {
		t.Run(kind, func(t *testing.T) {
			s := newStore(t)
			p := seedProduct(t, s, "pharmacy")
			queries := map[string]string{
				"state":    `UPDATE products SET state='suspended' WHERE key=$1`,
				"project":  `UPDATE products SET zitadel_project_id='other-project' WHERE key=$1`,
				"machine":  `UPDATE products SET machine_user_id='other-machine' WHERE key=$1`,
				"identity": `UPDATE products SET identity='gip' WHERE key=$1`,
			}
			if _, err := s.pool.Exec(context.Background(), queries[kind], p.Key); err != nil {
				t.Fatal(err)
			}
			org := Organization{ID: uuid.New(), ZitadelOrgID: "existing-org", Name: "Synthetic pharmacy"}
			if _, err := s.EnrolOrganization(context.Background(), org, p, audit.Event{}); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed product configuration accepted: %v", err)
			}
			if _, err := s.Organization(context.Background(), org.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("changed product left a registry row")
			}
		})
	}
}
