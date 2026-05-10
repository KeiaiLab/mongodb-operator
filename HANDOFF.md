# HANDOFF — mongodb-operator

> 본 문서는 *다음 세션이 컨버세이션 컨텍스트 없이 재개* 가능하도록 작성된다.
> SSOT 는 본 파일 (컨텍스트·결정) + 마지막 commit log (사실).
> 글로벌 `standards/token-budget.md §5` + `standards/workflow.md §2`.

## 2026-05-10 v1.4.20 release + ShardDraining 백오프 regression fix 운영 배포

본 세션 (Ralph loop, 2026-05-10) 의 결정적 산출:

| 영역 | PR | 결과 |
|---|---|---|
| PR-A5 first cut (pkg/finalizer migration) | #119 | merged |
| **PR-A5.2 meta.SetStatusCondition + ShardDraining 백오프 regression fix (ADR-0022)** | #120 | merged |
| OSS hygiene (CITATION.cff) | #121 | merged |
| README MongoDB version badge | #122 | merged |
| OperatorHub bundle scaffold (PR-B9, ADR-0023) | #123 | merged |
| **INC-0001 cross-cut audit (ADR-0024, valkey ADR-0039 패턴 정합)** | #124 | merged |
| release v1.4.20 prep (Chart bump) | #125 | merged |
| CSV icon 빈 블록 제거 (community-operators 정합) | #126 | merged |

### v1.4.20 production 배포

- GHCR image: `ghcr.io/keiailab/mongodb-operator:v1.4.20`
- Git tag v1.4.20 / GH Release / Helm gh-pages publish (7b6fcef)
- argos-platform-data PR #9 (umbrella 0.1.14 → 0.1.15) merged
- ArgoCD sync 후 운영 `platform-data-mongodb-mongodb-operator` 가 v1.4.20 image active

### PR-A5.2 — ShardDraining 백오프 regression fix (ADR-0022)

`recordShardDrainingCondition` 이 매 reconcile `LastTransitionTime: metav1.Now()` 갱신
→ `scaleInPollInterval` 의 elapsed 측정 무력화 → 백오프 분기 *항상 30s* 만 반환 →
장시간 drain 시 mongos 부하 증대. **잠재 production regression**.

Fix: 6 site (3 PrimaryUnreachable + 3 ShardDraining) `filterConditionsByType + append`
패턴 → `meta.SetStatusCondition` / `meta.RemoveStatusCondition` 위임. ADR-0013
정합 단일화 + LastTransitionTime 보존 (Status 전이 시에만 갱신) → elapsed 백오프
정상 동작.

### ADR-0024 INC-0001 cross-cut audit

valkey-operator INC-0001 (19h cluster fail) 의 root cause `ClusterInitialized=true`
once-shot pattern 의 cross-cut audit. mongodb 도 비슷한 `!ReplicaSetInitialized`
분기 (mongodb_controller.go:165) 보유 — 단 line 191 의 hasPrimary check 가 *부분
mitigation* (PrimaryUnreachable condition + 운영자 알람). valkey 같은 침묵 stuck
안 됨. Phase 1 audit 기록, Phase 2 auto RS reconfig 별 RFC 후속.

### OperatorHub.io 등록 신청 (외부)

community-operators PR [#8092](https://github.com/k8s-operatorhub/community-operators/pull/8092) (`keiailab-mongodb-operator` 1.4.19, Opstree 의 기존 `mongodb-operator` namespace 충돌 회피 위해 `keiailab-` prefix). CSV icon 빈 블록 lint fail 후 force push fix. CI 재실행 진행 중.

### 후속 (다음 ralph-loop iteration)

1. community-operators PR #8092 CI 재검증 + reviewer 응답.
2. logo (svg → base64 PNG) 추가 후속 PR.
3. ADR-0024 Phase 2 — auto RS reconfig RFC.

---

## 2026-05-09 Sprint A 진입 (PR-A5) — Helm 차트 비교 plan

> Plan: `~/.claude/plans/1-https-artifacthub-io-packages-helm-clo-synthetic-gem.md`
>
> mongodb-operator 측 Sprint A 진입점은 PR-A5 단일. commons v0.6.0
> (PR-A1 commit 완료) tag 머지 후 진입.

### PR-A5: pkg/finalizer + pkg/status migration (T3)

- **의존**: operator-commons v0.6.0 (RFC-0018 §3.1 +§3.2 적용) tag 머지.
- **변경 범위**:
  - 3 controller (mongodb / mongodbsharded / mongodbbackup) 의
    `controllerutil.AddFinalizer/RemoveFinalizer` → `finalizer.Add/Remove`
    (commons `pkg/finalizer` import).
  - `setCondition` 호출 → `status.SetReady/SetReadyFalse/SetAvailable` 위임.
  - 도메인 ConditionType (`PrimaryUnreachable`, `ScalePolicyDeliberateFalse`)
    + 도메인 Reason 보존 — generic 4종 + 6 Reason 만 commons 사용.
- **ADR**: 신규 (현재 mongodb-operator INDEX 최신 ADR 번호 +1 부여).
  파일명: `docs/kb/adr/NNNN-rfc-0018-pkg-status-finalizer-adoption.md`.
- **호환성**: `controllerutil.AddFinalizer` 와 `finalizer.Add` 동시 사용 단계 무영향 (apimeta dedup).
- **회귀 검증**:
  - `make test` (envtest 포함) + e2e (kind cluster).
  - alert rule 사용자에게 reason 변경 (`ReconcileFailed` → `ReconcileError`) release note 의무.

### 차단점

- commons v0.6.0 tag 머지 의존. PR-A1 의 lint 통과 (사용자 환경
  golangci-lint 설치 필요) + commit + tag 후 진입.

### 근거 링크

- Plan §2 D10/D11.
- RFC-0018: `operator-commons/docs/kb/rfc/0018-status-finalizer-standard.md`.
- ADR-0003 (commons): `operator-commons/docs/kb/adr/0003-rfc-0018-pkg-status-finalizer-adoption.md`.

---

## 2026-05-07 ralph-loop iteration 47 (cluster ops mode) — 운영 상태 + release readiness

### 라이브 사실 (CLAUDE.md §7 게이트, <!-- live-verified: 2026-05-07 -->)

```
$ kubectl config current-context
argos
$ kubectl get ns data
NAME   STATUS   AGE
data   Active   29h
$ kubectl get application -n argocd platform-data-mongodb -o jsonpath='{.status.sync.status}/{.status.health.status}'
Synced/Healthy
$ kubectl get application -n argocd platform-data-valkey -o jsonpath='{.status.sync.status}/{.status.health.status}'
Synced/Healthy
$ kubectl get application -n argocd platform-data-postgres-operator -o jsonpath='{.status.sync.status}/{.status.health.status}'
Synced/Healthy
```

전 platform-data 9 apps Synced/Healthy.

### Workload 상태 (data ns, 2026-05-07)

| 워크로드 | 상태 | Age | 비고 |
|---|---|---|---|
| `mongodbsharded/argos-mongo` | Running, 5 shards × 3 + 3 cfg + 3 mongos | 21h | 13h 전 shard-1/2/4-0 1회 restart 안정화 |
| `valkeycluster/keiailab-valkey-prod` | Running, 3 shards, 16384 slots ok | 6h42m | — |
| `postgrescluster/argos-postgres` shard-0-0 | Running | 117m | — |
| mongodb-operator | Running 1.4.11 | 3h23m | webhook 비활성, 1.4.12 main branch ready |
| valkey-operator | Running | 87m | webhook 비활성 |
| postgres-operator | Running | 118m | webhook 비활성 |

3 operator log errors 0 (5min). events 0 (1h).

### Release readiness (1.4.12)

| 영역 | 상태 |
|---|---|
| 코드 commit | ✅ main (it45-47 31 commits) |
| Chart bump | ✅ Chart.yaml 1.4.12 + CHANGELOG (7096bb7) |
| ADR 6건 | ✅ 0013-0017 + Errata |
| envtest | ✅ 22 unit + 9 ginkgo PASS, coverage 95.1% |
| 사용자 docs | ✅ webhook 가이드 양쪽 operator |
| Image build/push | ⏳ 미빌드 |
| GH Release v1.4.12 | ⏳ 미생성 |
| gh-pages 1.4.12 | ⏳ 미발행 |
| ArgoCD sync 1.4.12 | ⏳ umbrella bump 필요 |

`make release VERSION=v1.4.12` 1단계 실행으로 외부 effect 4건. 사용자 명시 승인 시점.

### cluster-ops mode 누적 (it cluster-ops, 14 cycles, 22 commits)

| 영역 | 산출 | 위치 |
|---|---|---|
| 격차 audit | 11건 (C24-C36 + T27, 3 High / 5 Medium / 3 Low) | `docs/operations/cluster-audit.md` |
| Clean 영역 | 15건 (PSS / RBAC / imagePull / GitOps mongodb·postgres / Storage / probe / Resource 등) | `docs/operations/cluster-audit.md` |
| 통합 sprint plan | 7 phase (A Quick wins → G Service mesh) | `docs/operations/production-grade-sprint.md` |
| DR snapshot | keiailab-valkey-prod CR 임시 보관 | `docs/operations/cluster-snapshots/2026-05-07/` |
| ADR | 0015-0018 + Errata 다수 | `docs/kb/adr/` |
| 운영자 navigation hub | single entry point | `docs/operations/README.md` |
| Main README link | 3-tier audience (User / Operations / Developer) | `docs/README.md` |

### Progress measurement

- clean ratio 현재: **57.7%** (15/26)
- Phase A-F 완료 예상: **92.3%** (24/26)
- 잔여: G Service mesh (장기 RFC) + I28 trigger (30일 후)

### Active 디버깅 영역

**Critical 격차 발견** (2026-05-07 추가 audit): data plane GitOps 일관성 미충족.

3 operator 의 argos-platform-data umbrella 통합 audit (`gh api ... contents/<op>/Chart.yaml?ref=stable`):

| operator | argos-platform-data path | dependency | ArgoCD-managed | 격차 |
|---|---|---|---|---|
| mongodb-operator | `mongodb/` | `mongodb-operator: 1.4.11` | ✅ `platform-data-mongodb` | — |
| postgres-operator | `postgres-operator/` | `postgres-operator: 0.3.0-alpha.4` | ✅ `platform-data-postgres-operator` | — |
| **valkey-operator** | (부재) | (bitnami valkey 5.6.1 만) | ❌ keiailab-valkey-prod 별도 helm install | **GitOps drift detection 부재** |

증거 (live):
```
$ kubectl get deploy -n data valkey-operator-prod -o jsonpath='{.metadata.labels}'
{"app.kubernetes.io/managed-by":"Helm",...,"helm.sh/chart":"valkey-operator-1.0.3"}
```

CR 라벨 audit (deeper) — *operator + 인스턴스 둘 다* 격차:

| CR | managed-by | argos.io/managed | GitOps 추적 |
|---|---|---|---|
| mongodbsharded/argos-mongo | Helm | argocd | ✅ |
| postgrescluster/argos-postgres | Helm | argocd | ✅ |
| **valkeycluster/keiailab-valkey-prod** | (없음) | (없음) | **❌ manual apply 추정** |

증거 (live):
```
$ kubectl get valkeycluster -n data keiailab-valkey-prod -o jsonpath='{.metadata.labels}'
(라벨 0건 — argos.io/managed / app.kubernetes.io/managed-by 둘 다 부재)
```

즉 valkey 영역의 GitOps 격차 *2단계*:
1. operator chart (`valkey-operator-1.0.3`) — helm-direct (ArgoCD 추적 부재).
2. CR 인스턴스 (`keiailab-valkey-prod`) — helm/argocd 라벨 0건, **manual apply 확정**.

CR 추가 증거 (live):
```
$ kubectl get valkeycluster -n data keiailab-valkey-prod -o jsonpath='{.metadata.annotations}'
{"kubectl.kubernetes.io/last-applied-configuration": "..."}
$ gh search code "keiailab-valkey-prod" --owner keiailab
keiailab/mongodb-operator:HANDOFF.md: ... (운영 상태 언급만)
# 즉 어느 repo 에도 spec yaml 부재 — git 추적 0.
```

CR 라벨 (의도 단서):
- `argos.io/component: keiailab-valkey`
- `argos.io/migration-phase: production-equivalent` ← *production 도달 전
  임시 단계* 신호. 정상 마이그레이션 진행 중 가능성.

helm release history (data ns):
- `sh.helm.release.v1.valkey-operator-prod.v1` ~ `.v4` (96m ~ 6h52m).
- 4 revision = chart bump 4회 진행 중 — *진행 중 작업* 의 일부.

**상용제품 도달 결정적 격차**:
- spec 변경 history 추적 부재 (git 0).
- disaster recovery 불가 (CR yaml 누락 시 spec 복원 불가, 운영 메모리만).
- 6 pods × 16384 slots 의 *유일한 spec 원본* = `kubectl get -o yaml` (cluster only).

후속 통합 작업 시 *둘 다* GitOps-managed 로 마이그레이션 필요.

---

### Critical 격차 #2 — Prometheus Operator 부재 (observability scrape 무력화)

cluster ops audit deeper 결과 *상용제품 수준* 의 두번째 결정적 격차 발견.

증거 (live):
```
$ kubectl api-resources | grep -iE "prometheus|monitoring|service.*monitor"
podlogs    monitoring.grafana.com/v1alpha2   true   PodLogs
# monitoring.coreos.com (Prometheus Operator group) 부재.

$ kubectl get application -n argocd | grep -iE "prometheus|monitor|grafana"
platform-observability-grafana   Synced   Healthy
# Prometheus 자체 부재. Grafana 만 존재.
```

영향:
- 3 operator 의 metrics endpoint (`mongodb-operator-metrics:8443` /
  `valkey-operator-prod-metrics:8443` / `postgres-operator` 의 metrics)
  모두 *expose 만 됨 / scrape 0*.
- 워크로드 metrics 도 동일: `keiailab-valkey-prod-metrics:9121`,
  `argos-mongo-mongos:9216`, `gitlab-redis-metrics:9121`,
  `shared-valkey-metrics:9121`, `ch-operator-metrics:8888,9999` 등.
- Grafana 는 *데이터 소스 부재* → 대시보드 / alerting 무력.

**fail-soft 동작 확정** (it 내 audit, valkey-operator `internal/controller/
resources_apply.go:124`):
```go
if err != nil && (apierrors.IsNotFound(err) || meta.IsNoMatchError(err)) {
    return nil
}
```
`meta.IsNoMatchError` 가 *RESTMapper 에 GroupVersionKind 미등록* (CRD 부재)
시 nil 반환 → 컨트롤러 reconcile 정상 진행, errors 0. design-intent
graceful skip — Prometheus Operator 설치 후 다음 reconcile 에서 자동
ServiceMonitor 생성.

3 operator ServiceMonitor 코드 영역 cross-cut audit:

| operator | chart-level (operator pod metrics) | controller-level (CR 인스턴스 metrics) | fail-soft |
|---|---|---|---|
| mongodb | ✅ `templates/servicemonitor.yaml` | ❌ (I16 orphan — spec.monitoring.serviceMonitor 정의되었으나 controller 미사용) | N/A |
| valkey  | ✅ chart 자동 + controller dynamic | ✅ (`commons.monitoring.NewServiceMonitor` + applyServiceMonitor fail-soft) | ✅ NoMatchError 흡수 |
| postgres| ❌ (0건) | ❌ (0건) | N/A |

상용제품 수준 *비대칭*: valkey 만 controller-level 동적 ServiceMonitor 생성.
mongodb 의 I16 (MonitoringSpec orphan) 와 postgres 의 ServiceMonitor 부재
영역이 *동일 결정 영역* — 향후 통합 plan 에서 cross-cut 통일 후보 (별 cycle).

상용제품 도달 영역:
- platform-observability stack 에 `kube-prometheus-stack` 또는 `Prometheus
  Operator` 추가 (별 ArgoCD app).
- ServiceMonitor CRD 등록 후 모든 operator + 워크로드 자동 scrape.
- alerting rules (PrometheusRule CRD) 정착.

이외 운영 안정 — 3 operator log errors 0, events 0 (1h+).

`platform-data-valkey` ArgoCD app 은 *bitnami valkey* (shared-valkey-primary/replicas)
만 sync. keiailab-valkey-prod 인스턴스 (6 pods, 16384 slots) 는 *helm install*
직접 배포 → drift 발생 시 ArgoCD self-heal 불가.

상용제품 수준 진전 영역:
- argos-platform-data 에 `valkey-operator/` umbrella 추가 (mongodb 패턴 차용).
- helm install 로 배포된 ValkeyCluster CR + Helm release 를 ArgoCD-managed
  로 마이그레이션.
- 별 cycle (release pipeline 외부 effect 동반).

이외 운영 안정 — 3 operator log errors 0, events 0 (1h+).

---

## 2026-05-07 ralph-loop iteration 48 — T22 `make sbom` 타겟 + v1.4.11 SBOM backfill (통합 plan T0-1 mongodb)

| Iteration | Repo | Commit | 산출물 |
|---|---|---|---|
| **it48** | mongodb-operator | `e898c30` | `Makefile` 에 `.PHONY: sbom` 타겟 추가 (valkey L465-472 패턴 byte-identical). v1.4.11 GitHub Release 에 retroactive `mongodb-operator-v1.4.11.spdx.json` (836664 bytes, SPDX-2.3, 103 packages) `gh release upload`. `release-smoke-test.sh v1.4.11` 결과 SBOM FAIL 1건 → **12 PASS / 0 FAIL** 회복. |

### 동기

본 iteration 은 *통합 plan SSoT* 의 T0 (즉시 차단 해소) 단위. ~/.claude/plans/wondrous-tumbling-porcupine.md (사용자 승인 30/60/90일 로드맵) 의 T0-1 을 *mongodb 부분만* 처리 (postgres 부분은 다음 cycle).

배경:
- it45 release-smoke retry 정책 도입 (b01f5cd) 후 v1.4.11 검증 시 1 FAIL 잔존 — SBOM SPDX asset 누락 (`make sbom` 타겟 부재).
- 사용자 광범위 분석 (3 Explore + 3 Plan agent + 외부 OSS 비교) 결과 4 repo 통합 로드맵 도출. 그 *첫 단계* 가 본 작업.

### 변경 요약

1. `Makefile` L131-136 (`release-notes` 타겟) 직후 `.PHONY: sbom` 8 라인 블록 삽입. valkey L465-472 syft 패턴 byte-identical (chart name 만 mongodb-operator 로 치환).
2. `make help` 자동 출력에 sbom 타겟 즉시 등록 — Makefile help target 의 awk parse-by-`##` 컨벤션 덕분.
3. v1.4.11 GitHub Release 에 SBOM asset retroactive upload (gh CLI operation, no code commit).

### 검증 인용

```
$ syft version
Application:   syft
Version:       1.44.0  (Homebrew, 2026-04-29 빌드)

$ make sbom VERSION=v1.4.11
=== syft scan ghcr.io/keiailab/mongodb-operator:v1.4.11 ===
✓ SBOM: /tmp/mongodb-operator-v1.4.11.spdx.json (836664 bytes)

$ jq '{spdxVersion, name, packages: (.packages | length)}' /tmp/mongodb-operator-v1.4.11.spdx.json
{"spdxVersion": "SPDX-2.3", "name": "ghcr.io/keiailab/mongodb-operator", "packages": 103}

$ gh release upload v1.4.11 /tmp/mongodb-operator-v1.4.11.spdx.json -R keiailab/mongodb-operator
(silent success)

$ gh release view v1.4.11 -R keiailab/mongodb-operator --json assets --jq '.assets[].name'
mongodb-operator-1.4.11.tgz
mongodb-operator-v1.4.11.spdx.json

$ ./scripts/release-smoke-test.sh v1.4.11
✓ release v1.4.11 존재
✓ chart .tgz asset 첨부
✓ SBOM (SPDX) asset 첨부 — supply chain 표준
✓ image ghcr.io/keiailab/mongodb-operator:v1.4.11 (digest: sha256:b20f8bed36a5...)
✓ Pages status=built
✓ index.yaml fetch / version: 1.4.11 존재
✓ helm pull / helm template (default) / helm template (features.cluster/backup/autoscaling=true)
✓ trivy image: 0 HIGH+CRITICAL (fixed CVE 없음)
RESULT: 12 PASS / 0 FAIL
```

### 다음 iteration 자연 진입점

본 plan 의 T0-1 후속:
- it49: postgres-operator 에 동일 `make sbom` 타겟 추가 (valkey 패턴 이식). v0.3.0-alpha.4 retroactive SBOM upload + release-smoke 재검증.
- it50+: T0-2 — release tag 시 `make sbom && gh release upload` 자동화 (mongodb + postgres release.sh 또는 Makefile release 타겟 통합).
- 그 후 P0 단계 — A 거버넌스 (NOTICE / ADOPTERS / CODEOWNERS owner 정합) 가 1인 maintainer 가용 시간 적합.

### 사용자 결정 대기 항목 (통합 plan G 절)

- A-P0-2 GitHub `keiailab/maintainers` team 실재 + `@eightynine01` 멤버 여부
- A-P0-6 4 repo Discussions enable 토글
- C-P0-1 멀티아키 강등 (postgres/valkey arm64/s390x/ppc64le → amd64-only) 동의 여부
- C-P0-2 mongodb go directive 1.26.2 → 1.25.7 다운그레이드 vs 3 repo 1.26.2 업그레이드
- B-P0-7 mongodb MonitoringSpec 구현 vs 삭제

답변 받으면 후속 iteration 진입 가능. 답변 없이 진행 가능한 항목은 본 plan 의 권고 시작 순서 1~2 단계 (T0-1 postgres + T0-2 자동화 + A-P0-1 NOTICE + A-P0-3 GOVERNANCE 임계 + A-P0-4 ADOPTERS + A-P0-7 Scorecard badge + X-P0-1 templates/governance/).

<!-- live-verified: 2026-05-07 -->

---

## 아카이브 (압축됨)

iteration 1~45 (2026-04월 ~ 2026-05-07) 의 누적 기록은 다음으로 이전되었습니다:

- `docs/kb/handoff-archive/2026-05-07-iterations-1-48-archive.md` — 전체 history 보존본 (2491줄)

본 HANDOFF.md 는 *활성 iteration (47, 48)* 만 유지합니다. 정책 근거: standards/token-budget.md §4 (HANDOFF 60% 이전 갱신) + audit plan `~/.claude/plans/mongodb-operator-operator-commons-postgr-tranquil-horizon.md` P0-4 액션. 압축 일시: 2026-05-09.

이전 iteration 의 결정·근거가 필요할 때:
- 핵심 결정 → `docs/kb/adr/` 의 ADR 우선 참조
- iteration 별 변경 + 검증 인용 → 위 archive 파일
- 시간 순 변경 → `CHANGELOG.md`
