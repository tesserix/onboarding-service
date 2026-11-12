package onboarding

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestVerifiedCustomerWakesPendingIdentityWithoutDeclaringReadiness(t *testing.T) {
	for _, kind := range []customercontract.Kind{customercontract.Hospital, customercontract.Pharmacy} {
		t.Run(string(kind), func(t *testing.T) {
			svc, kube, row, name := pendingCustomerReconcile(t, kind)
			state, code := svc.deliverCustomer(t.Context(), row)
			if state != "identity_requested" || code != "" {
				t.Fatalf("verification fabricated readiness: state=%s code=%s", state, code)
			}
			resource := schema.GroupVersionResource{Group: "identity.tesserix.app", Version: "v1alpha1", Resource: "zitadelorganizations"}
			current, err := kube.Resource(resource).Namespace("identity-operator").Get(t.Context(), name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if current.GetAnnotations()[hospitalclaims.SecurityReconcileAnnotation] != row.ID.String() {
				t.Fatal("verified owner did not wake the pending identity reconciler")
			}
		})
	}
}

func pendingCustomerReconcile(t *testing.T, kind customercontract.Kind) (*Service, *fake.FakeDynamicClient, store.CustomerSignup, string) {
	t.Helper()
	id := uuid.New()
	tenant := customerTenantID(id.String())
	row := store.CustomerSignup{
		ID: id, TenantID: tenant, ResourceID: uuid.Must(uuid.NewV7()),
		OwnerSubject: "owner-" + tenant.String(), State: "identity_requested", ReceiptVerified: true,
		Input: customercontract.Signup{
			Setup: customercontract.Setup{Name: "Test Workspace", Kind: kind, CountryCode: customercontract.India, StateCode: customercontract.Maharashtra},
			Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: "owner@example.test"},
		},
	}
	spec, err := json.Marshal(hospitalclaims.Spec{Product: "helivanta", TenantID: tenant.String(), DisplayName: row.Input.Name, InitialAdminUserID: row.OwnerSubject, InitialAdmin: row.Input.Owner})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(spec, &fields); err != nil {
		t.Fatal(err)
	}
	claim := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "identity.tesserix.app/v1alpha1", "kind": "ZitadelOrganization",
		"metadata": map[string]any{"name": "helivanta-" + tenant.String(), "namespace": "identity-operator", "resourceVersion": "7", "generation": int64(1)},
		"spec":     fields,
	}}
	kube := fake.NewSimpleDynamicClient(runtime.NewScheme(), claim)
	svc := &Service{hospitals: &HospitalConfig{Product: "helivanta", Claims: hospitalclaims.New(kube, "identity-operator")}}
	return svc, kube, row, claim.GetName()
}

func TestUnverifiedCustomerDoesNotWakeIdentity(t *testing.T) {
	svc, kube, row, _ := pendingCustomerReconcile(t, customercontract.Hospital)
	row.ReceiptVerified = false
	state, code := svc.deliverCustomer(t.Context(), row)
	if state != "identity_requested" || code != "" {
		t.Fatalf("state=%s code=%s", state, code)
	}
	for _, action := range kube.Actions() {
		if action.GetVerb() == "patch" {
			t.Fatal("unverified owner woke identity reconciliation")
		}
	}
}

func TestExistingOwnerContinuationDoesNotWakeFreshOwnerVerification(t *testing.T) {
	svc, kube, row, name := pendingCustomerReconcile(t, customercontract.Hospital)
	row.ExistingOwnerSubject = "existing-owner"
	resource := schema.GroupVersionResource{Group: "identity.tesserix.app", Version: "v1alpha1", Resource: "zitadelorganizations"}
	claim, err := kube.Resource(resource).Namespace("identity-operator").Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(claim.Object, row.ExistingOwnerSubject, "spec", "initialAdminUserId"); err != nil {
		t.Fatal(err)
	}
	unstructured.RemoveNestedField(claim.Object, "spec", "initialAdmin")
	if _, err = kube.Resource(resource).Namespace("identity-operator").Update(t.Context(), claim, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	state, code := svc.deliverCustomer(t.Context(), row)
	if state != "identity_requested" || code != "" {
		t.Fatalf("existing owner changed: state=%s code=%s", state, code)
	}
	for _, action := range kube.Actions() {
		if action.GetVerb() == "patch" {
			t.Fatal("existing owner continuation was modified")
		}
	}
}

func TestVerifiedCustomerWakeupIsIdempotent(t *testing.T) {
	svc, kube, row, _ := pendingCustomerReconcile(t, customercontract.Pharmacy)
	for range 2 {
		state, code := svc.deliverCustomer(t.Context(), row)
		if state != "identity_requested" || code != "" {
			t.Fatalf("state=%s code=%s", state, code)
		}
	}
	patches := 0
	for _, action := range kube.Actions() {
		if action.GetVerb() == "patch" {
			patches++
		}
	}
	if patches != 1 {
		t.Fatalf("replayed delivery wrote %d wakeups, want one", patches)
	}
}

func TestVerifiedCustomerWakeupRetriesDependencyFailure(t *testing.T) {
	svc, kube, row, _ := pendingCustomerReconcile(t, customercontract.Pharmacy)
	failed := false
	kube.PrependReactor("patch", "zitadelorganizations", func(ktesting.Action) (bool, runtime.Object, error) {
		if !failed {
			failed = true
			return true, nil, errors.New("Kubernetes unavailable")
		}
		return false, nil, nil
	})
	state, code := svc.deliverCustomer(t.Context(), row)
	if state != "identity_requested" || code != "identity_unavailable" {
		t.Fatalf("failure lost pending state: state=%s code=%s", state, code)
	}
	state, code = svc.deliverCustomer(t.Context(), row)
	if state != "identity_requested" || code != "" {
		t.Fatalf("retry failed: state=%s code=%s", state, code)
	}
}

func TestVerifiedCustomerCannotWakeForeignOwnerClaim(t *testing.T) {
	svc, kube, row, name := pendingCustomerReconcile(t, customercontract.Hospital)
	resource := schema.GroupVersionResource{Group: "identity.tesserix.app", Version: "v1alpha1", Resource: "zitadelorganizations"}
	claim, err := kube.Resource(resource).Namespace("identity-operator").Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(claim.Object, "foreign-owner", "spec", "initialAdminUserId"); err != nil {
		t.Fatal(err)
	}
	if _, err = kube.Resource(resource).Namespace("identity-operator").Update(t.Context(), claim, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	state, code := svc.deliverCustomer(t.Context(), row)
	if state != "needs_support" || code != "" {
		t.Fatalf("foreign owner accepted: state=%s code=%s", state, code)
	}
	for _, action := range kube.Actions() {
		if action.GetVerb() == "patch" {
			t.Fatal("foreign owner claim was modified")
		}
	}
}
