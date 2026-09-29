//go:build ignore

// Package main provides a compact, standalone example of how a validating admission
// webhook handler could consult the DecisionRequest CRD (group: apiserver.api-extension.harikube.info,
// version: v1, resource: decisionrequests) using the Kubernetes dynamic client to ask
// whether an incoming object's source contains sensitive information and then
// allow or reject the admission request based on the decision.
//
// This example is intentionally small and focuses on the essential flow: build a
// kube client, create a DecisionRequest with spec.state and spec.questions, read
// the decision answer, and construct an AdmissionReview-style allow/deny response.
// It is not a full server implementation; adapt polling/watches, timeouts and
// CRD status paths to your real environment.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// buildKubeConfig returns a kubernetes REST config, preferring KUBECONFIG and
// falling back to in-cluster configuration.
func buildKubeConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

// createDecisionRequest posts a DecisionRequest CR to the apiserver using the
// dynamic client. The DecisionRequest follows this repo's convention: spec.state
// and spec.questions. We ask a single question with key "contains_sensitive_info".
func createDecisionRequest(ctx context.Context, dc dynamic.Interface, namespace, source string) (*unstructured.Unstructured, error) {
	gvr := schema.GroupVersionResource{
		Group:    "apiserver.api-extension.harikube.info",
		Version:  "v1",
		Resource: "decisionrequests",
	}

	name := fmt.Sprintf("dr-validate-%d", time.Now().UnixNano())
	questionKey := "contains_sensitive_info"
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiserver.api-extension.harikube.info/v1",
			"kind":       "DecisionRequest",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"spec": map[string]interface{}{
				"state": map[string]interface{}{
					"source": source,
				},
				"questions": map[string]interface{}{
					questionKey: map[string]interface{}{
						"type":         "choice",
						"instructions": "Does the provided source contain sensitive information?",
						"criteria": map[string]interface{}{
							"true":  "contains-sensitive-info",
							"false": "no-sensitive-info",
						},
					},
				},
			},
		},
	}

	return dc.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
}

// extractSensitiveDecision is a best-effort reader that interprets the answer
// for the question key "contains_sensitive_info" from the DecisionRequest
// object's spec.answers (shape may vary by implementation).
func extractSensitiveDecision(dr *unstructured.Unstructured) bool {
	questionKey := "contains_sensitive_info"
	spec, found, _ := unstructured.NestedMap(dr.Object, "spec")
	if !found {
		return false
	}

	answersRaw, ok := spec["answers"]
	if !ok {
		return false
	}
	answers, ok := answersRaw.(map[string]interface{})
	if !ok {
		return false
	}

	val, ok := answers[questionKey]
	if !ok {
		return false
	}

	switch v := val.(type) {
	case bool:
		return v
	case string:
		if v == "true" || v == "yes" || v == "contains-sensitive-info" || v == "contains_sensitive_info" {
			return true
		}
		return false
	case map[string]interface{}:
		if vv, ok := v["value"]; ok {
			switch tv := vv.(type) {
			case bool:
				return tv
			case string:
				if tv == "true" || tv == "contains-sensitive-info" {
					return true
				}
			}
		}
	}
	return false
}

// admissionResponse builds a compact AdmissionReview-style response that a
// validating webhook would return to the apiserver. For brevity we only emit
// the fields the apiserver examines: response.allowed and an optional status
// message when rejecting.
func admissionResponse(allowed bool, message string, uid string) ([]byte, error) {
	resp := map[string]interface{}{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"response": map[string]interface{}{
			"uid":     uid,
			"allowed": allowed,
		},
	}
	if !allowed {
		if r := resp["response"].(map[string]interface{}); r != nil {
			r["status"] = map[string]interface{}{
				"message": message,
				"code":    403,
			}
		}
	}
	return json.MarshalIndent(resp, "", "  ")
}

// Example entrypoint: simulate a validating webhook receiving an object whose
// textual source is analyzed by the DecisionRequest-based decision service.
func main() {
	ctx := context.Background()

	cfg, err := buildKubeConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build kube config: %v\n", err)
		os.Exit(2)
	}

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create dynamic client: %v\n", err)
		os.Exit(2)
	}

	namespace := "default" // in a real webhook derive from the AdmissionReview's object namespace

	// This is the textual content the decision service should inspect.
	source := `User message: here is my password: P@ssw0rd` // adapt per webhook input

	// Create the DecisionRequest and allow a short moment for the controller to
	// reconcile and populate answers. Replace with a watch/poll in production.
	dr, err := createDecisionRequest(ctx, dyn, namespace, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create DecisionRequest: %v\n", err)
		os.Exit(2)
	}

	// Small sleep to allow asynchronous update; production code should poll or
	// watch for the status update with proper timeout and error handling.
	time.Sleep(500 * time.Millisecond)

	gvr := schema.GroupVersionResource{Group: "apiserver.api-extension.harikube.info", Version: "v1", Resource: "decisionrequests"}
	dr, err = dyn.Resource(gvr).Namespace(namespace).Get(ctx, dr.GetName(), metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get DecisionRequest after create: %v\n", err)
		os.Exit(2)
	}

	contains := extractSensitiveDecision(dr)

	// Simulate the AdmissionReview request UID and the incoming raw object bytes
	// (AdmissionReview.Request.Object.Raw). A real webhook handler reads these
	// from the HTTP request body.
	admissionUID := "example-uid-1234"
	incomingRaw := []byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"example","namespace":"default"}}`)

	// Build the AdmissionReview response: reject when contains==true.
	allowed := !contains
	message := ""
	if !allowed {
		message = "rejected by validation webhook: source contains sensitive information"
	}

	respBytes, err := admissionResponse(allowed, message, admissionUID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build admission response: %v\n", err)
		os.Exit(2)
	}

	// Print outcome for the example runner. A live webhook would write these
	// bytes as the HTTP response with Content-Type: application/json.
	fmt.Printf("Incoming object: %s\n", string(incomingRaw))
	fmt.Printf("DecisionRequest indicates contains_sensitive_info=%v\n", contains)
	fmt.Println("AdmissionReview response to return:")
	fmt.Println(string(respBytes))
}
