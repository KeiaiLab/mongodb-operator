<p align="center">
  <a href="DESIGN.md">English</a> |
  <a href="DESIGN.ko.md">한국어</a> |
  <a href="DESIGN.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 设计 — mongodb-operator (Open Source)

> *开源设计文档*。project charter + design decisions + extension points + contribution surface。本文档聚焦 *what + why*,*how* 请参阅 [ARCHITECTURE.md](ARCHITECTURE.md) + ADR。

## §1 Charter

mongodb-operator 通过 **Kubernetes 的 declarative API 在零外部依赖下运维 MongoDB 的 Day-0 / Day-1 / Day-2 lifecycle**。

**目标用户**:
- *Day-0*: 希望用单个 Kubernetes manifest 部署新 MongoDB cluster 的 platform engineer
- *Day-1*: 追求 replicaset / sharded / backup / auth 的 *production-grade* 运维的 SRE
- *Day-2*: 追求 MongoDB 版本升级 + scale + restore 的 *无破坏自动化* 的 DBRE

**Non-goals**:
- 替代 Atlas / DocumentDB 等 managed service — 本 operator 是 *self-hosted MongoDB on Kubernetes*。
- mongodb-community-operator 或 percona-server-mongodb-operator 的 fork — 本 operator 是 *独立实现*,基于不同的 design 思想。
- 依赖 MongoDB Inc. license 的能力 (Enterprise Auditing、Atlas integration) — *strictly OSS* (为 *规避* `SSPLv1` 兼容性,仅提供 *外部系统抽象*)。

## §2 核心设计原则 (5 条)

### §2.1 Declarative Boundary

*K8s API 是 single source of truth*。CR 的 `spec` 即 desired state,operator 通过 *reconcile loop* 将 actual state 向 spec 收敛。来自 operator 外部的 mutation (mongosh、manual scale 等) 会被 *immediately reconciled or rejected* — webhook 进行拦截。

### §2.2 Minimal Surface

凡是可以用 K8s 标准资源 (StatefulSet + PVC + Service + Secret) 表达的内容,均 *直接使用*。operator 不会自造 type。CRD 仅引入 *MongoDB-specific 的 invariant* (replicaSet 成员数 + sharded topology + backup PITR)。

### §2.3 Race-Free Bootstrap

通过 distributed lock (K8s Lease) 保证 *replicaset init + admin user bootstrap* 的 race-free。多 replica controller 的 *leader election* 标准 (controller-runtime) + *resource-level lease* (每个 CR 的 init lock)。

### §2.4 Defense-in-depth

- **Webhook**: 使用 `ValidatingAdmissionWebhook` 校验 spec 的 invariant (storage size 负值、replicaset 成员数为偶数等)。`failurePolicy=Fail` (ADR-0015)。
- **Status conditions**: 对齐 K8s convention (meta.SetStatusCondition、ADR-0013)。明示 state machine。
- **NetworkPolicy** (helm chart 选项): namespace-level zero-trust + ingress/egress allow-list。
- **Cosign**: container image + Helm chart + SBOM 全部 keyless OIDC signed (G-13、ADR-0023)。

### §2.5 Pluggable External Systems

LDAP / OIDC / Vault Transit / cross-cluster federation 这类 *external system integration* 拆分为 *abstract interface* + *driver impl* (cycle 17)。新增 system 时 *只需编写 driver*。Core reconciler 不受影响。

## §3 CRD surface

参见 [ARCHITECTURE.md §CRD surface](ARCHITECTURE.md)。3 owned CRDs (`MongoDB`、`MongoDBSharded`、`MongoDBBackup`) + 辅助 CRDs。

每个 CRD 的 `spec` 表示 *user intent*,`status` 表示 *operator observation* — 明确分离。

## §4 Deployment models

3 path matrix — [INSTALL.md](INSTALL.md) 的 §1。本 design 要点:

- **OLM v1** (现代标准,ADR-0029) — *GitOps + ClusterExtension* 单 manifest 即可 operator+install。面向外部用户的默认方式。
- **Helm chart** — *local dev + single-cluster* 的简单路径。继续保留。
- **OLM v0** — 兼容 *OpenShift / OperatorHub.io community-operators*。legacy,但为了将 community-operators *已注册版本从 0.3.0 升级至 1.5.0*,仍 *持续 release* (ADR-0027 自动化)。

## §5 Extension Points

### §5.1 Custom Storage Backends

`spec.storage.storageClassName` — 兼容所有 CSI driver。tested:
- Ceph RBD (`ceph-block`)、Ceph FS (`ceph-fs`)
- Rook Ceph (rook-ceph.svc)
- LocalPV (single-node dev)

新增 storage — *无需编写 driver*。但建议补充 *latency 测量* + *RPO/RTO 场景* e2e。

### §5.2 Backup Storage (MongoDBBackup)

通过 `spec.storage.type` 分支:
- `s3` — Rook Ceph RGW S3 (default,本 cluster 模式)、AWS S3、MinIO
- `gcs`、`azure-blob` (planned)
- `nfs` (legacy)

新增 backend — 新建 `internal/backup/<driver>/` + 实现 `Storage` interface (cycle 15 模式)。

### §5.3 External Authentication

`internal/external/` 下的 driver:
- `ldap/` — LDAP/AD probe + bind
- `oidc/` — OIDC discovery + JWT verify
- `vault/` — Vault Transit (envelope encryption)

新增 IdP — 添加 driver + 实现 `external.Provider`。通过 ADR 论证采用理由。

### §5.4 Webhook / Mutation policies

`internal/webhook/` — `Validator` + `Defaulter` interfaces。新增 invariant = 增加 `Validate*` 方法 + e2e 测试 (ADR-0017 — *unreachable invariant 的 reject* 视为 dead code)。

## §6 Compatibility Matrix (以 v1.5.0 为准)

| 领域 | Supported |
|---|---|
| Kubernetes | v1.26+,tested v1.26~v1.36 (k3s) |
| MongoDB | 7.0、8.0、8.3 (`spec.version.version`) |
| OLM | v1.x (recommended) + v0.30+ (legacy) |
| Helm | v3.8+ |
| cert-manager | v1.20+ (webhook TLS) |
| CSI | 所有 dynamic provisioner (storageClassName) |
| Architectures | linux/amd64 (production)、linux/arm64 (operator-controller intermediate only) |

## §7 Contribution Surface

### §7.1 4 typical contributions

| Type | 入口 | 验证 |
|---|---|---|
| **Bug fix** | `internal/controller/*` | unit + envtest + e2e (`test/e2e/`) |
| **New feature** | 先提交 RFC 或 ADR → `internal/<feature>/` | scorecard + e2e + ROADMAP entry |
| **External system driver** | `internal/external/<driver>/` | LDAP probe / OIDC discovery / Vault Transit (cycle 17 模式) |
| **Documentation** | `README.md` + `INSTALL.md` + `docs/` | live-verified marker (workflow §2.7) |

### §7.2 Quality gates (PR 合并条件)

- lint 0 (ruff/biome/clippy 对应领域; mongodb-operator 使用 Go — `golangci-lint`)
- typecheck 0 (`go vet ./...`)
- 测试 PASS (`make test` + `make e2e` for breaking)
- conventional commits (`<type>(<scope>): <subject>`)
- ADR 或 RFC reference (偏离全局标准时)

### §7.3 Maintainers

参见 [MAINTAINERS.md](MAINTAINERS.md)。

## §8 Roadmap / Non-goals

详见: [ROADMAP.md](ROADMAP.md)。

要点:
- v1.5.0 = sharded GA + Webhook validation + Cosign + OLM v1 对外用户公开。
- v1.6.x (planned) = narrow OLM v1 installer RBAC + community-operators 0.3.0 → 1.5.0 upstream sync + mailstory FerretDB cutover plan (单独 plan)。
- v2.0 候选 — Enterprise auth (Kerberos)、multi-cluster federation、FIPS mode。

**Non-goals** (有意排除):
- ❌ 替代 MongoDB Atlas (managed service)
- ❌ Enterprise Auditing (license 领域)
- ❌ Embedded MongoDB binary (image-only)
- ❌ MongoDB Inc. specific API extensions

## §9 Open Source Lifecycle

### §9.1 License + Governance

- License: Apache-2.0 (LICENSE 文件)
- Governance: [GOVERNANCE.md](GOVERNANCE.md)
- Code of Conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- Security: [SECURITY.md](SECURITY.md) — CVE coordinator、supported versions

### §9.2 Release cadence

- **Patch** (`vX.Y.Z+1`): 1-2 周,security 或 critical bug
- **Minor** (`vX.Y+1.0`): 4-8 周,new feature
- **Major** (`vX+1.0.0`): 暂无 (v1.5.0 stable,等到 v2 的 charter 发生变更时)

### §9.3 Public artifacts

- ArtifactHub: <https://artifacthub.io/packages/helm/mongodb-operator/mongodb-operator>
- OperatorHub.io: <https://operatorhub.io/operator/mongodb-operator> (community-operators 注册)
- ghcr.io/keiailab — operator + bundle + catalog images (public 或 internal — 参见 INSTALL §5)
- GitHub releases — source tarball + checksums + SBOM + Cosign signatures

### §9.4 Issue / PR flow

- GitHub Issues — bug + feature request + RFC discussion
- GitHub Discussions — Q&A + design proposal
- PR — `feat|fix|docs|refactor|test|chore` conventional types,明示 AI co-author (Co-Authored-By)
- Review SLA — 24h (maintainer pool)

## §10 design 决策的 self-documentation

本 design 的 *所有不可逆决策* 均以 ADR 保存。从 ADR-0001 至 ADR-N (目前 29 条)。`docs/kb/adr/INDEX.md` 为 SSOT。

design 变更流程:
1. **轻微修改**: 直接提交 PR 到本 DESIGN.md
2. **策略变更**: 编写 ADR + 更新本 DESIGN.md 的 reference
3. **cross-repo 影响**: 经 ai-dev/rfcs/ 的 RFC 后,以 ADR 形式采纳

## §11 References

- [README.md](README.md) — project overview + Quick Start
- [INSTALL.md](INSTALL.md) — 3-path installation guide
- [ARCHITECTURE.md](ARCHITECTURE.md) — internal architecture (CRD + RBAC + reconcile flow)
- [ROADMAP.md](ROADMAP.md) — feature roadmap
- [CONTRIBUTING.md](CONTRIBUTING.md) — PR + commit conventions
- [docs/kb/adr/INDEX.md](docs/kb/adr/INDEX.md) — 29 ADRs (所有决策记录)
- [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) — 外部用户运维水平
- [ADR-0029](docs/kb/adr/0029-olm-v1-migration-from-v0.md) — OLM v1 采用 (现代标准)
- [Red Hat: OLM v1 announcement (2025-11)](https://www.redhat.com/en/blog/announcing-olm-v1-next-generation-operator-lifecycle-management)
- [operator-framework/operator-controller](https://github.com/operator-framework/operator-controller)

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://github.com/keiailab">keiailab.com</a>
</p>
