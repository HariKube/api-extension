# api-extension

This repository contains a Kubernetes API extension to implement advanced data management.

## Endpoints

> Examples are based on `kubectl`, but any client can do the same.

### Count

```bash
# Cluster scope resource
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer

# Namespace scope resource
kubectl get counts -A --field-selector=apiVersion=cert-manager.io/v1,kind=Issuer
kubectl get counts --namespace default --field-selector=apiVersion=cert-manager.io/v1,kind=Issuer

# Label selector
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer --selector=key=value

# Field selector
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer,.spec.field=value

# Full name call
kubectl get counts.apiserver.api-extension.harikube.info --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer

# Get count only
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=Issuer -o jsonpath='{.items[0].spec.count}'

# Raw call
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/counts?fieldSelector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer&labelSelector=key=value"
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/counts?fieldSelector=apiVersion=cert-manager.io/v1,kind=Issuer&labelSelector=key=value"
```

### Transaction

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactions" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: Transaction
metadata:
  name: make-payment-XXX
spec:
  create:
  - apiVersion: v1
    kind: Configmap
    metadata:
      name: user-payment-AAA
  update:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: wallet-AAA
      resourceVersion: "6"
  - apiVersion: v1
    kind: Secret
    metadata:
      name: wallet-BBB
      resourceVersion: "2"
  delete:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: token-BBB
      resourceVersion: "9"
EOF
```

## Future Endpoints

> Examples are based on `kubectl`, but any client can do the same.

### Custom Queries

```bash
cat <<EOF | kubectl get --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/queries" -X POST -H "Content-Type: application/yaml" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: Query
metadata:
    name: uid-not-equal-XXX
spec:
  query: uid != ?
  params:
  - f47ac10b-58cc-4372-a567-0e02b2c3d479
EOF
```
