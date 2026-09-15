package apiserver

import (
	"context"
	"net/http"

	goplural "github.com/gertd/go-pluralize"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	authorizationclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/restmapper"
)

var (
	pluralize = goplural.NewClient()
)

func pluraliza() *goplural.Client {
	return pluralize
}

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
