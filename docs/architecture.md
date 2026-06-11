<p align="center">
  <b>English</b> |
  <a href="ARCHITECTURE.ko.md">한국어</a> |
  <a href="ARCHITECTURE.ja.md">日本語</a> |
  <a href="ARCHITECTURE.zh.md">中文</a>
</p>

# ARCHITECTURE — mongodb-operator

> Single-page architecture description. Updated when CRD surface / RBAC / reconcile pattern changes.

## Overview

- **Purpose**: Kubernetes Operator that automates deployment, scaling, and management of MongoDB ReplicaSets and Sharded Clusters via declarative CRDs.
- **Scope**: K8s controllers reconciling `MongoDB`, `MongoDBSharded`, `MongoDBBackup`, federation, and insights CRDs over MongoDB 7.0+ deployments.
- **Stability tier**: v1.5.0 (GA scope = ReplicaSet; Sharded / Backup / HPA = beta feature gates).
- **Latest release**: v1.5.0 (2026-05-13)
- **License**: MIT
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

## keiailab-commons import surface

Adoption per `keiailab-commons/ARCHITECTURE.md` matrix: **6/8 (75%)**.

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

### Build artifacts (per release tag, e.g. v1.5.0)

| Artifact | Image / Path | Purpose |
|---|---|---|
| Operator container | `ghcr.io/keiailab/mongodb-operator:v1.5.0` | manager pod runtime |
| Helm chart | `charts/mongodb-operator/` → `helm package` | Path 2 install (single command) |
| OLM bundle (CSV + CRDs + scorecard) | `bundle/` → `ghcr.io/keiailab/mongodb-operator-bundle:v1.5.0` | OLM packaging unit (referenced by FBC catalog) |
| FBC catalog | `deploy/catalog/` → `ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0` | OLM v1 ClusterCatalog source (ADR-0028 Phase D) |
| ArtifactHub repo | `artifacthub-repo.yml` | discovery + signature verification |
| SBOM | `make sbom` → SPDX-2.3 | SLSA / EU CRA |

### 3 deployment models (외부 사용자 노출, ADR-0028 + ADR-0029)

| Model | Cluster install | Operator install | Modernity | Day-2 |
|---|---|---|---|---|
| **OLM v1** *(recommended)* | `operator-controller + catalogd` (olmv1-system ns) | `ClusterCatalog + ClusterExtension` 단 2 자원 | 🟢 next-generation (2026-02 GA) | catalog channels + version pin/range |
| Helm chart | (없음, direct deploy) | `helm install` | 🟡 stable | `helm upgrade/rollback` |
| OLM v0 *(legacy)* | `olm-operator + catalog-operator + packageserver` (olm ns) | `CatalogSource + OperatorGroup + Subscription + InstallPlan` | 🔴 maintenance mode | Subscription channels + approve |

상세 절차 + Day-2 upgrade/rollback: [Installation Guide](install.md). KeiaiLab Cluster 라이브 evidence: [deploy/olm-v1/README.md](deploy/olm-v1/README.md) (OLM v0 path 는 ADR-0028 Phase D 로 폐기).

### Release pipeline

- CI: ADR-0027 community-operators upstream sync (OLM v0 path) + 본 release tag → GHCR push (operator + bundle + catalog) + Helm chart Pages publish.
- Cosign: container image + Helm chart + SBOM 모두 keyless OIDC signed (G-13, ADR-0023).
- Renovate: digest pinning (ADR-0066 정합).

## Feature gates (beta scope opt-in)

Per `values.yaml`:
- `features.sharded.enabled` (default false) — gates `MongoDBSharded` CRD watch + RBAC
- `features.backup.enabled` (default false) — gates `MongoDBBackup` CRD watch + Job RBAC
- `features.autoscaling.enabled` (default false) — gates HPA reconciler

Production-cluster pattern: GA-only. Beta CRDs require explicit opt-in.

## ADR cross-link (29 ADRs)

Notable:
- ADR-0001: charter / project identity
- ADR-0013: scorecard OLM test parity standard (postgres co-author)
- ADR-0023: OperatorHub bundle scaffold
- ADR-0027: community-operators upstream sync automation
- ADR-0028: 외부 사용자 운영 수준 (5 결격 해소, channel/maturity stable 승격)
- **ADR-0029: OLM v1 채택 (next-generation, ClusterCatalog + ClusterExtension)**

Full list: `docs/kb/adr/INDEX.md`.

## Roadmap status

- Phase 1 Production hardening: **100%** (21/21)
- Phase 2 Enterprise auth + multi-region: **100%** (21/21)
- Phase 3 Advanced enterprise: **100%** (16/16)
- Phase 4 upstream chart parity: **100%** (26/26)
- Phase 5 (post-v1.5.0): *defining* — `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md`

## Non-goals

- ❌ MongoDB version < 7.0 (per `pkg/version` allowlist)
- ❌ Operator-bundled MongoDB Enterprise binaries (MIT license boundary)
- ❌ MongoDB Atlas / cloud-managed integration (out of scope)
- ❌ Embedded `bitnami/mongodb` chart (we implement parity natively)

## References

- `README.md` / `README.ko.md`
- `(roadmap.md)` (Phase 1-4 100% complete)
- `(changelog.md)`
- `(adopters.md)`
- `(contributing.md)` / `CONTRIBUTING.ko.md`
- `(governance.md)`
- `(support.md)`
- `AGENTS.md` — AI assistant runbook
- `docs/kb/adr/INDEX.md` — 28 ADRs

