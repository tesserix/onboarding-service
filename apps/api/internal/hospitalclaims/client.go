// Package hospitalclaims accesses only private runtime organization claims.
package hospitalclaims

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var resource = schema.GroupVersionResource{Group: "identity.tesserix.app", Version: "v1alpha1", Resource: "zitadelorganizations"}
var ErrConflict = errors.New("hospital identity binding conflicts")

type Spec struct {
	Product             string `json:"product"`
	TenantID            string `json:"tenantId"`
	DisplayName         string `json:"displayName"`
	OrganizationID      string `json:"organizationId,omitempty"`
	PreserveLoginPolicy bool   `json:"preserveLoginPolicy,omitempty"`
	InitialAdminUserID  string `json:"initialAdminUserId"`
}
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
	Generation int64 `json:"-"`
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
	return decode(obj)
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
	return result, e
}
