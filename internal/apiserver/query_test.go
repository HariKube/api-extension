package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"
	authorizationv1 "k8s.io/api/authorization/v1"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
)

func TestQueryCreateHandlerStoresRequestInEtcd(t *testing.T) {
	originalSAR := querySubjectAccessReview
	originalPut := putQuery
	t.Cleanup(func() {
		querySubjectAccessReview = originalSAR
		putQuery = originalPut
	})

	querySubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}

	called := false
	var storedValue string
	putQuery = func(_ context.Context, _ *clientv3.Client, _, value string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		called = true
		storedValue = value
		return &clientv3.PutResponse{}, nil
	}

	handler := getQueryHandler(nil, nil)

	body := `kind: Query
metadata:
  name: example
spec:
  query: uid != ?
  params:
  - f47ac10b-58cc-4372-a567-0e02b2c3d479
`
	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/queries", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if !called {
		t.Fatalf("putQuery was not called")
	}
	if storedValue != body {
		t.Fatalf("stored value mismatch: got %q", storedValue)
	}
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status code = %d, want 2xx", rec.Code)
	}
}

func TestQueryCreateHandlerReturnsForbiddenWhenUnauthorized(t *testing.T) {
	originalSAR := querySubjectAccessReview
	originalPut := putQuery
	t.Cleanup(func() {
		querySubjectAccessReview = originalSAR
		putQuery = originalPut
	})

	querySubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: false}}, nil
	}
	putQuery = func(_ context.Context, _ *clientv3.Client, _, _ string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		t.Fatal("putQuery should not be called")
		return nil, nil
	}

	handler := getQueryHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/queries", strings.NewReader("kind: Query\nmetadata:\n  name: denied\n"))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestQueryCreateHandlerRejectsInvalidBody(t *testing.T) {
	originalSAR := querySubjectAccessReview
	originalPut := putQuery
	t.Cleanup(func() {
		querySubjectAccessReview = originalSAR
		putQuery = originalPut
	})

	querySubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	putQuery = func(_ context.Context, _ *clientv3.Client, _, _ string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		t.Fatal("putQuery should not be called")
		return nil, nil
	}

	handler := getQueryHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/namespaces/default/queries", strings.NewReader(": invalid"))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
