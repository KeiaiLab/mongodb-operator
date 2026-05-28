<p align="center">
  <a href="DESIGN.md">English</a> |
  <b>한국어</b> |
  <a href="DESIGN.ja.md">日本語</a> |
  <a href="DESIGN.zh.md">中文</a>
</p>

# DESIGN — mongodb-operator (Open Source) (한국어)

> English DESIGN: [DESIGN.md](DESIGN.md) — canonical / 정본

> *오픈소스 설계 문서*. project charter + design decisions + extension points + contribution surface. 본 문서는 *what + why*. *how* 는 [ARCHITECTURE.md](ARCHITECTURE.md) + ADR.

## §1 Charter

mongodb-operator 는 **Kubernetes 의 declarative API 로 MongoDB 의 Day-0 / Day-1 / Day-2 lifecycle 을 외부 종속성 없이 운영** 합니다.

**대상 사용자**:
- *Day-0*: 신규 MongoDB cluster 를 Kubernetes manifest 1 개로 배포하려는 platform engineer
- *Day-1*: replicaset / sharded / backup / auth 의 *production-grade* 운영을 원하는 SRE
- *Day-2*: MongoDB 버전 업그레이드 + scale + restore 의 *무파괴 자동화* 를 원하는 DBRE

**Non-goals**:
- Atlas / DocumentDB 같은 managed service 의 대체 — 본 operator 는 *self-hosted MongoDB on Kubernetes*.
- mongodb-community-operator 또는 percona-server-mongodb-operator 의 fork — 본 operator 는 *독립 구현*, 별도 design 정신.
- MongoDB Inc. license 종속 기능 (Enterprise Auditing, Atlas integration) — *strictly OSS* (`SSPLv1` 호환 *우회* 를 위해 *외부 시스템 추상화* 만 제공).

## §2 핵심 설계 원칙 (5)

### §2.1 Declarative Boundary

*K8s API 가 single source of truth*. CR 의 `spec` 이 desired state, operator 가 *reconcile loop* 으로 actual state 를 spec 에 수렴시킵니다. operator 외부의 mutation (mongosh, manual scale, etc.) 은 *immediately reconciled or rejected* — webhook 으로 차단합니다.

### §2.2 Minimal Surface

K8s 표준 자원 (StatefulSet + PVC + Service + Secret) 으로 표현 가능한 것은 *그대로 사용*. operator 가 자체 type 을 만들지 않습니다. CRD 는 *MongoDB-specific 의 invariant* (replicaSet 멤버 수 + sharded topology + backup PITR) 만 도입합니다.

### §2.3 Race-Free Bootstrap

distributed lock (K8s Lease) 을 활용한 *replicaset init + admin user 부트스트랩* 의 race-free 보장. 다중 replica controller 의 *leader election* 표준 (controller-runtime) + *resource-level lease* (CR 별 init lock).

### §2.4 Defense-in-depth

- **Webhook**: `ValidatingAdmissionWebhook` 으로 spec 의 invariant 검증 (storage size 음수, replicaset 멤버 짝수, etc.). `failurePolicy=Fail` (ADR-0015).
- **Status conditions**: K8s convention 정합 (meta.SetStatusCondition, ADR-0013). state machine 명시.
- **NetworkPolicy** (helm chart 의 옵션): namespace-level zero-trust + ingress/egress allow-list.
- **Cosign**: container image + Helm chart + SBOM 모두 keyless OIDC signed (G-13, ADR-0023).

### §2.5 Pluggable External Systems

LDAP / OIDC / Vault Transit / cross-cluster federation 같은 *external system integration* 은 *abstract interface* + *driver impl* 로 분리 (cycle 17). 신규 system 추가 시 *driver 만 작성*. Core reconciler 무영향.

## §3 CRD surface

[ARCHITECTURE.md §CRD surface](ARCHITECTURE.md) 참조. 3 owned CRDs (`MongoDB`, `MongoDBSharded`, `MongoDBBackup`) + 보조 CRDs.

각 CRD 의 `spec` 은 *user intent*, `status` 는 *operator observation* — 명확하게 분리됩니다.

## §4 Deployment models

3 path matrix — [INSTALL.md](INSTALL.md) 의 §1. 본 design 의 핵심:

- **OLM v1** (현대 표준, ADR-0029) — *GitOps + ClusterExtension* 단일 manifest 로 operator+install. 외부 사용자 노출의 default.
- **Helm chart** — *local dev + single-cluster* 의 단순 경로. 보존.
- **OLM v0** — *OpenShift / OperatorHub.io community-operators* 호환. legacy 이지만 community-operators 의 *기존 등록 버전 0.3.0 → 1.5.0 upgrade* 를 위해 *지속 release* (ADR-0027 자동화).

## §5 Extension Points

### §5.1 Custom Storage Backends

`spec.storage.storageClassName` — 모든 CSI driver 호환. tested:
- Ceph RBD (`ceph-block`), Ceph FS (`ceph-fs`)
- Rook Ceph (rook-ceph.svc)
- LocalPV (single-node dev)

신규 storage 추가 — *driver 작성 불필요*. 단 *latency 측정* + *RPO/RTO 시나리오* e2e 권장.

### §5.2 Backup Storage (MongoDBBackup)

`spec.storage.type` 으로 분기:
- `s3` — Rook Ceph RGW S3 (default, 본 cluster 패턴), AWS S3, MinIO
- `gcs`, `azure-blob` (planned)
- `nfs` (legacy)

신규 backend — `internal/backup/<driver>/` 신설 + `Storage` interface 구현 (cycle 15 패턴).

### §5.3 External Authentication

`internal/external/` 의 driver:
- `ldap/` — LDAP/AD probe + bind
- `oidc/` — OIDC discovery + JWT verify
- `vault/` — Vault Transit (envelope encryption)

신규 IdP — driver 추가 + `external.Provider` 구현. ADR 으로 적용 정당화.

### §5.4 Webhook / Mutation policies

`internal/webhook/` — `Validator` + `Defaulter` interfaces. 신규 invariant 추가 = `Validate*` method 추가 + e2e 테스트 (ADR-0017 — *unreachable invariant 거부* 시 dead code 로 분류).

## §6 Compatibility Matrix (v1.5.0 기준)

| 영역 | Supported |
|---|---|
| Kubernetes | v1.26+, tested v1.26~v1.36 (k3s) |
| MongoDB | 7.0, 8.0, 8.3 (`spec.version.version`) |
| OLM | v1.x (recommended) + v0.30+ (legacy) |
| Helm | v3.8+ |
| cert-manager | v1.20+ (webhook TLS) |
| CSI | 모든 dynamic provisioner (storageClassName) |
| Architectures | linux/amd64 (production), linux/arm64 (operator-controller intermediate only) |

## §7 Contribution Surface

### §7.1 4 typical contributions

| Type | 입구 | 검증 |
|---|---|---|
| **Bug fix** | `internal/controller/*` | unit + envtest + e2e (`test/e2e/`) |
| **New feature** | RFC 또는 ADR 먼저 → `internal/<feature>/` | scorecard + e2e + ROADMAP entry |
| **External system driver** | `internal/external/<driver>/` | LDAP probe / OIDC discovery / Vault Transit (cycle 17 패턴) |
| **Documentation** | `README.md` + `INSTALL.md` + `docs/` | live-verified marker (workflow §2.7) |

### §7.2 Quality gates (PR 머지 조건)

- 린트 0 (ruff/biome/clippy 해당 영역; mongodb-operator 는 Go — `golangci-lint`)
- typecheck 0 (`go vet ./...`)
- 테스트 PASS (`make test` + `make e2e` for breaking)
- conventional commits (`<type>(<scope>): <subject>`)
- ADR 또는 RFC reference (글로벌 표준 일탈 시)

### §7.3 Maintainers

[MAINTAINERS.md](MAINTAINERS.md) 참조.

## §8 Roadmap / Non-goals

Detailed: [ROADMAP.md](ROADMAP.md).

핵심:
- v1.5.0 = sharded GA + Webhook validation + Cosign + OLM v1 외부 사용자 노출.
- v1.6.x (planned) = narrow OLM v1 installer RBAC + community-operators 0.3.0 → 1.5.0 upstream sync + mailstory FerretDB cutover plan (별도 plan).
- v2.0 후보 — Enterprise auth (Kerberos), multi-cluster federation, FIPS mode.

**Non-goals** (의식적 비대상):
- ❌ MongoDB Atlas (managed service) 대체
- ❌ Enterprise Auditing (license 영역)
- ❌ Embedded MongoDB binary (image-only)
- ❌ MongoDB Inc. specific API extensions

## §9 Open Source Lifecycle

### §9.1 License + Governance

- License: Apache-2.0 (LICENSE 파일)
- Governance: [GOVERNANCE.md](GOVERNANCE.md)
- Code of Conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- Security: [SECURITY.md](SECURITY.md) — CVE coordinator, supported versions

### §9.2 Release cadence

- **Patch** (`vX.Y.Z+1`): 1-2 주, security 또는 critical bug
- **Minor** (`vX.Y+1.0`): 4-8 주, new feature
- **Major** (`vX+1.0.0`): N/A 까지 (v1.5.0 stable, v2 의 charter 변경 시점)

### §9.3 Public artifacts

- ArtifactHub: <https://artifacthub.io/packages/helm/mongodb-operator/mongodb-operator>
- OperatorHub.io: <https://operatorhub.io/operator/mongodb-operator> (community-operators 등록)
- ghcr.io/keiailab — operator + bundle + catalog images (public 또는 internal — INSTALL §5 참조)
- GitHub releases — source tarball + checksums + SBOM + Cosign signatures

### §9.4 Issue / PR flow

- GitHub Issues — bug + feature request + RFC discussion
- GitHub Discussions — Q&A + design proposal
- PR — `feat|fix|docs|refactor|test|chore` conventional types, AI co-author 명시 (Co-Authored-By)
- Review SLA — 24h (maintainer pool)

## §10 design 결정의 self-documentation

본 design 의 *모든 비역행 결정* 은 ADR 로 보존합니다. ADR-0001 부터 ADR-N (현재 29 개). `docs/kb/adr/INDEX.md` 가 SSOT.

design 변경 절차:
1. **사소한 수정**: 본 DESIGN.md 직접 PR
2. **정책 변경**: ADR 작성 + 본 DESIGN.md 의 reference 갱신
3. **cross-repo 영향**: ai-dev/rfcs/ 의 RFC 후 ADR 으로 채택

## §11 References

- [README.md](README.md) — project overview + Quick Start
- [INSTALL.md](INSTALL.md) — 3-path installation guide
- [ARCHITECTURE.md](ARCHITECTURE.md) — internal architecture (CRD + RBAC + reconcile flow)
- [ROADMAP.md](ROADMAP.md) — feature roadmap
- [CONTRIBUTING.md](CONTRIBUTING.md) — PR + commit conventions
- [docs/kb/adr/INDEX.md](docs/kb/adr/INDEX.md) — 29 ADRs (모든 결정 기록)
- [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) — 외부 사용자 운영 수준
- [ADR-0029](docs/kb/adr/0029-olm-v1-migration-from-v0.md) — OLM v1 채택 (현대 표준)
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
