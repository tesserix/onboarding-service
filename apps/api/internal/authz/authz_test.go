package authz

import (
	"context"
	"errors"
	"testing"
)

// entitlements is the org_products join, in memory.
type entitlements map[string]map[string]bool

func (e entitlements) Entitled(_ context.Context, orgID, productKey string) (bool, error) {
	return e[orgID][productKey], nil
}

var apolloUsesHMS = entitlements{"apollo": {"hms": true}, "acme": {"mark8ly": true}}

func platform(roles ...string) Principal {
	return Principal{Kind: KindPlatform, Subject: "sam@tesserix.app", Roles: roles}
}

func product(key string) Principal {
	return Principal{Kind: KindProduct, Subject: "svc-" + key, ProductKey: key}
}

func orgAdmin(orgID string) Principal {
	return Principal{Kind: KindOrgAdmin, Subject: "admin@apollo.com", OrgID: orgID}
}

func TestPlatformAdminMayDoAnything(t *testing.T) {
	a := New(apolloUsesHMS)
	for _, action := range AllActions() {
		if err := a.Authorize(context.Background(), platform(RolePlatformAdmin), action, "apollo"); err != nil {
			t.Errorf("platform-admin denied %s: %v", action, err)
		}
	}
}

func TestPlatformOperatorIsReadOnly(t *testing.T) {
	a := New(apolloUsesHMS)
	if err := a.Authorize(context.Background(), platform(RolePlatformOperator), ActionOrgRead, "apollo"); err != nil {
		t.Fatalf("operator denied a read: %v", err)
	}
	if err := a.Authorize(context.Background(), platform(RolePlatformOperator), ActionOrgWrite, "apollo"); !errors.Is(err, ErrDenied) {
		t.Fatalf("operator allowed a write: %v", err)
	}
}

func TestPlatformPrincipalWithNoRoleIsDenied(t *testing.T) {
	a := New(apolloUsesHMS)
	if err := a.Authorize(context.Background(), platform(), ActionOrgRead, "apollo"); !errors.Is(err, ErrDenied) {
		t.Fatalf("roleless platform principal allowed: %v", err)
	}
}

// The rule the whole service exists to enforce.
func TestProductCannotTouchAnotherProductsCustomer(t *testing.T) {
	a := New(apolloUsesHMS)
	err := a.Authorize(context.Background(), product("hms"), ActionOrgRead, "acme")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("hms allowed to read mark8ly's customer: %v", err)
	}
}

func TestProductMayActOnItsOwnCustomer(t *testing.T) {
	a := New(apolloUsesHMS)
	for _, action := range []Action{ActionOrgRead, ActionOrgWrite, ActionDomainWrite, ActionIDPWrite, ActionMemberWrite, ActionAuditRead} {
		if err := a.Authorize(context.Background(), product("hms"), action, "apollo"); err != nil {
			t.Errorf("hms denied %s on its own customer: %v", action, err)
		}
	}
}

func TestProductMayCreateAnOrgWithoutAnExistingEntitlement(t *testing.T) {
	a := New(apolloUsesHMS)
	if err := a.Authorize(context.Background(), product("hms"), ActionOrgCreate, ""); err != nil {
		t.Fatalf("product denied org creation: %v", err)
	}
}

func TestProductMayNotDeactivateOrManageProducts(t *testing.T) {
	a := New(apolloUsesHMS)
	for _, action := range []Action{ActionOrgDeactivate, ActionProductWrite} {
		if err := a.Authorize(context.Background(), product("hms"), action, "apollo"); !errors.Is(err, ErrDenied) {
			t.Errorf("product allowed %s: %v", action, err)
		}
	}
}

func TestOrgAdminIsConfinedToTheirOwnOrg(t *testing.T) {
	a := New(apolloUsesHMS)
	if err := a.Authorize(context.Background(), orgAdmin("apollo"), ActionIDPWrite, "apollo"); err != nil {
		t.Fatalf("org admin denied on their own org: %v", err)
	}
	if err := a.Authorize(context.Background(), orgAdmin("apollo"), ActionIDPWrite, "acme"); !errors.Is(err, ErrDenied) {
		t.Fatal("org admin allowed on another org")
	}
}

func TestOrgAdminMayNotManageGrants(t *testing.T) {
	a := New(apolloUsesHMS)
	if err := a.Authorize(context.Background(), orgAdmin("apollo"), ActionGrantWrite, "apollo"); !errors.Is(err, ErrDenied) {
		t.Fatal("org admin allowed to grant themselves a product")
	}
}

func TestOrgScopedActionRequiresAnOrgID(t *testing.T) {
	a := New(apolloUsesHMS)
	if err := a.Authorize(context.Background(), product("hms"), ActionOrgRead, ""); !errors.Is(err, ErrDenied) {
		t.Fatal("org-scoped action allowed with an empty org id")
	}
}

func TestUnknownActionIsDenied(t *testing.T) {
	a := New(apolloUsesHMS)
	if err := a.Authorize(context.Background(), platform(RolePlatformAdmin), Action("org.nuke"), "apollo"); !errors.Is(err, ErrDenied) {
		t.Fatal("unknown action allowed — policy must fail closed")
	}
}

func TestAnEntitlementLookupFailureDeniesRatherThanAllows(t *testing.T) {
	a := New(brokenEntitlements{})
	err := a.Authorize(context.Background(), product("hms"), ActionOrgRead, "apollo")
	if errors.Is(err, ErrDenied) {
		t.Fatal("lookup failure reported as a clean denial; it must surface as an error")
	}
	if err == nil {
		t.Fatal("lookup failure allowed the request")
	}
}

type brokenEntitlements struct{}

func (brokenEntitlements) Entitled(context.Context, string, string) (bool, error) {
	return false, errors.New("database is down")
}
