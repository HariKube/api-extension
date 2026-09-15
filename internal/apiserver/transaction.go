package apiserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	apiextv1 "github.com/harikube/api-extension/api/v1"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.yaml.in/yaml/v2"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/restmapper"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	transactionLogger              = logf.Log.WithName("api-extension.transaction")
	transactionSubjectAccessReview = subjectAccessReview
	transactionGetResource         = getResurce
	transactionCommit              = func(ctx context.Context, client *clientv3.Client, resources map[string][]byte) (*clientv3.TxnResponse, error) {
		keys := make([]string, 0, len(resources))
		for k := range resources {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		ops := make([]clientv3.Op, 0, len(keys))
		for _, k := range keys {
			etcdKey, operation, _ := strings.Cut(k, "#")
			switch operation {
			case "create", "update":
				ops = append(ops, clientv3.OpPut(etcdKey, string(resources[k])))
			case "delete":
				ops = append(ops, clientv3.OpDelete(etcdKey))
			default:
				return nil, fmt.Errorf("unknown operation %q", operation)
			}
		}

		return client.Txn(ctx).Then(ops...).Commit()
	}
)

type transactionRequest struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
	Metadata   struct {
		Name      string `json:"name" yaml:"name"`
		Namespace string `json:"namespace" yaml:"namespace"`
	} `json:"metadata" yaml:"metadata"`
	Spec struct {
		Create []map[string]interface{} `json:"create" yaml:"create"`
		Update []map[string]interface{} `json:"update" yaml:"update"`
		Delete []map[string]interface{} `json:"delete" yaml:"delete"`
	} `json:"spec" yaml:"spec"`
}

func getTransactionHandler(authClient *authorizationclientv1.AuthorizationV1Client, harikubeClient *clientv3.Client, coreResources []string, mapper *restmapper.DeferredDiscoveryRESTMapper) *kaf.APIKind {
	coreResourcesMap := map[string]bool{}
	for i := range coreResources {
		coreResourcesMap[coreResources[i]] = true
	}

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

				if err := transactionAuthorizeAll(ctx, authClient, r.Header, &transaction, namespace, mapper); err != nil {
					http.Error(w, "resource forbidden", http.StatusForbidden)

					return
				}

				resources := map[string][]byte{}

				for _, entry := range transaction.Spec.Create {
					if err := transactionResourceMap(resources, entry, namespace, "create", coreResourcesMap, mapper); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)

						return
					}
				}
				for _, entry := range transaction.Spec.Update {
					if err := transactionResourceMap(resources, entry, namespace, "update", coreResourcesMap, mapper); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)

						return
					}
				}
				for _, entry := range transaction.Spec.Delete {
					if err := transactionResourceMap(resources, entry, namespace, "delete", coreResourcesMap, mapper); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)

						return
					}
				}

				if len(resources) == 0 {
					http.Error(w, "empty transaction spec", http.StatusBadRequest)

					return
				}

				logger := transactionLogger.WithValues("namespace", namespace, "name", transaction.Metadata.Name, "operations", len(resources))
				logger.Info("Creating transaction")

				commitResp, err := transactionCommit(ctx, harikubeClient, resources)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)

					return
				} else if commitResp == nil {
					http.Error(w, "transaction not stored", http.StatusInternalServerError)

					return
				} else if !commitResp.Succeeded {
					http.Error(w, "transaction failed", http.StatusInternalServerError)

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
						ResourceVersion: fmt.Sprintf("%d", commitResp.Header.Revision),
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
					logger.Error(err, "Write error")

					return
				}
			},
		},
	}
}

func transactionResourceMap(resources map[string][]byte, entry map[string]interface{}, requestNamespace, operation string, coreResourcesMap map[string]bool, mapper *restmapper.DeferredDiscoveryRESTMapper) error {
	apiVersion, _ := entry["apiVersion"].(string)
	kind, _ := entry["kind"].(string)
	if apiVersion == "" {
		return fmt.Errorf("missing apiVersion in %s resource", operation)
	}
	if kind == "" {
		return fmt.Errorf("missing kind in %s resource", operation)
	}

	gvk := schema.GroupVersionKind{Kind: kind}
	if parts := strings.Split(apiVersion, "/"); len(parts) == 1 {
		gvk.Version = parts[0]
	} else {
		gvk.Group = parts[0]
		gvk.Version = parts[1]
	}

	resource, err := transactionGetResource(gvk, mapper)
	if err != nil {
		return fmt.Errorf("failed to get resource mapping for %s: %w", gvk.String(), err)
	}
	if resource == nil {
		return fmt.Errorf("resource not found for %s", gvk.String())
	}

	ns := requestNamespace
	if metaEntry, ok := entry["metadata"].(map[interface{}]interface{}); ok {
		if metaMap, ok := metaEntry["namespace"]; ok {
			if nsStr, ok := metaMap.(string); ok && nsStr != "" {
				ns = nsStr
			}
		}
	} else if metaEntry, ok := entry["metadata"].(map[string]interface{}); ok {
		if metaMap, ok := metaEntry["namespace"]; ok {
			if nsStr, ok := metaMap.(string); ok && nsStr != "" {
				ns = nsStr
			}
		}
	}

	if resource.Scope.Name() == meta.RESTScopeNameRoot {
		ns = ""
	}

	prefix, err := etcdKeyForGroupVersionKind(gvk, ns, resource, coreResourcesMap)
	if err != nil {
		return fmt.Errorf("failed to build etcd key for %s: %w", gvk.String(), err)
	}

	resourceName, _ := entry["metadata"].(map[interface{}]interface{})
	var name string
	if resourceName != nil {
		if n, ok := resourceName["name"]; ok {
			name, _ = n.(string)
		}
	}
	if name == "" {
		if resourceName, ok := entry["metadata"].(map[string]interface{}); ok {
			if n, ok := resourceName["name"]; ok {
				name, _ = n.(string)
			}
		}
	}
	if name == "" {
		return fmt.Errorf("missing metadata.name in %s resource", operation)
	}

	value, err := yaml.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal %s resource: %w", operation, err)
	}

	resources[prefix+name+"#"+operation] = value

	return nil
}

func transactionAuthorizeAll(ctx context.Context, authClient *authorizationclientv1.AuthorizationV1Client, headers http.Header, transaction *transactionRequest, requestNamespace string, mapper *restmapper.DeferredDiscoveryRESTMapper) error {
	type authKey struct {
		group     string
		resource  string
		verb      string
		namespace string
	}

	cache := map[authKey]*authorizationv1.SubjectAccessReviewStatus{}

	entries := []struct {
		items []map[string]interface{}
		verb  string
	}{
		{items: transaction.Spec.Create, verb: "create"},
		{items: transaction.Spec.Update, verb: "update"},
		{items: transaction.Spec.Delete, verb: "delete"},
	}

	for _, group := range entries {
		for _, entry := range group.items {
			apiVersion, _ := entry["apiVersion"].(string)
			kind, _ := entry["kind"].(string)
			if apiVersion == "" || kind == "" {
				continue
			}

			gvk := schema.GroupVersionKind{Kind: kind}
			if parts := strings.Split(apiVersion, "/"); len(parts) == 1 {
				gvk.Version = parts[0]
			} else {
				gvk.Group = parts[0]
				gvk.Version = parts[1]
			}

			resource, err := transactionGetResource(gvk, mapper)
			if err != nil || resource == nil {
				continue
			}

			ns := requestNamespace
			if metaEntry, ok := entry["metadata"].(map[interface{}]interface{}); ok {
				if metaMap, ok := metaEntry["namespace"]; ok {
					if nsStr, ok := metaMap.(string); ok && nsStr != "" {
						ns = nsStr
					}
				}
			} else if metaEntry, ok := entry["metadata"].(map[string]interface{}); ok {
				if metaMap, ok := metaEntry["namespace"]; ok {
					if nsStr, ok := metaMap.(string); ok && nsStr != "" {
						ns = nsStr
					}
				}
			}

			if resource.Scope.Name() == meta.RESTScopeNameRoot {
				ns = ""
			}

			key := authKey{
				group:     gvk.Group,
				resource:  resource.Resource.Resource,
				verb:      group.verb,
				namespace: ns,
			}

			if status, ok := cache[key]; ok {
				if !status.Allowed {
					return fmt.Errorf("forbidden: %s %s in namespace %q", group.verb, resource.Resource.Resource, ns)
				}

				continue
			}

			result, err := transactionSubjectAccessReview(ctx, authClient,
				&authorizationv1.ResourceAttributes{
					Namespace: ns,
					Verb:      group.verb,
					Group:     gvk.Group,
					Resource:  resource.Resource.Resource,
				}, headers)
			if err != nil {
				return fmt.Errorf("authorization check failed for %s %s: %w", gvk.Group, resource.Resource.Resource, err)
			}

			cache[key] = &result.Status

			if !result.Status.Allowed {
				return fmt.Errorf("forbidden: %s %s in namespace %q", group.verb, resource.Resource.Resource, ns)
			}
		}
	}

	return nil
}
