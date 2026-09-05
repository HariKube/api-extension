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
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/counts?fieldSelector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer&labelSelector=key=value"
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/counts?fieldSelector=apiVersion=cert-manager.io/v1,kind=Issuer&labelSelector=key=value"
```

## Future Endpoints

> Examples are based on `kubectl`, but any client can do the same.

### Transaction

cat <<EOF | kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/transactions" -X POST -H "Content-Type: application/yaml" -f -
apiVersion: apiserver.api-extension.harikube.info/v1
kind: Transaction
metadata:
    name: make-payment-XXX
spec:
    resources:
    - apiVersion: v1
      kind: Secret
      metadata:
        name: wallet-AAA
      ...
    - apiVersion: v1
      kind: Secret
      metadata:
        name: wallet-BBB
      ...
EOF

### Custom Queries

cat <<EOF | kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/queries" -X POST -H "Content-Type: application/yaml" -f -
apiVersion: apiserver.api-extension.harikube.info/v1
kind: Query
metadata:
    name: uid-not-equal-XXX
spec:
  query: uid != ?
  params:
  - f47ac10b-58cc-4372-a567-0e02b2c3d479
EOF
