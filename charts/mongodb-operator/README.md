# MongoDB Operator Helm Chart

A Kubernetes Operator for deploying and managing MongoDB ReplicaSets and Sharded Clusters.

## Features

- **MongoDB ReplicaSet**: Deploy highly available 3+ member replica sets
- **Sharded Cluster**: Deploy distributed clusters with config servers, shards, and mongos routers
- **TLS Encryption**: Automatic TLS with cert-manager integration
- **Authentication**: SCRAM-SHA-256 authentication with keyfile support
- **Monitoring**: Prometheus metrics with ServiceMonitor support
- **Backup/Restore**: Automated backups to S3 or PVC storage
- **Auto-scaling**: HPA support for Mongos routers

## Prerequisites

- Kubernetes 1.26+
- Helm 3.8+
- kubectl configured to communicate with your cluster

### Optional Dependencies

- [cert-manager](https://cert-manager.io/) for TLS certificate management
- [Prometheus Operator](https://prometheus-operator.dev/) for metrics collection
- S3-compatible storage for backups (e.g., AWS S3, MinIO, Ceph ObjectStore)

## Installation

### Add the Helm Repository

```bash
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update
```

### Install the Chart

```bash
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

### Install with Custom Values

```bash
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace \
  --set replicaCount=1 \
  --set metrics.serviceMonitor.enabled=true
```

## Configuration

See [values.yaml](./values.yaml) for the full list of configurable parameters.

### Common Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of operator replicas | `1` |
| `image.repository` | Operator image repository | `keiailab/mongodb-operator` |
| `image.tag` | Operator image tag | Chart appVersion |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |

### RBAC Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create service account | `true` |
| `rbac.create` | Create RBAC resources | `true` |
| `rbac.clusterScope` | Use ClusterRole | `true` |

### Metrics Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metrics.enabled` | Enable metrics endpoint | `true` |
| `metrics.secure` | Enable HTTPS for metrics | `true` |
| `metrics.serviceMonitor.enabled` | Create ServiceMonitor | `false` |
| `metrics.serviceMonitor.interval` | Scrape interval | `30s` |

### Resource Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `resources.requests.cpu` | CPU request | `100m` |
| `resources.requests.memory` | Memory request | `128Mi` |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `512Mi` |

## Usage

### Create a MongoDB ReplicaSet

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  members: 3
  version:
    version: "8.3.1"
  storage:
    storageClassName: standard
    size: 10Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin
  monitoring:
    enabled: true
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: letsencrypt
        kind: ClusterIssuer
```

### Create a Sharded Cluster

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: my-sharded
  namespace: database
spec:
  version:
    version: "8.3.1"
  configServer:
    members: 3
    storage:
      size: 5Gi
  shards:
    count: 3
    membersPerShard: 3
    storage:
      size: 50Gi
  mongos:
    replicas: 2
    service:
      type: LoadBalancer
    autoScaling:
      enabled: true
      minReplicas: 2
      maxReplicas: 10
```

### Create a Backup

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: my-backup
  namespace: database
spec:
  clusterRef:
    name: my-mongodb
    kind: MongoDB
  type: full
  compression: true
  compressionType: zstd
  storage:
    type: s3
    s3:
      bucket: mongodb-backups
      endpoint: https://s3.amazonaws.com
      credentialsRef:
        name: s3-credentials
```

## Custom Resource Definitions

The operator manages the following CRDs:

| CRD | Short Name | Description |
|-----|------------|-------------|
| `MongoDB` | `mdb` | MongoDB ReplicaSet |
| `MongoDBSharded` | `mdbsh` | MongoDB Sharded Cluster |
| `MongoDBBackup` | `mdbbackup` | Backup configuration |

### List MongoDB Resources

```bash
kubectl get mdb,mdbsh,mdbbackup -A
```

## Uninstallation

```bash
helm uninstall mongodb-operator -n mongodb-operator-system
```

**Note**: CRDs are not removed by default. To remove CRDs:

```bash
kubectl delete crd mongodbs.mongodb.keiailab.com
kubectl delete crd mongodbshardeds.mongodb.keiailab.com
kubectl delete crd mongodbbackups.mongodb.keiailab.com
```

## Upgrading

```bash
helm repo update
helm upgrade mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system
```

## Troubleshooting

### Check Operator Logs

```bash
kubectl logs -n mongodb-operator-system -l app.kubernetes.io/name=mongodb-operator -f
```

### Check MongoDB Status

```bash
kubectl describe mdb my-mongodb -n database
```

### Check Events

```bash
kubectl get events -n database --sort-by='.lastTimestamp'
```

## License

This project is licensed under the Apache License 2.0.

## Contributing

Contributions are welcome! Please read our [Contributing Guide](https://github.com/keiailab/mongodb-operator/blob/main/CONTRIBUTING.md) for details.

## Support

- [GitHub Issues](https://github.com/keiailab/mongodb-operator/issues)
- [Documentation](https://github.com/keiailab/mongodb-operator/blob/main/docs/)

## Secure metrics scraping with Prometheus

When `metrics.secure: true` the operator exposes `/metrics` over HTTPS behind
kube-rbac-proxy (or the controller-runtime native authenticated handler). For
Prometheus to scrape it, the ServiceMonitor must present a bearer token and the
Prometheus ServiceAccount must be allowed to invoke a TokenReview.

### Minimal setup with kube-prometheus-stack

```yaml
metrics:
  enabled: true
  secure: true
  serviceMonitor:
    enabled: true
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    tlsConfig:
      insecureSkipVerify: true   # or pin CA via caFile / ca.configMap
```

Grant the Prometheus ServiceAccount `system:auth-delegator` so kube-rbac-proxy's
TokenReview succeeds:

```bash
kubectl create clusterrolebinding prometheus-auth-delegator \
  --clusterrole=system:auth-delegator \
  --serviceaccount=monitoring:kube-prometheus-stack-prometheus
```

For a hardened setup, prefer `authorization.credentials` (referencing a Secret)
over `bearerTokenFile`, and pin the CA instead of `insecureSkipVerify`.
