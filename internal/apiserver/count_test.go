// nolint:goconst
package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiextv1 "github.com/harikube/api-extension/api/v1"
	etcdserverpb "go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/restmapper"
)

func TestCountListHandlerRejectsMissingAPIVersion(t *testing.T) {
	handler := getCountHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, nil, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodGet, "/counts?fieldSelector=kind=Pod", nil)
	rec := httptest.NewRecorder()

	handler.CustomResource.ListHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "missing apiVersion") {
		t.Fatalf("expected body to contain 'missing apiVersion', got %q", body)
	}
}

func TestCountListHandlerRejectsMissingKind(t *testing.T) {
	handler := getCountHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, nil, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodGet, "/counts?fieldSelector=apiVersion=v1", nil)
	rec := httptest.NewRecorder()

	handler.CustomResource.ListHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "missing kind") {
		t.Fatalf("expected body to contain 'missing kind', got %q", body)
	}
}

func TestCountListHandlerReturnsForbiddenWhenUnauthorized(t *testing.T) {
	originalSAR := countSubjectAccessReview
	originalGetResource := countGetResource
	originalCountGet := countGet
	t.Cleanup(func() {
		countSubjectAccessReview = originalSAR
		countGetResource = originalGetResource
		countGet = originalCountGet
	})

	countSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: false}}, nil
	}
	countGetResource = func(_ schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		t.Fatal("countGetResource should not be called")
		return nil, nil
	}
	countGet = func(_ context.Context, _ *clientv3.Client, _ string, _ ...clientv3.OpOption) (*clientv3.GetResponse, error) {
		t.Fatal("countGet should not be called")
		return nil, nil
	}

	handler := getCountHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, nil, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodGet, "/counts?fieldSelector=apiVersion=v1,kind=Pod", nil)
	rec := httptest.NewRecorder()

	handler.CustomResource.ListHandler("default", "", rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestCountListHandlerGroupReturnsCountResponseList(t *testing.T) {
	originalSAR := countSubjectAccessReview
	originalGetResource := countGetResource
	originalCountGet := countGet
	t.Cleanup(func() {
		countSubjectAccessReview = originalSAR
		countGetResource = originalGetResource
		countGet = originalCountGet
	})

	var gotResourceAttributes authorizationv1.ResourceAttributes
	var gotPrefix string

	countSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, resourceAttributes *authorizationv1.ResourceAttributes, headers http.Header) (*authorizationv1.SubjectAccessReview, error) {
		if headers.Get("X-Remote-User") != "alice" {
			t.Fatalf("unexpected remote user: %q", headers.Get("X-Remote-User"))
		}
		gotResourceAttributes = *resourceAttributes

		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	countGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		wantGVK := schema.GroupVersionKind{Group: "apps", Kind: "Pod"}
		if gvk != wantGVK {
			t.Fatalf("gvk = %#v, want %#v", gvk, wantGVK)
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Group: "apps", Resource: "pods"},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}
	countGet = func(_ context.Context, _ *clientv3.Client, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
		gotPrefix = key
		if len(opts) != 4 {
			t.Fatalf("len(opts) = %d, want %d", len(opts), 4)
		}

		return &clientv3.GetResponse{Header: &etcdserverpb.ResponseHeader{Revision: 11}, Count: 3}, nil
	}

	handler := getCountHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodGet, "/counts?fieldSelector=apiVersion=apps,kind=Pod,metadata.name=demo&labelSelector=app%3Ddemo", nil)
	req.Header.Set("X-Remote-User", "alice")
	rec := httptest.NewRecorder()

	handler.CustomResource.ListHandler("default", "", rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want %q", got, "application/json")
	}
	if gotPrefix != "/registry/apps/pods/default/" {
		t.Fatalf("prefix = %q, want %q", gotPrefix, "/registry/pods/default/")
	}
	if gotResourceAttributes.Namespace != "default" || gotResourceAttributes.Verb != "list" || gotResourceAttributes.Group != "apps" || gotResourceAttributes.Version != "" || gotResourceAttributes.Resource != "pods" {
		t.Fatalf("resourceAttributes = %#v", gotResourceAttributes)
	}

	var resp apiextv1.CountResponseList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if resp.APIVersion != apiextv1.SchemeBuilder.GroupVersion.String() {
		t.Fatalf("apiVersion = %q, want %q", resp.APIVersion, apiextv1.SchemeBuilder.GroupVersion.String())
	}
	if resp.Kind != "CountResponseList" {
		t.Fatalf("kind = %q, want %q", resp.Kind, "CountResponseList")
	}
	if resp.ResourceVersion != "11" {
		t.Fatalf("resourceVersion = %q, want %q", resp.ResourceVersion, "11")
	}
	if len(resp.Items) != 1 {
		t.Fatalf("len(items) = %d, want %d", len(resp.Items), 1)
	}

	item := resp.Items[0]
	if item.Name != "Pod" {
		t.Fatalf("item name = %q, want %q", item.Name, "Pod")
	}
	if item.Namespace != "default" {
		t.Fatalf("item namespace = %q, want %q", item.Namespace, "default")
	}
	if item.ResourceVersion != "11" {
		t.Fatalf("item resourceVersion = %q, want %q", item.ResourceVersion, "11")
	}
	if item.Spec.Count != 3 {
		t.Fatalf("item count = %d, want %d", item.Spec.Count, 3)
	}
	if item.CreationTimestamp == (metav1.Time{}) {
		t.Fatal("creationTimestamp was not set")
	}
}

func TestCountListHandlerVersionReturnsCountResponseList(t *testing.T) {
	originalSAR := countSubjectAccessReview
	originalGetResource := countGetResource
	originalCountGet := countGet
	t.Cleanup(func() {
		countSubjectAccessReview = originalSAR
		countGetResource = originalGetResource
		countGet = originalCountGet
	})

	var gotResourceAttributes authorizationv1.ResourceAttributes
	var gotPrefix string

	countSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, resourceAttributes *authorizationv1.ResourceAttributes, headers http.Header) (*authorizationv1.SubjectAccessReview, error) {
		if headers.Get("X-Remote-User") != "alice" {
			t.Fatalf("unexpected remote user: %q", headers.Get("X-Remote-User"))
		}
		gotResourceAttributes = *resourceAttributes

		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	countGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		wantGVK := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
		if gvk != wantGVK {
			t.Fatalf("gvk = %#v, want %#v", gvk, wantGVK)
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}
	countGet = func(_ context.Context, _ *clientv3.Client, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
		gotPrefix = key
		if len(opts) != 4 {
			t.Fatalf("len(opts) = %d, want %d", len(opts), 4)
		}

		return &clientv3.GetResponse{Header: &etcdserverpb.ResponseHeader{Revision: 11}, Count: 3}, nil
	}

	handler := getCountHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodGet, "/counts?fieldSelector=apiVersion=/v1,kind=Pod,metadata.name=demo&labelSelector=app%3Ddemo", nil)
	req.Header.Set("X-Remote-User", "alice")
	rec := httptest.NewRecorder()

	handler.CustomResource.ListHandler("default", "", rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want %q", got, "application/json")
	}
	if gotPrefix != "/registry/pods/default/" {
		t.Fatalf("prefix = %q, want %q", gotPrefix, "/registry/pods/default/")
	}
	if gotResourceAttributes.Namespace != "default" || gotResourceAttributes.Verb != "list" || gotResourceAttributes.Group != "" || gotResourceAttributes.Version != "v1" || gotResourceAttributes.Resource != "pods" {
		t.Fatalf("resourceAttributes = %#v", gotResourceAttributes)
	}

	var resp apiextv1.CountResponseList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if resp.APIVersion != apiextv1.SchemeBuilder.GroupVersion.String() {
		t.Fatalf("apiVersion = %q, want %q", resp.APIVersion, apiextv1.SchemeBuilder.GroupVersion.String())
	}
	if resp.Kind != "CountResponseList" {
		t.Fatalf("kind = %q, want %q", resp.Kind, "CountResponseList")
	}
	if resp.ResourceVersion != "11" {
		t.Fatalf("resourceVersion = %q, want %q", resp.ResourceVersion, "11")
	}
	if len(resp.Items) != 1 {
		t.Fatalf("len(items) = %d, want %d", len(resp.Items), 1)
	}

	item := resp.Items[0]
	if item.Name != "Pod" {
		t.Fatalf("item name = %q, want %q", item.Name, "Pod")
	}
	if item.Namespace != "default" {
		t.Fatalf("item namespace = %q, want %q", item.Namespace, "default")
	}
	if item.ResourceVersion != "11" {
		t.Fatalf("item resourceVersion = %q, want %q", item.ResourceVersion, "11")
	}
	if item.Spec.Count != 3 {
		t.Fatalf("item count = %d, want %d", item.Spec.Count, 3)
	}
	if item.CreationTimestamp == (metav1.Time{}) {
		t.Fatal("creationTimestamp was not set")
	}
}

func TestCountListHandlerGroupVersionReturnsCountResponseList(t *testing.T) {
	originalSAR := countSubjectAccessReview
	originalGetResource := countGetResource
	originalCountGet := countGet
	t.Cleanup(func() {
		countSubjectAccessReview = originalSAR
		countGetResource = originalGetResource
		countGet = originalCountGet
	})

	var gotResourceAttributes authorizationv1.ResourceAttributes
	var gotPrefix string

	countSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, resourceAttributes *authorizationv1.ResourceAttributes, headers http.Header) (*authorizationv1.SubjectAccessReview, error) {
		if headers.Get("X-Remote-User") != "alice" {
			t.Fatalf("unexpected remote user: %q", headers.Get("X-Remote-User"))
		}
		gotResourceAttributes = *resourceAttributes

		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	countGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		wantGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Pod"}
		if gvk != wantGVK {
			t.Fatalf("gvk = %#v, want %#v", gvk, wantGVK)
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "pods"},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}
	countGet = func(_ context.Context, _ *clientv3.Client, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
		gotPrefix = key
		if len(opts) != 4 {
			t.Fatalf("len(opts) = %d, want %d", len(opts), 4)
		}

		return &clientv3.GetResponse{Header: &etcdserverpb.ResponseHeader{Revision: 11}, Count: 3}, nil
	}

	handler := getCountHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodGet, "/counts?fieldSelector=apiVersion=apps/v1,kind=Pod,metadata.name=demo&labelSelector=app%3Ddemo", nil)
	req.Header.Set("X-Remote-User", "alice")
	rec := httptest.NewRecorder()

	handler.CustomResource.ListHandler("default", "", rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want %q", got, "application/json")
	}
	if gotPrefix != "/registry/apps/pods/default/" {
		t.Fatalf("prefix = %q, want %q", gotPrefix, "/registry/pods/default/")
	}
	if gotResourceAttributes.Namespace != "default" || gotResourceAttributes.Verb != "list" || gotResourceAttributes.Group != "apps" || gotResourceAttributes.Version != "v1" || gotResourceAttributes.Resource != "pods" {
		t.Fatalf("resourceAttributes = %#v", gotResourceAttributes)
	}

	var resp apiextv1.CountResponseList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if resp.APIVersion != apiextv1.SchemeBuilder.GroupVersion.String() {
		t.Fatalf("apiVersion = %q, want %q", resp.APIVersion, apiextv1.SchemeBuilder.GroupVersion.String())
	}
	if resp.Kind != "CountResponseList" {
		t.Fatalf("kind = %q, want %q", resp.Kind, "CountResponseList")
	}
	if resp.ResourceVersion != "11" {
		t.Fatalf("resourceVersion = %q, want %q", resp.ResourceVersion, "11")
	}
	if len(resp.Items) != 1 {
		t.Fatalf("len(items) = %d, want %d", len(resp.Items), 1)
	}

	item := resp.Items[0]
	if item.Name != "Pod" {
		t.Fatalf("item name = %q, want %q", item.Name, "Pod")
	}
	if item.Namespace != "default" {
		t.Fatalf("item namespace = %q, want %q", item.Namespace, "default")
	}
	if item.ResourceVersion != "11" {
		t.Fatalf("item resourceVersion = %q, want %q", item.ResourceVersion, "11")
	}
	if item.Spec.Count != 3 {
		t.Fatalf("item count = %d, want %d", item.Spec.Count, 3)
	}
	if item.CreationTimestamp == (metav1.Time{}) {
		t.Fatal("creationTimestamp was not set")
	}
}

func TestCountListHandlerClearsNamespaceForClusterScopedResources(t *testing.T) {
	originalSAR := countSubjectAccessReview
	originalGetResource := countGetResource
	originalCountGet := countGet
	t.Cleanup(func() {
		countSubjectAccessReview = originalSAR
		countGetResource = originalGetResource
		countGet = originalCountGet
	})

	var gotPrefix string

	countSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	countGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		wantGVK := schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}
		if gvk != wantGVK {
			t.Fatalf("gvk = %#v, want %#v", gvk, wantGVK)
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Group: wantGVK.Group, Version: wantGVK.Version, Resource: "clusterroles"},
			Scope:    meta.RESTScopeRoot,
		}, nil
	}
	countGet = func(_ context.Context, _ *clientv3.Client, key string, _ ...clientv3.OpOption) (*clientv3.GetResponse, error) {
		gotPrefix = key
		return &clientv3.GetResponse{Header: &etcdserverpb.ResponseHeader{Revision: 17}, Count: 5}, nil
	}

	handler := getCountHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodGet, "/counts?fieldSelector=apiVersion=rbac.authorization.k8s.io/v1,kind=ClusterRole", nil)
	rec := httptest.NewRecorder()

	handler.CustomResource.ListHandler("default", "", rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotPrefix != "/registry/rbac.authorization.k8s.io/clusterroles/" {
		t.Fatalf("prefix = %q, want %q", gotPrefix, "/registry/rbac.authorization.k8s.io/clusterroles/")
	}

	var resp apiextv1.CountResponseList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("len(items) = %d, want %d", len(resp.Items), 1)
	}
	if resp.Items[0].Namespace != "" {
		t.Fatalf("item namespace = %q, want empty", resp.Items[0].Namespace)
	}
	if resp.Items[0].Spec.Count != 5 {
		t.Fatalf("item count = %d, want %d", resp.Items[0].Spec.Count, 5)
	}
}
