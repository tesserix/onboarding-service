package hospitalclaims_test

import (
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"testing"
)

func TestClaimReplayCannotReplaceTenantAdministrator(t *testing.T) {
	k := fake.NewSimpleDynamicClient(runtime.NewScheme())
	c := hospitalclaims.New(k, "identity-operator")
	spec := hospitalclaims.Spec{Product: "helivanta", TenantID: "8f8ed375-596b-4633-b90b-27fd3e6f299c", DisplayName: "TEST Hospital", InitialAdminUserID: "admin"}
	for range 2 {
		if _, e := c.Create(t.Context(), spec); e != nil {
			t.Fatal(e)
		}
	}
	spec.InitialAdminUserID = "different"
	if _, e := c.Create(t.Context(), spec); e == nil {
		t.Fatal("replay replaced immutable admin")
	}
	result, e := c.Get(t.Context(), "helivanta", spec.TenantID)
	if e != nil || result.Spec.InitialAdminUserID != "admin" {
		t.Fatalf("binding lost: %+v %v", result, e)
	}
}
