package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	goplural "github.com/gertd/go-pluralize"
	"go.yaml.in/yaml/v2"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/restmapper"
)

var (
	pluralize = goplural.NewClient()
)

func subjectAccessReview(ctx context.Context, authClient *authorizationclientv1.AuthorizationV1Client, resourceAttributes *authorizationv1.ResourceAttributes, headers http.Header) (*authorizationv1.SubjectAccessReview, error) {
	sar := authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			ResourceAttributes: resourceAttributes,
			User:               headers.Get("X-Remote-User"),
			Groups:             headers.Values("X-Remote-Group"),
		},
	}

	return authClient.SubjectAccessReviews().Create(ctx, &sar, metav1.CreateOptions{})
}

func getResurce(gvk schema.GroupVersionKind, mapper *restmapper.DeferredDiscoveryRESTMapper) (*meta.RESTMapping, error) {
	m, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		mapper.Reset()

		return mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}

	return m, nil
}

func etcdKeyForGroupVersionKind(gvk schema.GroupVersionKind, namespace string, resource *meta.RESTMapping, coreResources map[string]bool) (string, error) {
	if resource == nil {
		return "", fmt.Errorf("resource mapping is required for %s", gvk.String())
	}

	key := "/registry/"
	if _, ok := coreResources[gvk.Group]; !ok {
		key += gvk.Group + "/"
	}
	key += resource.Resource.Resource + "/"
	if resource.Scope.Name() != meta.RESTScopeNameRoot && namespace != "" {
		key += namespace + "/"
	}

	return key, nil
}

func responseContent(headers http.Header) (contentType, contentDetails string) {
	accept := headers.Get("Content-Type")
	if accept == "" {
		accept = strings.Split(strings.Join(headers.Values("Accept"), ","), ",")[0]
	}

	contentType, contentDetails, _ = strings.Cut(accept, ";")

	return contentType, contentDetails
}

func tableResponse(contentDetails, resourceVersion string, columns []metav1.TableColumnDefinition, cells []interface{}, object runtime.Object) (any, bool, error) {
	if contentDetails != "as=Table;v=v1;g=meta.k8s.io" && contentDetails != "as=Table;v=v1beta1;g=meta.k8s.io" {
		return nil, false, nil
	}

	objectRaw, err := json.Marshal(object)
	if err != nil {
		return nil, false, err
	}

	row := metav1.TableRow{
		Cells: cells,
		Object: runtime.RawExtension{
			Object: object,
			Raw:    objectRaw,
		},
	}

	switch contentDetails {
	case "as=Table;v=v1;g=meta.k8s.io":
		return metav1.Table{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "meta.k8s.io/v1",
				Kind:       "Table",
			},
			ListMeta: metav1.ListMeta{
				ResourceVersion: resourceVersion,
			},
			ColumnDefinitions: columns,
			Rows:              []metav1.TableRow{row},
		}, true, nil
	case "as=Table;v=v1beta1;g=meta.k8s.io":
		return metav1beta1.Table{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "meta.k8s.io/v1beta1",
				Kind:       "Table",
			},
			ListMeta: metav1.ListMeta{
				ResourceVersion: resourceVersion,
			},
			ColumnDefinitions: columns,
			Rows:              []metav1.TableRow{row},
		}, true, nil
	default:
		return nil, false, nil
	}
}

func writeResponse(w http.ResponseWriter, statusCode int, container any, contentType string) error {
	var (
		containerRaw []byte
		err          error
	)

	switch contentType {
	case "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml":
		if containerRaw, err = yaml.Marshal(&container); err != nil {
			return err
		}

		w.Header().Set("Content-Type", "application/yaml")
	case "application/json":
		fallthrough
	default:
		if containerRaw, err = json.Marshal(&container); err != nil {
			return err
		}

		w.Header().Set("Content-Type", "application/json")
	}

	w.WriteHeader(statusCode)
	if _, err := w.Write(containerRaw); err != nil {
		return err
	}

	return nil
}
