<p align="center">
  <a href="ARCHITECTURE.md">English</a> |
  <a href="ARCHITECTURE.ko.md">한국어</a> |
  <a href="ARCHITECTURE.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 架构 — mongodb-operator

> 单页架构说明文档。当 CRD 表面 / RBAC / reconcile 模式发生变化时更新。

## 概述

- **目的**: 通过声明式 CRD 自动化 MongoDB ReplicaSet 和 Sharded Cluster 的部署、伸缩与运维的 Kubernetes Operator。
- **范围**: 针对 MongoDB 7.0+ 部署,对 `MongoDB`、`MongoDBSharded`、`MongoDBBackup`、federation、insights 等 CRD 进行 reconcile 的 K8s 控制器。
- **稳定性层级**: v1.5.0 (GA scope = ReplicaSet,Sharded / Backup / HPA = beta feature gate)。
- **最新发布**: v1.5.0 (2026-05-13)
- **许可证**: MIT
- **模块路径**: `github.com/keiailab/mongodb-operator`

## CRD 表面 (8 个 CRD)

| CRD | apiVersion | Scope | Tier | 描述 |
|---|---|---|---|---|
| `MongoDB` | `mongodb.keiailab.com/v1alpha1` | Namespaced | **GA** | 3 个以上成员的 ReplicaSet + 自动 failover |
| `MongoDBSharded` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (feature gate `sharded.enabled`) | Sharded 集群: config server + shard + mongos 路由 |
| `MongoDBBackup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (`backup.enabled`) | S3 或 PVC 备份快照 |
| `MongoDBBackupVerification` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta | 恢复演练验证 |
| `MongoDBClusterGroup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | 多集群分组 (federation scaffold) |
| `MongoDBFederation` | `mongodb.keiailab.com/v1alpha1` | Cluster | Alpha | 跨区域 federation |
| `MongoDBInsights` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | 性能洞察表面 |
| (通用类型) | `mongodb.keiailab.com/v1alpha1` | — | — | Conditions / Finalizers 辅助工具 |

## Reconcile 流程

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

- `MongoDB` reconciler: StatefulSet (3+ 副本) + headless Service + Secret (SCRAM keyfile) + cert-manager Certificate + PDB + ServiceMonitor
- `MongoDBSharded` reconciler: 3 个 StatefulSet (config / shard / mongos) 按顺序初始化
- `MongoDBBackup` reconciler: 将快照存入 S3 兼容存储或 PVC 的 Job

## RBAC 范围

- ClusterRole: CRD watch + cert-manager Certificate + PrometheusOperator ServiceMonitor
- Role (按命名空间): StatefulSet / Service / Secret / ConfigMap / PVC / PDB / NetworkPolicy
- ServiceAccount: `mongodb-operator` (应用 default-deny NetworkPolicy)

## keiailab-commons 导入表面

按 `keiailab-commons/ARCHITECTURE.md` 矩阵的采用情况: **6/8 (75%)**。

| 包 | 状态 | 用途 |
|---|---|---|
| `pkg/security` | ✅ | restricted PSA SecurityContext |
| `pkg/version` | ✅ | MongoDB 版本 allowlist |
| `pkg/labels` | ✅ | 推荐标签 (`app.kubernetes.io/*`) |
| `pkg/monitoring` | ⏳ | ServiceMonitor reconciler 本地实现 — commons 委派待办 |
| `pkg/networkpolicy` | ✅ | Deny-by-default + functional option |
| `pkg/webhook` | ⏳ | Admission validation 本地实现 — commons 委派待办 |
| `pkg/finalizer` | ✅ | `Add` / `Remove` / `Has` |
| `pkg/status` | ✅ | Condition reason 目录 |

## 测试层级

| 层级 | 位置 | 覆盖率 |
|---|---|---|
| Unit | `internal/**/_test.go` | ≥80% 目标 |
| Integration (envtest) | `test/integration/` | 核心 reconcile + finalizer 路径 |
| E2E (kind) | `test/e2e/` | release 关键场景 (RS + sharded + backup) |
| Scorecard | `bundle/tests/scorecard/` | OLM v1alpha3,与 postgres ADR-0013 实现 6-test parity |

## 构建 / 部署

### 构建产物 (按 release tag,如 v1.5.0)

| 产物 | 镜像 / 路径 | 用途 |
|---|---|---|
| Operator 容器 | `ghcr.io/keiailab/mongodb-operator:v1.5.0` | manager pod 运行时 |
| Helm chart | `charts/mongodb-operator/` → `helm package` | Path 2 安装 (单条命令) |
| OLM bundle (CSV + CRD + scorecard) | `bundle/` → `ghcr.io/keiailab/mongodb-operator-bundle:v1.5.0` | OLM 打包单元 (由 FBC 目录引用) |
| FBC 目录 | `deploy/catalog/` → `ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0` | OLM v1 ClusterCatalog 源 (ADR-0028 Phase D) |
| ArtifactHub repo | `artifacthub-repo.yml` | discovery + 签名验证 |
| SBOM | `make sbom` → SPDX-2.3 | SLSA / EU CRA |

### 3 种部署模式 (面向外部用户,ADR-0028 + ADR-0029)

| 模式 | 集群安装 | Operator 安装 | 现代性 | Day-2 |
|---|---|---|---|---|
| **OLM v1** *(推荐)* | `operator-controller + catalogd` (olmv1-system ns) | `ClusterCatalog + ClusterExtension` 仅 2 个资源 | 🟢 next-generation (2026-02 GA) | catalog channel + version pin/range |
| Helm chart | (无,direct deploy) | `helm install` | 🟡 stable | `helm upgrade/rollback` |
| OLM v0 *(legacy)* | `olm-operator + catalog-operator + packageserver` (olm ns) | `CatalogSource + OperatorGroup + Subscription + InstallPlan` | 🔴 maintenance mode | Subscription channel + approve |

详细步骤 + Day-2 upgrade/rollback: [Installation Guide](../install.md)。KeiaiLab Cluster 实时 evidence: [deploy/olm-v1/README.md](deploy/olm-v1/README.md) (OLM v0 path 已通过 ADR-0028 Phase D 废弃)。

### 发布流水线

- CI: ADR-0027 community-operators upstream sync (OLM v0 path) + 本 release tag → GHCR push (operator + bundle + catalog) + Helm chart Pages publish。
- Cosign: 容器镜像 + Helm chart + SBOM 均使用 keyless OIDC 签名 (G-13,ADR-0023)。
- Renovate: digest pinning (与 ADR-0066 一致)。

## Feature gate (beta scope 选择性启用)

按 `values.yaml`:
- `features.sharded.enabled` (默认 false) — gate `MongoDBSharded` CRD watch + RBAC
- `features.backup.enabled` (默认 false) — gate `MongoDBBackup` CRD watch + Job RBAC
- `features.autoscaling.enabled` (默认 false) — gate HPA reconciler

生产集群模式: 仅 GA。Beta CRD 需要显式 opt-in。

## ADR 交叉引用 (29 个 ADR)

主要项:
- ADR-0001: charter / 项目身份
- ADR-0013: scorecard OLM test parity 标准 (postgres co-author)
- ADR-0023: OperatorHub bundle scaffold
- ADR-0027: community-operators upstream sync 自动化
- ADR-0028: 外部用户运维水平 (5 项缺口消除,channel/maturity 晋升 stable)
- **ADR-0029: 采用 OLM v1 (next-generation,ClusterCatalog + ClusterExtension)**

完整列表: `docs/kb/adr/INDEX.md`。

## Roadmap 状态

- Phase 1 Production hardening: **100%** (21/21)
- Phase 2 Enterprise auth + multi-region: **100%** (21/21)
- Phase 3 Advanced enterprise: **100%** (16/16)
- Phase 4 Bitnami parity: **100%** (26/26)
- Phase 5 (post-v1.5.0): *定义中* — `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md`

## Non-goal (有意排除)

- ❌ MongoDB 7.0 以下版本 (依 `pkg/version` allowlist)
- ❌ Operator 捆绑 MongoDB Enterprise 二进制 (MIT 许可证边界)
- ❌ MongoDB Atlas / 云托管集成 (out of scope)
- ❌ 嵌入 `bitnami/mongodb` chart (我们以原生方式实现 parity)

## 参考资料

- `README.md` / `README.ko.md`
- `(../roadmap.md)` (Phase 1-4 100% complete)
- `(../changelog.md)`
- `ADOPTERS.md`
- `(../contributing.md)` / `CONTRIBUTING.ko.md`
- `(../governance.md)`
- `(../support.md)`
- `AGENTS.md` — AI 助手 runbook
- `docs/kb/adr/INDEX.md` — 28 个 ADR

