<p align="center">
  <a href="https://keiailab.com">
    <img src="docs/branding/symbol.png" alt="keiailab" width="96"/>
  </a>
</p>

# mongodb-operator

> **Keiailab data operator family** — MongoDB replica sets and sharded clusters on Kubernetes with TLS, metrics, and declarative topology.

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-0EA5A8.svg" alt="License: MIT"/></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26"/></a>
  <a href="#supported-mongodb-versions"><img src="https://img.shields.io/badge/MongoDB-8.0%20|%208.2%20|%208.3-47A248?logo=mongodb&logoColor=white" alt="MongoDB 8.x"/></a>
  <a href="#requirements"><img src="https://img.shields.io/badge/Kubernetes-1.26%2B-326CE5?logo=kubernetes&logoColor=white" alt="Kubernetes 1.26+"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator"><img src="https://img.shields.io/badge/GHCR-mongodb--operator-0EA5A8?logo=github&logoColor=white" alt="GHCR image"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/discussions"><img src="https://img.shields.io/github/discussions/keiailab/mongodb-operator?label=discussions&logo=github" alt="GitHub Discussions"/></a>
</p>

## Design assets

| Asset | Path | Usage |
|---|---|---|
| Centered service symbol | [`docs/branding/symbol.png`](docs/branding/symbol.png) | GitHub README, Artifact Hub icon/screenshot |
| Repository wordmark | [`docs/branding/logo.png`](docs/branding/logo.png) | Project pages and docs cards |
| Social cover | [`docs/branding/cover.png`](docs/branding/cover.png) | Social cards and launch posts |
| Branding guide | [`docs/branding.md`](docs/branding.md) | Public visual usage rules |

A Kubernetes operator for running MongoDB on Kubernetes. It manages the lifecycle
of MongoDB replica sets and sharded clusters through Custom Resources — bootstrapping
the cluster, creating the admin user, wiring up TLS and metrics, and reconciling
the desired topology.

The operator does not bundle or redistribute MongoDB. It pulls the official
`mongo` community images at runtime and orchestrates them; you remain responsible
for complying with MongoDB's [SSPL](https://www.mongodb.com/licensing/server-side-public-license)
license terms.

## Features

- **Replica sets** — declare a `MongoDB` resource and the operator creates the
  StatefulSet, headless and client Services, keyfile, runs `rs.initiate()`, waits
  for primary election, and creates the admin user via the localhost exception.
- **Sharded clusters** — a `MongoDBSharded` resource brings up config servers,
  shard replica sets, and mongos routers, initiates each replica set, and registers
  the shards. Increasing `spec.shards.count` adds and registers new shards.
- **TLS** — optional cert-manager integration for in-transit encryption.
- **Authentication** — SCRAM-SHA-256 with admin credentials sourced from a Secret.
- **Metrics** — Prometheus metrics, an optional `ServiceMonitor`, and
  `PrometheusRule` alerts.
- **High availability** — opt-in `PodDisruptionBudget` and `NetworkPolicy`
  (deny-by-default) generation.
- **Backups** — a `MongoDBBackup` resource targeting S3-compatible or PVC storage.
  See [Status](#status) for current limitations.

## Requirements

- Kubernetes 1.26+
- For TLS: [cert-manager](https://cert-manager.io/)
- For metrics scraping: a Prometheus stack that understands `ServiceMonitor`
  (e.g. kube-prometheus-stack)

## Installation

### Helm

```bash
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system --create-namespace
```

The replica-set and sharded controllers are enabled by default. The backup,
autoscaling, and webhook controllers are gated and off by default — enable them
via `--set features.backup.enabled=true`, `--set features.autoscaling.enabled=true`,
or `--set webhook.enabled=true` (the webhook requires cert-manager).

### From source

```bash
make install   # install CRDs into the current kube context
make deploy IMG=<your-registry>/mongodb-operator:<tag>
```

## Usage

### Replica set

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  members: 3
  version:
    version: "8.0.5"
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
kubectl create namespace database
kubectl create secret generic mongodb-admin \
  --from-literal=username=admin \
  --from-literal=password=change-me \
  -n database
kubectl apply -f my-mongodb.yaml
```

### Sharded cluster

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBSharded
metadata:
  name: my-sharded
  namespace: database
spec:
  version:
    version: "8.0.5"
  configServer:
    members: 3
    storage:
      size: 5Gi
  shards:
    count: 2
    membersPerShard: 3
    storage:
      size: 50Gi
  mongos:
    replicas: 2
```

To add shards, raise the count — the operator brings up the new shard's replica
set and runs `sh.addShard()`, after which MongoDB's balancer migrates chunks:

```bash
kubectl patch mongodbsharded my-sharded --type=merge \
  -p '{"spec":{"shards":{"count":3}}}'
```

More examples — minimal, production, GitOps, monitoring, backups — live in
[`examples/`](examples/), and runnable samples in [`config/samples/`](config/samples/).

> The CRDs are served at both `v1alpha1` and `v1beta1`; `v1beta1` is the storage
> version. New manifests should use `v1beta1`.

## Supported MongoDB versions

MongoDB 8.0, 8.2, and 8.3 (even-numbered stable releases on the 8.x line). The
admission webhook also enforces single-minor-step upgrades (e.g. 8.0 → 8.2, not
8.0 → 8.3). Version support is enforced by `IsSupportedMongoDBVersion`; see
`api/v1beta1/version_validation_test.go`.

## Status

What is wired into the manager and exercised by tests:

| Capability | State |
|---|---|
| Replica set lifecycle (initiate, admin bootstrap, TLS, metrics) | Stable, on by default |
| Sharded cluster lifecycle + shard scale-out | On by default; scale-out covered by unit tests, soak-test before relying on it for production |
| Backups to S3 / PVC | Gated off by default — no automated test coverage and credentials are not yet handled safely; treat as experimental |
| HPA for mongos | Gated off by default — experimental |
| Validating admission webhooks | Gated off by default — requires cert-manager |

Replica-set member removal is not automated: scaling down only reduces
StatefulSet replicas without calling `rs.remove()`, so remove members manually.

## Roadmap

The repository also contains CRDs and reconcile loops that are scaffolding for
future work — they watch their resources and update status, but do not yet perform
their real external integrations, and are off by default:

- Point-in-time recovery (oplog upload)
- Cross-cluster federation (`MongoDBFederation`, `MongoDBClusterGroup`)
- Query insights / advisories (`MongoDBInsights`)
- Encryption-at-rest via external KMS (Vault, AWS, GCP, Azure)
- LDAP authentication

## Development

```bash
make build       # build the manager binary
make test-unit   # unit tests (no cluster required)
make test        # full suite, requires envtest binaries
make run         # run the controller against the current kube context
make lint        # go vet + staticcheck + golangci-lint
```

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md); commits must
carry a `Signed-off-by` trailer ([DCO](https://developercertificate.org/)).

## License

[MIT](LICENSE).
