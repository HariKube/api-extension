package apiserver

import (
	"context"
	"net/http"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	Group   = "apiserver.api-extension.harikube.info"
	Version = "v1"
)

var (
	logger = logf.Log.WithName("api-extension")
)

func New(port, certFile, keyFile, harikubeUrl, harikubeCertFile, harikubeKeyFile string) *searchAPIServer {
	sas := searchAPIServer{
		Server: *kaf.NewServer(kaf.ServerConfig{
			Port:     port,
			CertFile: certFile,
			KeyFile:  keyFile,
			Group:    Group,
			Version:  Version,
			APIKinds: []kaf.APIKind{
				{
					ApiResource: metav1.APIResource{
						Name:       "counts",
						Namespaced: true,
						Kind:       "Count",
						Verbs:      []string{"get", "list"},
					},
					CustomResource: &kaf.CustomResource{
						GetHandler: func(namespace, name string, w http.ResponseWriter, r *http.Request) {
							w.WriteHeader(http.StatusOK)
							w.Write([]byte("hello get"))
						},
						ListHandler: func(namespace, name string, w http.ResponseWriter, r *http.Request) {
							w.WriteHeader(http.StatusOK)
							w.Write([]byte("hello list"))
						},
					},
				},
			},
		}),
	}

	return &sas
}

type searchAPIServer struct {
	kaf.Server
}

func (s *searchAPIServer) Start(ctx context.Context) (err error) {
	return s.Server.Start(ctx)
}
