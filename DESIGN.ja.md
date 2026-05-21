<p align="center">
  <a href="DESIGN.md">English</a> |
  <a href="DESIGN.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="DESIGN.zh.md">中文</a>
</p>

# 設計 — mongodb-operator (Open Source)

> *オープンソース設計ドキュメント*。project charter + design decisions + extension points + contribution surface。本ドキュメントは *what + why* を扱い、*how* は [ARCHITECTURE.md](ARCHITECTURE.md) + ADR を参照してください。

## §1 Charter

mongodb-operator は **Kubernetes の declarative API を通じて MongoDB の Day-0 / Day-1 / Day-2 lifecycle を外部依存なしに運用** します。

**対象ユーザー**:
- *Day-0*: 新規の MongoDB cluster を Kubernetes manifest 1 つでデプロイしたい platform engineer
- *Day-1*: replicaset / sharded / backup / auth の *production-grade* 運用を求める SRE
- *Day-2*: MongoDB バージョンアップグレード + scale + restore の *無破壊な自動化* を求める DBRE

**Non-goals**:
- Atlas / DocumentDB のような managed service の代替 — 本 operator は *self-hosted MongoDB on Kubernetes* です。
- mongodb-community-operator や percona-server-mongodb-operator の fork — 本 operator は *独立実装* であり、別個の design 思想に基づきます。
- MongoDB Inc. license に依存する機能 (Enterprise Auditing、Atlas integration) — *strictly OSS* (`SSPLv1` 互換性の *回避* のため、*外部システム抽象化* のみを提供)。

## §2 中核となる設計原則 (5)

### §2.1 Declarative Boundary

*K8s API が single source of truth*。CR の `spec` が desired state であり、operator は *reconcile loop* を通じて actual state を spec に収束させます。operator 外部からの mutation (mongosh、manual scale など) は *immediately reconciled or rejected* — webhook で遮断します。

### §2.2 Minimal Surface

K8s 標準リソース (StatefulSet + PVC + Service + Secret) で表現可能なものは *そのまま使用* します。operator が独自の type を作ることはしません。CRD は *MongoDB-specific な invariant* (replicaSet メンバー数 + sharded topology + backup PITR) のみを導入します。

### §2.3 Race-Free Bootstrap

distributed lock (K8s Lease) を活用した *replicaset init + admin user bootstrap* の race-free 保証。多重 replica controller の *leader election* 標準 (controller-runtime) + *resource-level lease* (CR ごとの init lock)。

### §2.4 Defense-in-depth

- **Webhook**: `ValidatingAdmissionWebhook` で spec の invariant を検証 (storage size 負値、replicaset メンバー偶数など)。`failurePolicy=Fail` (ADR-0015)。
- **Status conditions**: K8s convention に整合 (meta.SetStatusCondition、ADR-0013)。state machine を明示。
- **NetworkPolicy** (helm chart の option): namespace-level zero-trust + ingress/egress allow-list。
- **Cosign**: container image + Helm chart + SBOM をすべて keyless OIDC signed (G-13、ADR-0023)。

### §2.5 Pluggable External Systems

LDAP / OIDC / Vault Transit / cross-cluster federation のような *external system integration* は *abstract interface* + *driver impl* に分離します (cycle 17)。新規 system 追加時は *driver のみ作成*。Core reconciler への影響はありません。

## §3 CRD surface

[ARCHITECTURE.md §CRD surface](ARCHITECTURE.md) を参照。3 owned CRDs (`MongoDB`、`MongoDBSharded`、`MongoDBBackup`) + 補助 CRDs。

各 CRD の `spec` は *user intent*、`status` は *operator observation* — 明確に分離されています。

## §4 Deployment models

3 path matrix — [INSTALL.md](INSTALL.md) の §1。本 design の要点:

- **OLM v1** (現代標準、ADR-0029) — *GitOps + ClusterExtension* 単一 manifest で operator+install。外部ユーザー公開の default。
- **Helm chart** — *local dev + single-cluster* のシンプルな経路。継続維持。
- **OLM v0** — *OpenShift / OperatorHub.io community-operators* 互換。legacy ではあるが、community-operators の *既存登録バージョン 0.3.0 → 1.5.0 upgrade* のために *継続 release* (ADR-0027 自動化)。

## §5 Extension Points

### §5.1 Custom Storage Backends

`spec.storage.storageClassName` — すべての CSI driver 互換。tested:
- Ceph RBD (`ceph-block`)、Ceph FS (`ceph-fs`)
- Rook Ceph (rook-ceph.svc)
- LocalPV (single-node dev)

新規 storage 追加 — *driver 作成は不要*。ただし *latency 測定* + *RPO/RTO シナリオ* e2e を推奨。

### §5.2 Backup Storage (MongoDBBackup)

`spec.storage.type` で分岐:
- `s3` — Rook Ceph RGW S3 (default、本 cluster パターン)、AWS S3、MinIO
- `gcs`、`azure-blob` (planned)
- `nfs` (legacy)

新規 backend — `internal/backup/<driver>/` を新設 + `Storage` interface を実装 (cycle 15 パターン)。

### §5.3 External Authentication

`internal/external/` の driver:
- `ldap/` — LDAP/AD probe + bind
- `oidc/` — OIDC discovery + JWT verify
- `vault/` — Vault Transit (envelope encryption)

新規 IdP — driver を追加 + `external.Provider` を実装。ADR で適用を正当化。

### §5.4 Webhook / Mutation policies

`internal/webhook/` — `Validator` + `Defaulter` interfaces。新規 invariant の追加 = `Validate*` メソッド追加 + e2e テスト (ADR-0017 — *unreachable invariant の reject* は dead code に分類)。

## §6 Compatibility Matrix (v1.5.0 基準)

| 領域 | Supported |
|---|---|
| Kubernetes | v1.26+、tested v1.26~v1.36 (k3s) |
| MongoDB | 7.0、8.0、8.3 (`spec.version.version`) |
| OLM | v1.x (recommended) + v0.30+ (legacy) |
| Helm | v3.8+ |
| cert-manager | v1.20+ (webhook TLS) |
| CSI | すべての dynamic provisioner (storageClassName) |
| Architectures | linux/amd64 (production)、linux/arm64 (operator-controller intermediate only) |

## §7 Contribution Surface

### §7.1 4 typical contributions

| Type | 入口 | 検証 |
|---|---|---|
| **Bug fix** | `internal/controller/*` | unit + envtest + e2e (`test/e2e/`) |
| **New feature** | RFC または ADR を先に → `internal/<feature>/` | scorecard + e2e + ROADMAP entry |
| **External system driver** | `internal/external/<driver>/` | LDAP probe / OIDC discovery / Vault Transit (cycle 17 パターン) |
| **Documentation** | `README.md` + `INSTALL.md` + `docs/` | live-verified marker (workflow §2.7) |

### §7.2 Quality gates (PR マージ条件)

- リント 0 (ruff/biome/clippy 該当領域; mongodb-operator は Go — `golangci-lint`)
- typecheck 0 (`go vet ./...`)
- テスト PASS (`make test` + `make e2e` for breaking)
- conventional commits (`<type>(<scope>): <subject>`)
- ADR または RFC reference (グローバル標準を逸脱する場合)

### §7.3 Maintainers

[MAINTAINERS.md](MAINTAINERS.md) を参照。

## §8 Roadmap / Non-goals

Detailed: [ROADMAP.md](ROADMAP.md)。

要点:
- v1.5.0 = sharded GA + Webhook validation + Cosign + OLM v1 外部ユーザー公開。
- v1.6.x (planned) = narrow OLM v1 installer RBAC + community-operators 0.3.0 → 1.5.0 upstream sync + mailstory FerretDB cutover plan (別 plan)。
- v2.0 候補 — Enterprise auth (Kerberos)、multi-cluster federation、FIPS mode。

**Non-goals** (意図的に対象外):
- ❌ MongoDB Atlas (managed service) の代替
- ❌ Enterprise Auditing (license 領域)
- ❌ Embedded MongoDB binary (image-only)
- ❌ MongoDB Inc. specific API extensions

## §9 Open Source Lifecycle

### §9.1 License + Governance

- License: Apache-2.0 (LICENSE ファイル)
- Governance: [GOVERNANCE.md](GOVERNANCE.md)
- Code of Conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- Security: [SECURITY.md](SECURITY.md) — CVE coordinator、supported versions

### §9.2 Release cadence

- **Patch** (`vX.Y.Z+1`): 1-2 週間、security または critical bug
- **Minor** (`vX.Y+1.0`): 4-8 週間、new feature
- **Major** (`vX+1.0.0`): N/A まで (v1.5.0 stable、v2 の charter 変更時点)

### §9.3 Public artifacts

- ArtifactHub: <https://artifacthub.io/packages/helm/mongodb-operator/mongodb-operator>
- OperatorHub.io: <https://operatorhub.io/operator/mongodb-operator> (community-operators 登録)
- ghcr.io/keiailab — operator + bundle + catalog images (public または internal — INSTALL §5 を参照)
- GitHub releases — source tarball + checksums + SBOM + Cosign signatures

### §9.4 Issue / PR flow

- GitHub Issues — bug + feature request + RFC discussion
- GitHub Discussions — Q&A + design proposal
- PR — `feat|fix|docs|refactor|test|chore` conventional types、AI co-author を明示 (Co-Authored-By)
- Review SLA — 24h (maintainer pool)

## §10 design 決定の self-documentation

本 design における *非可逆な決定はすべて* ADR として保存します。ADR-0001 から ADR-N まで (現在 29 件)。`docs/kb/adr/INDEX.md` が SSOT です。

design 変更の手順:
1. **軽微な修正**: 本 DESIGN.md への直接 PR
2. **方針変更**: ADR 作成 + 本 DESIGN.md の reference 更新
3. **cross-repo な影響**: ai-dev/rfcs/ の RFC を経て ADR として採択

## §11 References

- [README.md](README.md) — project overview + Quick Start
- [INSTALL.md](INSTALL.md) — 3-path installation guide
- [ARCHITECTURE.md](ARCHITECTURE.md) — internal architecture (CRD + RBAC + reconcile flow)
- [ROADMAP.md](ROADMAP.md) — feature roadmap
- [CONTRIBUTING.md](CONTRIBUTING.md) — PR + commit conventions
- [docs/kb/adr/INDEX.md](docs/kb/adr/INDEX.md) — 29 ADRs (すべての決定の記録)
- [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) — 外部ユーザー運用水準
- [ADR-0029](docs/kb/adr/0029-olm-v1-migration-from-v0.md) — OLM v1 採用 (現代標準)
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
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://keiailab.com">keiailab.com</a>
</p>
