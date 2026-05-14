# ARCHITECTURE — mongodb-operator

> Single-page architecture description. Updated when CRD surface / RBAC / reconcile pattern changes.

## Overview

- **Purpose**: Kubernetes Operator that automates deployment, scaling, and management of MongoDB ReplicaSets and Sharded Clusters via declarative CRDs.
- **Scope**: K8s controllers reconciling `MongoDB`, `MongoDBSharded`, `MongoDBBackup`, federation, and insights CRDs over MongoDB 7.0+ deployments.
- **Stability tier**: v1.5.0 (GA scope = ReplicaSet; Sharded / Backup / HPA = beta feature gates).
- **Latest release**: v1.5.0 (2026-05-13)
- **License**: Apache-2.0
- **Module path**: `github.com/keiailab/mongodb-operator`

## CRD surface (8 CRDs)

| CRD | apiVersion | Scope | Tier | Description |
|---|---|---|---|---|
| `MongoDB` | `mongodb.keiailab.com/v1alpha1` | Namespaced | **GA** | ReplicaSet with 3+ members + automatic failover |
| `MongoDBSharded` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (feature gate `sharded.enabled`) | Sharded cluster: config servers + shards + mongos routers |
| `MongoDBBackup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (`backup.enabled`) | S3 or PVC backup snapshots |
| `MongoDBBackupVerification` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta | Restore drill verification |
| `MongoDBClusterGroup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | Multi-cluster grouping (federation scaffold) |
| `MongoDBFederation` | `mongodb.keiailab.com/v1alpha1` | Cluster | Alpha | Cross-region federation |
| `MongoDBInsights` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | Performance insights surface |
| (common types) | `mongodb.keiailab.com/v1alpha1` | — | — | Conditions / Finalizers helpers |

## Reconcile flow

```
┌──────────────────────────────────────────────────────────────┐
│                    MongoDB Operator                          │
├──────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌──────────────────────┐ │
│  │  MongoDB    │  │ MongoDBShar │  │   MongoDBBackup      │ │
│  │  Controller │  │ Controller  │  │   Controller         │ │
│  └─────────────┘  └─────────────┘  └──────────────────────┘ │
│           │              │                  │                │
│           ▼              ▼                  ▼                │
│  StatefulSet / Service / Secret / ConfigMap / PDB / NP /     │
│  ServiceMonitor / cert-manager Certificate                   │
└──────────────────────────────────────────────────────────────┘
```

- `MongoDB` reconciler: StatefulSet (3+ replicas) + headless Service + Secret (SCRAM keyfile) + cert-manager Certificate + PDB + ServiceMonitor
- `MongoDBSharded` reconciler: 3 StatefulSets (config / shard / mongos) with ordered init
- `MongoDBBackup` reconciler: Jobs that snapshot to S3-compatible storage or PVC

## RBAC scope

- ClusterRole: CRD watch + cert-manager Certificate + PrometheusOperator ServiceMonitor
- Role (per ns): StatefulSet / Service / Secret / ConfigMap / PVC / PDB / NetworkPolicy
- ServiceAccount: `mongodb-operator` (default-deny NetworkPolicy applied)

## operator-commons import surface

Adoption per `operator-commons/ARCHITECTURE.md` matrix: **6/8 (75%)**.

| Package | Status | Usage |
|---|---|---|
| `pkg/security` | ✅ | restricted PSA SecurityContext |
| `pkg/version` | ✅ | MongoDB version allowlist |
| `pkg/labels` | ✅ | Recommended labels (`app.kubernetes.io/*`) |
| `pkg/monitoring` | ⏳ | ServiceMonitor reconciler local impl — commons delegation pending |
| `pkg/networkpolicy` | ✅ | Deny-by-default + functional options |
| `pkg/webhook` | ⏳ | Admission validation local impl — commons delegation pending |
| `pkg/finalizer` | ✅ | `Add` / `Remove` / `Has` |
| `pkg/status` | ✅ | Condition reasons catalog |

## Test layers

| Layer | Location | Coverage |
|---|---|---|
| Unit | `internal/**/_test.go` | ≥80% target |
| Integration (envtest) | `test/integration/` | core reconcile + finalizer paths |
| E2E (kind) | `test/e2e/` | release-critical scenarios (RS + sharded + backup) |
| Scorecard | `bundle/tests/scorecard/` | OLM v1alpha3, 6-test parity with postgres ADR-0013 |

## Build / deploy

- Container image: `ghcr.io/keiailab/mongodb-operator:v1.5.0`
- Helm chart: `charts/mongodb-operator/`
- OLM bundle: `bundle/`
- ArtifactHub: `mongodb-operator` repo (`artifacthub-repo.yml`)
- CI: GitHub Actions per ADR-0027 (community-operators upstream sync automation)

## Feature gates (beta scope opt-in)

Per `values.yaml`:
- `features.sharded.enabled` (default false) — gates `MongoDBSharded` CRD watch + RBAC
- `features.backup.enabled` (default false) — gates `MongoDBBackup` CRD watch + Job RBAC
- `features.autoscaling.enabled` (default false) — gates HPA reconciler

Production-cluster pattern: GA-only. Beta CRDs require explicit opt-in.

## ADR cross-link (28 ADRs)

Notable:
- ADR-0001: charter / project identity
- ADR-0013: scorecard OLM test parity standard (postgres co-author)
- ADR-0027: community-operators upstream sync automation
- ADR-0028: latest (cycle 25 sealing)

Full list: `docs/kb/adr/INDEX.md`.

## Roadmap status

- Phase 1 Production hardening: **100%** (21/21)
- Phase 2 Enterprise auth + multi-region: **100%** (21/21)
- Phase 3 Advanced enterprise: **100%** (16/16)
- Phase 4 Bitnami parity: **100%** (26/26)
- Phase 5 (post-v1.5.0): *defining* — `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md`

## Non-goals

- ❌ MongoDB version < 7.0 (per `pkg/version` allowlist)
- ❌ Operator-bundled MongoDB Enterprise binaries (Apache-2.0 license boundary)
- ❌ MongoDB Atlas / cloud-managed integration (out of scope)
- ❌ Embedded `bitnami/mongodb` chart (we implement parity natively)

## References

- `README.md` / `README.ko.md`
- `ROADMAP.md` (Phase 1-4 100% complete)
- `CHANGELOG.md`
- `ADOPTERS.md`
- `CONTRIBUTING.md` / `CONTRIBUTING.ko.md`
- `GOVERNANCE.md`
- `SUPPORT.md`
- `AGENTS.md` — AI assistant runbook
- `docs/kb/adr/INDEX.md` — 28 ADRs
