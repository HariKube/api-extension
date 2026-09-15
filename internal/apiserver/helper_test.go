package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.yaml.in/yaml/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResponseContentUsesContentTypeHeader(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", contentTypeYAML+";"+contentDetailsTableV1)
	headers.Set("Accept", contentTypeJSON)

	contentType, contentDetails := responseContent(headers)

	if contentType != contentTypeYAML {
		t.Fatalf("contentType = %q, want %q", contentType, contentTypeYAML)
	}
	if contentDetails != contentDetailsTableV1 {
		t.Fatalf("contentDetails = %q, want %q", contentDetails, contentDetailsTableV1)
	}
}

func TestResponseContentFallsBackToAcceptHeader(t *testing.T) {
	headers := http.Header{}
	headers["Accept"] = []string{contentTypeJSON, contentTypeYAML}

	contentType, contentDetails := responseContent(headers)

	if contentType != contentTypeJSON {
		t.Fatalf("contentType = %q, want %q", contentType, contentTypeJSON)
	}
	if contentDetails != "" {
		t.Fatalf("contentDetails = %q, want empty", contentDetails)
	}
}

func TestWriteResponseWritesYAML(t *testing.T) {
	rec := httptest.NewRecorder()
	container := map[string]string{"kind": "TransactionResponse", "name": "example"}

	if err := writeResponse(rec, http.StatusCreated, container, contentTypeYAML); err != nil {
		t.Fatalf("writeResponse() error = %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != contentTypeYAML {
		t.Fatalf("content type = %q, want %q", got, contentTypeYAML)
	}

	var got map[string]string
	if err := yaml.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if got["kind"] != container["kind"] || got["name"] != container["name"] {
		t.Fatalf("body = %#v, want %#v", got, container)
	}
}

func TestWriteResponseWritesJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	container := map[string]string{"kind": "CountResponse"}

	if err := writeResponse(rec, http.StatusOK, container, contentTypeJSON); err != nil {
		t.Fatalf("writeResponse() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contentTypeJSON)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["kind"] != container["kind"] {
		t.Fatalf("body = %#v, want %#v", got, container)
	}
}

func TestWriteResponseDefaultsToJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	container := map[string]string{"kind": "CountResponse"}

	if err := writeResponse(rec, http.StatusOK, container, "text/plain"); err != nil {
		t.Fatalf("writeResponse() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contentTypeJSON)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["kind"] != container["kind"] {
		t.Fatalf("body = %#v, want %#v", got, container)
	}
}

func TestTableResponseBuildsV1Table(t *testing.T) {
	columns := []metav1.TableColumnDefinition{{Name: "Name", Type: "string"}}
	cells := []interface{}{"example"}
	obj := &metav1.Status{Status: "Success"}

	container, ok, err := tableResponse(contentDetailsTableV1, "7", columns, cells, obj)
	if err != nil {
		t.Fatalf("tableResponse() error = %v", err)
	}
	if !ok {
		t.Fatal("tableResponse() ok = false, want true")
	}

	table, ok := container.(metav1.Table)
	if !ok {
		t.Fatalf("container type = %T, want %T", container, metav1.Table{})
	}
	if table.ResourceVersion != "7" {
		t.Fatalf("resourceVersion = %q, want %q", table.ResourceVersion, "7")
	}
	if len(table.Rows) != 1 || len(table.Rows[0].Cells) != 1 || table.Rows[0].Cells[0] != "example" {
		t.Fatalf("rows = %#v, want single row with example cell", table.Rows)
	}
	if len(table.Rows[0].Object.Raw) == 0 {
		t.Fatal("raw object is empty")
	}
}

func TestTableResponseBuildsV1Beta1Table(t *testing.T) {
	columns := []metav1.TableColumnDefinition{{Name: "Name", Type: "string"}}
	cells := []interface{}{"example"}
	obj := &metav1.Status{Status: "Success"}

	container, ok, err := tableResponse(contentDetailsTableV1Beta1, "9", columns, cells, obj)
	if err != nil {
		t.Fatalf("tableResponse() error = %v", err)
	}
	if !ok {
		t.Fatal("tableResponse() ok = false, want true")
	}

	table, ok := container.(metav1beta1.Table)
	if !ok {
		t.Fatalf("container type = %T, want %T", container, metav1beta1.Table{})
	}
	if table.ResourceVersion != "9" {
		t.Fatalf("resourceVersion = %q, want %q", table.ResourceVersion, "9")
	}
	if len(table.Rows) != 1 || len(table.Rows[0].Cells) != 1 || table.Rows[0].Cells[0] != "example" {
		t.Fatalf("rows = %#v, want single row with example cell", table.Rows)
	}
	if len(table.Rows[0].Object.Raw) == 0 {
		t.Fatal("raw object is empty")
	}
}

func TestTableResponseIgnoresNonTableContent(t *testing.T) {
	container, ok, err := tableResponse("", "7", nil, nil, &metav1.Status{})
	if err != nil {
		t.Fatalf("tableResponse() error = %v", err)
	}
	if ok {
		t.Fatalf("tableResponse() ok = true, want false (container=%#v)", container)
	}
	if container != nil {
		t.Fatalf("container = %#v, want nil", container)
	}
}

func TestEtcdKeyForGroupVersionKindBuildsCoreResourceNamespaceKey(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	resource := &meta.RESTMapping{
		Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"},
		Scope:    meta.RESTScopeNamespace,
	}

	key, err := etcdKeyForGroupVersionKind(gvk, "default", resource, map[string]bool{"": true})
	if err != nil {
		t.Fatalf("etcdKeyForGroupVersionKind() error = %v", err)
	}
	if key != "/registry/pods/default/" {
		t.Fatalf("key = %q, want %q", key, "/registry/pods/default/")
	}
}

func TestEtcdKeyForGroupVersionKindBuildsClusterScopedGroupedKey(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}
	resource := &meta.RESTMapping{
		Resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
		Scope:    meta.RESTScopeRoot,
	}

	key, err := etcdKeyForGroupVersionKind(gvk, "default", resource, map[string]bool{"": true})
	if err != nil {
		t.Fatalf("etcdKeyForGroupVersionKind() error = %v", err)
	}
	if key != "/registry/rbac.authorization.k8s.io/clusterroles/" {
		t.Fatalf("key = %q, want %q", key, "/registry/rbac.authorization.k8s.io/clusterroles/")
	}
}
