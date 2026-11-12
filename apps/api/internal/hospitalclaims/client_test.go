package hospitalclaims_test

import (
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"reflect"
	"strings"

	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
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

func TestFirstOwnerProfilePersistsAndCannotBeReplacedByReplay(t *testing.T) {
	client := hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")
	var spec hospitalclaims.Spec
	input := `{"product":"helivanta","tenantId":"11111111-1111-4111-8111-111111111111","displayName":"Hospital","initialAdminUserId":"owner-11111111-1111-4111-8111-111111111111","initialAdmin":{"givenName":"Test","familyName":"Owner","email":"owner@example.test"}}`
	if err := json.Unmarshal([]byte(input), &spec); err != nil {
		t.Fatal(err)
	}
	result, err := client.Create(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"email":"owner@example.test"`) {
		t.Fatal("first owner profile was discarded before native identity provisioning")
	}
	if _, err = client.Create(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(strings.ReplaceAll(input, "owner@example.test", "changed@example.test")), &spec); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Create(t.Context(), spec); err == nil {
		t.Fatal("replay changed first owner profile")
	}
}

func TestCreateRejectsServerPruningOfFirstOwnerBinding(t *testing.T) {
	kube := fake.NewSimpleDynamicClient(runtime.NewScheme())
	writes := 0
	kube.PrependReactor("create", "zitadelorganizations", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		if len(action.(ktesting.CreateActionImpl).GetCreateOptions().DryRun) == 0 {
			writes++
		}
		object := create.GetObject().(*unstructured.Unstructured).DeepCopy()
		unstructured.RemoveNestedField(object.Object, "spec", "initialAdmin")
		return true, object, nil
	})
	client := hospitalclaims.New(kube, "identity-operator")
	var spec hospitalclaims.Spec
	if err := json.Unmarshal([]byte(`{"product":"helivanta","tenantId":"11111111-1111-4111-8111-111111111111","displayName":"Hospital","initialAdminUserId":"owner-11111111-1111-4111-8111-111111111111","initialAdmin":{"givenName":"Test","familyName":"Owner","email":"owner@example.test"}}`), &spec); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Create(t.Context(), spec); err == nil {
		t.Fatal("an API server with an outdated schema silently discarded the approved owner")
	}
	if writes != 0 {
		t.Fatalf("schema mismatch wrote an incomplete immutable claim: %d", writes)
	}
}

func TestSecurityReconcilePreservesSpecAndFencesConcurrentChanges(t *testing.T) {
	kube := fake.NewSimpleDynamicClient(runtime.NewScheme())
	client := hospitalclaims.New(kube, "identity-operator")
	var spec hospitalclaims.Spec
	if err := json.Unmarshal([]byte(`{"product":"helivanta","tenantId":"11111111-1111-4111-8111-111111111111","displayName":"Hospital","initialAdminUserId":"owner"}`), &spec); err != nil {
		t.Fatal(err)
	}
	_, err := client.Create(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	// Fake clients do not assign resource versions; use a real-looking captured version.
	object, err := kube.Resource(schema.GroupVersionResource{Group: "identity.tesserix.app", Version: "v1alpha1", Resource: "zitadelorganizations"}).Namespace("identity-operator").Get(t.Context(), "helivanta-"+spec.TenantID, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	object.SetResourceVersion("7")
	if _, err = kube.Resource(schema.GroupVersionResource{Group: "identity.tesserix.app", Version: "v1alpha1", Resource: "zitadelorganizations"}).Namespace("identity-operator").Update(t.Context(), object, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := client.Get(t.Context(), "helivanta", spec.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := client.RequestSecurityReconcile(t.Context(), before, "11111111-1111-4111-8111-111111111112")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Spec, after.Spec) || after.SecurityRequest != "11111111-1111-4111-8111-111111111112" {
		t.Fatal("reconcile mutated immutable identity binding")
	}
	if _, err = client.RequestSecurityReconcile(t.Context(), after, after.SecurityRequest); err != nil {
		t.Fatal(err)
	}
	before.Spec.PreserveLoginPolicy = true
	if _, err = client.RequestSecurityReconcile(t.Context(), before, "11111111-1111-4111-8111-111111111113"); err == nil {
		t.Fatal("legacy policy cutover accepted")
	}
}
