package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiextv1 "github.com/harikube/api-extension/api/v1"
	etcdserverpb "go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.yaml.in/yaml/v2"
	authorizationv1 "k8s.io/api/authorization/v1"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
)

func TestTransactionCreateHandlerStoresRequestInEtcd(t *testing.T) {
	body := `apiVersion: apiserver.api-extension.harikube.info/v1
kind: Transaction
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
	originalPut := putTransaction
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		putTransaction = originalPut
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}

	putTransaction = func(_ context.Context, _ *clientv3.Client, key, value string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		if key != "/harikube/transaction" {
			t.Fatalf("unexpected key: %s", key)
		}
		if value != body {
			t.Fatalf("unexpected value: %s", value)
		}

		return &clientv3.PutResponse{Header: &etcdserverpb.ResponseHeader{Revision: 7}}, nil
	}

	handler, err := getTransactionHandler(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("getTransactionHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/yaml" {
		t.Fatalf("content type = %q, want %q", got, "application/yaml")
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

func TestTransactionCreateHandlerReturnsForbiddenWhenUnauthorized(t *testing.T) {
	originalSAR := transactionSubjectAccessReview
	originalPut := putTransaction
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		putTransaction = originalPut
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: false}}, nil
	}
	putTransaction = func(_ context.Context, _ *clientv3.Client, _, _ string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		t.Fatal("putTransaction should not be called")
		return nil, nil
	}

	handler, err := getTransactionHandler(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("getTransactionHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactions", strings.NewReader("kind: Transaction\nmetadata:\n  name: denied\n"))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestTransactionCreateHandlerRejectsInvalidBody(t *testing.T) {
	originalSAR := transactionSubjectAccessReview
	originalPut := putTransaction
	t.Cleanup(func() {
		transactionSubjectAccessReview = originalSAR
		putTransaction = originalPut
	})

	transactionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	putTransaction = func(_ context.Context, _ *clientv3.Client, _, _ string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		t.Fatal("putTransaction should not be called")
		return nil, nil
	}

	handler, err := getTransactionHandler(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("getTransactionHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactions", strings.NewReader(": invalid"))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
