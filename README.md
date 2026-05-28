<p align="center">
  <img src="docs/branding/logo.png" alt="MongoDB Operator" width="360"/>
</p>

# mongodb-operator

> **Apache-2.0 MongoDB Operator for Kubernetes — ReplicaSet + Sharded Cluster + Backup, vanilla MongoDB 8.x**

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License"/></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go" alt="Go Version"/></a>
  <a href="https://www.mongodb.com/"><img src="https://img.shields.io/badge/MongoDB-7.0%2B-47A248?logo=mongodb" alt="MongoDB"/></a>
  <a href="https://kubernetes.io/"><img src="https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes" alt="Kubernetes"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator"><img src="https://img.shields.io/badge/ghcr.io-keiailab%2Fmongodb--operator-blue?logo=github" alt="Container Image"/></a>
  <a href="https://github.com/keiailab/mongodb-operator"><img src="https://img.shields.io/badge/dynamic/yaml?url=https://raw.githubusercontent.com/keiailab/mongodb-operator/main/charts/mongodb-operator/Chart.yaml&label=helm%20v" alt="Helm Chart"/></a>
  <a href="https://artifacthub.io/packages/search?repo=mongodb-operator"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/mongodb-operator" alt="Artifact Hub"/></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/keiailab/mongodb-operator"><img src="https://api.scorecard.dev/projects/github.com/keiailab/mongodb-operator/badge" alt="OpenSSF Scorecard"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/discussions"><img src="https://img.shields.io/github/discussions/keiailab/mongodb-operator?label=discussions&logo=github" alt="GitHub Discussions"/></a>
</p>

<p align="center">
  <b>English</b> |
  <a href="docs/i18n/ko/readme.md">한국어</a> |
  <a href="docs/i18n/ja/readme.md">日本語</a> |
  <a href="docs/i18n/zh/readme.md">中文</a>
</p>

---

A Kubernetes Operator for deploying and managing MongoDB ReplicaSets and Sharded Clusters.

## Overview

MongoDB Operator automates the deployment, scaling, and management of MongoDB clusters on Kubernetes. It provides a declarative way to manage MongoDB infrastructure using Custom Resource Definitions (CRDs).

### Features

- **MongoDB ReplicaSet**: Deploy highly available 3+ member replica sets with automatic failover
- **Sharded Cluster**: Deploy distributed clusters with config servers, shards, and mongos routers
- **TLS Encryption**: Automatic TLS certificate management with cert-manager integration
- **Authentication**: SCRAM-SHA-256, X.509, and LDAP authentication support
- **Monitoring**: Prometheus metrics, ServiceMonitor, PrometheusRule, and Grafana dashboards
- **Backup/Restore**: Automated backups to S3-compatible storage or PVC via `MongoDBBackup` CRD
- **Auto-scaling**: Horizontal Pod Autoscaler support for Mongos routers
- **Network Security**: NetworkPolicy automation with deny-by-default + PodDisruptionBudget
- **Production Hardened**: Priority classes, topology spread, volume permissions, diagnostic mode

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    MongoDB Operator                              │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │  MongoDB    │  │ MongoDBShar │  │    MongoDBBackup        │  │
│  │  Controller │  │ Controller  │  │    Controller           │  │
│  └──────┬──────┘  └──────┬──────┘  └───────────┬─────────────┘  │
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  Resource Builder                           ││
│  │  (StatefulSets, Deployments, Services, Secrets, Jobs)       ││
│  └─────────────────────────────────────────────────────────────┘│
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  MongoDB Package                            ││
│  │  (Executor, ReplicaSet, Auth, Sharding)                     ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
├─────────────────────────────────────────────────────────────────┤
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │  StatefulSet  │  │  StatefulSet  │  │  Deployment   │        │
│  │  (ReplicaSet) │  │  (Shards)     │  │  (Mongos)     │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
│                                                                  │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │   Services    │  │    Secrets    │  │  ConfigMaps   │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
└─────────────────────────────────────────────────────────────────┘
```

### Automatic Initialization

The operator automatically handles MongoDB cluster initialization:

**ReplicaSet Initialization:**
```
1. Create Keyfile Secret (for internal auth)
2. Create ConfigMap (mongod.conf)
3. Create Services (headless + client)
4. Create StatefulSet
5. Wait for all pods ready
6. Execute rs.initiate() on primary candidate
7. Wait for primary election
8. Create admin user (via localhost exception)
```

**Sharded Cluster Initialization:**
```
1. Create shared Keyfile Secret
2. Deploy Config Server StatefulSet (port 27019)
3. Deploy Shard StatefulSets (port 27018)
4. Deploy Mongos Deployment (port 27017)
5. Initialize Config Server ReplicaSet
6. Initialize each Shard ReplicaSet
7. Create admin user on Mongos
8. Execute sh.addShard() for each shard
```

### Port Configuration

| Component | Port | Flag |
|-----------|------|------|
| Mongos | 27017 | - |
| Shard | 27018 | `--shardsvr` |
| Config Server | 27019 | `--configsvr` |

## Quick Start

### Prerequisites

- Kubernetes cluster v1.26+
- kubectl configured with cluster access
- Additional requirements per install method:
  - **OLM v1** (recommended, modern): cert-manager + cluster admin (one-time bootstrap)
  - **Helm**: Helm v3.8+
  - **OLM v0** (legacy): Not recommended — use OLM v1 or Helm instead

### Installation — 3 paths (matrix)

| Method | Target audience | Modernity | Steps |
|---|---|---|---|
| **OLM v1** *(recommended)* | external users, GitOps platforms (ArgoCD App-of-Apps), Day-0 production | **next-generation** (v1.8.0, 2026-02 GA) | 2 manifests (ClusterCatalog + ClusterExtension) |
| Helm chart | local dev, single-cluster simple deploy | stable | 1 command (`helm install`) |
| OLM v0 | OpenShift legacy, OperatorHub.io community | maintenance mode (v0.42, 2026-04) | 4 manifests + InstallPlan approve |

See [Installation Guide](docs/install.md) for detailed instructions.

#### Path 1 — OLM v1 (recommended)

```bash
# (1) OLM v1 cluster install — one-time bootstrap
curl -L -s https://github.com/operator-framework/operator-controller/releases/latest/download/install.sh | bash -s

# (2) Apply ClusterCatalog + ClusterExtension
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clustercatalog.yaml
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clusterextension.yaml

# (3) Verify installation
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s
```

#### Path 2 — Helm chart

```bash
# Add Helm repository
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

# Install the operator
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

<!-- Path 3 (OLM v0 legacy) removed — ADR-0028 Phase D. Use OLM v1 or Helm. -->


### Deploy a MongoDB ReplicaSet

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
```

```bash
# Create namespace and credentials
kubectl create namespace database
kubectl create secret generic mongodb-admin \
  --from-literal=username=admin \
  --from-literal=password=your-secure-password \
  -n database

# Deploy MongoDB
kubectl apply -f mongodb-replicaset.yaml
```

### Deploy a Sharded Cluster

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
```

## Custom Resource Definitions

### MongoDB (ReplicaSet)

| Field | Description | Default |
|-------|-------------|---------|
| `spec.members` | Number of replica set members | `3` |
| `spec.version.version` | MongoDB version | `8.3.1` |
| `spec.storage.storageClassName` | Storage class name | - |
| `spec.storage.size` | PVC size per member | `10Gi` |
| `spec.auth.mechanism` | Authentication mechanism | `SCRAM-SHA-256` |
| `spec.tls.enabled` | Enable TLS | `false` |
| `spec.monitoring.enabled` | Enable Prometheus metrics | `false` |
| `spec.arbiter.enabled` | Enable arbiter node | `false` |

### MongoDBSharded

| Field | Description | Default |
|-------|-------------|---------|
| `spec.configServer.members` | Config server replica count | `3` |
| `spec.shards.count` | Number of shards | `2` |
| `spec.shards.membersPerShard` | Members per shard | `3` |
| `spec.mongos.replicas` | Mongos router replicas | `2` |
| `spec.mongos.autoScaling.enabled` | Enable HPA for mongos | `false` |

## Scaling

### Horizontal Scale Out (Adding Shards)

The operator supports dynamic shard scaling. When you increase `spec.shards.count`, the operator automatically:

1. Creates new Shard StatefulSet and headless Service
2. Waits for all pods to become ready
3. Initializes the new shard's ReplicaSet (`rs.initiate()`)
4. Registers the new shard with mongos (`sh.addShard()`)
5. MongoDB balancer automatically migrates chunks to the new shard

**Example: Scale from 3 to 5 shards**

```bash
# Check current shard count
kubectl get mongodbsharded my-cluster -o jsonpath='{.spec.shards.count}'
# Output: 3

# Scale out to 5 shards
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"shards":{"count":5}}}'

# Monitor new shard pods
kubectl get pods -l app.kubernetes.io/component=shard

# Verify shards registered
kubectl exec -it my-cluster-mongos-xxx -c mongos -- \
  mongosh -u admin -p $PASSWORD --eval 'sh.status()'
```

**Status Tracking:**
```yaml
status:
  shardsInitialized: [true, true, true, true, true]
  shardsAdded: [true, true, true, true, true]
  shards:
    - name: my-cluster-shard-0
      phase: Running
    - name: my-cluster-shard-1
      phase: Running
    - name: my-cluster-shard-2
      phase: Running
    - name: my-cluster-shard-3
      phase: Running
    - name: my-cluster-shard-4
      phase: Running
```

### Vertical Scaling (Resource Adjustment)

Update resource requests/limits (triggers rolling restart):

```bash
kubectl patch mongodbsharded my-cluster --type='merge' -p '{
  "spec": {
    "shards": {
      "resources": {
        "requests": {"memory": "2Gi", "cpu": "1"},
        "limits": {"memory": "4Gi", "cpu": "2"}
      }
    }
  }
}'
```

### Mongos Replica Scaling

Scale mongos routers up or down:

```bash
# Scale up
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":3}}}'

# Scale down
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":1}}}'
```

## Resource Recommendations

### Minimum Requirements

| Component | Memory | CPU | Notes |
|-----------|--------|-----|-------|
| Config Server | 256Mi | 100m | 3 members required |
| Shard Member | 512Mi | 250m | Per replica |
| Mongos | 512Mi | 250m | 256Mi causes OOM |

### Production Recommendations

| Component | Memory | CPU | Storage |
|-----------|--------|-----|---------|
| Config Server | 1Gi | 500m | 10Gi SSD |
| Shard Member | 4Gi | 2 | 100Gi+ SSD |
| Mongos | 1Gi | 500m | - |

## Tested Features

Status legend:
- **✅ Stable**: Regression tests + unit tests + real mongod workload verification (testcontainers/kind/production cluster). Stress test evidence preserved.
- **✅ Implemented**: Code + envtest regression + unit tests confirm functional correctness. Load verification is operator responsibility.
- **⚠️ Beta**: Code works but limited unit tests, no production environment verification — additional testing recommended before production use.

| Feature | Status | Notes |
|---------|--------|-------|
| ReplicaSet auto-initialization | ✅ Implemented | rs.initiate() automatic. envtest + driver unit tests. |
| Sharded cluster initialization | ✅ Implemented | Config server, shards, mongos. envtest verified. |
| Admin user creation | ✅ Implemented | Driver-based bootstrap with K8s Lease lock + post-bootstrap usersInfo verify. |
| Shard scale out (2→5) | ⚠️ Beta | Automatic sh.addShard() — driver-level verified, production load testing recommended. |
| Shard scale in (5→2) | ⚠️ Beta | Automatic removeShard() + ShardDraining condition + resource cleanup (PVC retained). Chunk migration uses 30s fixed polling. |
| Mongos replica scaling | ✅ Implemented | Deployment replicas change triggers rolling update. |
| Resource updates | ✅ Implemented | Rolling restart via StatefulSet UpdateStrategy. |
| Data integrity during scaling | ⚠️ Beta | Data loss prevention via PVC retain + removeShard drain wait — production load testing recommended. |
| Concurrent writes during scale | ⚠️ Beta | Stress test coverage planned via testcontainers-go. |
| PodDisruptionBudget automation | ✅ Implemented | opt-in via spec.podDisruptionBudget (MongoDB + Sharded). Builder unit tests verify 4-component creation. |
| NetworkPolicy automation | ✅ Implemented | opt-in via spec.networkPolicy (deny-by-default + additional peers). Unit tests verify cfg=27019/shard=27018/mongos=27017 ports. |
| Admin bootstrap race-free | ✅ Implemented | K8s Lease distributed lock (30s TTL) + post-bootstrap usersInfo verify. Fake-client unit tests (busy/takeover/release). |

## Limitations

### Not Yet Supported

| Feature | Status | Workaround |
|---------|--------|------------|
| ReplicaSet member removal | ❌ Not implemented | Manual `rs.remove()` required |
| Automatic backup scheduling | ❌ Planned | Use external CronJob |
| Cross-cluster replication | ❌ Planned | - |
| Sharded Arbiter/Hidden topology | ⚠️ ReplicaSet only | Arbiter is supported in MongoDB CR; Sharded extension is on the roadmap |

### Known Issues

1. **Mongos Memory**: Minimum 512Mi recommended. 256Mi causes OOM under load.
2. **No graceful ReplicaSet member removal**: Scaling down ReplicaSet members doesn't call `rs.remove()` — only StatefulSet replicas are reduced.
3. **Scale-in PVC retention**: After `removeShard` completes, PVCs of the drained shard are intentionally retained to prevent accidental data loss. Operators are expected to delete them manually after verification.

### MongoDBBackup

| Field | Description | Default |
|-------|-------------|---------|
| `spec.clusterRef.name` | Target cluster name | - |
| `spec.clusterRef.kind` | Target cluster kind | `MongoDB` |
| `spec.type` | Backup type (full/incremental) | `full` |
| `spec.compression` | Enable compression | `true` |
| `spec.storage.type` | Storage type (s3/pvc) | `s3` |

## Configuration

### TLS with cert-manager

```yaml
spec:
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
```

### Prometheus Monitoring

```yaml
spec:
  monitoring:
    enabled: true
    prometheusRule:
      enabled: true
    serviceMonitor:
      interval: 30s
```

### Backup to S3

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: daily-backup
spec:
  clusterRef:
    name: my-mongodb
    kind: MongoDB
  storage:
    type: s3
    s3:
      bucket: mongodb-backups
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      credentialsRef:
        name: s3-credentials
```

## Development

### Prerequisites

- Go 1.21+
- Docker
- kubectl
- Kind or Minikube (for local testing)

### Building

```bash
# Build the operator
make build

# Run tests
make test

# Build Docker image
make docker-build IMG=your-registry/mongodb-operator:tag

# Push Docker image
make docker-push IMG=your-registry/mongodb-operator:tag
```

### Local Development

```bash
# Install CRDs
make install

# Run the operator locally
make run

# Create a sample MongoDB
kubectl apply -f config/samples/mongodb_replicaset.yaml
```

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

### Third-Party Licenses

This operator manages MongoDB databases but does not include or distribute MongoDB software. MongoDB Community Server is licensed under the [Server Side Public License (SSPL)](https://www.mongodb.com/licensing/server-side-public-license).

**Important License Notes:**
- This operator (Apache 2.0) is independent software that orchestrates MongoDB deployments
- MongoDB container images are pulled from official MongoDB repositories
- Users are responsible for complying with MongoDB's licensing terms
- The operator does not modify or redistribute MongoDB binaries

## Contributing

Contributions are welcome! Please read our [Contributing Guide](docs/contributing.md) for details on our code of conduct and the process for submitting pull requests.

## Support

- **Issues**: [GitHub Issues](https://github.com/keiailab/mongodb-operator/issues)
- **Discussions**: [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)

## Roadmap

- [x] Automatic ReplicaSet initialization
- [x] Automatic Sharded Cluster initialization
- [x] Horizontal shard scaling (scale out)
- [x] Admin user auto-creation
- [ ] Point-in-Time Recovery (PITR)
- [ ] Automated version upgrades
- [ ] Cross-cluster replication
- [ ] Grafana dashboard templates
- [ ] Backup scheduling with CronJob
- [ ] Scale down with data migration

## Acknowledgments

- [Kubernetes](https://kubernetes.io/)
- [Operator SDK](https://sdk.operatorframework.io/)
- [MongoDB](https://www.mongodb.com/)
- [Bitnami MongoDB Charts](https://github.com/bitnami/charts) for inspiration

---

<p align="center">© 2026 keiailab · Apache-2.0 · <a href="https://github.com/keiailab">keiailab.com</a></p>
