package apiserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	apiextv1 "github.com/harikube/api-extension/api/v1"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.yaml.in/yaml/v2"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const transactionKey = "/harikube/transaction"

var (
	transactionLogger              = logf.Log.WithName("api-extension.transaction")
	transactionSubjectAccessReview = subjectAccessReview
	putTransaction                 = func(ctx context.Context, client *clientv3.Client, key, value string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
		return client.Put(ctx, key, value, opts...)
	}
)

type transactionRequest struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
	Metadata   struct {
		Name      string `json:"name" yaml:"name"`
		Namespace string `json:"namespace" yaml:"namespace"`
	} `json:"metadata" yaml:"metadata"`
}

func getTransactionHandler(authClient *authorizationclientv1.AuthorizationV1Client, _ *rest.Config, harikubeClient *clientv3.Client, _ []string, _ *restmapper.DeferredDiscoveryRESTMapper) (*kaf.APIKind, error) {
	return &kaf.APIKind{
		ApiResource: metav1.APIResource{
			Name:       "transactions",
			Namespaced: true,
			Kind:       "Transaction",
			Verbs:      []string{"create"},
		},
		CustomResource: &kaf.CustomResource{
			CreateHandler: func(namespace, name string, w http.ResponseWriter, r *http.Request) {
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()

				if result, err := transactionSubjectAccessReview(ctx, authClient,
					&authorizationv1.ResourceAttributes{
						Namespace: namespace,
						Verb:      "create",
						Group:     Group,
						Resource:  "transactions",
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

				transaction := transactionRequest{}
				if err := yaml.Unmarshal(body, &transaction); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)

					return
				}
				if transaction.Metadata.Namespace != "" && namespace != "" && transaction.Metadata.Namespace != namespace {
					http.Error(w, "metadata.namespace does not match request namespace", http.StatusBadRequest)

					return
				}

				logger := transactionLogger.WithValues("namespace", namespace, "name", transaction.Metadata.Name, "key", transactionKey)
				logger.Info("Creating transaction")

				putResp, err := putTransaction(ctx, harikubeClient, transactionKey, string(body))
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)

					return
				} else if putResp == nil {
					http.Error(w, "transaction not stored", http.StatusInternalServerError)

					return
				}

				responseName := transaction.Metadata.Name
				if responseName == "" {
					responseName = name
				}
				if responseName == "" {
					responseName = "transaction"
				}

				resp := apiextv1.TransactionResponse{
					TypeMeta: metav1.TypeMeta{
						APIVersion: apiextv1.SchemeBuilder.GroupVersion.String(),
						Kind:       "TransactionResponse",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      responseName,
						CreationTimestamp: metav1.Time{
							Time: time.Now(),
						},
						ResourceVersion: fmt.Sprintf("%d", putResp.Header.Revision),
					},
					Spec: apiextv1.TransactionResponseSpec{},
				}

				contentType, contentDetails := responseContent(r.Header)

				columns := []metav1.TableColumnDefinition{
					{
						Name:   "Name",
						Type:   "string",
						Format: "name",
					},
					{
						Name: "Error",
						Type: "string",
					},
				}
				cells := []interface{}{resp.Name, resp.Spec.Error}

				container, ok, err := tableResponse(contentDetails, resp.ResourceVersion, columns, cells, &resp)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)

					return
				}
				if !ok {
					container = resp
				}

				if err := writeResponse(w, http.StatusCreated, container, contentType); err != nil {
					logger.Info("Write error", "error", err)

					return
				}
			},
		},
	}, nil
}
