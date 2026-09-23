package apiserver

import (
	"context"
	"encoding/json"
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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured/unstructuredscheme"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/restmapper"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	transactionLogger              = logf.Log.WithName("api-extension.transaction")
	transactionSubjectAccessReview = subjectAccessReview
	transactionGetResource         = getResurce
	transactionCommit              = func(ctx context.Context, client *clientv3.Client, namespace, name string, resources map[string][]byte) (*clientv3.TxnResponse, error) {
		payloadBytes, err := buildTransactionCommitPayload(namespace, name, resources)
		if err != nil {
			return nil, err
		}

		txnKey := transactionStorageKey(namespace, name)

		return client.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(txnKey), "=", 0)).
			Then(clientv3.OpPut(txnKey, string(payloadBytes))).
			Else(clientv3.OpGet(txnKey)).
			Commit()
	}

	unstructuredDecoder = kjson.NewSerializerWithOptions(
		kjson.DefaultMetaFactory,
		unstructuredscheme.NewUnstructuredCreator(),
		unstructuredscheme.NewUnstructuredObjectTyper(),
		kjson.SerializerOptions{Yaml: true, Pretty: false, Strict: false},
	)
)

type transactionRequest struct {
	Name      string
	Namespace string
	Create    [][]byte
	Update    [][]byte
	Delete    [][]byte
}

func buildTransactionCommitPayload(namespace, name string, resources map[string][]byte) ([]byte, error) {
	spec := map[string]interface{}{
		"resources": resources,
	}

	return json.Marshal(map[string]interface{}{
		"apiVersion": apiextv1.SchemeBuilder.GroupVersion.String(),
		"kind":       "Transaction",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec":  spec,
		"specs": spec,
	})
}

func decodeTransactionRequest(body []byte) (*transactionRequest, error) {
	var payload struct {
		Metadata map[string]interface{} `yaml:"metadata"`
		Spec     struct {
			Create []map[string]interface{} `yaml:"create"`
			Update []map[string]interface{} `yaml:"update"`
			Delete []map[string]interface{} `yaml:"delete"`
		} `yaml:"spec"`
	}

	if err := yaml.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	marshalEntries := func(entries []map[string]interface{}) ([][]byte, error) {
		result := make([][]byte, 0, len(entries))
		for i := range entries {
			entryBytes, err := yaml.Marshal(entries[i])
			if err != nil {
				return nil, err
			}
			result = append(result, entryBytes)
		}
		return result, nil
	}

	create, err := marshalEntries(payload.Spec.Create)
	if err != nil {
		return nil, fmt.Errorf("marshal create resources: %w", err)
	}
	update, err := marshalEntries(payload.Spec.Update)
	if err != nil {
		return nil, fmt.Errorf("marshal update resources: %w", err)
	}
	deleteResources, err := marshalEntries(payload.Spec.Delete)
	if err != nil {
		return nil, fmt.Errorf("marshal delete resources: %w", err)
	}

	request := &transactionRequest{
		Create: create,
		Update: update,
		Delete: deleteResources,
	}
	if payload.Metadata != nil {
		if name, ok := payload.Metadata["name"].(string); ok {
			request.Name = name
		}
		if namespace, ok := payload.Metadata["namespace"].(string); ok {
			request.Namespace = namespace
		}
	}

	return request, nil
}

func transactionRequestNamespace(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "namespaces" && parts[i+1] != "" {
			return parts[i+1]
		}
	}

	return ""
}

func transactionStorageKey(namespace, name string) string {
	return fmt.Sprintf("/harikube/transaction/%s/%s", namespace, name)
}

func getTransactionHandler(authClient *authorizationclientv1.AuthorizationV1Client, harikubeClient *clientv3.Client, coreResources []string, mapper *restmapper.DeferredDiscoveryRESTMapper) *kaf.APIKind {
	coreResourcesMap := map[string]bool{}
	for i := range coreResources {
		coreResourcesMap[coreResources[i]] = true
	}

	return &kaf.APIKind{
		ApiResource: metav1.APIResource{
			Name:         "transactionrequests",
			SingularName: "transactionrequest",
			Namespaced:   true,
			Kind:         "TransactionRequest",
			Verbs:        []string{"create"},
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

				transaction, err := decodeTransactionRequest(body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)

					return
				}

				requestNamespace := namespace
				if requestNamespace == "" {
					requestNamespace = transactionRequestNamespace(r.URL.Path)
				}
				if transaction.Namespace != "" && requestNamespace != "" && transaction.Namespace != requestNamespace {
					http.Error(w, "metadata.namespace does not match request namespace", http.StatusBadRequest)

					return
				}
				if requestNamespace == "" {
					requestNamespace = transaction.Namespace
				}

				if err := transactionAuthorizeAll(ctx, authClient, r.Header, transaction, requestNamespace, mapper); err != nil {
					http.Error(w, "resource forbidden", http.StatusForbidden)

					return
				}

				resources := map[string][]byte{}

				for _, entry := range transaction.Create {
					if err := transactionResourceMap(resources, entry, requestNamespace, "create", coreResourcesMap, mapper); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)

						return
					}
				}
				for _, entry := range transaction.Update {
					if err := transactionResourceMap(resources, entry, requestNamespace, "update", coreResourcesMap, mapper); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)

						return
					}
				}
				for _, entry := range transaction.Delete {
					if err := transactionResourceMap(resources, entry, requestNamespace, "delete", coreResourcesMap, mapper); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)

						return
					}
				}

				if len(resources) == 0 {
					http.Error(w, "empty transaction spec", http.StatusBadRequest)

					return
				}

				txnName := transaction.Name
				if txnName == "" {
					txnName = name
				}
				if txnName == "" {
					txnName = fmt.Sprintf("txn-%d", time.Now().UnixNano())
				}

				logger := transactionLogger.WithValues("namespace", requestNamespace, "name", txnName, "operations", len(resources))
				logger.Info("Creating transaction")

				commitResp, err := transactionCommit(ctx, harikubeClient, requestNamespace, txnName, resources)
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

				resp := apiextv1.TransactionResponse{
					TypeMeta: metav1.TypeMeta{
						APIVersion: apiextv1.SchemeBuilder.GroupVersion.String(),
						Kind:       "TransactionResponse",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: requestNamespace,
						Name:      txnName,
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
					logger.Info("Write error", "error", err)

					return
				}
			},
		},
	}
}

func transactionResourceMap(resources map[string][]byte, entry []byte, requestNamespace, operation string, coreResourcesMap map[string]bool, mapper *restmapper.DeferredDiscoveryRESTMapper) error {
	obj := &unstructured.Unstructured{}
	if _, _, err := unstructuredDecoder.Decode(entry, nil, obj); err != nil {
		return err
	}

	apiVersion, _ := obj.Object["apiVersion"].(string)
	kind, _ := obj.Object["kind"].(string)
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
	if metaEntry, ok := obj.Object["metadata"].(map[interface{}]interface{}); ok {
		if metaMap, ok := metaEntry["namespace"]; ok {
			if nsStr, ok := metaMap.(string); ok && nsStr != "" {
				ns = nsStr
			}
		}
	} else if metaEntry, ok := obj.Object["metadata"].(map[string]interface{}); ok {
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

	resourceName, _ := obj.Object["metadata"].(map[interface{}]interface{})
	var name string
	if resourceName != nil {
		if n, ok := resourceName["name"]; ok {
			name, _ = n.(string)
		}
	}
	if name == "" {
		if resourceName, ok := obj.Object["metadata"].(map[string]interface{}); ok {
			if n, ok := resourceName["name"]; ok {
				name, _ = n.(string)
			}
		}
	}
	if name == "" {
		return fmt.Errorf("missing metadata.name in %s resource", operation)
	}

	value, err := yaml.Marshal(obj.Object)
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
		items [][]byte
		verb  string
	}{
		{items: transaction.Create, verb: "create"},
		{items: transaction.Update, verb: "update"},
		{items: transaction.Delete, verb: "delete"},
	}

	for _, group := range entries {
		for i := range group.items {
			obj := &unstructured.Unstructured{}
			if _, _, err := unstructuredDecoder.Decode(group.items[i], nil, obj); err != nil {
				return err
			}

			apiVersion, _ := obj.Object["apiVersion"].(string)
			kind, _ := obj.Object["kind"].(string)
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
			if metaEntry, ok := obj.Object["metadata"].(map[interface{}]interface{}); ok {
				if metaMap, ok := metaEntry["namespace"]; ok {
					if nsStr, ok := metaMap.(string); ok && nsStr != "" {
						ns = nsStr
					}
				}
			} else if metaEntry, ok := obj.Object["metadata"].(map[string]interface{}); ok {
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
