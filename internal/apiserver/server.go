package apiserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"time"

	kaf "github.com/HariKube/kubernetes-aggregator-framework/pkg/framework"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	authclientv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

const (
	Group   = "apiserver.api-extension.harikube.info"
	Version = "v1"
)

func New(kubeConfig *rest.Config, port, certFile, keyFile string, coreResources []string, harikubeUrls []string, harikubeCertFile, harikubeKeyFile string, harikubeCAFile string, harikubeSkipVerify bool, decisionMakerURL string, decisionMakerTimeout time.Duration) *searchAPIServer {
	sas := searchAPIServer{
		kubeConfig:           kubeConfig,
		port:                 port,
		certFile:             certFile,
		keyFile:              keyFile,
		coreResources:        coreResources,
		harikubeUrls:         harikubeUrls,
		harikubeCertFile:     harikubeCertFile,
		harikubeKeyFile:      harikubeKeyFile,
		harikubeCAFile:       harikubeCAFile,
		harikubeSkipVerify:   harikubeSkipVerify,
		decisionMakerURL:     decisionMakerURL,
		decisionMakerTimeout: decisionMakerTimeout,
	}

	return &sas
}

type searchAPIServer struct {
	kaf.Server
	kubeConfig           *rest.Config
	port                 string
	certFile             string
	keyFile              string
	coreResources        []string
	harikubeUrls         []string
	harikubeCertFile     string
	harikubeKeyFile      string
	harikubeCAFile       string
	harikubeSkipVerify   bool
	decisionMakerURL     string
	decisionMakerTimeout time.Duration
}

func (s *searchAPIServer) Start(ctx context.Context) (err error) {
	tlsConfig, err := s.clientConfig(s.harikubeUrls)
	if err != nil {
		return err
	}

	authClient, err := authclientv1.NewForConfig(s.kubeConfig)
	if err != nil {
		return err
	}

	harikubeClient, err := clientv3.New(clientv3.Config{
		Endpoints:            s.harikubeUrls,
		TLS:                  tlsConfig,
		DialTimeout:          5 * time.Second,
		DialKeepAliveTime:    10 * time.Second,
		DialKeepAliveTimeout: 3 * time.Second,
		AutoSyncInterval:     10 * time.Second,
		MaxUnaryRetries:      3,
		Logger:               zap.New(zapcore.NewNopCore()),
		MaxCallSendMsgSize:   16 * 1024 * 1024,
		MaxCallRecvMsgSize:   16 * 1024 * 1024,
		PermitWithoutStream:  false,
		DialOptions: []grpc.DialOption{
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(16*1024*1024),
				grpc.MaxCallSendMsgSize(16*1024*1024),
			),
		},
	})
	if err != nil {
		return err
	}

	discoveryKubeClient, err := discovery.NewDiscoveryClientForConfig(s.kubeConfig)
	if err != nil {
		return err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryKubeClient))

	countHandler := getCountHandler(authClient, harikubeClient, s.coreResources, mapper)
	transactionHandler := getTransactionHandler(authClient, harikubeClient, s.coreResources, mapper)
	queryHandler := getQueryHandler(authClient, harikubeClient)
	decisionHandler := getDecisionHandler(decisionHandlerConfig{
		authorize: func(
			ctx context.Context,
			resourceAttributes *authorizationv1.ResourceAttributes,
			headers http.Header,
		) (*authorizationv1.SubjectAccessReview, error) {
			return decisionSubjectAccessReview(ctx, authClient, resourceAttributes, headers)
		},
		decisionMakerURL:     s.decisionMakerURL,
		decisionMakerTimeout: s.decisionMakerTimeout,
	})

	s.Server = *kaf.NewServer(kaf.ServerConfig{
		Port:     s.port,
		CertFile: s.certFile,
		KeyFile:  s.keyFile,
		Group:    Group,
		Version:  Version,
		APIKinds: []kaf.APIKind{
			*countHandler,
			*transactionHandler,
			*queryHandler,
			*decisionHandler,
		},
	})

	return s.Server.Start(ctx)
}

func (s *searchAPIServer) clientConfig(urls []string) (*tls.Config, error) {
	if s.harikubeCertFile == "" || s.harikubeKeyFile == "" || s.harikubeCAFile == "" {
		for i := range urls {
			if strings.HasPrefix(urls[i], "https://") {
				return nil, errors.New("missing harikube TLS config")
			}
		}

		return nil, nil
	}

	info := &transport.TLSInfo{
		CertFile:           s.harikubeCertFile,
		KeyFile:            s.harikubeKeyFile,
		TrustedCAFile:      s.harikubeCAFile,
		InsecureSkipVerify: s.harikubeSkipVerify,
	}

	tlsConfig, err := info.ClientConfig()
	if err != nil {
		return nil, err
	}

	return tlsConfig, nil
}
