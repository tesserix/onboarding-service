package onboarding

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

func TestHelivantaRequiresArchivedOperatorWorkflow(t *testing.T) {
	for _, route := range []string{"create", "product create", "grant", "enrol"} {
		t.Run(route, func(t *testing.T) {
			svc, idp, st := newService(t)
			ctx := context.Background()
			seedProduct(t, st, "helivanta")
			tenant := uuid.New()
			if _, err := st.CreateOrganization(ctx, store.Organization{ID: tenant, ZitadelOrgID: "existing", Name: "Existing", State: "active"}); err != nil {
				t.Fatal(err)
			}
			var err error
			switch route {
			case "create":
				_, err = svc.CreateOrganization(ctx, platform(), CreateOrgInput{Name: "Hospital", Products: []string{"helivanta"}})
			case "product create":
				_, err = svc.CreateOrganization(ctx, product("helivanta"), CreateOrgInput{Name: "Hospital"})
			case "grant":
				err = svc.AddGrant(ctx, platform(), tenant, "helivanta")
			case "enrol":
				_, err = svc.EnrolOrganization(ctx, platform(), EnrolOrgInput{TenantID: tenant, ZitadelOrgID: "existing", ProductKey: "helivanta", Name: "Hospital", Reason: "Adoption"})
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("legacy %s must require hospital workflow, got %v", route, err)
			}
			if len(idp.calls) != 0 {
				t.Fatalf("identity mutated: %v", idp.calls)
			}
			orgs, err := st.Organizations(ctx)
			if err != nil || len(orgs) != 1 || orgs[0].ID != tenant {
				t.Fatalf("projection changed: %v %v", orgs, err)
			}
			grants, err := st.Grants(ctx, tenant)
			if err != nil || len(grants) != 0 {
				t.Fatalf("grant created: %v %v", grants, err)
			}
		})
	}
}
