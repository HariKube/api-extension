package apiserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	apiextv1 "github.com/harikube/api-extension/api/v1"
	clientv3 "go.etcd.io/etcd/client/v3"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var countLogger = logf.Log.WithName("api-extension.count")

// nolint:gocyclo
func getCountHandler(authClient *authorizationclientv1.AuthorizationV1Client, kubeConfig *rest.Config, harikubeClient *clientv3.Client, coreResources []string, mapper *restmapper.DeferredDiscoveryRESTMapper) (*kaf.APIKind, error) {
	coreResourcesMap := map[string]bool{}
	for i := range coreResources {
		coreResourcesMap[coreResources[i]] = true
	}

	return &kaf.APIKind{
		ApiResource: metav1.APIResource{
			Name:       "counts",
			Namespaced: true,
			Kind:       "CountResponse",
			Verbs:      []string{"list"},
		},
		CustomResource: &kaf.CustomResource{
			ListHandler: func(namespace, name string, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()

				fieldSelectors := strings.Split(query.Get("fieldSelector"), ",")

				apiVersion := ""
				kind := ""
				fieldSelector := ""
				for i := range fieldSelectors {
					fs := strings.TrimSpace(fieldSelectors[i])

					if strings.HasPrefix(fs, "apiVersion") {
						if _, value, ok := strings.Cut(fs, "="); ok {
							apiVersion = value
						}
					} else if strings.HasPrefix(fs, "kind") {
						if _, value, ok := strings.Cut(fs, "="); ok {
							kind = value
						}
					} else {
						if fieldSelector != "" {
							fieldSelector += ","
						}
						fieldSelector += fs
					}
				}
				if apiVersion == "" {
					http.Error(w, "missing apiVersion", http.StatusBadRequest)

					return
				} else if kind == "" {
					http.Error(w, "missing kind", http.StatusBadRequest)

					return
				}

				gvk := schema.GroupVersionKind{Kind: kind}
				if parts := strings.Split(apiVersion, "/"); len(parts) == 1 {
					gvk.Version = parts[0]
				} else {
					gvk.Group = parts[0]
					gvk.Version = parts[1]
				}
				gvr := schema.GroupVersionResource{
					Group:    gvk.Group,
					Version:  gvk.Version,
					Resource: pluralize.Plural(strings.ToLower(gvk.Kind)),
				}

				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()

				if result, err := subjectAccessReview(ctx, authClient,
					&authorizationv1.ResourceAttributes{
						Namespace: namespace,
						Verb:      "list",
						Group:     gvr.Group,
						Resource:  gvr.Resource,
					}, r.Header); err != nil {
					http.Error(w, "resource not found", http.StatusNotFound)

					return
				} else if !result.Status.Allowed {
					http.Error(w, "resource forbidden", http.StatusForbidden)

					return
				}

				resource, err := getResurce(gvk, mapper)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)

					return
				} else if resource == nil {
					http.Error(w, "resource not found", http.StatusNotFound)

					return
				}

				if resource.Scope.Name() == meta.RESTScopeNameRoot {
					namespace = ""
				}

				prefix := "/registry/"
				if _, ok := coreResourcesMap[gvk.Group]; !ok {
					prefix += gvk.Group + "/"
				}
				prefix += gvr.Resource + "/"
				if namespace != "" {
					prefix += namespace + "/"
				}

				logger := countLogger.WithValues("gvk", gvk.String(), "namespace", namespace, "selector", query.Get("labelSelector"), "field-selector", fieldSelector, "prefix", prefix)
				logger.Info("Counting")

				opts := []clientv3.OpOption{
					clientv3.WithCountOnly(),
					clientv3.WithPrefix(),
				}
				if ls := query.Get("labelSelector"); ls != "" {
					opts = append(opts, clientv3.WithLabelSelector(ls))
				}
				if fieldSelector != "" {
					opts = append(opts, clientv3.WithFieldSelector(fieldSelector))
				}

				countResp, err := harikubeClient.Get(ctx, prefix, opts...)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)

					return
				} else if countResp == nil {
					http.Error(w, "prefix not found", http.StatusNotFound)

					return
				}

				resp := apiextv1.CountResponse{
					TypeMeta: metav1.TypeMeta{
						APIVersion: apiextv1.SchemeBuilder.GroupVersion.String(),
						Kind:       "CountResponse",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      kind,
						CreationTimestamp: metav1.Time{
							Time: time.Now(),
						},
						ResourceVersion: fmt.Sprintf("%d", countResp.Header.Revision),
					},
					Spec: apiextv1.CountResponseSpec{
						Count: countResp.Count,
					},
				}

				contentType, contentDetails := responseContent(r.Header)

				columns := []metav1.TableColumnDefinition{
					{
						Name:   "Name",
						Type:   "string",
						Format: "name",
					},
					{
						Name: "Count",
						Type: "integer",
					},
				}
				cells := []interface{}{kind, countResp.Count}

				container, ok, err := tableResponse(contentDetails, resp.ResourceVersion, columns, cells, &resp)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)

					return
				}
				if !ok {
					container = apiextv1.CountResponseList{
						TypeMeta: metav1.TypeMeta{
							APIVersion: apiextv1.SchemeBuilder.GroupVersion.String(),
							Kind:       "CountResponseList",
						},
						ListMeta: metav1.ListMeta{
							ResourceVersion: resp.ResourceVersion,
						},
						Items: []apiextv1.CountResponse{resp},
					}
				}

				if err := writeResponse(w, http.StatusOK, container, contentType); err != nil {
					logger.Info("Write error", "error", err)

					return
				}
			},
		},
	}, nil
}
