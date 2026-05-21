# Testing Strategy

This document defines the testing approach, coverage goals, and test scenarios for the MongoDB Operator.

## Test Pyramid

```
            ┌─────────────┐
            │    E2E      │  <- 10 tests, full user workflows
            │  (slow)     │     Real Kind/Minikube cluster
            └──────┬──────┘
                   │
      ┌────────────┴─────────────┐
      │   Integration Tests      │  <- 30+ tests, CRD validation
      │    (medium speed)       │     envtest, real K8s API
      └────────────┬─────────────┘
                   │
      ┌────────────┴─────────────┐
      │     Unit Tests          │  <- 100+ tests, fast isolation
      │      (fast)            │     Mock dependencies
      └─────────────────────────┘
```

### Unit Tests

**Purpose:** Fast feedback on individual functions and business logic.

**Scope:**
- Controller reconciliation logic
- Resource builder functions (`internal/resources/`)
- MongoDB package functions (`internal/mongodb/`)
- Validation and webhook logic
- Error handling and edge cases

**Tools:**
- Testing framework: Ginkgo v2 + Gomega
- Mocking: `gomock` for external dependencies
- Coverage: `go test -cover`

**Example:**
```go
It("should create StatefulSet with correct replica count", func() {
    builder := &StatefulSetBuilder{}
    sts, err := builder.Build(mongodb)
    Expect(err).NotTo(HaveOccurred())
    Expect(*sts.Spec.Replicas).To(Equal(int32(3)))
})
```

### Integration Tests

**Purpose:** Verify controller behavior against real Kubernetes API.

**Scope:**
- CRD creation and validation
- Resource reconciliation (StatefulSets, Services, Secrets, ConfigMaps)
- Controller status updates
- Kubernetes version compatibility

**Tools:**
- envtest (Kubernetes 1.31.0)
- controller-runtime testing utilities
- Ginkgo/Gomega

**Example:**
```go
It("should create StatefulSet when MongoDB resource created", func() {
    Expect(k8sClient.Create(ctx, mongodb)).Should(Succeed())

    Eventually(func() bool {
        return statefulSetExists(ctx, "mongodb-0", "default")
    }, timeout, interval).Should(BeTrue())
})
```

### End-to-End (E2E) Tests

**Purpose:** Validate complete user workflows from deployment to operations.

**Scope:**
- Fresh ReplicaSet deployment with admin user
- Sharded cluster deployment and initialization
- Backup/restore workflows
- Scaling operations (horizontal/vertical)
- TLS certificate rotation
- MongoDB version upgrades

**Tools:**
- Kind or Minikube cluster
- kubectl for cluster interactions
- mongosh for database operations
- Ginkgo/Gomega for test assertions

**Example:**
```go
It("should deploy ReplicaSet and perform CRUD operations", func() {
    // Deploy MongoDB
    kubectlApply("mongodb-replicaset.yaml")

    // Wait for ready
    waitForPodReady("mongodb-0")
    waitForPrimaryReady()

    // Perform CRUD
    connectAndWriteData()
    verifyDataIntegrity()
})
```

### Performance Tests

**Purpose:** Benchmark critical paths and establish performance baselines.

**Scope:**
- ReplicaSet initialization speed
- Sharded cluster initialization
- Concurrent connection handling
- Resource consumption under load

**Tools:**
- Go's built-in `testing.B` benchmarks
- `k6` for load testing
- Prometheus metrics for monitoring

**Example:**
```go
func BenchmarkReplicaSetInitialization(b *testing.B) {
    for i := 0; i < b.N; i++ {
        start := time.Now()
        deployReplicaSet()
        b.ReportMetric(time.Since(start).Seconds(), "init_seconds")
    }
}
```

## Coverage Goals

| Test Type | Target Coverage | Rationale |
|-----------|----------------|-----------|
| Unit Tests | 80%+ | Critical business logic, high confidence |
| Integration Tests | 70%+ | Controller paths, CRD interactions |
| E2E Tests | 60%+ | User workflows, feature coverage |

**Coverage Enforcement:**
```bash
# Check coverage threshold
go test ./... -cover -coverprofile=coverage.out
go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//' | \
  awk '{if ($1 < 80) exit 1}'
```

**Exclusions:**
- Generated files (`zz_*.go`, `*.pb.go`)
- Test files (`*_test.go`)
- Main package (`cmd/`)
- Third-party code (`vendor/`, `third_party/`)

## Test Data Management

### Mock Data Fixtures

Location: `internal/controller/testfixtures/`

```go
// fixtures.go
var (
    DefaultMongoDB = &mongodbv1alpha1.MongoDB{
        Spec: mongodbv1alpha1.MongoDBSpec{
            Members: 3,
            Version: mongodbv1alpha1.MongoDBVersion{Version: "8.2"},
            Storage: mongodbv1alpha1.StorageSpec{
                Size: resource.MustParse("10Gi"),
            },
        },
    }

    DefaultMongoDBSharded = &mongodbv1alpha1.MongoDBSharded{
        Spec: mongodbv1alpha1.MongoDBShardedSpec{
            ConfigServer: mongodbv1alpha1.ConfigServerSpec{Members: 3},
            Shards: mongodbv1alpha1.ShardSpec{Count: 2, MembersPerShard: 3},
            Mongos: mongodbv1alpha1.MongosSpec{Replicas: 2},
        },
    }
)
```

### Test Secrets

```yaml
# test-secrets.yaml
apiVersion: v1
kind: Secret
metadata:
  name: mongodb-admin-test
type: Opaque
stringData:
  username: admin
  password: test-password-12345
```

## Kubernetes Version Compatibility

| Version | Status | Tested Features |
|---------|--------|-----------------|
| 1.31.0 | ✅ Primary | All features |
| 1.29.0 | ✅ Stable | All features |
| 1.28.0 | ✅ Stable | All features |
| 1.26.0 | ✅ Minimum | Core features |
| 1.27.0 | ⚠️ Deprecated | Legacy support only |

**Version Matrix in CI:**
```yaml
strategy:
  matrix:
    k8s-version: [1.26.0, 1.28.0, 1.29.0, 1.31.0]
```

## Integration Test Enhancements

### 1. Shard Scale Out (2 → 5)

**Purpose:** Validate horizontal scaling without data loss.

**Test Steps:**
```go
It("should scale from 2 to 5 shards", func() {
    // Deploy initial cluster with 2 shards
    deployShardedCluster(2)

    // Verify initial state
    expectShards(2)
    expectAllPodsReady()

    // Scale to 5 shards
    updateShardCount(5)

    // Verify new shards created and initialized
    expectShards(5)
    expectShardsInitialized([true, true, true, true, true])

    // Verify balancer distributes data
    waitForBalancerActive()
    verifyChunkDistribution()
})
```

### 2. Scale Down (5 → 3)

**Purpose:** Ensure graceful removal of shards (orphan resource tracking).

**Test Steps:**
```go
It("should track orphaned resources on scale down", func() {
    deployShardedCluster(5)
    updateShardCount(3)

    // Verify shards 0-2 remain, shards 3-4 orphaned
    expectActiveShards(3)
    expectOrphanedShards(2)
})
```

### 3. TLS Certificate Rotation

**Purpose:** Verify cluster reconnection with new certificates.

**Test Steps:**
```go
It("should rotate TLS certificates", func() {
    deployWithTLSEnabled()

    // Create initial cert
    certManager.CreateCertificate("mongodb-tls")

    // Verify cluster connects
    expectTLSConnectionValid()

    // Rotate certificate
    certManager.RotateCertificate("mongodb-tls")

    // Verify cluster reconnects
    expectPodsRestarted()
    expectTLSConnectionValid()
})
```

### 4. Backup/Restore Workflow

**Purpose:** Validate backup creation and data restoration.

**Test Steps:**
```go
It("should backup and restore ReplicaSet", func() {
    deployReplicaSet()

    // Create test data
    writeTestData(collection, documents)

    // Create backup
    createBackup("backup-1")
    expectBackupComplete("backup-1")

    // Delete cluster
    deleteCluster()

    // Restore from backup
    restoreFromBackup("backup-1")
    expectDataIntegrity(collection, documents)
})
```

### 5. Version Upgrade Path

**Purpose:** Verify seamless MongoDB version upgrades.

**Test Steps:**
```go
It("should upgrade from 7.0 to 8.2", func() {
    deployReplicaSetWithVersion("7.0")

    // Verify initial state
    expectVersion("7.0")

    // Upgrade to 8.2
    updateVersion("8.2")

    // Verify rolling upgrade
    expectRollingUpdate()
    expectVersion("8.2")

    // Verify data integrity
    verifyAllDataMigrated()
})
```

### 6. Admin User Auto-Creation Edge Cases

**Purpose:** Test localhost exception handling.

**Test Scenarios:**
- Admin user already exists
- Credentials secret missing
- Invalid password complexity
- User creation timeout
- Concurrent reconciliation

```go
It("should handle existing admin user", func() {
    deployReplicaSet()
    manuallyCreateAdminUser()

    // Reconcile should not fail
    reconcile()
    expectStatusReady()
})
```

## Performance Tests

### Benchmark 1: ReplicaSet Initialization

```go
func BenchmarkReplicaSetInitialization(b *testing.B) {
    for i := 0; i < b.N; i++ {
        start := time.Now()
        deployReplicaSet(3)
        waitForAllPodsReady()
        b.ReportMetric(time.Since(start).Seconds(), "init_seconds")
    }
}

**Performance Threshold:** < 120 seconds for 3-member ReplicaSet
```

### Benchmark 2: Sharded Cluster Initialization

```go
func BenchmarkShardedClusterInit(b *testing.B) {
    shards := []int{2, 3, 5}
    for _, count := range shards {
        b.Run(fmt.Sprintf("%d-shards", count), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                start := time.Now()
                deployShardedCluster(count)
                waitForClusterReady()
                b.ReportMetric(time.Since(start).Seconds(), "init_seconds")
            }
        })
    }
}

**Performance Threshold:**
- 2 shards: < 180 seconds
- 3 shards: < 240 seconds
- 5 shards: < 360 seconds
```

### Load Test: Concurrent Connections

```javascript
// k6 script: concurrent-connections.js
import http from 'k6/http';
import { check } from 'k6';

export let options = {
    stages: [
        { duration: '1m', target: 100 },  // Ramp up to 100
        { duration: '3m', target: 100 },  // Stay at 100
        { duration: '1m', target: 0 },    // Ramp down
    ],
};

export default function() {
    let res = http.post('http://mongodb-service:27017/data/insert', JSON.stringify({
        collection: 'test',
        data: { key: randomString() },
    }));
    check(res, { 'status is 200': (r) => r.status === 200 });
}
```

**Performance Threshold:**
- 99th percentile latency: < 500ms
- Error rate: < 0.1%
- No connection failures during load

## E2E Test Scenarios

### Scenario 1: Fresh Deployment

**Goal:** Deploy ReplicaSet, create admin user, perform CRUD operations, verify persistence.

```yaml
# e2e/01-fresh-deployment.yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: test-fresh-deploy
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
```

**Test Steps:**
```go
Describe("Fresh Deployment", func() {
    It("should deploy and verify CRUD operations", func() {
        // 1. Deploy MongoDB
        kubectlApply("e2e/01-fresh-deployment.yaml")

        // 2. Wait for all pods ready
        Eventually(func() int {
            pods := getPods("app.kubernetes.io/name=mongodb-operator")
            return len(pods)
        }, "5m", "10s").Should(Equal(3))

        // 3. Wait for primary elected
        Eventually(func() bool {
            return hasPrimary("test-fresh-deploy-0")
        }, "2m", "10s").Should(BeTrue())

        // 4. Write test data
        mongoshExec(`db.test.insert({name: "test"})`)

        // 5. Read and verify
        result := mongoshExec(`db.test.find({name: "test"}).count()`)
        Expect(result).To(Equal("1"))

        // 6. Restart pod, verify data persists
        deletePod("test-fresh-deploy-0")
        waitForPodReady("test-fresh-deploy-0")
        result = mongoshExec(`db.test.find({name: "test"}).count()`)
        Expect(result).To(Equal("1"))
    })
})
```

### Scenario 2: Sharded Cluster

**Goal:** Deploy MongoDBSharded with 3 shards, verify initialization, add shard via `sh.addShard()`, verify balancer distribution.

```yaml
# e2e/02-sharded-cluster.yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: test-sharded
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
```

**Test Steps:**
```go
Describe("Sharded Cluster", func() {
    It("should deploy and verify shard scaling", func() {
        // 1. Deploy sharded cluster
        kubectlApply("e2e/02-sharded-cluster.yaml")

        // 2. Verify config server initialized
        Eventually(func() bool {
            return isReplicaSetReady("test-sharded-cfg")
        }, "3m", "10s").Should(BeTrue())

        // 3. Verify all shards initialized
        Eventually(func() int {
            return getInitializedShards("test-sharded")
        }, "5m", "10s").Should(Equal(3))

        // 4. Verify shards registered with mongos
        result := mongoshExecOnMongos(`sh.status().shards.length`)
        Expect(result).To(Equal("3"))

        // 5. Add 2 more shards (scale to 5)
        patchShardCount(5)

        // 6. Verify new shards initialized and added
        Eventually(func() int {
            return getInitializedShards("test-sharded")
        }, "5m", "10s").Should(Equal(5))

        // 7. Verify balancer active
        Eventually(func() bool {
            return isBalancerActive("test-sharded")
        }, "2m", "10s").Should(BeTrue())

        // 8. Write test data and verify distribution
        mongoshExecOnMongos(`
            for (let i = 0; i < 10000; i++) {
                db.test.insertOne({shardKey: i % 10})
            }
        `)

        // 9. Verify chunk distribution
        Eventually(func() bool {
            return areChunksBalanced("test-sharded")
        }, "3m", "10s").Should(BeTrue())
    })
})
```

### Scenario 3: Backup & Restore

**Goal:** Deploy MongoDB, create data, create MongoDBBackup, verify completion, delete MongoDB, restore, verify integrity.

```yaml
# e2e/03-backup-restore.yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: test-backup
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
---
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: test-backup-job
spec:
  clusterRef:
    name: test-backup
    kind: MongoDB
  storage:
    type: s3
    s3:
      bucket: test-backups
      endpoint: http://minio:9000
      region: us-east-1
      credentialsRef:
        name: s3-credentials
```

**Test Steps:**
```go
Describe("Backup & Restore", func() {
    It("should backup and restore with data integrity", func() {
        // 1. Deploy MongoDB
        kubectlApply("e2e/03-backup-restore.yaml")

        // 2. Wait for cluster ready
        waitForClusterReady("test-backup")

        // 3. Create test data
        mongoshExec(`
            db.test.insertMany([
                {id: 1, name: "doc1"},
                {id: 2, name: "doc2"},
                {id: 3, name: "doc3"}
            ])
        `)

        // 4. Create backup
        waitForBackupComplete("test-backup-job", "5m")

        // 5. Verify backup exists in S3
        backupFile := getLatestBackupFromS3("test-backups")
        Expect(backupFile).NotTo(BeEmpty())

        // 6. Delete cluster
        kubectlDelete("mongodb/test-backup")
        waitForDeletion("statefulset", "test-backup")

        // 7. Restore from backup (manual or via operator)
        restoreFromBackup(backupFile)

        // 8. Verify all data restored
        count := mongoshExec(`db.test.countDocuments({})`)
        Expect(count).To(Equal("3"))

        doc1 := mongoshExec(`db.test.findOne({id: 1})`)
        Expect(doc1).To(ContainSubstring("doc1"))
    })
})
```

### Scenario 4: Upgrade Path

**Goal:** Deploy version X, upgrade to X+1, verify data migration, no breaking changes.

```go
Describe("Version Upgrade", func() {
    It("should upgrade from 7.0 to 8.2 without data loss", func() {
        // 1. Deploy MongoDB 7.0
        deployWithVersion("7.0")
        waitForClusterReady()

        // 2. Create test data
        mongoshExec(`db.upgrade_test.insert({version: "7.0"})`)

        // 3. Verify current version
        version := getMongoDBVersion()
        Expect(version).To(ContainSubstring("7.0"))

        // 4. Upgrade to 8.2
        patchVersion("8.2")

        // 5. Verify rolling upgrade
        pods := getPods()
        for _, pod := range pods {
            Expect(pod.Spec.Image).To(ContainSubstring("8.2"))
        }

        // 6. Wait for all pods ready
        waitForAllPodsReady()

        // 7. Verify new version
        version = getMongoDBVersion()
        Expect(version).To(ContainSubstring("8.2"))

        // 8. Verify data integrity
        result := mongoshExec(`db.upgrade_test.findOne({version: "7.0"})`)
        Expect(result).NotTo(BeEmpty())

        // 9. Verify no breaking changes (feature flags)
        features := getMongoDBFeatures()
        Expect(features).To(ContainElement("time-series"))
        Expect(features).To(ContainElement("change-streams"))
    })
})
```

### Scenario 5: TLS Rotation

**Goal:** Deploy with TLS, rotate cert-manager certificate, verify cluster reconnects.

```yaml
# e2e/05-tls-rotation.yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: test-tls
spec:
  members: 3
  version:
    version: "8.2"
  storage:
    storageClassName: standard
    size: 10Gi
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: selfsigned-issuer
        kind: ClusterIssuer
```

**Test Steps:**
```go
Describe("TLS Rotation", func() {
    It("should rotate certificates without downtime", func() {
        // 1. Deploy with TLS
        kubectlApply("e2e/05-tls-rotation.yaml")
        waitForClusterReady()

        // 2. Verify TLS connection
        isSecure := verifyTLSConnection()
        Expect(isSecure).To(BeTrue())

        // 3. Get initial certificate
        initialCert := getCertificateSecret("test-tls-tls")
        initialCertTime := initialCert.CreationTimestamp

        // 4. Force certificate rotation
        forceCertManagerRenewal("test-tls-tls")

        // 5. Wait for certificate renewal
        Eventually(func() bool {
            newCert := getCertificateSecret("test-tls-tls")
            return newCert.CreationTimestamp.After(initialCertTime)
        }, "2m", "10s").Should(BeTrue())

        // 6. Verify pods restarted with new cert
        Eventually(func() bool {
            pods := getPods()
            for _, pod := range pods {
                if pod.CreationTimestamp.After(initialCertTime) {
                    return true
                }
            }
            return false
        }, "3m", "10s").Should(BeTrue())

        // 7. Verify TLS connection still valid
        isSecure = verifyTLSConnection()
        Expect(isSecure).To(BeTrue())

        // 8. Verify no data loss
        count := mongoshExecTLS(`db.test.countDocuments({})`)
        Expect(count).NotTo(BeEmpty())
    })
})
```

### Scenario 6: Scaling Operations

**Goal:** Horizontal scale (add mongos replicas), vertical scale (increase resources), add shard, verify health, no data loss.

```go
Describe("Scaling Operations", func() {
    It("should perform all scaling operations safely", func() {
        // 1. Deploy sharded cluster
        deployShardedCluster(3)

        // Horizontal scale: Add mongos replicas
        patchMongosReplicas(4)
        Eventually(func() int32 {
            mongos := getMongosDeployment()
            return *mongos.Spec.Replicas
        }, "2m", "10s").Should(Equal(int32(4)))

        // Vertical scale: Increase resources
        patchShardResources(`{
            "requests": {"memory": "2Gi", "cpu": "1"},
            "limits": {"memory": "4Gi", "cpu": "2"}
        }`)

        // Verify rolling restart
        waitForRollingRestart()

        // Add shard (scale out)
        patchShardCount(4)
        waitForShardAdded(3) // index 3

        // Verify all pods healthy
        pods := getPods()
        for _, pod := range pods {
            Expect(pod.Status.Phase).To(Equal("Running"))
        }

        // Verify no data loss during scaling
        beforeCount := mongoshExec(`db.test.countDocuments({})`)

        // Perform scaling operations concurrently
        goroutines.WaitGroup.Add(3)
        go func() {
            defer goroutines.WaitGroup.Done()
            patchMongosReplicas(5)
        }()
        go func() {
            defer goroutines.WaitGroup.Done()
            patchShardCount(5)
        }()
        go func() {
            defer goroutines.WaitGroup.Done()
            patchShardResources(`{
                "requests": {"memory": "3Gi", "cpu": "1.5"},
                "limits": {"memory": "6Gi", "cpu": "3"}
            }`)
        }()
        goroutines.WaitGroup.Wait()

        // Verify data integrity after scaling
        afterCount := mongoshExec(`db.test.countDocuments({})`)
        Expect(beforeCount).To(Equal(afterCount))

        // Verify cluster healthy
        Eventually(func() bool {
            return isClusterHealthy()
        }, "5m", "10s").Should(BeTrue())
    })
})
```

## Test Environment Setup

### Kind Installation (Local Testing)

```bash
# Install Kind
go install sigs.k8s.io/kind@v0.22.0

# Create Kind cluster
kind create cluster --name mongodb-operator-test \
  --image=kindest/node:v1.31.0 \
  --config=- <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraMounts:
      - containerPath: /var/lib/kubelet/pods
        hostPath: /tmp/kind-pods
  - role: worker
  - role: worker
EOF

# Load Docker image
docker load -i mongodb-operator.tar
kind load docker-image --name mongodb-operator-test keiailab/mongodb-operator:test

# Install operator
helm install mongodb-operator ./charts/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace \
  --set image.tag=test
```

### Minikube Installation

```bash
# Install Minikube
brew install minikube  # macOS
# or
curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube-linux-amd64

# Start Minikube
minikube start --kubernetes-version=v1.31.0 --driver=docker --memory=4096

# Enable registry addon (for local images)
minikube addons enable registry

# Build and load operator
eval $(minikube docker-env)
make docker-build IMG=localhost:5000/mongodb-operator:test
docker push localhost:5000/mongodb-operator:test

# Install operator
helm install mongodb-operator ./charts/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace \
  --set image.repository=localhost:5000/mongodb-operator \
  --set image.tag=test
```

## Running Tests

### Unit Tests

```bash
# Run all unit tests
make test-unit

# Run specific package tests
go test ./internal/controller -v

# Run with coverage
go test ./... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html

# Run with race detector
go test ./... -race -short
```

### Integration Tests

```bash
# Run integration tests (requires envtest setup)
make test-integration

# Run with specific Kubernetes version
ENVTEST_K8S_VERSION=1.31.0 make test-integration

# Run with verbose output
go test ./internal/controller -v -ginkgo.v
```

### E2E Tests

```bash
# Run E2E tests (requires Kind/Minikube cluster)
make test-e2e

# Run specific E2E scenario
ginkgo -v -focus="Fresh Deployment" e2e/

# Run with debug logging
ginkgo -v -ginkgo.v e2e/
```

### Performance Tests

```bash
# Run benchmarks
go test -bench=. -benchmem ./...

# Run specific benchmark
go test -bench=BenchmarkReplicaSetInitialization ./internal/controller

# Run load test with k6
k6 run tests/load/concurrent-connections.js
```

## Debugging Failing Tests

### Enable Debug Logging

```bash
# Run with debug logs
ginkgo -v -ginkgo.v -ginkgo.trace e2e/

# Set log level
export LOG_LEVEL=debug
make test-integration
```

### Inspect Test Cluster

```bash
# Get cluster state
kubectl get all -n mongodb-operator-system

# Check operator logs
kubectl logs -n mongodb-operator-system deployment/mongodb-operator

# Check MongoDB pod logs
kubectl logs -f my-mongodb-0 -c mongodb

# Execute into pod
kubectl exec -it my-mongodb-0 -c mongodb -- mongosh
```

### Dump Test State

```bash
# Dump all resources in namespace
kubectl get all,secrets,configmaps,pvc -n test-ns -o yaml > test-state.yaml

# Get MongoDB resource status
kubectl get mongodb my-mongodb -o yaml

# Describe resources
kubectl describe statefulset my-mongodb
kubectl describe mongodb my-mongodb
```

### Retry Flaky Tests

```go
// Ginkgo flaky test detection
It("should handle flaky conditions", FlakeAttempts(3), func() {
    // Test logic
})

// Manual retry
ginkgo -v -repeat=10 e2e/
```

## Test Organization Structure

```
mongodb-operator/
├── internal/
│   ├── controller/
│   │   ├── mongodb_controller_test.go      # Controller tests
│   │   ├── mongodbsharded_controller_test.go
│   │   ├── mongodbbackup_controller_test.go
│   │   ├── suite_test.go                # Test setup
│   │   └── testfixtures/                # Mock data
│   │       ├── fixtures.go
│   │       └── secrets.yaml
│   ├── resources/
│   │   ├── statefulset_test.go         # Resource builder tests
│   │   ├── service_test.go
│   │   └── secret_test.go
│   └── mongodb/
│       ├── replicaset_test.go           # MongoDB package tests
│       ├── auth_test.go
│       └── sharding_test.go
├── tests/
│   ├── e2e/
│   │   ├── 01-fresh-deployment_test.go
│   │   ├── 02-sharded-cluster_test.go
│   │   ├── 03-backup-restore_test.go
│   │   ├── 04-upgrade-path_test.go
│   │   ├── 05-tls-rotation_test.go
│   │   └── 06-scaling-operations_test.go
│   ├── load/
│   │   └── concurrent-connections.js   # k6 load test
│   └── performance/
│       └── benchmarks_test.go          # Go benchmarks
└── config/
    └── crd/
        └── bases/                     # CRDs for testing
```

## Coverage Requirements

### Minimum Coverage by Package

| Package | Target | Current | Status |
|---------|--------|---------|--------|
| `internal/controller` | 80% | - | 📊 |
| `internal/resources` | 85% | - | 📊 |
| `internal/mongodb` | 80% | - | 📊 |
| `api/v1alpha1` | 70% | - | 📊 |

### Critical Path Coverage

**Must-have 100% coverage:**
- ReplicaSet initialization logic
- Sharded cluster initialization
- Shard scale-out (`sh.addShard()`)
- Admin user creation (localhost exception)
- TLS certificate handling
- Secret management (keyfile generation)

**Recommended 90%+ coverage:**
- Resource builders (StatefulSet, Service, ConfigMap)
- Status condition updates
- Error handling and retry logic

### Coverage Reports

```bash
# Generate coverage report
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out > coverage.txt

# Generate HTML report
go tool cover -html=coverage.out -o coverage.html

# Generate coverage profile
go test ./... -coverprofile=coverage.out -covermode=atomic
```

**Coverage badges in CI:**
```yaml
- name: Check coverage
  run: |
    COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')
    echo "Coverage: ${COVERAGE}%"
    if (( $(echo "$COVERAGE < 80" | bc -l) )); then
      echo "Coverage below 80%"
      exit 1
    fi
```

## Running Tests Locally

### Quick Start

```bash
# 1. Clone repository
git clone https://github.com/keiailab/mongodb-operator.git
cd mongodb-operator

# 2. Install dependencies
go mod download
make tools

# 3. Run unit tests
make test-unit

# 4. Run integration tests
make test-integration

# 5. Create Kind cluster for E2E tests
kind create cluster --name mongodb-test

# 6. Run E2E tests
make test-e2e
```

### Local Development Workflow

```bash
# 1. Make code changes
vim internal/controller/mongodb_controller.go

# 2. Run affected tests
go test -run TestMongoDBController ./internal/controller -v

# 3. Run pre-commit hooks
pre-commit run --all-files

# 4. Run full test suite
make test

# 5. Build and push local image
make docker-build IMG=localhost:5000/mongodb-operator:dev
docker push localhost:5000/mongodb-operator:dev

# 6. Test in Kind cluster
kind load docker-image localhost:5000/mongodb-operator:dev --name mongodb-test
helm upgrade mongodb-operator ./charts/mongodb-operator \
  --set image.tag=dev \
  --namespace mongodb-operator-system
```

### Continuous Integration

Tests run automatically on:
- Push to `main` branch
- Pull requests to `main` branch
- Manual workflow dispatch

**CI workflow:**
```yaml
# .github/workflows/test.yml
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true
      - run: make test-unit
      - run: make test-integration
```

## Summary

This testing strategy provides comprehensive coverage across all MongoDB Operator features:

- **Unit tests**: Fast, isolated logic validation
- **Integration tests**: Controller and CRD behavior
- **E2E tests**: Real-world user workflows
- **Performance tests**: Benchmarks and load testing
- **Coverage goals**: 80% unit, 70% integration, 60% E2E

Follow this strategy to ensure high-quality, reliable releases of the MongoDB Operator.
