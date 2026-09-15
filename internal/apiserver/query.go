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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// stubbed package-level variables to allow tests to replace them
var (
	querySubjectAccessReview = subjectAccessReview
	putQuery                 = func(ctx context.Context, client *clientv3.Client, key, value string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		return client.Put(ctx, key, value, opts...)
	}
)

// getQueryHandler returns an APIKind for queries. It mirrors transaction style but is minimal.
func getQueryHandler(authClient *authorizationclientv1.AuthorizationV1Client, _ *rest.Config, harikubeClient *clientv3.Client, _ []string, _ *restmapper.DeferredDiscoveryRESTMapper) (*kaf.APIKind, error) {
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

				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if len(strings.TrimSpace(string(body))) == 0 {
					http.Error(w, "empty body", http.StatusBadRequest)
					return
				}

				// minimal validation: unmarshal to map to ensure valid YAML
				var m map[string]interface{}
				if err := yaml.Unmarshal(body, &m); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}

				// store in etcd (key naming mirrors transactionKey)
				key := "queries/" + name
				if _, err := putQuery(ctx, harikubeClient, key, string(body)); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				// respond with a minimal object to indicate success
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte("{}"))
			},
		},
	}, nil
}
