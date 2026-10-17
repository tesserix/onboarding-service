//go:build identityintegration

package zitadel

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func liveIdentityFailure(t *testing.T, stage string, err error) {
	t.Helper()
	var api *APIError
	if errors.As(err, &api) {
		t.Fatalf("local identity fixture %s failed with HTTP %d", stage, api.Status)
	}
	t.Fatalf("local identity fixture %s failed (%T)", stage, err)
}

func TestLiveOrganizationIdentityAgainstIsolatedLocalZitadelFixtures(t *testing.T) {
	base := os.Getenv("ONBOARDING_LIVE_IDENTITY_URL")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Hostname() != "auth.tesserix.localhost" || u.Port() != "20080" || u.Path != "" || u.User != nil || u.RawQuery != "" {
		t.Fatal("live identity fixture test requires the exact local-only Zitadel origin")
	}
	raw, err := os.ReadFile(os.Getenv("ONBOARDING_LIVE_IDENTITY_PAT_FILE"))
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		t.Fatal("local fixture credential unavailable")
	}
	token := StaticToken(strings.TrimSpace(string(raw)))
	ctx := context.Background()
	seed := New(base, token, "", nil)
	fixtureOrg := func(name string) string {
		id, e := seed.CreateOrg(ctx, name)
		if e != nil {
			liveIdentityFailure(t, "create organisation", e)
		}
		if !identitySourceID.MatchString(id) {
			t.Fatal("created fixture has no safe source ID")
		}
		var check struct {
			Org struct{ ID, Name string } `json:"org"`
		}
		if e = seed.do(ctx, http.MethodGet, "/admin/v1/orgs/"+id, "", nil, &check); e != nil {
			liveIdentityFailure(t, "verify fixture ownership", e)
		}
		if check.Org.ID != id || check.Org.Name != name {
			t.Fatal("refusing cleanup or use of an organisation not created by this test")
		}
		t.Cleanup(func() {
			if e := seed.RemoveOrg(ctx, id); e != nil {
				t.Error("could not remove owned local organisation fixture")
			}
		})
		return id
	}
	suffix := uuid.NewString()
	platform := fixtureOrg("TEST Identity Platform " + suffix)
	company := fixtureOrg("TEST Identity Company " + suffix)
	client := New(base, token, platform, nil)
	tenant := uuid.NewString()
	if err = client.SetOrgMetadata(ctx, company, "tenant_id", tenant); err != nil {
		liveIdentityFailure(t, "bind canonical tenant", err)
	}
	project, err := client.CreateProject(ctx, "TEST Identity Project "+suffix, []string{"identity-test"})
	if err != nil {
		liveIdentityFailure(t, "create project", err)
	}
	if !identitySourceID.MatchString(project) {
		t.Fatal("created project has no safe ID")
	}
	var checkProject struct {
		Project struct{ ID, Name string } `json:"project"`
	}
	if err = client.do(ctx, http.MethodGet, "/management/v1/projects/"+project, platform, nil, &checkProject); err != nil {
		liveIdentityFailure(t, "verify project ownership", err)
	}
	if checkProject.Project.ID != project || checkProject.Project.Name != "TEST Identity Project "+suffix {
		t.Fatal("refusing cleanup or use of a project not created by this test")
	}
	t.Cleanup(func() {
		if e := client.do(ctx, http.MethodDelete, "/management/v1/projects/"+project, platform, nil, nil); e != nil {
			t.Error("could not remove owned local project fixture")
		}
	})
	grant, err := client.CreateProjectGrant(ctx, project, company, []string{"identity-test"})
	if err != nil {
		liveIdentityFailure(t, "create entitlement", err)
	}
	active := func() OrganizationIdentity {
		t.Helper()
		proof, e := client.VerifyOrganization(ctx, company, tenant, project)
		if e != nil {
			t.Fatalf("live source verification did not establish the test fixture: %v", e)
		}
		if proof.State != "active" || proof.GrantID != grant || proof.TenantID != tenant {
			t.Fatal("wrong live identity observation")
		}
		return proof
	}
	first := active()
	if err = client.SetOrgMetadata(ctx, company, "tenant_id", uuid.NewString()); err != nil {
		liveIdentityFailure(t, "change test binding", err)
	}
	if proof, e := client.VerifyOrganization(ctx, company, tenant, project); e == nil && proof.State == "active" {
		t.Fatal("mismatched live tenant binding was accepted")
	}
	if err = client.SetOrgMetadata(ctx, company, "tenant_id", tenant); err != nil {
		liveIdentityFailure(t, "restore test binding", err)
	}
	if active().SourceRevisions.Metadata == first.SourceRevisions.Metadata {
		t.Fatal("live metadata revision was replaced with cached evidence")
	}
	if err = client.DeactivateOrg(ctx, company); err != nil {
		liveIdentityFailure(t, "deactivate fixture", err)
	}
	if proof, e := client.VerifyOrganization(ctx, company, tenant, project); e == nil && proof.State == "active" {
		t.Fatal("inactive live organisation was accepted")
	}
	if err = client.ReactivateOrg(ctx, company); err != nil {
		liveIdentityFailure(t, "reactivate fixture", err)
	}
	active()
	for _, action := range []string{"_deactivate", "_reactivate"} {
		if err = client.do(ctx, http.MethodPost, "/management/v1/projects/"+project+"/grants/"+grant+"/"+action, platform, map[string]any{}, nil); err != nil {
			liveIdentityFailure(t, "change fixture grant", err)
		}
		if action == "_deactivate" {
			if proof, e := client.VerifyOrganization(ctx, company, tenant, project); e == nil && proof.State == "active" {
				t.Fatal("inactive live grant was accepted")
			}
		} else {
			active()
		}
	}
	if err = client.do(ctx, http.MethodPost, "/management/v1/projects/"+project+"/_deactivate", platform, map[string]any{}, nil); err != nil {
		liveIdentityFailure(t, "deactivate project", err)
	}
	if proof, e := client.VerifyOrganization(ctx, company, tenant, project); e == nil && proof.State == "active" {
		t.Fatal("inactive live project was accepted")
	}
	if err = client.do(ctx, http.MethodPost, "/management/v1/projects/"+project+"/_reactivate", platform, map[string]any{}, nil); err != nil {
		liveIdentityFailure(t, "reactivate project", err)
	}
	active()
	if err = client.do(ctx, http.MethodDelete, "/management/v1/projects/"+project+"/grants/"+grant, platform, nil, nil); err != nil {
		liveIdentityFailure(t, "revoke fixture grant", err)
	}
	if proof, e := client.VerifyOrganization(ctx, company, tenant, project); e == nil && proof.State == "active" {
		t.Fatal("missing live grant was accepted")
	}
}
