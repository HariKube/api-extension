// Package main provides a compact, standalone example of how an admission-webhook
// style mutating/defaulting handler might consult an external DecisionRequest
// CRD (group: apiserver.api-extension.harikube.info, version: v1, resource: decisionrequests)
// using the Kubernetes dynamic client to decide whether a provided source contains
// sensitive information and then emit a JSONPatch that sets the label
// "harikube.io/source-contains-sensitive-info" on the incoming object.
//
// This file is intended as a runnable example (adapt the CRD's actual status
// schema and runtime integration details to your environment). It focuses on
// building a client, creating a DecisionRequest, reading the response, and
// constructing the admission JSONPatch; it is not a full webhook server.
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

// This example handler demonstrates the essential flow an admission webhook
// would perform: given an incoming object's source content (string), it creates
// a DecisionRequest CR in a namespace, reads back the decision, and returns a
// JSONPatch that sets the label harikube.io/source-contains-sensitive-info.

// buildKubeConfig returns a kubernetes REST config, preferring KUBECONFIG and
// falling back to in-cluster configuration.
func buildKubeConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

// createDecisionRequest creates a namespaced DecisionRequest CR using the
// dynamic client. We insert the provided source string into the spec so a
// decision service can analyze it. The function returns the created object as
// Unstructured for best-effort inspection of the status/answer.
func createDecisionRequest(ctx context.Context, dc dynamic.Interface, namespace, source string) (*unstructured.Unstructured, error) {
	// The API this repo exposes expects a DecisionRequest-style resource whose
	// spec contains `state` (opaque value) and `questions` (map of named
	// questions). We construct a simple state and a single choice question with
	// the key "contains_sensitive_info" that asks whether the provided source
	// contains sensitive information.
	gvr := schema.GroupVersionResource{
		Group:    "apiserver.api-extension.harikube.info",
		Version:  "v1",
		Resource: "decisionrequests",
	}

	name := fmt.Sprintf("dr-sample-%d", time.Now().UnixNano())
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

// extractSensitiveDecision tries to find a boolean decision in the returned
// DecisionRequest object's status; real CRDs vary, so the code checks common
// paths and falls back conservatively to false.
func extractSensitiveDecision(dr *unstructured.Unstructured) bool {
	// The server in this repo returns a DecisionResponse-like object whose
	// answers are placed under spec.answers. We look up the concrete question
	// key we asked for and interpret common answer shapes (bool, string, or
	// nested object).
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
		// No answer for our question key.
		return false
	}

	switch v := val.(type) {
	case bool:
		return v
	case string:
		// Accept a variety of textual answers that mean "yes".
		if v == "true" || v == "yes" || v == "contains-sensitive-info" || v == "contains_sensitive_info" {
			return true
		}
		return false
	case map[string]interface{}:
		// Some decision makers may return a structured answer like {"value": "true"}
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

// buildJSONPatchForLabel constructs an admission-style JSONPatch that ensures the
// label harikube.io/source-contains-sensitive-info is set to the string "true"
// or "false" on the incoming object rawBytes. The incoming object must be the
// original object the webhook received (as raw JSON) so we can inspect whether
// metadata.labels exists and emit the correct sequence of ops.
func buildJSONPatchForLabel(incomingRaw []byte, contains bool) ([]byte, error) {
	var incoming map[string]interface{}
	if err := json.Unmarshal(incomingRaw, &incoming); err != nil {
		return nil, err
	}
	// Determine whether metadata.labels exists so we can either add them first
	// or directly add the specific label.
	hasLabels := false
	if md, ok := incoming["metadata"].(map[string]interface{}); ok {
		if _, ok := md["labels"]; ok {
			hasLabels = true
		}
	}

	labelPath := "/metadata/labels/harikube.io~1source-contains-sensitive-info"
	labelValue := "false"
	if contains {
		labelValue = "true"
	}

	op := make([]map[string]interface{}, 0, 2)
	if !hasLabels {
		// Add an empty labels map first so the next op can insert the specific label.
		op = append(op, map[string]interface{}{
			"op":   "add",
			"path": "/metadata/labels",
			"value": map[string]string{},
		})
	}
	// Add the label as a string value (kubernetes labels are strings).
	op = append(op, map[string]interface{}{
		"op":   "add",
		"path": labelPath,
		"value": labelValue,
	})

	return json.Marshal(op)
}

// Example entrypoint: simulate the admission flow for a sample source string.
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

	namespace := "default" // adapt to the request's namespace in a real webhook
	source := `User posted: My password is P@ssw0rd and my SSN is 123-45-6789` // incoming content

	// Create the DecisionRequest and wait briefly for the controller to answer.
	dr, err := createDecisionRequest(ctx, dyn, namespace, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create DecisionRequest: %v\n", err)
		os.Exit(2)
	}

	// In many deployments the decision service will update status asynchronously.
	// A production webhook typically performs the create and then either polls or
	// relies on synchronous admission webhooks backed by a fast decision service.
	// For this example, we do a tiny sleep to allow a controller to reconcile.
	// NOTE: Adjust or replace with watch/poll to match real-world latency.
	time.Sleep(500 * time.Millisecond)

	// Re-fetch the DR to observe status updates.
	gvr := schema.GroupVersionResource{Group: "apiserver.api-extension.harikube.info", Version: "v1", Resource: "decisionrequests"}
	dr, err = dyn.Resource(gvr).Namespace(namespace).Get(ctx, dr.GetName(), metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get DecisionRequest after create: %v\n", err)
		os.Exit(2)
	}

	contains := extractSensitiveDecision(dr)

	// Simulate the raw incoming object the webhook received. In a real webhook this
	// is the Raw object bytes from the AdmissionReview.Request.Object.Raw.
	incomingSample := []byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"sample","namespace":"default"}}`)

	patch, err := buildJSONPatchForLabel(incomingSample, contains)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build json patch: %v\n", err)
		os.Exit(2)
	}

	fmt.Println("DecisionRequest result indicates containsSensitive=", contains)
	fmt.Println("JSONPatch to apply (Content-Type: application/json-patch+json):")
	fmt.Println(string(patch))
}
