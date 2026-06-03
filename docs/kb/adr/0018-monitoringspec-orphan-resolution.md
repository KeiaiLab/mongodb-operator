# ADR-0018: MonitoringSpec orphan 의 단계적 해소 — Phase 1 deprecation, Phase 2/3 결정 보류

- Date: 2026-05-07
- Status: Accepted
- Authors: @claude (it cluster-ops audit)
- Refs: I16/I26 발견 commit `f234517`, cluster-ops audit `e72930a`, ADR-0016 cross-cut audit pattern

## Context

iteration 46 cross-cut audit 에서 mongodb-operator 의 `MonitoringSpec`
(`api/v1alpha1/common_types.go:185-201`) 이 *CRD 정의 부재*. 사용자가
`spec.monitoring.{serviceMonitor,prometheusRules,exporter}` 설정해도 *어느
controller 도 reconcile 안 함* — UX 함정 (silent ignore).

대조군: sister operator 의 `internal/resources/servicemonitor.go` 이
`commons.monitoring.NewServiceMonitor` 위임 + `applyServiceMonitor` fail-soft
패턴 (NoMatchError 흡수). 즉 valkey 는 *full reconcile 보유*, mongodb 는
*spec-only no-impl*.

### 결정 옵션

| 옵션 | 행동 | 호환 영향 | 작업 비용 |
|---|---|---|---|
| **a** | spec 삭제 (v2alpha1 conversion) | breaking — 기존 사용자 spec 변경 필요 | 큼 (CRD 버전 분기 + 마이그레이션 webhook) |
| **b** | valkey 패턴 차용 구현 (M4) | non-breaking | 큼 (controller-level + RBAC + commons 통합) |
| **c** | godoc deprecation marker + spec 보존 | 0 | 작음 (주석 + 문서) |

### 환경 제약 (C25 cluster-ops audit 발견)

keiailab 클러스터 *Prometheus Operator 부재* (commit `14ff831`). monitoring.coreos.com
group CRD 부재 → ServiceMonitor / PrometheusRule reconcile *불가능*.

영향:
- 옵션 b 즉시 구현해도 *Prometheus 도입 전엔 fail-soft skip 만* — 기능 0.
- 옵션 a/b 모두 *Prometheus 선결* 의무.
- 옵션 c 만 *현 환경 즉시 가치* (사용자 인지 + 미래 결정 보존).

## Decision

**Phase 1: 옵션 c (godoc deprecation marker) 즉시 적용. Phase 2/3 보류.**

### Phase 1 (즉시, 별 PR)

- `api/v1alpha1/common_types.go` 의 `MonitoringSpec` / `ServiceMonitorSpec` /
  `PrometheusRulesSpec` / `ExporterSpec` 에 godoc `// Deprecated:` 추가.
- 주석 본문: 현재 controller 미구현 + Prometheus 부재 + 향후 결정 ADR-0018
  reference.
- chart 의 example samples / docs 에 *해당 영역 사용 비권장* 명시.

### Phase 2 (보류, C25 해소 후 trigger)

keiailab 클러스터에 Prometheus Operator 도입 (C25) 완료 후:
- 사용자 사용 빈도 measurement (controller log grep + recent 30 days
  reconcile event audit).
- 사용 0건이면 옵션 a 진행 (v2alpha1 spec 삭제).
- 사용 1건+이면 옵션 b 진행 (valkey 패턴 구현 + 3 operator commons.monitoring 통일).

### Phase 3 (조건부)

옵션 a 진행 시:
- v2alpha1 작성 + conversion webhook.
- 기존 사용자 v1alpha1 spec 의 monitoring 영역 *명시 ignore* 경고.

## Consequences

### 긍정

- *현 환경 즉시 가치* — 사용자 silent-ignore UX 함정 해소 (deprecation marker).
- *결정 cost 미루기* — Prometheus 도입 후 측정 기반 결정.
- breaking change 0 (v1alpha1 호환 유지).

### 부정

- *spec 보존* 으로 *codebase 의 dead code* 잔존 (api/v1alpha1 의 Monitoring 관련
  타입 4개). PR 작성자 혼동 가능 — godoc 으로 mitigation.
- Phase 2/3 결정 *영구 미루기* 위험 — 30일 audit (ADR-0017 의 governance-report
  unreachable ratio 메트릭과 동일 영역) 으로 강제 trigger.

### 트레이드오프

dead code 보존 < Prometheus 부재 환경의 즉시 결정 cost. C25 해소 + 측정 후
정밀 결정.

### 후속 작업

- Phase 1 PR (별 cycle): godoc deprecation marker + chart docs.
- C25 해소 (Prometheus 도입) 후 Phase 2 trigger event.
- ADR-0017 의 *cross-cut audit metric* 에 *orphan deprecation 30일 audit*
  추가 candidate.

## Alternatives Considered

### Alternative A — 옵션 a 즉시 진행

v2alpha1 conversion 후 spec 삭제. 가장 *clean* 한 상태.

**거절 사유**: breaking change. 기존 사용자가 *모르는 사이* spec 사용 중일
수 있음 (현 controller 가 silent ignore 하지만 *사용자 의도* 는 향후 reconcile
받기 — 우리가 *결정 변경* 으로 해당 의도 깨뜨림). 대안 *옵션 c 의 deprecation
window* 가 사용자에게 명시 신호 + 시간 부여.

### Alternative B — 옵션 b 즉시 진행

Phase 1 에서 바로 controller 구현 + Prometheus 도입 plan 동반.

**거절 사유**: Prometheus 도입 (C25) 자체가 별 cycle (외부 effect, ArgoCD app
신규). *둘 다 같은 cycle 에 묶음* 시 *partial completion 위험* (Prometheus 만
도입되고 mongodb controller 미구현 시 사용자 ignore 당황). 단계 분리로
*independent rollout* 보장.

### Alternative C — 무행동

발견사항으로 두고 cycle 마다 재발견.

**거절 사유**: ADR-0016 cross-cut audit pattern 의 *audit 결과 등록 의무* 위반.
30일 audit 의 *불변* 발견 (Phase 2 trigger 이전 변화 0) 보장 안 됨 — 명시적
상태 (Phase 1 deprecated) 가 *이행 추적* 의 baseline.
