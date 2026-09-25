// nolint:goconst
package apiserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apiextv1 "github.com/harikube/api-extension/api/v1"
	etcdserverpb "go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.yaml.in/yaml/v2"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

func TestTransactionRequestNamespaceFromPath(t *testing.T) {
	if got := transactionRequestNamespace("/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/transactionrequests"); got != "default" {
		t.Fatalf("transactionRequestNamespace() = %q, want %q", got, "default")
	}
	if got := transactionRequestNamespace("/apis/apiserver.api-extension.harikube.info/v1/transactionrequests"); got != "" {
		t.Fatalf("transactionRequestNamespace() = %q, want empty", got)
	}
}

func TestDecodeTransactionRequestPreservesResources(t *testing.T) {
	body := []byte(`apiVersion: apiserver.api-extension.harikube.info/v1
kind: TransactionRequest
metadata:
  name: update-wallets
  namespace: default
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: wallet-alice
`)

	transaction, err := decodeTransactionRequest(body)
	if err != nil {
		t.Fatalf("decodeTransactionRequest() error = %v", err)
	}
	if transaction.Name != "update-wallets" {
		t.Fatalf("name = %q, want %q", transaction.Name, "update-wallets")
	}
	if transaction.Namespace != "default" {
		t.Fatalf("namespace = %q, want %q", transaction.Namespace, "default")
	}
	if len(transaction.Create) != 1 {
		t.Fatalf("len(create) = %d, want 1", len(transaction.Create))
	}

	var resource map[string]interface{}
	if err := yaml.Unmarshal(transaction.Create[0], &resource); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if resource["kind"] != "ConfigMap" {
		t.Fatalf("resource kind = %v, want %q", resource["kind"], "ConfigMap")
	}
}

func TestTransactionCreateHandlerStoresRequestInEtcd(t *testing.T) {
	body := `apiVersion: apiserver.api-extension.harikube.info/v1
kind: TransactionRequest
metadata:
  name: make-payment-XXX
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-AAA
`

	originalSAR := transactionSubjectAccessReview
	originalGetResource := transactionGetResource
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionGetResource = originalGetResource
		transactionCommit = originalCommit
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}

	transactionGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		if gvk.Kind != "ConfigMap" {
			t.Fatalf("unexpected gvk: %v", gvk)
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}

	var (
		gotNamespace string
		gotName      string
		gotResources map[string][]byte
	)
	transactionCommit = func(_ context.Context, _ *clientv3.Client, namespace, name string, resources map[string][]byte) (*clientv3.TxnResponse, error) {
		gotNamespace = namespace
		gotName = name
		gotResources = resources

		return &clientv3.TxnResponse{
			Succeeded: true,
			Header:    &etcdserverpb.ResponseHeader{Revision: 7},
		}, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("", "", rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/yaml" {
		t.Fatalf("content type = %q, want %q", got, "application/yaml")
	}

	if gotNamespace != "default" {
		t.Fatalf("commit namespace = %q, want %q", gotNamespace, "default")
	}
	if gotName != "make-payment-XXX" {
		t.Fatalf("commit name = %q, want %q", gotName, "make-payment-XXX")
	}

	if len(gotResources) != 1 {
		t.Fatalf("len(resources) = %d, want 1", len(gotResources))
	}
	key := "/registry/configmaps/default/user-payment-AAA#create"
	value, ok := gotResources[key]
	if !ok {
		t.Fatalf("resources missing key %q, got %v", key, gotResources)
	}

	var resource map[string]interface{}
	if err := yaml.Unmarshal(value, &resource); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if resource["apiVersion"] != "v1" {
		t.Fatalf("resource apiVersion = %v, want %q", resource["apiVersion"], "v1")
	}
	if resource["kind"] != "ConfigMap" {
		t.Fatalf("resource kind = %v, want %q", resource["kind"], "ConfigMap")
	}

	var resp apiextv1.TransactionResponse
	if err := yaml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	if resp.Name != "make-payment-XXX" {
		t.Fatalf("response name = %q, want %q", resp.Name, "make-payment-XXX")
	}
	if resp.Namespace != "default" {
		t.Fatalf("response namespace = %q, want %q", resp.Namespace, "default")
	}
	if resp.ResourceVersion != "7" {
		t.Fatalf("response resourceVersion = %q, want %q", resp.ResourceVersion, "7")
	}
	if resp.Spec.Error != "" {
		t.Fatalf("response error = %q, want empty", resp.Spec.Error)
	}
}

func TestTransactionCreateHandlerBuildsResourceMapForAllOperations(t *testing.T) {
	body := `apiVersion: apiserver.api-extension.harikube.info/v1
kind: TransactionRequest
metadata:
  name: make-payment-XXX
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-AAA
  update:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: wallet-AAA
      resourceVersion: "6"
  delete:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: token-BBB
      resourceVersion: "9"
`

	originalSAR := transactionSubjectAccessReview
	originalGetResource := transactionGetResource
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionGetResource = originalGetResource
		transactionCommit = originalCommit
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}

	transactionGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		resource := "configmaps"
		if gvk.Kind == "Secret" {
			resource = "secrets"
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: resource},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}

	var gotResources map[string][]byte
	transactionCommit = func(_ context.Context, _ *clientv3.Client, _, _ string, resources map[string][]byte) (*clientv3.TxnResponse, error) {
		gotResources = resources

		return &clientv3.TxnResponse{
			Succeeded: true,
			Header:    &etcdserverpb.ResponseHeader{Revision: 7},
		}, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusCreated)
	}

	if len(gotResources) != 3 {
		t.Fatalf("len(resources) = %d, want 3", len(gotResources))
	}

	expectedKeys := map[string]bool{
		"/registry/configmaps/default/user-payment-AAA#create": false,
		"/registry/secrets/default/wallet-AAA#update":          false,
		"/registry/secrets/default/token-BBB#delete":           false,
	}
	for k := range gotResources {
		if _, ok := expectedKeys[k]; !ok {
			t.Fatalf("unexpected resource key %q", k)
		}
		expectedKeys[k] = true
	}
	for k, found := range expectedKeys {
		if !found {
			t.Fatalf("missing resource key %q", k)
		}
	}
}

func TestTransactionCreateHandlerRejectsEmptySpec(t *testing.T) {
	originalSAR := transactionSubjectAccessReview
	originalGetResource := transactionGetResource
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionGetResource = originalGetResource
		transactionCommit = originalCommit
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}

	var commitCalled bool
	transactionCommit = func(_ context.Context, _ *clientv3.Client, _, _ string, _ map[string][]byte) (*clientv3.TxnResponse, error) {
		commitCalled = true

		return nil, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader("kind: Transaction\nmetadata:\n  name: missing-spec\nspec: {}\n"))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if commitCalled {
		t.Fatal("transactionCommit should not be called")
	}
}

func TestTransactionCreateHandlerReturnsForbiddenWhenUnauthorized(t *testing.T) {
	originalSAR := transactionSubjectAccessReview
	originalGetResource := transactionGetResource
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionGetResource = originalGetResource
		transactionCommit = originalCommit
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: false}}, nil
	}
	transactionGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}
	transactionCommit = func(_ context.Context, _ *clientv3.Client, _, _ string, _ map[string][]byte) (*clientv3.TxnResponse, error) {
		t.Fatal("transactionCommit should not be called")
		return nil, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader(`apiVersion: apiserver.api-extension.harikube.info/v1
kind: TransactionRequest
metadata:
  name: denied
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-AAA
`))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestTransactionCreateHandlerRejectsInvalidBody(t *testing.T) {
	originalSAR := transactionSubjectAccessReview
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionCommit = originalCommit
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	transactionCommit = func(_ context.Context, _ *clientv3.Client, _, _ string, _ map[string][]byte) (*clientv3.TxnResponse, error) {
		t.Fatal("transactionCommit should not be called")
		return nil, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader(": invalid"))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestTransactionCreateHandlerRejectsResourcesFailingValidation(t *testing.T) {
	body := `apiVersion: apiserver.api-extension.harikube.info/v1
kind: TransactionRequest
metadata:
  name: validation-failure
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-AAA
`

	originalSAR := transactionSubjectAccessReview
	originalGetResource := transactionGetResource
	originalValidateAndDefault := transactionValidateAndDefault
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionGetResource = originalGetResource
		transactionValidateAndDefault = originalValidateAndDefault
		transactionCommit = originalCommit
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	transactionGetResource = func(_ schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}
	transactionValidateAndDefault = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *meta.RESTMapping, operation, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		if operation != "create" {
			t.Fatalf("operation = %q, want %q", operation, "create")
		}
		if namespace != "default" {
			t.Fatalf("namespace = %q, want %q", namespace, "default")
		}
		if obj.GetName() != "user-payment-AAA" {
			t.Fatalf("name = %q, want %q", obj.GetName(), "user-payment-AAA")
		}

		return nil, fmt.Errorf("validation failed")
	}
	transactionCommit = func(_ context.Context, _ *clientv3.Client, _, _ string, _ map[string][]byte) (*clientv3.TxnResponse, error) {
		t.Fatal("transactionCommit should not be called")
		return nil, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "validation failed") {
		t.Fatalf("response body = %q, want validation error", rec.Body.String())
	}
}

func TestTransactionResourceMapStoresDefaultedResource(t *testing.T) {
	originalGetResource := transactionGetResource
	originalValidateAndDefault := transactionValidateAndDefault
	t.Cleanup(func() {
		transactionGetResource = originalGetResource
		transactionValidateAndDefault = originalValidateAndDefault
	})

	transactionGetResource = func(_ schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}

	calls := 0
	transactionValidateAndDefault = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *meta.RESTMapping, operation, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
		calls++
		if operation != "create" {
			t.Fatalf("operation = %q, want %q", operation, "create")
		}
		if namespace != "default" {
			t.Fatalf("namespace = %q, want %q", namespace, "default")
		}

		defaulted := obj.DeepCopy()
		defaulted.SetNamespace(namespace)
		defaulted.SetLabels(map[string]string{"managed-by": "apiserver"})

		return defaulted, nil
	}

	resources := map[string][]byte{}
	entry := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: user-payment-AAA
`)

	if err := transactionResourceMap(context.Background(), &authorizationclientv1.AuthorizationV1Client{}, resources, entry, "default", "create", map[string]bool{"": true}, &restmapper.DeferredDiscoveryRESTMapper{}); err != nil {
		t.Fatalf("transactionResourceMap() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("validation/defaulting calls = %d, want 1", calls)
	}

	value, ok := resources["/registry/configmaps/default/user-payment-AAA#create"]
	if !ok {
		t.Fatalf("resources missing key %q, got %v", "/registry/configmaps/default/user-payment-AAA#create", resources)
	}

	var resource map[string]interface{}
	if err := yaml.Unmarshal(value, &resource); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	metadata, ok := resource["metadata"].(map[interface{}]interface{})
	if !ok {
		t.Fatalf("metadata = %#v, want map", resource["metadata"])
	}
	if metadata["namespace"] != "default" {
		t.Fatalf("metadata.namespace = %v, want %q", metadata["namespace"], "default")
	}
	labels, ok := metadata["labels"].(map[interface{}]interface{})
	if !ok {
		t.Fatalf("metadata.labels = %#v, want map", metadata["labels"])
	}
	if labels["managed-by"] != "apiserver" {
		t.Fatalf("metadata.labels.managed-by = %v, want %q", labels["managed-by"], "apiserver")
	}
}

func TestValidateAndDefaultTransactionResourceUsesTargetResourcePath(t *testing.T) {
	var requestMethod string
	var requestPath string
	var requestQuery url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMethod = r.Method
		requestPath = r.URL.Path
		requestQuery = r.URL.Query()

		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept header = %q, want %q", got, "application/json")
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type header = %q, want %q", got, "application/json")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("io.ReadAll() error = %v", err)
		}
		if !bytes.Contains(body, []byte(`"kind":"Deployment"`)) {
			t.Fatalf("request body = %s, want deployment JSON", body)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"wallet-processor","namespace":"default"},"spec":{"revisionHistoryLimit":10}}`))
	}))
	defer server.Close()

	authClient, err := authorizationclientv1.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("authorizationclientv1.NewForConfig() error = %v", err)
	}

	resource := &meta.RESTMapping{
		Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		Scope:    meta.RESTScopeNamespace,
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name": "wallet-processor",
		},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{"app": "wallet-processor"},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{"app": "wallet-processor"},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{"name": "worker", "image": "nginx:1.27.5"},
					},
				},
			},
		},
	}}

	validated, err := validateAndDefaultTransactionResource(context.Background(), authClient, resource, transactionOperationCreate, "default", obj)
	if err != nil {
		t.Fatalf("validateAndDefaultTransactionResource() error = %v", err)
	}
	if requestMethod != http.MethodPost {
		t.Fatalf("request method = %q, want %q", requestMethod, http.MethodPost)
	}
	if requestPath != "/apis/apps/v1/namespaces/default/deployments" {
		t.Fatalf("request path = %q, want %q", requestPath, "/apis/apps/v1/namespaces/default/deployments")
	}
	if requestQuery.Get("dryRun") != "All" {
		t.Fatalf("dryRun = %q, want %q", requestQuery.Get("dryRun"), "All")
	}
	if requestQuery.Get("fieldValidation") != "Strict" {
		t.Fatalf("fieldValidation = %q, want %q", requestQuery.Get("fieldValidation"), "Strict")
	}
	if requestQuery.Get("fieldManager") != transactionValidationFieldManager {
		t.Fatalf("fieldManager = %q, want %q", requestQuery.Get("fieldManager"), transactionValidationFieldManager)
	}
	if validated.GetNamespace() != "default" {
		t.Fatalf("validated namespace = %q, want %q", validated.GetNamespace(), "default")
	}
	if got := validated.Object["spec"].(map[string]interface{})["revisionHistoryLimit"]; got != float64(10) {
		t.Fatalf("revisionHistoryLimit = %v, want %v", got, float64(10))
	}
}

func TestTransactionCreateHandlerCachesSubjectAccessReviewPerResourceType(t *testing.T) {
	body := `apiVersion: apiserver.api-extension.harikube.info/v1
kind: TransactionRequest
metadata:
  name: make-payment-XXX
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-AAA
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-BBB
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-CCC
  delete:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: token-BBB
  - apiVersion: v1
    kind: Secret
    metadata:
      name: token-CCC
`

	originalSAR := transactionSubjectAccessReview
	originalGetResource := transactionGetResource
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionGetResource = originalGetResource
		transactionCommit = originalCommit
	})

	sarCalls := 0
	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		sarCalls++

		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}

	transactionGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		resource := "configmaps"
		if gvk.Kind == "Secret" {
			resource = "secrets"
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: resource},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}

	transactionCommit = func(_ context.Context, _ *clientv3.Client, _, _ string, _ map[string][]byte) (*clientv3.TxnResponse, error) {
		return &clientv3.TxnResponse{
			Succeeded: true,
			Header:    &etcdserverpb.ResponseHeader{Revision: 7},
		}, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusCreated)
	}
	if sarCalls != 2 {
		t.Fatalf("subject access review calls = %d, want 2", sarCalls)
	}
}

func TestTransactionCreateHandlerReturnsForbiddenWhenIndividualResourceDenied(t *testing.T) {
	body := `apiVersion: apiserver.api-extension.harikube.info/v1
kind: TransactionRequest
metadata:
  name: make-payment-XXX
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: user-payment-AAA
  update:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: wallet-AAA
`

	originalSAR := transactionSubjectAccessReview
	originalGetResource := transactionGetResource
	originalCommit := transactionCommit
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		transactionGetResource = originalGetResource
		transactionCommit = originalCommit
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, attrs *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		allowed := attrs.Resource != "secrets"

		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: allowed}}, nil
	}
	transactionGetResource = func(gvk schema.GroupVersionKind, _ *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
		resource := "configmaps"
		if gvk.Kind == "Secret" {
			resource = "secrets"
		}

		return &meta.RESTMapping{
			Resource: schema.GroupVersionResource{Version: "v1", Resource: resource},
			Scope:    meta.RESTScopeNamespace,
		}, nil
	}
	var commitCalled bool
	transactionCommit = func(_ context.Context, _ *clientv3.Client, _, _ string, _ map[string][]byte) (*clientv3.TxnResponse, error) {
		commitCalled = true

		return nil, nil
	}

	handler := getTransactionHandler(&authorizationclientv1.AuthorizationV1Client{}, &clientv3.Client{}, []string{""}, &restmapper.DeferredDiscoveryRESTMapper{})

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if commitCalled {
		t.Fatal("transactionCommit should not be called")
	}
}
