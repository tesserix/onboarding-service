package store

import (
	"github.com/google/uuid"
	"testing"
)

func TestMCPPageExcludesForeignAndRevokedTenantsAndResumesByID(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedProduct(t, s, "helivanta")
	seedProduct(t, s, "other")
	a := seedOrg(t, s, "a", "helivanta")
	b := seedOrg(t, s, "b", "helivanta")
	foreign := seedOrg(t, s, "foreign", "other")
	revoked := seedOrg(t, s, "revoked", "helivanta")
	for _, o := range []Organization{a, b, revoked} {
		if err := s.Grant(ctx, o.ID, "helivanta", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Grant(ctx, foreign.ID, "other", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeGrant(ctx, revoked.ID, "helivanta"); err != nil {
		t.Fatal(err)
	}
	first, err := s.HospitalTenants(ctx, "helivanta", uuid.Nil, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := s.HospitalTenants(ctx, "helivanta", first[0].ID, 1)
	if err != nil || len(second) != 1 || second[0].ID == first[0].ID {
		t.Fatalf("second page: %+v %v", second, err)
	}
	for _, o := range append(first, second...) {
		if o.ID != a.ID && o.ID != b.ID {
			t.Fatalf("foreign or revoked tenant exposed: %s", o.ID)
		}
	}
	last, err := s.HospitalTenants(ctx, "helivanta", second[0].ID, 1)
	if err != nil || len(last) != 0 {
		t.Fatalf("end page: %+v %v", last, err)
	}
}
