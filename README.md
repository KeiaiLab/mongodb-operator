# MongoDB Operator for Kubernetes

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://golang.org/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes)](https://kubernetes.io/)
[![Build Status](https://github.com/eightynine01/mongodb-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/eightynine01/mongodb-operator/actions)
[![GitHub Container Registry](https://img.shields.io/docker/image-size/ghcr.io/eightynine01/mongodb-operator/latest)](https://github.com/eightynine01/mongodb-operator/pkgs/container/mongodb-operator)
[![Helm Chart](https://img.shields.io/badge/dynamic/yaml?url=https://raw.githubusercontent.com/eightynine01/mongodb-operator/main/charts/mongodb-operator/Chart.yaml&label=helm%20v)](https://github.com/eightynine01/mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/eightynine01/mongodb-operator)](https://goreportcard.com/report/github.com/eightynine01/mongodb-operator)
[![codecov](https://codecov.io/gh/eightynine01/mongodb-operator/branch/main/graph/badge.svg)](https://codecov.io/gh/eightynine01/mongodb-operator)

A Kubernetes Operator for deploying and managing MongoDB ReplicaSets and Sharded Clusters.

## Overview

MongoDB Operator automates the deployment, scaling, and management of MongoDB clusters on Kubernetes. It provides a declarative way to manage MongoDB infrastructure using Custom Resource Definitions (CRDs).

### Features

- **MongoDB ReplicaSet**: Deploy highly available 3+ member replica sets with automatic failover
- **Sharded Cluster**: Deploy distributed clusters with config servers, shards, and mongos routers
- **TLS Encryption**: Automatic TLS certificate management with cert-manager integration
- **Authentication**: SCRAM-SHA-256 authentication with keyfile support for internal cluster communication
- **Monitoring**: Prometheus metrics export with ServiceMonitor support
- **Backup/Restore**: Automated backups to S3-compatible storage or PVC
- **Auto-scaling**: Horizontal Pod Autoscaler support for Mongos routers

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
- Helm v3.8+
- kubectl configured with cluster access

### Installation

```bash
# Add Helm repository
helm repo add mongodb-operator https://eightynine01.github.io/mongodb-operator
helm repo update

# Install the operator
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

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
    version: "8.2"
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
    version: "8.2"
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
| `spec.version.version` | MongoDB version | `8.2` |
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

상태 표기 기준:
- **✅ Stable**: envtest 회귀 + 단위 테스트 + 실제 mongod workload(testcontainers/kind/실 클러스터)에서 부하/내구 검증 완료. 증거(stress test 결과/incident 후처리) 보존.
- **✅ Implemented**: 코드 + envtest 회귀 + 단위 테스트로 *기능적* 정확성 확인. *부하 검증은 운영자 책임*.
- **⚠️ Beta**: 코드는 동작하나 단위 테스트만 일부, 실 환경 검증 없음 — 운영 환경에 적용 전 추가 검증 필요.

| Feature | Status | Notes |
|---------|--------|-------|
| ReplicaSet auto-initialization | ✅ Implemented | `rs.initiate()` automatic. envtest + driver 단위 테스트. |
| Sharded cluster initialization | ✅ Implemented | Config server, shards, mongos. envtest 검증. |
| Admin user creation | ✅ Implemented | Driver-based bootstrap with K8s Lease lock + post-bootstrap usersInfo verify. |
| Shard scale out (2→5) | ⚠️ Beta | Automatic `sh.addShard()` — driver 호출 검증, *실 cluster 부하* 미검증. |
| Shard scale in (5→2) | ⚠️ Beta | Automatic `removeShard()` + ShardDraining condition + resource cleanup (PVC retained). chunks 마이그레이션 long-running polling은 30s 고정(backoff 미적용). |
| Mongos replica scaling | ✅ Implemented | Deployment replicas 변경 → rolling. |
| Resource updates | ✅ Implemented | Rolling restart via STS UpdateStrategy. |
| Data integrity during scaling | ⚠️ Beta | 코드 흐름상 데이터 손실 차단(PVC retain, removeShard drain wait) — *실 데이터 부하 검증* 미수행. |
| Concurrent writes during scale | ⚠️ Beta | stress test 증거 없음. 향후 testcontainers-go 기반 부하 시험 예정. |
| PodDisruptionBudget automation | ✅ Implemented | opt-in via `spec.podDisruptionBudget` (MongoDB + Sharded). builder 단위 테스트로 4 컴포넌트 생성 검증. |
| NetworkPolicy automation | ✅ Implemented | opt-in via `spec.networkPolicy` (deny-by-default + additional peers). 단위 테스트로 cfg=27019/shard=27018/mongos=27017 포트 검증. *실 통신 차단 검증 미수행*. |
| Admin bootstrap race-free | ✅ Implemented | K8s Lease distributed lock(30s TTL) + post-bootstrap `usersInfo` verify. fake-client 단위 테스트(busy/takeover/release). holder pod crash 시 30s까지 다른 reconcile은 backoff. |

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

Contributions are welcome! Please read our [Contributing Guide](CONTRIBUTING.md) for details on our code of conduct and the process for submitting pull requests.

## Support

- **Issues**: [GitHub Issues](https://github.com/eightynine01/mongodb-operator/issues)
- **Discussions**: [GitHub Discussions](https://github.com/eightynine01/mongodb-operator/discussions)

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
