# api-extension
This repository contains a Kubernetes API extension to implement advanced data management.

## Endpoints

### Count

```bash
# Cluster scope resource
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer

# Namespace scope resource
kubectl get counts --namespace default --field-selector=apiVersion=cert-manager.io/v1,kind=Issuer

# Label selector
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer --selector=key=value

# Field selector
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer,.spec.field=value

# Full name call
kubectl get counts.apiserver.api-extension.harikube.info --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer

# Raw call
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/counts?fieldSelector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer&labelSelector=key=value"
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/counts?fieldSelector=apiVersion=cert-manager.io/v1,kind=Issuer&labelSelector=key=value"
```