<p align="center">
  <a href="https://keiailab.com">
    <img src="docs/branding/symbol.png" alt="keiailab" width="96"/>
  </a>
</p>

# mongodb-operator

**The all-in-one MongoDB operator for Kubernetes.** Run ReplicaSets, sharded
clusters with automatic scale-out, S3/PVC backups, authentication
(SCRAM / X.509 / LDAP / OIDC), TLS, and bundled Prometheus + Grafana — all from
one set of Custom Resources. *PITR and multi-region federation are in beta.*

It manages the full lifecycle of MongoDB replica sets and sharded clusters through
Custom Resources — bootstrapping the cluster, creating the admin user, wiring up
TLS and metrics, and reconciling the desired topology.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/KeiaiLab/mongodb-operator)](https://github.com/KeiaiLab/mongodb-operator/releases)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/keiailab-mongodb-operator)](https://artifacthub.io/packages/helm/keiailab-mongodb-operator/mongodb-operator)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](go.mod)
[![MongoDB](https://img.shields.io/badge/MongoDB-8.0%20|%208.2%20|%208.3-47A248?logo=mongodb)](#supported-mongodb-versions)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes)](#requirements)

> **Why another MongoDB operator?** Unlike the official Community Operator
> (ReplicaSet only, no built-in backup) or Bitnami's chart (static manifests, no
> reconciliation), mongodb-operator manages the **whole lifecycle from one CRD** —
> for **vanilla MongoDB 8.x** under a permissive **MIT** license.
> [See the full comparison →](docs/gap-analysis.md)

## Design assets

| Asset | Path | Usage |
|---|---|---|
| Centered service symbol | [`docs/branding/symbol.png`](docs/branding/symbol.png) | GitHub README, Artifact Hub icon/screenshot |
| Keiailab base symbol | [`docs/branding/base-symbol.png`](docs/branding/base-symbol.png) | Source reference for the outer rotating-arrow mark |
| Repository wordmark | [`docs/branding/logo.png`](docs/branding/logo.png) | Project pages and docs cards |
| Social cover | [`docs/branding/cover.png`](docs/branding/cover.png) | Social cards and launch posts |
| Branding guide | [`docs/BRANDING.md`](docs/BRANDING.md) | Public visual usage rules |

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
- **Authentication** — SCRAM-SHA-256 and X.509 (GA); **LDAP** and **OIDC/OAuth2**
  supported (e2e hardening in progress). Admin credentials sourced from a Secret.
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
