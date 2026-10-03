package apiserver

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.yaml.in/yaml/v2"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// stubbed package-level variables to allow tests to replace them
var (
	queryLogger              = logf.Log.WithName("api-extension.query")
	querySubjectAccessReview = subjectAccessReview
	putQuery                 = func(ctx context.Context, client *clientv3.Client, key, value string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		return client.Put(ctx, key, value, opts...)
	}
)

// parseQueryRequestBody reads and minimally validates the request body as YAML; on error it
// returns a non-zero HTTP status and an error message suitable for http.Error.
func parseQueryRequestBody(r *http.Request) ([]byte, int, string, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, http.StatusBadRequest, err.Error(), err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, http.StatusBadRequest, "empty body", nil
	}

	var m map[string]interface{}
	if err := yaml.Unmarshal(body, &m); err != nil {
		return nil, http.StatusBadRequest, err.Error(), err
	}

	return body, 0, "", nil
}

// writeCreatedMinimal sends the minimal created response body and logs write errors.
func writeCreatedMinimal(w http.ResponseWriter) {
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write([]byte("{}")); err != nil {
		queryLogger.Info("Write error", "error", err)
	}
}

// getQueryHandler returns an APIKind for queries. It mirrors transaction style but is minimal.
//
//nolint:unparam // parameter usage is intentional; suppress unparam warning for this handler factory.
func getQueryHandler(authClient *authorizationclientv1.AuthorizationV1Client, harikubeClient *clientv3.Client) *kaf.APIKind {
	return &kaf.APIKind{
		ApiResource: metav1.APIResource{
			Name:       "queries",
			Namespaced: true,
			Kind:       "Query",
			Verbs:      []string{"create"},
		},
		CustomResource: &kaf.CustomResource{
			CreateHandler: func(namespace, name string, w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()

				if result, err := querySubjectAccessReview(ctx, authClient,
					&authorizationv1.ResourceAttributes{
						Namespace: namespace,
						Verb:      "create",
						Group:     Group,
						Resource:  "queries",
					}, r.Header); err != nil {
					http.Error(w, "resource not found", http.StatusNotFound)
					return
				} else if !result.Status.Allowed {
					http.Error(w, "resource forbidden", http.StatusForbidden)
					return
				}

				body, status, msg, err := parseQueryRequestBody(r)
				if status != 0 {
					// parseQueryRequestBody already gives the correct HTTP status and message
					http.Error(w, msg, status)
					return
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}

				// store in etcd (key naming mirrors transactionKey)
				key := "queries/" + name
				if _, err := putQuery(ctx, harikubeClient, key, string(body)); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				writeCreatedMinimal(w)
			},
		},
	}
}
