package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	goplural "github.com/gertd/go-pluralize"
	apiextv1 "github.com/harikube/api-extension/api/v1"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.yaml.in/yaml/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var countLogger = logf.Log.WithName("api-extension.count")

// nolint:gocyclo
func getCountHandler(kubeConfig *rest.Config, harikubeClient *clientv3.Client, coreResources []string) (*kaf.APIKind, error) {
	kubeClient, err := discovery.NewDiscoveryClientForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(kubeClient))

	getResurce := func(gvk schema.GroupVersionKind) (*meta.RESTMapping, error) {
		m, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			mapper.Reset()

			return mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		}

		return m, nil
	}

	pluralize := goplural.NewClient()

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

				resource, err := getResurce(gvk)
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
				prefix += pluralize.Plural(strings.ToLower(gvk.Kind)) + "/"
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

				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()

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

				accept := r.Header.Get("Content-Type")
				if accept == "" {
					accept = strings.Split(strings.Join(r.Header.Values("Accept"), ","), ",")[0]
				}
				contentType, contentDetails, _ := strings.Cut(accept, ";")

				var container any
				switch contentDetails {
				case "as=Table;v=v1;g=meta.k8s.io":
					respRaw, err := json.Marshal(&resp)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)

						return
					}

					container = metav1.Table{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "meta.k8s.io/v1",
							Kind:       "Table",
						},
						ListMeta: metav1.ListMeta{
							ResourceVersion: resp.ResourceVersion,
						},
						ColumnDefinitions: []metav1.TableColumnDefinition{
							{
								Name:   "Name",
								Type:   "string",
								Format: "name",
							},
							{
								Name: "Count",
								Type: "integer",
							},
						},
						Rows: []metav1.TableRow{
							{
								Cells: []interface{}{
									kind, countResp.Count,
								},
								Object: runtime.RawExtension{
									Object: &resp,
									Raw:    respRaw,
								},
							},
						},
					}
				case "as=Table;v=v1beta1;g=meta.k8s.io":
					respRaw, err := json.Marshal(&resp)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)

						return
					}

					container = metav1beta1.Table{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "meta.k8s.io/v1beta1",
							Kind:       "Table",
						},
						ListMeta: metav1.ListMeta{
							ResourceVersion: resp.ResourceVersion,
						},
						ColumnDefinitions: []metav1.TableColumnDefinition{
							{
								Name:   "Name",
								Type:   "string",
								Format: "name",
							},
							{
								Name: "Count",
								Type: "integer",
							},
						},
						Rows: []metav1.TableRow{
							{
								Cells: []interface{}{
									kind, countResp.Count,
								},
								Object: runtime.RawExtension{
									Object: &resp,
									Raw:    respRaw,
								},
							},
						},
					}
				default:
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

				containerRaw := []byte{}
				switch contentType {
				case "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml":
					if containerRaw, err = yaml.Marshal(&container); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)

						return
					}

					w.Header().Set("Content-Type", "application/yaml")
				case "application/json":
					if containerRaw, err = json.Marshal(&container); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)

						return
					}

					w.Header().Set("Content-Type", "application/json")
				default:

				}

				w.WriteHeader(http.StatusOK)
				if _, err := w.Write(containerRaw); err != nil {
					logger.Info("Write error", "error", err)

					return
				}
			},
		},
	}, nil
}
