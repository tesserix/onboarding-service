// Package hospitalclaims accesses only private runtime organization claims.
package hospitalclaims

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var resource = schema.GroupVersionResource{Group: "identity.tesserix.app", Version: "v1alpha1", Resource: "zitadelorganizations"}
var ErrConflict = errors.New("hospital identity binding conflicts")

type Spec = identity.ZitadelOrganizationSpec

type Result struct {
	Spec   Spec `json:"spec"`
	Status struct {
		Activated          bool               `json:"activated"`
		OrganizationID     string             `json:"organizationId"`
		ProjectID          string             `json:"projectId"`
		ArchiveObject      string             `json:"archiveObject"`
		ArchiveGeneration  string             `json:"archiveGeneration"`
		ObservedGeneration int64              `json:"observedGeneration"`
		Conditions         []metav1.Condition `json:"conditions"`
	} `json:"status"`
	Generation      int64  `json:"-"`
	ResourceVersion string `json:"-"`
	SecurityRequest string `json:"-"`
}
type Client struct{ resource dynamic.ResourceInterface }

func New(kube dynamic.Interface, namespace string) *Client {
	return &Client{resource: kube.Resource(resource).Namespace(namespace)}
}
func (c *Client) Create(ctx context.Context, spec Spec) (Result, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return Result{}, err
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		return Result{}, err
	}
	claim := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "identity.tesserix.app/v1alpha1", "kind": "ZitadelOrganization", "metadata": map[string]any{"name": spec.Product + "-" + spec.TenantID}, "spec": fields}}
	if spec.InitialAdmin != nil {
		preview, err := c.resource.Create(ctx, claim, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return Result{}, err
		}
		if err == nil {
			accepted, err := decode(preview)
			if err != nil {
				return Result{}, err
			}
			if !reflect.DeepEqual(accepted.Spec, spec) {
				return Result{}, ErrConflict
			}
		}
	}
	obj, err := c.resource.Create(ctx, claim, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		result, e := c.Get(ctx, spec.Product, spec.TenantID)
		if e != nil {
			return Result{}, e
		}
		if !reflect.DeepEqual(result.Spec, spec) {
			return Result{}, ErrConflict
		}
		return result, nil
	}
	if err != nil {
		return Result{}, err
	}
	accepted, err := decode(obj)
	if err != nil {
		return Result{}, err
	}
	if !reflect.DeepEqual(accepted.Spec, spec) {
		return Result{}, ErrConflict
	}
	return accepted, nil
}
func (c *Client) Get(ctx context.Context, product, tenant string) (Result, error) {
	obj, e := c.resource.Get(ctx, product+"-"+tenant, metav1.GetOptions{})
	if e != nil {
		return Result{}, e
	}
	return decode(obj)
}
func decode(obj *unstructured.Unstructured) (Result, error) {
	var result Result
	raw, e := json.Marshal(obj.Object)
	if e != nil {
		return result, e
	}
	e = json.Unmarshal(raw, &result)
	result.Generation = obj.GetGeneration()
	result.ResourceVersion = obj.GetResourceVersion()
	result.SecurityRequest = obj.GetAnnotations()[SecurityReconcileAnnotation]
	return result, e
}

const SecurityReconcileAnnotation = "identity.tesserix.app/reconcile-request"

// RequestSecurityReconcile wakes the existing policy owner without changing its immutable spec.
func (c *Client) RequestSecurityReconcile(ctx context.Context, before Result, requestID string) (Result, error) {
	id, err := uuid.Parse(requestID)
	if err != nil || id == uuid.Nil || id.String() != requestID || before.Spec.Product != "helivanta" || before.Spec.PreserveLoginPolicy || before.ResourceVersion == "" {
		return Result{}, ErrConflict
	}
	if before.SecurityRequest == requestID {
		return before, nil
	}
	current, err := c.resource.Get(ctx, before.Spec.Product+"-"+before.Spec.TenantID, metav1.GetOptions{})
	if err != nil {
		return Result{}, err
	}
	if current.GetResourceVersion() != before.ResourceVersion {
		return Result{}, ErrConflict
	}
	annotations := current.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[SecurityReconcileAnnotation] = requestID
	patch, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/resourceVersion", "value": before.ResourceVersion}, {"op": "add", "path": "/metadata/annotations", "value": annotations}})
	if err != nil {
		return Result{}, err
	}
	obj, err := c.resource.Patch(ctx, current.GetName(), types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return Result{}, err
	}
	return decode(obj)
}
