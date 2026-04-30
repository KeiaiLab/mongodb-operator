# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.4.3] - 2026-05-01

Sharded P0 — Helm chart 의 bundled CRD 가 v1.x 시점에서 멈춰 있었고 (`charts/mongodb-operator/crds/*.yaml` 586 줄 vs `config/crd/bases/*.yaml` 5,858 줄, 5,272 줄 drift), 이로 인해 K8s API server 가 `Status.AdminUserCreated`/`ConfigServerInitialized`/`ShardsInitialized`/`ShardsAdded` 등 status 신규 필드를 OpenAPI schema validation 단계에서 silently drop. 결과적으로 reconcile flag 가 영구 empty 로 유지되어 `reconcileAddShards` 가 `Status.ShardsInitialized[i]` 에서 `index out of range [0] with length 0` panic, MongoDBSharded.phase=Failed 영속화. v1.4.1/v1.4.2 모두 동일 결함 (chart bundle 만 stale 했으므로 코드 fix 만으로는 무용).

### Fixed
- **Sharded P0 — Helm chart bundled CRD drift** (`charts/mongodb-operator/crds/`):
  `config/crd/bases/*.yaml` (controller-gen 출력) 으로 모든 3 CRD (mongodbs, mongodbshardeds, mongodbbackups) 일괄 sync. 이전 chart bundle 은 v1.x 시점에 수동 복사된 후 갱신 누락. v1.4.3 부터 `make manifests` 가 `sync-crds` target 을 자동 의존성으로 호출 → drift 영구 차단.
- **Sharded P0 — `reconcileAddShards` index panic** (`internal/controller/mongodbsharded_controller.go:537`):
  CRD drop 으로 `Status.ShardsInitialized` 가 empty/short 일 때 `[i]` 접근 panic. 길이 가드 추가 — 미초기화 상태면 `return nil` 로 wait. CRD sync 가 root cause fix 이지만 본 가드는 향후 schema 회귀 / partial status update 시나리오 방어.

### Added
- **`make sync-crds` Makefile target**: `manifests` target 의 의존성으로 자동 호출. 향후 `controller-gen` 출력과 chart bundle 의 drift 를 release pipeline 에서 차단.

## [1.4.2] - 2026-04-30

Sharded mongos Deployment 무한 reconcile fight P0 fix.

### Fixed
- **Sharded P0 — applyDeployment server-default ping-pong** (`internal/controller/resources_apply.go`):
  `BuildMongosDeployment` 가 `RevisionHistoryLimit` / `ProgressDeadlineSeconds` 를 nil 로 두지만 `applyDeployment` 가 매 reconcile 마다 desired (=nil) 를 운영 객체에 그대로 덮어씌움 → K8s Deployment 컨트롤러가 즉시 server default (10/600) 를 재주입 → 다음 reconcile 에서 또 nil 화. `argos` 클러스터에서 mongos Deployment generation 이 4시간 23분 만에 **116,128** 까지 증가하면서 `MongoDBSharded.status.phase=Failed` 영속화, `sh.addShard()` 자동 호출 단계까지 도달 못함을 재현. v1.4.2 는 `desired.Spec.RevisionHistoryLimit != nil` / `desired.Spec.ProgressDeadlineSeconds != nil` 가드를 추가해 server-defaulted 필드를 운영 중 객체에 보존. STS 6 개는 generation=1 로 안정 — `applyStatefulSet` 가 동일 패턴을 가지지 않아 본 결함은 mongos Deployment 단독.

### Added
- **회귀 테스트 1 케이스** (`internal/controller/resources_apply_unit_test.go`):
  - `TestApplyDeployment_IdempotentWithServerDefaults` — server-defaulted (10/600) Deployment 에 nil-pointer desired 로 apply 2회 호출 시 ResourceVersion 불변(멱등) 검증. 가드 부재 시 fight 재현.

## [1.4.1] - 2026-04-30

Sharded 모드 P1 2건 fix — 베타 carve-out 종료. `features.sharded.enabled` 기본값 false → true.

### Fixed
- **Sharded P1 #3 — HPA informer cache timeout** (`SetupWithManager`):
  Sharded controller 의 `Owns(...)` 목록에 `autoscalingv2.HorizontalPodAutoscaler` 가 누락 → controller-runtime 의 default cached reader 가 HPA informer 를 lazy 생성 시도 → cache sync wait timeout (default 2분) → `r.Get(... HPA ...)` 가 영구 hang. RS controller (`mongodb_controller.go:724`) 는 v1.2.0 도입 시 추가됐으나 Sharded 는 v1.2.0 (mongos HPA) + v1.3.0 (cfg HPA) 시점에 *누락*. v1.4.1 에서 동일 라인 추가.
- **Sharded P1 #1 — ConfigServer init/HPA ordering** (`internal/controller/mongodbsharded_controller.go`):
  Reconcile() 단계 6.7/6.8 (mongos/cfg HPA reconcile) 이 단계 7/8 (rs.initiate) 이전에 실행되어, HPA controller가 cfg RS 미초기화 상태의 mongos crashloop pod를 metric으로 sample → 잘못된 스케일링 발생. HPA reconcile을 단계 11.5/11.6 (AdminUser + AddShards 이후)으로 이동 + readiness gate (`Status.ConfigServerInitialized` && `areShardsInitialized()`) 이중 가드 추가.
- **Sharded P1 #2 — Status.Mongos/ConfigServer Total 영구 divergence** (`updateStatus()`):
  HPA active 시 `Spec.Replicas`의 owner는 HPA controller로 넘어간다(CR.Spec과 별개로 desired 변경). 이전 v1.4.0은 `Total = mdbsh.Spec.X.Replicas` hardcode → 24h soak에서 영구 divergence. v1.4.1은 HPA active 시 `obj.Spec.Replicas`, inactive 시 `CR.Spec`으로 source-of-truth 분기.
- **`isClusterReady` HPA 호환성**: HPA가 traffic에 따라 scale 한 직후에도 cluster ready 판정 기준이 함께 따라가도록 수정 (`Status.Total` 사용). 이전엔 `Spec.Replicas` 절대값 비교 → HPA scale-down 후 영구 `Initializing` phase에 갇히는 secondary 결함.
- **`updateStatus` silent skip 차단**: `r.Get` NotFound는 status 보존 후 skip(이전 동작 유지), transient 에러는 `errors.IsNotFound` 분기로 propagate (이전엔 모두 silent).

### Added
- **`areShardsInitialized` helper**: `Status.ShardsInitialized []bool` 슬라이스의 *모든* 인덱스가 true인지 검사. HPA readiness gate에서 사용.
- **회귀 테스트 6 케이스** (`internal/controller/mongodbsharded_p1_unit_test.go`):
  - `TestReconcileMongosHPA_SkipsBeforeRSInit` — gate 미충족 시 HPA 미생성
  - `TestReconcileMongosHPA_CreatesAfterRSInit` — gate 통과 시 정상 생성 + MinReplicas 검증
  - `TestUpdateStatus_HPAActiveUsesDeploymentSpec` — CR.Spec=2 vs Deployment.Spec=5 divergence 시나리오에서 Total=5 정확
  - `TestUpdateStatus_HPAInactiveUsesCRSpec` — HPA inactive 시 Total=CR.Spec
  - `TestUpdateStatus_DeploymentNotFoundDoesNotError` — NotFound silent skip
  - `TestAreShardsInitialized` — 5 sub-case (empty/partial/all/short/zero-count)

### Changed
- **`charts/mongodb-operator/values.yaml`**: `features.sharded.enabled: false → true`. 베타 carve-out 종료.
- **`charts/mongodb-operator/Chart.yaml`**: `prerelease: "true" → "false"`. v1.4.x line이 GA stable.

### Refs
- ADR-0010 — sharded HPA ordering + status truth source
- 영향: Issue #1은 v1.3.0에서 HPA 통합 시점에 도입된 ordering 결함, Issue #2는 v1.2.0에서 mongos HPA 도입 시점에 도입된 truth-source 결함. v1.4.1이 양쪽 동시 해소.

## [1.4.0-rc.1] - 2026-04-30

Tier 1 코드 감소 리팩터링 — release-1.4 브랜치에서 7-commit 단계별 구현. `v1.3.2-beta.*` carve-out 베타 track과 분리하여 *구조 변경*을 SemVer minor bump로 외부에 신호.

### Changed (RFC: Tier 1 리팩터링)
- **`internal/mongodb/retry.go` 일괄 제거** (-80 prod, -375 test): RetryWithBackoff/RetryUntilSuccess/WaitForCondition/WaitForConditionWithBackoff/WithTimeout/WithDeadline 6 함수가 production 호출처 0건. 표준 `k8s.io/apimachinery/pkg/util/wait` + `context.WithTimeout`로 충분.
- **`int32Ptr`/`int64Ptr`/`boolPtr` → `k8s.io/utils/ptr.To` 표준 채택** (35 사용처 일괄 교체): k8s.io/utils 의존성을 indirect → direct 승격.
- **3 reconciler 중복 패턴 통합 (`internal/controller/helpers.go` 신규)**:
  - `reconcileSecretIfNotExists`: keyfile Secret 멱등 생성 — RS/Sharded 99% 동일 코드 통합 (-32 LoC)
  - `handleFinalizerCleanup`: deletionTimestamp + finalizer 패턴 — 3 reconciler 통합 (-32 LoC, type-specific cleanup은 closure로 보존)
  - `applyErrorCondition` + `Statusable` interface: ReconcileError condition + EventRecorder Warning 발행 통합 (-32 LoC, MongoDBSharded에 EventRecorder 자동 주입 추가)
- **bash 스크립트 외부화 (`internal/assets/scripts/*.sh.tpl` + `//go:embed`)**:
  - `readiness.sh.tpl`, `bootstrap-admin.sh.tpl`, `backup-s3.sh.tpl`, `backup-pvc.sh.tpl` 4 템플릿 분리
  - `text/template` 변수 주입으로 type-safe (이전 `fmt.Sprintf` 위치 인자 대비)
  - IDE syntax highlight + shellcheck 적용 가능
  - 5 회귀 보호 테스트 추가 (RS init 12 핵심 토큰 검증)

### Added
- **`Makefile setup` 타겟**: `make setup`이 `pre-commit install --hook-type pre-commit --hook-type pre-push`를 일괄 실행 — pre-push hook silent skip 위험 해소.

### Removed
- **`MongoDBReconciler.eventf` wrapper**: helpers.go::applyErrorCondition 통합 후 unused (staticcheck U1000) 정리.

### LoC 변화 (v1.3.2-beta.6 → v1.4.0-rc.1)
- Production Go: **8,215 → 8,088 (-127)**
- Test Go: 미사용 retry_test.go 삭제 (-375), embed asset test 추가 (+115)
- 가장 큰 가치는 *3 reconciler drift 위험 제거* + *bash 스크립트 외부 도구 적용 가능성*.

### Migration
- 본 RC는 v1.3.2 carve-out 정책 동일 적용 (`features.{sharded,backup,autoscaling}.enabled=false` 기본).
- 외부 사용자 영향 0 — 모든 변경은 internal package 리팩터링.
- 정식 1.4.0 GA는 잔여 P1 (테스트 커버리지 70%+, PodMonitor 자동 생성, PrometheusRule) 후 진행.

## [1.3.2-beta.6] - 2026-04-30

Release 자동화 — `make release VERSION=v1.x.y` 단일 명령 도입.

### Added (RFC 0002 release.yml + helm-publish.yml 대체)
- **`Makefile release` 타겟**: 6단계 자동 pipeline.
  1. `make gate` (lint/test/audit/validate)
  2. Chart.yaml ↔ VERSION 일치 확인
  3. `docker buildx build --push` (linux/amd64)
  4. `git tag` + `git push` tag
  5. `gh release create` (--prerelease auto-detect for beta/rc/alpha)
  6. `make helm-publish` (gh-pages worktree + index merge + push)
- 1단계라도 실패 시 즉시 중단. tag/release 중복 시 skip + 경고.

### Verified
- 본 v1.3.2-beta.6 release 자체가 `make release VERSION=v1.3.2-beta.6` 한 줄로 출시되어 자동화의 첫 라이브 검증 사례.

## [1.3.2-beta.5] - 2026-04-30

EventRecorder 도입 + gosec G115 정합 + Makefile/pre-commit 운영 친화 개선. 1.4.0 GA P0 (관측성)을 부분 해소.

### Added (관측성 P0 — 부분 해소)
- **`internal/controller/mongodb_controller.go` EventRecorder 통합**:
  - `MongoDBReconciler.Recorder record.EventRecorder` 필드 추가 + nil-safe `eventf` 래퍼.
  - `SetupWithManager`에서 `mgr.GetEventRecorderFor("mongodb-controller")` 자동 주입.
  - `updateStatusError`에서 `Warning ReconcileError` 이벤트 발행 — `kubectl describe mongodb`로 즉시 디버깅 가능.
  - RBAC `+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch` 추가.
- **`Makefile setup` 타겟**: `pre-commit install --hook-type pre-commit --hook-type pre-push` 일괄 — pre-push 게이트가 silent 미설치되는 P1 해소.

### Fixed
- **gosec G115 integer overflow 2건 fix** (`mongodbsharded_controller.go:701, 764`): `int(len(slice)) → int32` 변환 전 `math.MaxInt32` bounds check 추가. 실용적으로 슬라이스 2^31 초과는 etcd object size limit으로 불가능하지만 명시 가드. `make audit` gosec HIGH issues **2 → 0**.
- **`internal/controller/resources_apply_unit_test.go` 신규 추가**: `applyStatefulSet`/`applyDeployment`의 `preserveReplicas` 분기 fake-client 단위 테스트 3건 (HPA 활성 시 STS replicas 보존, deliberate=false guard, Deployment preserve). envtest 불필요. **16/16 PASS**.

### Security 검증
- trivy v1.3.2-beta.5: debian 0건 / gobinary 0건 (예상)
- govulncheck: No vulnerabilities found
- gosec HIGH: **0** (이전 2건 → 0)
- make gate: All RFC 0002 local gates passed

## [1.3.2-beta.4] - 2026-04-30

긴급 hotfix — code-reviewer 에이전트가 v1.3.2-beta.3에서 발견한 carve-out 구멍 + 잔존 옛 owner 참조 + 본질적 재발 방지 (pre-push hook).

### Security / Carve-out (P0)
- **`--enable-autoscaling` flag가 reconciler에 미주입되어 carve-out 무력화 fix**: `MongoDBReconciler` / `MongoDBShardedReconciler` struct에 `EnableAutoscaling bool` 필드 추가. `reconcileRSHPA` / `reconcileMongosHPA` / `reconcileConfigServerHPA` 진입부에 guard 추가. `cmd/main.go`에서 struct 초기화 시 주입. **이전 베타에서는 helm `features.autoscaling.enabled=false`로 설정해도 HPA reconcile이 계속 일어나는 silent 무력화 상태였음**.

### Fixed
- **README.md 옛 owner 참조 정리**: `eightynine01.github.io` helm repo URL + Issues/Discussions 링크 등 잔존 4건을 `keiailab`로 정정 (line 125, 472, 473).
- **`.github/dependabot.yml`**: `reviewers: [keiailab]` 제거 — Organization명 단독은 GitHub Dependabot에서 invalid (404 → orphan PR). 유효 user/team 정해지면 `keiailab/team-slug` 형태로 추가. `open-pull-requests-limit` 50 → 10 (운영 부담 감소).
- **`internal/mongodb/replicaset.go`**: 사용 안 되는 `notYetInitializedCode` const 제거 (staticcheck U1000).

### Added (RFC 0002 본질적 재발 방지)
- **`.pre-commit-config.yaml`**: hook stage 분리 + 보안 scanner 통합.
  - `pre-commit` stage: `go-fmt`, `go-vet` (빠른 피드백)
  - `pre-push` stage: `go-test` (race), **`govulncheck`** (call-graph CVE), **`trivy fs`** (lockfile/base CVE), `gitleaks` (시크릿)
  - 이번 사이클의 stdlib CVE 7건 + carve-out flag 누락이 *둘 다* 이 게이트로 차단되어야 했음.
- **`Makefile`** 신규 타겟 4개 (RFC 0002 L3 게이트):
  - `make lint` — go vet + staticcheck + golangci-lint
  - `make audit` — govulncheck + trivy + gosec
  - `make validate` — helm lint + helm template
  - `make gate` — `lint test-unit audit validate` 일괄 (pre-push 동등)

### Documentation
- README의 Helm repo `helm repo add` 명령 URL 정정 (옛 owner → keiailab).

## [1.3.2-beta.3] - 2026-04-30

Carve-out 정합성 강화 — 코드 레벨 feature gate 도입 + 문서/예제 베타 경고 + otel SDK v1.43.0 false positive 제거. v1.3.2-beta.2의 잔여 P1 일괄 해소.

### Added (Carve-out — 코드 레벨)
- **`cmd/main.go`**: `--enable-sharded-controller`, `--enable-backup-controller`, `--enable-autoscaling` flag 도입. 기본값 모두 `false`. flag가 `false`이면 reconciler 자체가 등록되지 않아 controller log에 Forbidden 에러 발생 자체가 차단됨.
- **`charts/mongodb-operator/templates/deployment.yaml`**: helm values의 `features.{sharded,backup,autoscaling}.enabled`에 따라 위 cli flag를 deploy args에 주입. RBAC carve-out과 *코드 carve-out*이 정합되어 진정한 carve-out 달성.

### Changed
- **otel SDK v1.40.0 → v1.43.0**: trivy의 `CVE-2026-39883` (BSD kenv PATH hijacking) false positive 제거. 본 이미지는 Linux distroless 기반이라 코드 경로 실행 자체가 불가능했지만, scanner 신호 정리 차원에서 정식 업그레이드. otel/{otel,metric,trace,sdk} + grpc 동반 업그레이드.

### Documentation (베타 carve-out 경고)
- **`README.md`**: 상단에 베타 출시 안내 블록 추가. Features 섹션의 Sharded/Backup/Auto-scaling 항목에 `(베타 비활성)` 표기.
- **`README.md` 배지**: 옛 `eightynine01` 참조 정리 (`Build Status`, `Container Image`, `Helm Chart`, `Go Report Card`, `codecov` 5건). keiailab GHCR + Helm으로 일관 정정.
- **`examples/minimal/mongodb-sharded.yaml`**: YAML 헤더에 `features.sharded.enabled=true` 필요 경고.
- **`examples/production/mongodb-sharded-prod.yaml`**: 1.4.0 GA 전 production 사용 권장 안 함 경고.
- **`examples/backups/s3-backup.yaml`**: YAML 헤더에 `features.backup.enabled=true` 필요 + 보안 위험 경고.

## [1.3.2-beta.2] - 2026-04-30

긴급 hotfix — v1.3.2-beta.1 게시 직후 trivy 이미지 스캔에서 stdlib CVE 8건(CRITICAL 1, HIGH 7) 발견. Go builder를 1.25.5 → 1.25.9로 업그레이드하여 stdlib CVE 전건 해소. otel exporter 잔존 v1.39.0 → v1.40.0으로 정렬.

### Security
- **stdlib CVE 7건 fix**: Go builder image 1.25.5 → **1.25.9** (`Dockerfile`).
  - CVE-2025-68121 (CRITICAL) — crypto/tls 세션 재개 인증서 검증
  - CVE-2025-61726, CVE-2025-61728, CVE-2026-25679, CVE-2026-32280, CVE-2026-32281, CVE-2026-32283 (HIGH)
- **otel exporter 동반 업그레이드**: `otlptrace`, `otlptracegrpc` v1.39.0 → v1.40.0 (`go.mod`). v1.3.2-beta.1에서 SDK만 v1.40.0이고 exporter는 v1.39.0에 머물러 trace export silent fail 위험 있던 부분 정정.

### Fixed
- **Dependabot orphan PR 방지**: `.github/dependabot.yml`의 `reviewers`/`assignees`를 `eightynine01` → `keiailab`로 수정. 옛 owner 참조로 인한 PR 할당 404 → 자동 보안 업데이트가 사실상 무력화되어 있던 부분 정정.

### 검증 후 잔여 P1 (다음 출시 대상)
- carve-out 정합성 — `cmd/main.go`에 features flag 미적용. RBAC는 거부하나 controller가 등록되어 Forbidden 로그 발생.
- CRD 무조건 install — helm 표준 동작. 미지원 CR 생성 차단 webhook 필요.
- README/examples/docs 베타 경고 부재.
- mongos optimistic lock 24h+ 응고 (sharded 자체가 carve-out이라 베타 영향 없음).

## [1.3.2-beta.1] - 2026-04-30

긴급 carve-out 베타 출시 — 출시 1시간 전 QA에서 P0 6건(CVE 2건 포함) 발견. 정식 출시는 연기되며 본 베타는 **MongoDB ReplicaSet 한정** 범위로 carve-out하여 공개한다. Sharded / Backup / Auto-scaling은 검증 미완료로 기본 비활성화된다.

### Security
- **CVE GO-2026-4762 — gRPC 인증 우회 fix**: `google.golang.org/grpc` v1.78.0 → **v1.79.3**. govulncheck "Authorization bypass via missing leading slash in :path" 취약점 해소. (`go.mod`)
- **CVE GO-2026-4394 — OpenTelemetry SDK 임의코드 실행 fix**: `go.opentelemetry.io/otel/sdk` v1.39.0 → **v1.40.0**. PATH hijacking 취약점 해소. otel/{otel,metric,trace}도 동반 v1.40.0으로 정렬. (`go.mod`)

### Changed (Carve-out)
- **`charts/mongodb-operator/values.yaml`**: `features.sharded.enabled`, `features.backup.enabled`, `features.autoscaling.enabled` 게이트 추가. 모두 기본 `false`. 운영자가 `true`로 명시 활성화한 경우에만 해당 RBAC 권한이 부여됨.
- **`charts/mongodb-operator/templates/clusterrole.yaml`**: `mongodbshardeds`, `mongodbbackups`, `batch:cronjobs`, `autoscaling:horizontalpodautoscalers`, `apps:deployments`, `apps:replicasets` 권한을 features 게이트 조건부 렌더링으로 전환. 기본 설치는 ReplicaSet에 필요한 최소 권한만 부여.

### Known Issues / Beta Scope
- **MongoDBSharded CR**: 활성화 시 ConfigServer init과 HPA 간 ordering race 가능성, mongos status 정합성 깨짐(24h 재현) 케이스 존재. 베타에서 미지원.
- **MongoDBBackup CR**: 자동 테스트 0건. `connectionString` 평문이 Job spec에 노출될 가능성. 베타에서 미지원.
- **HorizontalPodAutoscaler 통합**: RS/cfg drift 방지 mutex 부재. 베타에서 미지원.
- **관측성**: ServiceMonitor 자동 생성, PrometheusRule, EventRecorder 모두 부재. 운영 진입 시 별도 alert 구성 필요.
- **테스트 커버리지**: total 39.1% (controller 26.7%). DoD 80% 목표 대비 미달. 정식 출시 전 보강 필요.

### 운영 가이드
- 베타 사용자는 `MongoDB` (ReplicaSet) CR만 사용한다.
- `MongoDBSharded` / `MongoDBBackup` CR을 만들지 않는다 — RBAC가 권한을 거부하므로 reconcile되지 않음.
- 정식 1.4.0 출시까지 본 베타는 *비프로덕션 데이터* 한정 사용을 권장.

## [1.3.1] - 2026-04-29

배포 경로 복구 — owner가 `eightynine01` → `keiailab`로 이전됐는데 release/helm-publish 워크플로와 chart 메타데이터가 옛 owner를 참조해 v1.1.1~v1.3.0 release 워크플로가 모두 ghcr push 단계에서 fail했고, `index.yaml`의 .tgz URL이 죽은 도메인을 가리켜 ArtifactHub이 chart 본체를 가져가지 못한 문제를 일괄 정정한다.

### Fixed
- **`.github/workflows/release.yml`**: `IMAGE_NAME: keiailab/mongodb-operator`로 정정. `--url`과 chart annotation sed 패턴의 owner도 keiailab로 통일.
- **`.github/workflows/helm-publish.yml`**: `helm repo index --url`을 `https://keiailab.github.io/mongodb-operator`로 정정.
- **`charts/mongodb-operator/Chart.yaml`**: `artifacthub.io/images` annotation을 `ghcr.io/keiailab/mongodb-operator:1.3.1`로 갱신.
- **`charts/mongodb-operator/README.md`**: `helm repo add` URL을 keiailab로 갱신 (ArtifactHub 페이지 본문에 노출).
- **`config/manager/kustomization.yaml`, `config/manager/manager.yaml`, `Makefile`**: 기본 image 참조를 keiailab로 정정.

## [1.3.0] - 2026-04-29

Auto-scaling 통합 사이클 — ADR-0007 후속 4건 통합 구현. mongos drift 방지 + RS
HPA 이중 가드 + cfg HPA 이중 가드 + RS deliberate scale + Status.PendingScale
가시화. shard 갯수 자동화는 chunk migration 부작용으로 명시 거절(ADR-0009).

### Added
- **`ScalePolicy{Deliberate bool}`** (`api/v1alpha1/common_types.go`) — RS / cfg
  / shard 멤버 수 변경의 명시 승인 가드. `MongoDB.Spec.ScalePolicy`,
  `ConfigServer.ScalePolicy`, `Shards.ScalePolicy` 세 곳에 추가.
- **`HPAStatus`** + **`PendingScale`** 신규 타입 — CR `Status.HPA`,
  `Status.PendingScale`로 운영자에게 자동 스케일 현황과 보류된 변경 가시화.
- **`BuildReplicaSetHPA`** / **`BuildConfigServerHPA`** — `AutoScaling.Enabled
  =true` + `ScalePolicy.Deliberate=true` *이중 가드* 통과 시에만 HPA 객체 생성.
- **`reconcileRSHPA`** / **`reconcileConfigServerHPA`** — controller에 HPA
  reconcile 분기 추가, idempotent CreateOrUpdate + cleanup.
- **`recordPendingScale`** — `spec.Members` 변경 + `Deliberate=false`인 경우
  STS replicas 변경 보류 + Status.PendingScale 노출 + 로그 출력.
- **`applyStatefulSet`/`applyDeployment` `preserveReplicas` 인자** — HPA 활성
  또는 Deliberate=false 시 운영 중인 spec.Replicas를 desired로 덮어쓰지 않음.
  첫 Create 시점에는 desired 그대로(첫 deploy 영향 없음).
- **단위 테스트 8케이스 추가** (`builder_test.go`): RS HPA 이중 가드 4분기 +
  cfg HPA 3분기 + ScaleDeliberate helpers 4분기. coverage 72.3% → 73.5%.

### Decisions
- ADR-0008 — ReplicaSet 멤버 수 변경의 deliberate 가드.
- ADR-0009 — shard / cfg HPA의 RS 부작용과 이중 가드 (shard 갯수 자동화 명시
  거절).

## [1.2.0] - 2026-04-29

Mongos auto-scaling 사이클 — `Spec.Mongos.AutoScaling`이 v1alpha1 API에 이미
선언돼 있었으나 reconcile 로직이 비어 있던 결함 봉쇄. mongos는 stateless router
이므로 표준 HPA로 안전하게 수평 확장. RS / cfg / shard 멤버는 RS reconfig 부작용
으로 본 ADR 범위 외(ADR-0007).

### Added
- **`BuildMongosHPA`** (`internal/resources/builder.go`): 옵트인 HPA 빌더.
  CPU / Memory utilization 또는 custom Prometheus metric 지원. metric 미지정 시
  default `cpu 80%`. `MinReplicas` 미지정 시 1로 클램프.
- **`reconcileMongosHPA`** (`internal/controller/mongodbsharded_controller.go`):
  reconcile loop step 6.7. `Enabled=false` (또는 nil)이면 기존 HPA 삭제,
  enabled이면 `controllerutil.CreateOrUpdate`로 idempotent apply + OwnerReference.
- **RBAC 마커** — `+kubebuilder:rbac:groups=autoscaling,resources=horizontalpod
  autoscalers,verbs=get;list;watch;create;update;patch;delete` (chart는 v1.1.0
  부터 이미 권한 보유).
- **단위 테스트 7케이스** (`builder_test.go`): disabled/nil/default-cpu/min-clamp/
  cpu+memory/custom/empty-custom-name 모두 회귀 가드.

### Decisions
- ADR-0007: mongos HorizontalPodAutoscaler 지원 (RS 멤버는 제외)
  (`docs/kb/adr/0007-mongos-hpa-autoscaling.md`).

## [1.1.2] - 2026-04-29

Bootstrap self-heal 사이클 — 외부 connect로 풀 수 없는 부트스트랩 deadlock(mongod
`--auth+--keyFile` 시작 + localhost-exception 외부 미적용)을 *pod 내부 postStart hook*
으로 자동 해소. RS / cfg / shard 모두 CR 적용 후 사용자 개입 없이 PRIMARY 선출 +
admin user 생성까지 자동 진행. mongos는 Deployment+ClusterIP 구조에 맞는 별도
connect 패턴(`NewServiceConnectFactory`)으로 분리.

### Fixed
- **postStart hook에 RS init 통합** (`internal/resources/builder.go`):
  `buildAdminBootstrapScript`가 createUser뿐 아니라 *RS init도 함께 처리*.
  ordinal-0 분기로 RS init은 단 한 번만 시도(idempotent), 다른 멤버는 oplog로 자동
  합류. mongo의 localhost-exception은 createUser에만 적용되고 replSetInitiate에는
  적용되지 않으나 *pod 내부 localhost connect*에서는 두 명령 모두 0-user 윈도에서
  허용되는 점을 활용. 이는 외부 connect로는 풀 수 없는 deadlock을 근본 해소한다.
- **STS env 자동 주입** (`buildBootstrapEnv` helper): RS / cfg / shard StatefulSet의
  mongod 컨테이너에 `MONGO_PORT` / `MONGO_REPLSET` / `MONGO_MEMBERS` /
  `MONGO_CONFIGSVR` 4종을 주입. postStart 스크립트가 RS init config를 동적 구성.
- **`NewServiceConnectFactory`** (`internal/mongodb/replicaset.go`): mongos처럼
  Deployment+ClusterIP 구조의 컴포넌트 connect용 ConnectFactory. headless service
  + StatefulSet 전용인 `NewPodConnectFactory`와 분리. sharded controller가 mongos
  connect에서 사용 → 이전의 `<pod-name>.<svc>...` DNS 미해석 결함 해소.
- **mongos postStart deadlock 자동 해소**: mongos pod hostname은
  `<deploy>-<rs>-<pod-hash>`로 ordinal != 0 → postStart 스크립트의 ordinal-0 분기에
  걸리지 않아 자동 skip. mongos의 부트스트랩은 cfg/shard 부트스트랩 후 operator의
  ServiceConnectFactory connect로 처리.

### Added
- **회귀 가드 (port 검증 패턴 갱신)** (`internal/resources/builder_test.go`):
  스크립트가 `--port <port>` 리터럴 대신 `MONGO_PORT:-<port>` 디폴트 fallback을
  사용하도록 단위 테스트 갱신. 환경변수 기반 동적 port 설정 회귀 가드.

### Decisions
- ADR-0006: postStart hook 안에서 RS init + createUser 통합 처리
  (`docs/kb/adr/0006-poststart-bootstrap-rs-init.md`).

### Verification (2026-04-29 k3s 클러스터 실 부트스트랩)
- RS `rs-auto`: CR 적용 → Phase=Running 67초, 3 멤버 모두 RESTARTS=0, 인증 mongosh
  `rs.status().ok=1`/`usersInfo.ok=1`/members=3.
- Sharded `sh-auto`(cfg×3 + shard-0×3 + mongos×1): CR 적용 → Phase=Running 152초,
  7 pod 모두 RESTARTS=0, `hello.msg=isdbgrid`, balancer=full, 분산 write 1503건
  영속(bulk insert + range read + min/max aggregate 일관).
- 60s reconcile loop: rs-auto Successfully 4건/ERROR 0건, sh-auto Successfully
  1199건/ERROR 0건.

## [1.1.1] - 2026-04-29

Bootstrap-resilience 사이클 — 부트스트랩이 RS init까지만 완료된 채 admin user
생성 단계에서 중단된 상태에서 reconcile flow가 영구 stuck되는 P0 결함 1건 봉쇄
(2026-04-29 INC-0001), kubectl-apply 사용자를 위한 RBAC 마이그레이션 가이드 추가.

### Fixed
- **`IsInitialized` Unauthorized 분기 추가** (`internal/mongodb/replicaset.go`):
  익명 매니저가 auth-on RS에 `replSetGetStatus`를 호출해 Unauthorized(13) /
  AuthenticationFailed(18)을 받으면 RS는 *이미 init되어 auth가 켜진 상태*로
  판정하고 `(true, nil)` 반환. 이전에는 generic error로 propagate되어
  reconcile flow가 admin user 부트스트랩 단계로 진입하지 못하고 Phase=Failed로
  영구 stuck됐다.
- **`BootstrapAdminUser` 단순화** (`internal/mongodb/auth.go`): 직접 primary
  추적(`replSetGetStatus` preflight + 두 번째 connect)을 제거하고 driver의
  자동 server selection으로 위임(Direct=false). 이는 두 효과를 동시에 낸다.
  - 0-user 상태에서 익명 `replSetGetStatus`가 mongod 빌드/구성에 따라
    Unauthorized로 거부되는 경우에도 createUser는 정상 시도된다.
  - primary가 first pod이 아닐 때 직접 추적의 1회 실패 위험이 사라진다.
  멱등성은 그대로 — UserAlreadyExists / DuplicateKey / 모든 인증 요구 에러
  → idempotent skip.
- **`classifyReplSetGetStatusErr` helper 추출**: replSetGetStatus 응답 분류
  로직을 단위 테스트 가능한 순수 함수로 분리. `mongo.CommandError`를 wrap한
  에러도 `errors.As`로 정상 분류.

### Added
- **`docs/UPGRADING.md`**: chart 1.0 → 1.1 업그레이드 시 추가된 RBAC 권한
  3종(`coordination/leases`, `networking.k8s.io/networkpolicies`,
  `policy/poddisruptionbudgets`) 마이그레이션 가이드. Helm consumer는 자동
  처리, kubectl-apply consumer는 ClusterRole patch가 필요함을 명시.
- **회귀 가드** (`internal/mongodb/replicaset_test.go`): `TestClassifyReplSet
  GetStatusErr` 6개 케이스 — code 94/13/18/wrap된-13/unknown/non-server-error
  분기 모두 검증.

- **`reconcile` flow 보강** (`internal/controller/mongodb_controller.go`):
  `Status.AdminUserCreated=false` 단계에서는 step 8 `hasPrimary` 체크를
  생략하고 step 9 admin user 부트스트랩으로 직행한다. 익명 매니저가 auth-on
  RS의 `replSetGetStatus`를 호출하면 Unauthorized로 거부되어 부트스트랩
  진입이 영구 차단되던 회귀 봉쇄. 부트스트랩 후 `AdminUserCreated=true`
  영속화 → 다음 reconcile에서 인증 매니저로 정상 체크.

### Decisions
- ADR-0004: IsInitialized Unauthorized 분기를 init-completed 시그널로 사용
  (`docs/kb/adr/0004-isinitialized-unauthorized-as-init-completed.md`).
- ADR-0005: admin user 부트스트랩 전 primary 체크 생략
  (`docs/kb/adr/0005-skip-primary-check-before-admin-bootstrap.md`).

## [1.1.0] - 2026-04-28

Production-readiness 사이클 — 검수에서 식별된 P0 6건 + P1 6건을 단일 PR로 봉쇄,
Bitnami Helm chart 동등성 갭 4건 클로즈, envtest 회귀 봉쇄망 가동.

### Added
- **MongoDB ReplicaSet PodDisruptionBudget 자동화** — opt-in via
  `spec.podDisruptionBudget.{enabled,minAvailable,maxUnavailable}`. 기본
  `minAvailable=replicas-1` (3 멤버 RS면 한 번에 한 멤버만 maintenance).
- **MongoDBSharded PodDisruptionBudget 자동화** — cfg/shards/mongos 모든
  컴포넌트에 단일 spec 동일 적용.
- **MongoDB ReplicaSet NetworkPolicy 자동화** — opt-in via `spec.networkPolicy`,
  deny-by-default + intra-cluster ingress + `additionalIngressFrom` 사용자 peer.
- **MongoDBSharded NetworkPolicy 자동화** — 컴포넌트별 deny-by-default
  (cfg=27019/shard=27018/mongos=27017) + cluster 내부 cross-talk 자동 허용.
- **MongoDBSharded Scale-in** — `spec.shardCount` 감소 시 `removeShard` 자동
  호출, ShardDraining condition으로 진행 상황 노출, drain 완료된 자원
  (STS/Service/scripts CM/PDB/NetworkPolicy) cleanup. PVC는 의도적 보존(데이터
  손실 방지).
- **envtest 회귀 봉쇄망** — `internal/controller/suite_test.go`에 3개
  reconciler 등록, 신규 케이스로 downstream resource 생성·OwnerReference·
  finalizer 흐름 검증. controller coverage 23.8% → 29.0% (+5.2pp).
- **단위 회귀 가드** (silent_error_unit_test.go +3건) — condition 누적 차단,
  isClusterReady nil/zero 안전성, buildConditions 외부 condition 보존.

### Changed
- **부트스트랩 race-free**: K8s `coordination/v1.Lease` 분산 락으로 동일 CR에
  대한 동시 reconcile에서 첫 admin user 생성 race 차단. controller-runtime
  leader-election과 별개의 resource-level lock.
- **부트스트랩 검증 강화**: `BootstrapAdminUser` 직후 인증된 매니저로
  `usersInfo` ping → 통과해야만 `Status.AdminUserCreated=true`.
- **typed `mongo.ServerError` 도입** (auth.go): `isAuthRequiredErr` /
  `isUserAlreadyExistsErr`를 string match에서 `HasErrorCode(13/18 / 11000/51003)`
  으로 변경. TLS handshake 등 typed wrapping이 없는 경로용 fallback 메시지
  매칭은 유지(hybrid 패턴).
- **`controllerutil.CreateOrUpdate` 마이그레이션**: 자체 createOrUpdate(DeepCopy
  → Update) 헬퍼를 폐기하고 mutateFn 패턴으로 전환. Service/STS/Deployment의
  immutable 필드(ClusterIP, Selector, ServiceName, VolumeClaimTemplates 등)는
  Create 시점에만 설정 → spec 손실 위험 차단.
- **`updateStatusError` condition 누적 차단**: 동일 type ReconcileError가 매
  호출마다 append되던 P2 버그 수정 → `filterConditionsByType` 적용 후 1건만 유지.
- **`buildConditions` 외부 condition 보존**: 본 함수가 관리하지 않는 type
  (PrimaryUnreachable, ReconcileError 등)을 보존해 silent로 사라지지 않게 함.
- **`updateStatus`의 silent primary skip 제거**: rsManager 생성/조회 실패를
  `PrimaryUnreachable=True` condition으로 영속화 → 운영자가 phase=Running인
  채로 primary 추적이 멈춘 상태를 인지 가능.
- **`isClusterReady` nil/zero 안전성**: `len(Status.Shards) != Spec.Shards.Count`
  가드 + `Spec.Members/Replicas/MembersPerShard <= 0`인 잘못된 설정 차단.

### Fixed
- `Makefile` docker-build/docker-push: `--builder masblue-builder` 제거. 글로벌
  표준에 따라 docker buildx 기본 빌더(default)만 사용.
- `config/manager/kustomization.yaml`: dev 태그 `dev-5cdeb71-final` → `latest`
  (운영 배포가 dev 태그를 가리키던 상태 해소).

### Security
- `coordination.k8s.io/leases` RBAC 권한 추가 (부트스트랩 분산 락 용도).
- `networking.k8s.io/networkpolicies`, `policy/poddisruptionbudgets` RBAC 권한
  추가 (자동 reconcile 용도). controller-gen이 role.yaml에 자동 반영.

### 후속 사이클로 미룬 항목
- MongoDBSharded Arbiter/Hidden topology (Bitnami #1) — MongoDB CR에는 이미
  지원, Sharded는 builder STS 분기가 필요해 별도 사이클.
- ReplicaSet member graceful removal (`rs.remove()` 호출) — 현재 STS replicas
  축소만 처리.
- ROADMAP Phase 1.3 자동 롤링 업그레이드, Phase 1-2의 LDAP/OIDC/PITR/Grafana.

### Changed (Pre-existing in Unreleased before 1.1.0)
- **BREAKING (security)**: mongosh `kubectl exec` 의존을 mongo-go-driver v2 네트워크
  client로 완전 대체. `pods/exec` ClusterRole 권한이 RBAC에서 삭제됨.
  - operator Pod이 침해되어도 cluster의 임의 Pod에서 임의 명령 실행 불가능
  - 첫 admin user 생성은 mongodb pod / mongos pod의 `lifecycle.postStart`
    bootstrap 스크립트가 담당 (localhost exception을 operator가 아닌 pod
    자체에서 사용)
  - 기존 cluster 마이그레이션: admin user가 이미 있으면 자동 호환. 없거나
    password가 다르면 reconcile 영구 실패 → 운영 매뉴얼 별도 필요.
- 부수 효과: connection pooling, context 기반 timeout, BSON 타입 안전성으로
  JS 인젝션 가능성 0건.

### Removed
- `pods/exec` ClusterRole 규칙 (config/rbac/role.yaml,
  charts/mongodb-operator/templates/clusterrole.yaml)
- `internal/mongodb/{executor,escape}.go` 및 관련 테스트
- `docs/security/rbac-known-limitations.md` (한계 해소)

### Verification (사용자 권장)
fresh install 시:
```bash
kind create cluster --name mongo-op-e2e
make docker-build IMG=ghcr.io/eightynine01/mongodb-operator:test-v2
kind load docker-image ghcr.io/eightynine01/mongodb-operator:test-v2
make deploy IMG=ghcr.io/eightynine01/mongodb-operator:test-v2
kubectl apply -f examples/minimal/
# 검증:
# - replica set 3 멤버 init 성공
# - operator pod의 audit log에 pods/exec 호출 0건
# - sharded 예제에서 addShard 성공
```

후속 phase 권장:
- envtest binaries 자동 다운로드 + in-process mongod(memongo)로 driver
  호출 단위 검증
- kind 골든 패스를 GitHub Actions에 자동화

### Planned
- Point-in-Time Recovery (PITR)
- Automated version upgrades
- Cross-cluster replication
- Grafana dashboard templates
- Shard scale-in support

## [1.0.1] - 2026-01-22

### Changed
- **BREAKING**: Migrated container registry from Docker Hub to GitHub Container Registry (GHCR)
  - New image path: `ghcr.io/eightynine01/mongodb-operator` (previously `eightynine01/mongodb-operator`)
  - No Docker Hub Secrets required - uses GITHUB_TOKEN automatically
  - Public images with no rate limits for authenticated users
  - Better integration with GitHub Security scanning

### Migration Guide
Existing users need to update image references:
```yaml
# Old (Docker Hub)
image: eightynine01/mongodb-operator:1.0.0

# New (GHCR)
image: ghcr.io/eightynine01/mongodb-operator:1.0.1
```

For Helm users, the repository is automatically updated. Just upgrade:
```bash
helm repo update
helm upgrade mongodb-operator mongodb-operator/mongodb-operator --version 1.0.1
```

### Added
- Documentation: GHCR setup and migration guide (`docs/releases/ghcr-setup.md`)
- E2E testing framework with comprehensive test scripts (`test/e2e/`)
- Extended roadmap with MongoDB Enterprise feature comparison (`ROADMAP.md`)

### Fixed
- GitHub Actions Docker build failures due to Docker Hub authentication
- CI/CD pipeline now fully automated without external secrets

## [1.0.0] - 2026-01-21

### Summary
Initial stable (GA) release with comprehensive CI/CD, documentation, and examples. This release marks production-readiness of the MongoDB Operator with full enterprise-grade infrastructure for open source maintenance.

### Breaking Changes
None. This is a major release representing stabilization of the project with all features from previous pre-releases. No deprecations or breaking changes introduced.

### New Features

#### GitHub Repository Templates
- Issue template with bug report, feature request, and documentation categories
- Pull request template with checklist and contribution guidelines

#### GitHub Actions CI/CD (5 Workflows)
- `ci.yml`: Continuous integration with Go tests, linting, and Docker build verification
- `docker-build.yml`: Automated Docker image building and pushing to Docker Hub
- `release.yml`: Complete release automation for GitHub releases
- `helm-publish.yml`: Helm chart packaging and publishing to gh-pages branch
- `security.yml`: Comprehensive security scanning (dependencies, containers, licenses)

#### Comprehensive Documentation (9 Documents)
- `docs/ci-cd/overview.md`: CI/CD pipeline architecture and workflow descriptions
- `docs/ci-cd/workflows.md`: Detailed workflow configuration and troubleshooting
- `docs/ci-cd/release-process.md`: Automated release process documentation
- `docs/ci-cd/testing-strategy.md`: Test coverage strategy and guidelines
- `docs/ci-cd/quality-assurance.md`: Code quality standards and tooling
- `docs/ci-cd/artifact-hub-integration.md`: Artifact Hub package registry setup
- `docs/repository/github-settings.md`: GitHub repository configuration guide
- `docs/repository/issue-management.md`: Issue tracking and triage guidelines
- `docs/repository/pull-request-process.md`: Pull request workflow and review process

#### Comprehensive Examples (7 Examples)
- `examples/basic/mongodb-replicaset.yaml`: Simple 3-member ReplicaSet deployment
- `examples/basic/mongodb-sharded.yaml`: Basic sharded cluster with 2 shards
- `examples/production/mongodb-replicaset-resources.yaml`: ReplicaSet with production resource limits
- `examples/production/mongodb-sharded-resources.yaml`: Sharded cluster with production resource limits
- `examples/monitoring/mongodb-prometheus.yaml`: ReplicaSet with Prometheus monitoring enabled
- `examples/monitoring/mongodb-sharded-prometheus.yaml`: Sharded cluster with Prometheus monitoring
- `examples/backup/mongodb-backup-s3.yaml`: Backup configuration with S3 storage

#### Artifact Hub Integration
- Publisher configuration with repository ID (386b6255-6da7-4a73-8fc0-a8e79e3c7b90)
- Artifact Hub annotations in Helm chart
- Automatic metadata synchronization on releases

#### Dependency Automation
- Dependabot configuration for Go modules
- Automatic dependency update PRs
- Security vulnerability monitoring

#### Pre-commit Hooks
- Go code formatting with `gofmt`
- Linting with `golangci-lint`
- Shell script validation with `shellcheck`
- Markdown linting with `markdownlint`
- YAML formatting with `yamlfmt`
- JSON validation with `jsonlint`
- Trailing whitespace detection

#### Code Coverage with Codecov
- Automatic coverage upload on PRs
- Coverage threshold enforcement
- Badge integration in README

#### Helm Repository Publishing
- Automated chart packaging
- gh-pages branch management
- Helm repository index generation

#### Security Scanning
- Trivy vulnerability scanning for container images
- Dependabot for dependency security
- License compliance checking
- SBOM and Provenance attestations

#### Test Suite Strategy
- Unit test coverage requirements
- Integration test guidelines
- E2E test examples
- Coverage thresholds and goals

#### GitHub Repository Settings
- Branch protection rules (main branch)
- Security policies and alerts
- Team and collaborator access guidelines
- Issue and PR template setup

### Changed
- Marked all features as production-ready and stable
- Moved from pre-release (0.0.x) to stable (1.0.0) versioning
- Added comprehensive release documentation and maintainers guide

### Security
- All container images use immutable SHA256 digests
- SBOM and Provenance attestations enabled
- Regular security scanning automated
- CVE tracking and dependency updates automated

## [0.0.7] - 2026-01-05

### Security
- Upgraded Go version to 1.25.0 to address multiple CVEs (CVE-2025-22871, CVE-2025-61723, etc.)
- Upgraded `golang.org/x/oauth2` to v0.34.0 to fix CVE-2025-22868
- Updated base images to use immutable SHA256 digests for `golang:1.25` and `distroless/static:nonroot`
- Enabled SBOM and Provenance attestations in container image builds
- Updated all dependencies to latest secure versions

### Fixed
- Added `pods/exec` permission to ClusterRole to fix replica set initialization failures

## [0.0.6] - 2026-01-05

### Changed
- Updated image repository to `eightynine01/mongodb-operator`

## [0.0.5] - 2025-12-31

### Fixed
- Backup authentication: include credentials from auth secret in connection string
- Backup all databases: removed `/admin` path from URI to enable full cluster backup
  - Previously only backed up admin database
  - Now correctly backs up all databases using `?authSource=admin` only

## [0.0.4] - 2025-12-31

### Changed
- Helm chart version bump for Artifact Hub update
- Documentation updates for scaling and resource recommendations

## [0.0.3] - 2024-12-31

### Added
- Automatic ReplicaSet initialization with `rs.initiate()`
- Automatic Sharded Cluster initialization
  - Config server ReplicaSet initialization
  - Shard ReplicaSet initialization
  - Automatic `sh.addShard()` execution
- Admin user auto-creation via MongoDB localhost exception
- Horizontal shard scaling (scale out) support
- Resource recommendations documentation
- Tested features and limitations documentation
- Mongos replica scaling examples

### Fixed
- Preserve shard status arrays during scale out (prevent re-initialization)
- Port configuration: configsvr (27019), shardsvr (27018), mongos (27017)
- Keyfile Secret regeneration causing authentication failures
- Mongos readiness probe timeout (increased to 5s)
- Container-aware command execution for mongos pods

### Changed
- Marked as stable release (prerelease: false)
- Minimum mongos memory recommendation: 512Mi

## [0.0.2] - 2024-12-24

### Added
- Verified Publisher configuration for ArtifactHub

### Changed
- Updated repository metadata with repository ID (386b6255-6da7-4a73-8fc0-a8e79e3c7b90)

## [0.0.1] - 2024-12-23

### Added
- Initial pre-release for testing
- MongoDB ReplicaSet CRD and controller
  - Support for 3+ member replica sets
  - Automatic keyfile generation for internal authentication
  - SCRAM-SHA-256 authentication support
  - Arbiter node support
- MongoDB Sharded Cluster CRD and controller
  - Config server replica set management
  - Multiple shard support with configurable members per shard
  - Mongos router deployment with auto-scaling (HPA)
- MongoDBBackup CRD and controller
  - S3-compatible storage support
  - PVC-based backup storage
  - Full and incremental backup types
  - Compression support (gzip, zstd, snappy)
- TLS encryption support
  - cert-manager integration for automatic certificate management
  - Self-signed certificate option
- Prometheus monitoring integration
  - MongoDB exporter sidecar
  - ServiceMonitor resource creation
  - PrometheusRule for alerts
- Helm chart for easy deployment
  - Configurable values for all operator settings
  - CRD installation via Helm
  - RBAC resources included

### Security
- Non-root container execution
- Read-only root filesystem
- Dropped capabilities
- SeccompProfile enforcement

---

[Unreleased]: https://github.com/eightynine01/mongodb-operator/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.7...v1.0.0
[0.0.7]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.6...v0.0.7
[0.0.6]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/eightynine01/mongodb-operator/releases/tag/v0.0.1
