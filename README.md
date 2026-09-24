# api-extension

This repository contains a Kubernetes API extension to implement advanced data management.

## Endpoints

> Examples are based on `kubectl`, but any client can do the same.

### Count

> HariKube - OpenSource: release-0.15.0 Enterprise: release-0.15.0

Returns the number of resources that match standard Kubernetes selectors without requiring the client to list and count objects manually. This is useful for quotas, reconciliation guards, dashboards, and application-level business rules.

```bash
# Cluster scope resource
kubectl get counts --field-selector=apiVersion=cert-manager.io,kind=ClusterIssuer
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=ClusterIssuer

# Namespace scope resource
kubectl get counts -A --field-selector=apiVersion=cert-manager.io,kind=Issuer
kubectl get counts --namespace default --field-selector=apiVersion=cert-manager.io,kind=Issuer

# Label selector
kubectl get counts --field-selector=apiVersion=cert-manager.io,kind=ClusterIssuer --selector=key=value

# Field selector
kubectl get counts --field-selector=apiVersion=cert-manager.io,kind=ClusterIssuer,.spec.field=value

# Full name call
kubectl get counts.apiserver.api-extension.harikube.info --field-selector=apiVersion=cert-manager.io,kind=ClusterIssuer

# Get count only
kubectl get counts --field-selector=apiVersion=cert-manager.io/v1,kind=Issuer -o jsonpath='{.items[0].spec.count}'

# Raw call
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/counts?fieldSelector=apiVersion=cert-manager.io,kind=ClusterIssuer&labelSelector=key=value"
kubectl get --raw "/apis/apiserver.api-extension.harikube.info/v1/namespaces/default/counts?fieldSelector=apiVersion=cert-manager.io,kind=Issuer&labelSelector=key=value"
```

### Transaction

> HariKube - OpenSource: dev-v0.16.4-0 Enterprise: -

Executes multiple resource mutations as a single logical unit so applications can coordinate related creates, updates, and deletes together. This gives Kubernetes-backed workloads a safer primitive for multi-object state changes such as payments, provisioning, and workflow transitions.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/transactionrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: TransactionRequest
metadata:
  name: make-payment-XXX
spec:
  create:
  - apiVersion: v1
    kind: ConfigMap
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

Proposed endpoints should follow the same Kubernetes-friendly pattern: standard selectors can stay in native request fields when possible, and any non-standard input should be encapsulated in a request resource instead of custom CLI flags. For read-oriented APIs, a shared request shape is recommended so the same endpoint family can combine Kubernetes-native selectors, Kiine-specific expressions, pagination, ordering, projection, and grouping in a predictable way.

### Exists

Checks whether at least one object matches the target criteria, allowing clients to make fast presence checks without fetching full lists. This is useful for idempotency checks, conditional flows, and readiness logic.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/existsrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: ExistsRequest
metadata:
  name: wallet-AAA
spec:
  resource:
    apiVersion: v1
    kind: Secret
    namespace: default
  filters:
    names:
    - wallet-AAA
    labelSelector:
      matchLabels:
        app: payments
EOF

kubectl get existsrequests wallet-AAA -n default -o jsonpath='{.status.exists}'
```

### Query

Provides a general read API for application-style lookups over Kubernetes resources. It should be the main endpoint for combining native label selectors, native field selectors, Kiine-specific expressions, projection, ordering, pagination, grouping, count-style queries, distinct queries, and future query operators in a single structured request.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/queryrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: QueryRequest
metadata:
  name: running-api-pods
spec:
  resource:
    apiVersion: v1
    kind: Pod
    namespace: default
  filters:
    objectSelect:
    - expression: ABS(LENGTH(value) - LENGTH(prevValue)) AS diff
    objectWhere:
    - expression: uid != ?
      params:
      - f47ac10b-58cc-4372-a567-0e02b2c3d479
    label:
    - expression: key NOT LIKE '%?%'
      params:
      - foo
    fieldSelector:
    - expression: metadata.name = ?
      params:
      - buzz
#  distinct: ''
#  groupBy: ''
  orderBy: 'createRevision DESC'
  limit: 50
  offset: 0
  having: diff > 10
EOF
```

### Aggregates

Computes database-style aggregate functions such as `sum`, `min`, `max`, `avg`, and grouped counts over Kubernetes resources. This helps applications build metrics, quotas, reporting, and reconciliation decisions directly from cluster data.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/billing/aggregaterequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: AggregateRequest
metadata:
  name: deployment-ready-replicas
spec:
  resource:
    apiVersion: apps/v1
    kind: Deployment
    namespace: billing
  filters:
    labelSelector:
      matchLabels:
        team: payments
  aggregate:
    op: sum
    path: .status.readyReplicas
EOF

cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/aggregaterequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: AggregateRequest
metadata:
  name: pods-per-node
spec:
  resource:
    apiVersion: v1
    kind: Pod
  aggregate:
    op: count
    groupBy:
    - .spec.nodeName
EOF
```

### Text Search

Provides full-text indexing and search over projected Kubernetes resource fields such as titles, descriptions, annotations, and tags. Following the aggregation-layer pattern from the microservice blog post, the API server can maintain a dedicated metadata index, return scored matches plus highlights, and optionally feed the matched object references back into `Query` for richer structured filtering.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/textsearchrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: TextSearchRequest
metadata:
  name: tasks-about-payments
spec:
  resource:
    apiVersion: stable.harikube.info/v1
    kind: Task
    namespace: default
  index:
    analyzer: standard
    fields:
    - path: .metadata.name
      weight: 2
    - path: .spec.title
      weight: 5
    - path: .spec.description
      weight: 3
    - path: .spec.tags[*]
      weight: 2
  search:
    query: '"payment retry" OR invoice'
    defaultOperator: and
    fuzziness: 1
  project:
  - path: .metadata.uid
  - path: .metadata.name
  - path: .spec.title
  - path: .spec.status
  limit: 20
EOF

kubectl get textsearchrequests tasks-about-payments -n default -o jsonpath='{.status.hits[*].object.metadata.name}'
```

### Distinct

Returns the unique values for a projected field across matching resources. This is useful for discovering active tenants, regions, labels, node names, or any other deduplicated application dimension stored in Kubernetes objects.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/distinctrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: DistinctRequest
metadata:
  name: pod-node-names
spec:
  resource:
    apiVersion: v1
    kind: Pod
  project:
    path: .spec.nodeName
EOF

cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/billing/distinctrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: DistinctRequest
metadata:
  name: deployment-app-labels
spec:
  resource:
    apiVersion: apps/v1
    kind: Deployment
    namespace: billing
  project:
    path: .metadata.labels.app
EOF
```

### Get By UID

Fetches a resource directly by Kubernetes UID, which is useful for event correlation, idempotency records, audit trails, and application workflows that store stable object identity rather than mutable names.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/uidrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: UIDRequest
metadata:
  name: pod-by-uid
spec:
  resource:
    apiVersion: v1
    kind: Pod
    namespace: default
  uid: f47ac10b-58cc-4372-a567-0e02b2c3d479
EOF
```

### Owned Resources

Returns all resources owned by a given parent object using Kubernetes `ownerReferences`. This is useful for garbage-collection analysis, topology inspection, debugging controllers, and application workflows that need to enumerate everything created under one logical owner.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/ownedrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: OwnedRequest
metadata:
  name: deployment-owned-resources
spec:
  owner:
    apiVersion: apps/v1
    kind: Deployment
    namespace: default
    name: user-service
  options:
    recursive: true
EOF
```

### Find By Owners

Finds resources whose `ownerReferences` match one or more owners, making ownership a first-class query primitive. This is useful when applications want to search dependents by owner UID, owner kind, or owner name across namespaces and resource types.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/findbyownerrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: FindByOwnerRequest
metadata:
  name: secrets-owned-by-user-service
spec:
  resource:
    apiVersion: v1
    kind: Secret
  owners:
  - apiVersion: apps/v1
    kind: Deployment
    namespace: default
    name: user-service
  - apiVersion: batch/v1
    kind: Job
    namespace: default
    name: payment-sync
  match:
    mode: any
EOF
```

### Upsert

Creates a resource when it does not exist and updates it when it already exists, giving clients a single idempotent write primitive. This is useful for profiles, ledgers, checkpoints, and any application state that should converge without separate read-before-write logic.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/upsertrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: UpsertRequest
metadata:
  name: wallet-AAA
spec:
  match:
    apiVersion: v1
    kind: Secret
    metadata:
      name: wallet-AAA
  create:
    stringData:
      balance: "100"
  update:
    stringData:
      balance: "150"
EOF
```

### Compare And Set

Applies an update only when the target resource still matches an expected value, providing optimistic concurrency control for application data. This helps prevent lost updates and is especially useful for balances, state machines, counters, and workflow transitions.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/compareandsetrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: CompareAndSetRequest
metadata:
  name: wallet-AAA-balance
spec:
  target:
    apiVersion: v1
    kind: Secret
    metadata:
      name: wallet-AAA
      resourceVersion: "6"
  condition:
    path: .data.balance
    equals: "MTAw"
  patch:
    stringData:
      balance: "150"
EOF
```

### Batch

Executes multiple operations in one request to reduce round trips and coordinate bulk changes. Compared with `Transaction`, this endpoint is better positioned as a throughput-oriented primitive for bulk processing, onboarding, cleanup, and maintenance tasks.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/batchrequests" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: BatchRequest
metadata:
  name: onboarding-AAA
spec:
  batchSize: 10
  create:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: profile-AAA
  - apiVersion: v1
    kind: Secret
    metadata:
      name: wallet-AAA
  delete:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: invitation-token-AAA
EOF
```

### Reservations

Reserves a unique logical key with optional expiration so distributed applications can safely claim identifiers before committing a larger workflow. This is useful for usernames, invoice numbers, seat allocation, idempotency keys, and short-lived locks.

```bash
cat <<EOF | kubectl create --raw "/apis/apiserver.api-extension.harikube.info/namespaces/default/reservations" -f -
apiVersion: apiserver.api-extension.harikube.info
kind: Reservation
metadata:
  name: username-john
spec:
  key: usernames/john
  owner:
    apiVersion: apps/v1
    kind: Deployment
    name: user-service
  ttlSeconds: 300
EOF
```
