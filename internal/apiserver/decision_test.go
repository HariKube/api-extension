package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
)

func TestDecisionCreateHandlerCallsDecisionMakerAndReturnsDecisionResponse(t *testing.T) {
	originalSAR := decisionSubjectAccessReview
	originalInvoke := invokeDecisionMaker
	t.Cleanup(func() {
		decisionSubjectAccessReview = originalSAR
		invokeDecisionMaker = originalInvoke
	})

	decisionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}

	var capturedRequest *decisionSystemOneRequest
	invokeDecisionMaker = func(_ context.Context, endpoint string, request *decisionSystemOneRequest) (*decisionSystemOneResponse, error) {
		if endpoint != "http://decision-maker.system-one.svc/system-one" {
			t.Fatalf("endpoint = %q, want %q", endpoint, "http://decision-maker.system-one.svc/system-one")
		}
		capturedRequest = request
		return &decisionSystemOneResponse{
			Answers: map[string]interface{}{
				"department": map[string]interface{}{
					"choice": "billing",
					"probabilities": map[string]interface{}{
						"billing": 0.95,
						"support": 0.03,
						"sales":   0.02,
					},
				},
			},
			Usage: map[string]interface{}{"input_tokens": 42.0},
		}, nil
	}

	handler := getDecisionHandler(nil, "http://decision-maker.system-one.svc/system-one", 5*time.Second)

	body := `apiVersion: apiserver.api-extension.harikube.info/v1
kind: DecisionRequest
metadata:
  name: triage-refund
  namespace: default
spec:
  state:
    subject: Refund not received
    body: Please refund my payment
  questions:
    department:
      type: choice
      instructions: Which team should handle this ticket?
      criteria:
        billing: payments refunds invoices
        support: product help and bugs
        sales: new purchases
`
	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/decisionrequests", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusCreated)
	}
	if capturedRequest == nil {
		t.Fatal("invokeDecisionMaker was not called")
	}

	state, ok := capturedRequest.State.(map[string]interface{})
	if !ok {
		t.Fatalf("state type = %T, want map[string]interface{}", capturedRequest.State)
	}
	if state["subject"] != "Refund not received" {
		t.Fatalf("state.subject = %v, want %q", state["subject"], "Refund not received")
	}
	question := capturedRequest.Questions["department"]
	criteria, ok := question.Criteria.(map[string]string)
	if !ok {
		t.Fatalf("criteria type = %T, want map[string]string", question.Criteria)
	}
	if criteria["billing"] != "payments refunds invoices" {
		t.Fatalf("criteria[billing] = %q, want %q", criteria["billing"], "payments refunds invoices")
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response["kind"] != "DecisionResponse" {
		t.Fatalf("kind = %v, want %q", response["kind"], "DecisionResponse")
	}
	metadata, ok := response["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata type = %T, want map[string]interface{}", response["metadata"])
	}
	if metadata["name"] != "triage-refund" {
		t.Fatalf("metadata.name = %v, want %q", metadata["name"], "triage-refund")
	}
	if metadata["namespace"] != "default" {
		t.Fatalf("metadata.namespace = %v, want %q", metadata["namespace"], "default")
	}
	spec, ok := response["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("spec type = %T, want map[string]interface{}", response["spec"])
	}
	answers, ok := spec["answers"].(map[string]interface{})
	if !ok {
		t.Fatalf("answers type = %T, want map[string]interface{}", spec["answers"])
	}
	department, ok := answers["department"].(map[string]interface{})
	if !ok {
		t.Fatalf("department type = %T, want map[string]interface{}", answers["department"])
	}
	if department["choice"] != "billing" {
		t.Fatalf("department.choice = %v, want %q", department["choice"], "billing")
	}
}

func TestDecisionCreateHandlerReturnsForbiddenWhenUnauthorized(t *testing.T) {
	originalSAR := decisionSubjectAccessReview
	originalInvoke := invokeDecisionMaker
	t.Cleanup(func() {
		decisionSubjectAccessReview = originalSAR
		invokeDecisionMaker = originalInvoke
	})

	decisionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: false}}, nil
	}
	invokeDecisionMaker = func(_ context.Context, _ string, _ *decisionSystemOneRequest) (*decisionSystemOneResponse, error) {
		t.Fatal("invokeDecisionMaker should not be called")
		return nil, nil
	}

	handler := getDecisionHandler(nil, defaultDecisionMakerURL, 5*time.Second)

	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/decisionrequests", strings.NewReader("kind: DecisionRequest\nmetadata:\n  name: denied\n"))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestDecisionCreateHandlerRejectsNamespaceMismatch(t *testing.T) {
	originalSAR := decisionSubjectAccessReview
	originalInvoke := invokeDecisionMaker
	t.Cleanup(func() {
		decisionSubjectAccessReview = originalSAR
		invokeDecisionMaker = originalInvoke
	})

	decisionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	invokeDecisionMaker = func(_ context.Context, _ string, _ *decisionSystemOneRequest) (*decisionSystemOneResponse, error) {
		t.Fatal("invokeDecisionMaker should not be called")
		return nil, nil
	}

	handler := getDecisionHandler(nil, defaultDecisionMakerURL, 5*time.Second)

	body := `kind: DecisionRequest
metadata:
  name: mismatch
  namespace: other
spec:
  state:
    text: hello
  questions:
    intent:
      type: noul
      instructions: Is this a test?
`
	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/decisionrequests", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDecisionCreateHandlerRejectsInvalidChoiceCriteria(t *testing.T) {
	originalSAR := decisionSubjectAccessReview
	originalInvoke := invokeDecisionMaker
	t.Cleanup(func() {
		decisionSubjectAccessReview = originalSAR
		invokeDecisionMaker = originalInvoke
	})

	decisionSubjectAccessReview = func(_ context.Context, _ *authorizationclientv1.AuthorizationV1Client, _ *authorizationv1.ResourceAttributes, _ http.Header) (*authorizationv1.SubjectAccessReview, error) {
		return &authorizationv1.SubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	}
	invokeDecisionMaker = func(_ context.Context, _ string, _ *decisionSystemOneRequest) (*decisionSystemOneResponse, error) {
		t.Fatal("invokeDecisionMaker should not be called")
		return nil, nil
	}

	handler := getDecisionHandler(nil, defaultDecisionMakerURL, 5*time.Second)

	body := `kind: DecisionRequest
metadata:
  name: broken
  namespace: default
spec:
  state:
    text: hello
  questions:
    department:
      type: choice
      instructions: Which team?
      criteria:
      - invalid
`
	req := httptest.NewRequest(http.MethodPost, "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/decisionrequests", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.CustomResource.CreateHandler("default", "", rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
