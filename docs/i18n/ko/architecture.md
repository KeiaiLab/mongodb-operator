<p align="center">
  <a href="ARCHITECTURE.md">English</a> |
  <b>한국어</b> |
  <a href="ARCHITECTURE.ja.md">日本語</a> |
  <a href="ARCHITECTURE.zh.md">中文</a>
</p>

# ARCHITECTURE — mongodb-operator (한국어)

> English ARCHITECTURE: [Architecture](../architecture.md) — canonical / 정본

> 단일 페이지 아키텍처 기술서. CRD 표면 / RBAC / reconcile 패턴이 바뀔 때 갱신됩니다.

## 개요 (Overview)

- **목적**: 선언적 CRD 를 통해 MongoDB ReplicaSet 및 Sharded Cluster 의 배포, 스케일링, 운영을 자동화하는 Kubernetes Operator.
- **범위**: MongoDB 7.0+ 배포에 대해 `MongoDB`, `MongoDBSharded`, `MongoDBBackup`, federation, insights CRD 를 reconcile 하는 K8s 컨트롤러.
- **안정성 티어**: v1.5.0 (GA scope = ReplicaSet, Sharded / Backup / HPA = beta feature gate).
- **최신 릴리스**: v1.5.0 (2026-05-13)
- **라이선스**: MIT
- **모듈 경로**: `github.com/keiailab/mongodb-operator`

## CRD 표면 (CRD surface — 8 CRD)

| CRD | apiVersion | Scope | Tier | 설명 |
|---|---|---|---|---|
| `MongoDB` | `mongodb.keiailab.com/v1alpha1` | Namespaced | **GA** | 3 멤버 이상 ReplicaSet + 자동 failover |
| `MongoDBSharded` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (feature gate `sharded.enabled`) | Sharded 클러스터: config server + shard + mongos 라우터 |
| `MongoDBBackup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (`backup.enabled`) | S3 또는 PVC 백업 스냅샷 |
| `MongoDBBackupVerification` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta | 복원 드릴 검증 |
| `MongoDBClusterGroup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | 멀티 클러스터 그룹핑 (federation scaffold) |
| `MongoDBFederation` | `mongodb.keiailab.com/v1alpha1` | Cluster | Alpha | 리전 간 federation |
| `MongoDBInsights` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | 성능 인사이트 표면 |
| (공용 타입) | `mongodb.keiailab.com/v1alpha1` | — | — | Conditions / Finalizers helper |

## Reconcile 흐름 (Reconcile flow)

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

- `MongoDB` reconciler: StatefulSet (3+ replica) + headless Service + Secret (SCRAM keyfile) + cert-manager Certificate + PDB + ServiceMonitor
- `MongoDBSharded` reconciler: 3 개 StatefulSet (config / shard / mongos) 을 순서대로 초기화
- `MongoDBBackup` reconciler: S3 호환 스토리지 또는 PVC 로 스냅샷을 떠올리는 Job

## RBAC 범위 (RBAC scope)

- ClusterRole: CRD watch + cert-manager Certificate + PrometheusOperator ServiceMonitor
- Role (네임스페이스별): StatefulSet / Service / Secret / ConfigMap / PVC / PDB / NetworkPolicy
- ServiceAccount: `mongodb-operator` (default-deny NetworkPolicy 적용)

## keiailab-commons import 표면 (keiailab-commons import surface)

`keiailab-commons/ARCHITECTURE.md` 매트릭스 기준 채택률: **6/8 (75%)**.

| 패키지 | 상태 | 용도 |
|---|---|---|
| `pkg/security` | ✅ | restricted PSA SecurityContext |
| `pkg/version` | ✅ | MongoDB 버전 allowlist |
| `pkg/labels` | ✅ | 권장 라벨 (`app.kubernetes.io/*`) |
| `pkg/monitoring` | ⏳ | ServiceMonitor reconciler 로컬 구현 — commons 위임 대기 |
| `pkg/networkpolicy` | ✅ | Deny-by-default + functional option |
| `pkg/webhook` | ⏳ | Admission validation 로컬 구현 — commons 위임 대기 |
| `pkg/finalizer` | ✅ | `Add` / `Remove` / `Has` |
| `pkg/status` | ✅ | Condition reason 카탈로그 |

## 테스트 계층 (Test layers)

| 계층 | 위치 | 커버리지 |
|---|---|---|
| Unit | `internal/**/_test.go` | 80% 목표 이상 |
| Integration (envtest) | `test/integration/` | 핵심 reconcile + finalizer 경로 |
| E2E (kind) | `test/e2e/` | release 핵심 시나리오 (RS + sharded + backup) |
| Scorecard | `bundle/tests/scorecard/` | OLM v1alpha3, postgres ADR-0013 과 6-test parity |

## 빌드 / 배포 (Build / deploy)

### 빌드 산출물 (Build artifacts, release tag 단위, 예: v1.5.0)

| 산출물 | 이미지 / 경로 | 용도 |
|---|---|---|
| Operator 컨테이너 | `ghcr.io/keiailab/mongodb-operator:v1.5.0` | manager pod 런타임 |
| Helm chart | `charts/mongodb-operator/` → `helm package` | Path 2 설치 (단일 명령) |
| OLM bundle (CSV + CRD + scorecard) | `bundle/` → `ghcr.io/keiailab/mongodb-operator-bundle:v1.5.0` | OLM 패키징 단위 (FBC 카탈로그에서 참조) |
| FBC 카탈로그 | `deploy/catalog/` → `ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0` | OLM v1 ClusterCatalog 소스 (ADR-0028 Phase D) |
| ArtifactHub repo | `artifacthub-repo.yml` | discovery + 서명 검증 |
| SBOM | `make sbom` → SPDX-2.3 | SLSA / EU CRA |

### 3 가지 배포 모델 (외부 사용자 노출, ADR-0028 + ADR-0029)

| 모델 | Cluster 설치 | Operator 설치 | 최신성 | Day-2 |
|---|---|---|---|---|
| **OLM v1** *(권장)* | `operator-controller + catalogd` (olmv1-system ns) | `ClusterCatalog + ClusterExtension` 단 2 자원 | 🟢 next-generation (2026-02 GA) | catalog channel + version pin/range |
| Helm chart | (없음, direct deploy) | `helm install` | 🟡 stable | `helm upgrade/rollback` |
| OLM v0 *(legacy)* | `olm-operator + catalog-operator + packageserver` (olm ns) | `CatalogSource + OperatorGroup + Subscription + InstallPlan` | 🔴 maintenance mode | Subscription channel + approve |

상세 절차 + Day-2 upgrade/rollback: [Installation Guide](../install.md). KeiaiLab Cluster 라이브 evidence: [deploy/olm-v1/README.md](deploy/olm-v1/README.md) (OLM v0 path 는 ADR-0028 Phase D 로 폐기).

### 릴리스 파이프라인 (Release pipeline)

- CI: ADR-0027 community-operators upstream sync (OLM v0 path) + 본 release tag → GHCR push (operator + bundle + catalog) + Helm chart Pages publish.
- Cosign: 컨테이너 이미지 + Helm chart + SBOM 모두 keyless OIDC signed (G-13, ADR-0023).
- Renovate: digest pinning (ADR-0066 정합).

## Feature gate (beta scope opt-in)

`values.yaml` 기준:
- `features.sharded.enabled` (기본 false) — `MongoDBSharded` CRD watch + RBAC 를 gate
- `features.backup.enabled` (기본 false) — `MongoDBBackup` CRD watch + Job RBAC 를 gate
- `features.autoscaling.enabled` (기본 false) — HPA reconciler 를 gate

프로덕션 클러스터 패턴: GA 전용. Beta CRD 는 명시적 opt-in 필요.

## ADR 교차 링크 (29 ADR)

주요:
- ADR-0001: charter / 프로젝트 정체성
- ADR-0013: scorecard OLM test parity 표준 (postgres co-author)
- ADR-0023: OperatorHub bundle scaffold
- ADR-0027: community-operators upstream sync 자동화
- ADR-0028: 외부 사용자 운영 수준 (5 결격 해소, channel/maturity stable 승격)
- **ADR-0029: OLM v1 채택 (next-generation, ClusterCatalog + ClusterExtension)**

전체 목록: `docs/kb/adr/INDEX.md`.

## Roadmap 상태 (Roadmap status)

- Phase 1 Production hardening: **100%** (21/21)
- Phase 2 Enterprise auth + multi-region: **100%** (21/21)
- Phase 3 Advanced enterprise: **100%** (16/16)
- Phase 4 Bitnami parity: **100%** (26/26)
- Phase 5 (post-v1.5.0): *정의 중* — `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md`

## Non-goal (의도적 비목표)

- ❌ MongoDB 7.0 미만 버전 (`pkg/version` allowlist 기준)
- ❌ Operator 에 MongoDB Enterprise 바이너리 동봉 (MIT 라이선스 경계)
- ❌ MongoDB Atlas / cloud-managed 통합 (out of scope)
- ❌ `bitnami/mongodb` chart 임베드 (자체 구현으로 parity 달성)

## 참고 문서 (References)

- `README.md` / `README.ko.md`
- `(../roadmap.md)` (Phase 1-4 100% complete)
- `(../changelog.md)`
- `ADOPTERS.md`
- `(../contributing.md)` / `CONTRIBUTING.ko.md`
- `(../governance.md)`
- `(../support.md)`
- `AGENTS.md` — AI 어시스턴트 runbook
- `docs/kb/adr/INDEX.md` — 28 ADR

