# ADR-0017: kubebuilder:default 가 있는 field 의 zero-value 거부 invariant 는 dead code

- Date: 2026-05-07
- Status: Accepted
- Authors: @claude (it47)
- Refs: it47 commit `5f3f91c` (valkey ValkeyCluster autoFailover unreachable invariant 발견)

## Context

iteration 47 의 admission round-trip 테스트 작성 중 sister operator 의
`validateClusterSpec` 의 invariant *autoFailover=true + ReplicasPerShard=0 →
denial* 가 *real apiserver 통해 도달 불가능* 함을 발견:

- `ValkeyClusterSpec.ReplicasPerShard` 의 CRD marker 가
  `+kubebuilder:default=1`.
- K8s apiserver 가 admission 전에 *default 채움* (OpenAPI v3 default 동작).
- webhook 도달 시점엔 *항상* `ReplicasPerShard >= 1` — zero value 거부 검증
  실행 0회.
- unit test (`validateClusterSpec` in-process 호출) 는 *default 미적용* 으로
  통과 → false positive 가드.

### 영향

- **Dead code**: 검증 코드가 production 에서 절대 실행 안 됨. coverage report
  에 100% 인 줄 알지만 *실제 시나리오 보호 0*.
- **False sense of security**: ADR-0016 cross-cut audit 표에 "✅ 가드 있음"
  으로 기록되지만 실제는 *guard 가 dead code*.
- **테스트 비대칭**: unit-level 통과 ↔ envtest 통과 패턴 → unit-only 의존
  invariant 는 *runtime 에 unreachable*.

### 다른 패턴 가능성 (audit candidate)

- `ConfigServerSpec.Members` (mongodb): `+kubebuilder:default=3` — zero value
  거부 invariant 부재 (CRD enum=1;3 으로 strict). OK.
- `ShardSpec.Count` (mongodb): `+kubebuilder:default=2` — zero value 거부
  invariant 부재 (CRD minimum=1). OK.
- `MongosSpec.Replicas` (mongodb): CRD `minimum=1` — webhook 도달 전 거부.
- `Storage.Size` (양쪽): CRD `default="10Gi"/"8Gi"` — `IsZero()` skip 패턴
  (it46 step 7) 으로 의도된 통과. dead-code 가 아니라 *defensive*.

## Decision

**새 webhook invariant 작성 시 *대상 field 의 CRD `+kubebuilder:default=`
존재 여부* 점검 의무.**

### Audit 체크리스트 (ADR-0016 의 sub-clause)

신규 webhook invariant PR 시 다음 표 추가:

| field | CRD default | json omitempty | mutating defaulter 변환 | invariant zero 거부 | 분류 |
|---|---|---|---|---|---|
| Spec.X | `+kubebuilder:default=1` | yes | — | YES | **Type A** (제거) |
| Spec.X' | `+kubebuilder:default=1` | no | yes (0→1) | YES | **Type A'** (조건부, 유지 + 환경 의존성 명시) |
| Spec.X' | `+kubebuilder:default=1` | no | no | YES | **Type A** (제거 — explicit 0 admission 도달 가능하지만 invariant 자체가 default 와 모순) |
| Spec.Y | (none) | yes/no | — | YES | **Type C** (정상 reachable) |
| Spec.Z | `+kubebuilder:default=""` | — | — | YES | **Type C** (default 가 zero 와 동일) |
| Spec.W | `+kubebuilder:default="10Gi"` | yes | — | NO (skip) | **Type B** (defensive IsZero skip) |

### 분류

- **Type A — Dead invariant (절대 불가능)**: CRD default 가 *non-zero* 값
  으로 zero-value 를 채우고 *json tag 가 omitempty* 인 경우 → invariant 의
  *check 조건 자체* 가 모든 admission 환경에서 unreachable. → invariant 제거.
  - **추가 Errata (cluster-ops cycle 26+)**: *API 진화 차원* 의 Type A —
    library API (controller-runtime 등) 의 *type system 강화* 로 인해
    *runtime invariant 가 compile-time guarantee 로 격상* 시 unit test 자체
    *컴파일 불가*. 즉 invariant 가 *runtime 가드* 영역에서 *컴파일러 보장*
    영역으로 자연 이동.
  - 사례: controller-runtime v0.22.4 (non-generic, `runtime.Object` +
    type assertion) → v0.23+ (generic, `*MyResource`). v0.22.4 시절의
    `TestMongoDBCustomValidator_TypeAssertionFailure` (잘못된 GVK reject
    runtime test) 가 v0.23+ 환경에서 *컴파일 거부* — admission framework 가
    *컴파일 시점* 정확한 type 강제. commit `76269ec` 에서 본 unit test 3건
    제거 + 코드 주석 reference.
  - **검증 의무**: library API bump (특히 controller-runtime / kubebuilder)
    시 invariant 의 *Type A 격상 가능성* 점검. unit test 가 *컴파일 거부*
    하는 시나리오는 *실제 invariant 효과 ↑* 의 신호 (runtime 가드 → compile-time
    guarantee) — 단순 *test 깨짐* 이 아닌 *governance 진화* 인지 의무.
- **Type A' — 조건부 unreachable (Errata, it47 step 8 발견)**:
  CRD default 가 non-zero + *json tag omitempty 부재* 인 경우, K8s OpenAPI
  v3 의 default 동작이 *missing field* 만 채우고 *explicit zero value* 는
  그대로 둠. defaulting (mutating) webhook 이 zero→default 변환 보강 시 *그
  webhook 활성 환경* 에서만 unreachable. webhook.enabled=false 환경 (helm
  values opt-out, CRD only 모드) 에서는 reachable.
  → invariant 유지 (defensive). 단 *환경 의존성* 명시.
  - 사례: sister operator 의 `ValkeyClusterSpec.ReplicasPerShard` (no omitempty,
    CRD default=1, mutating defaulter 가 0→1 보강). it47 commit `5f3f91c` 의
    *autoFailover + ReplicasPerShard=0 → unreachable* 분석은 *webhook.enabled=true
    환경 한정* 이며, helm `webhook.enabled=false` 로 mutating defaulter 우회 시
    *명시 0 admission 도달* 가능. *완전 unreachable 아님*.
- **Type B — IsZero() defensive (의도)**: CRD default 가 채워지지 않은
  *dry-run / omitempty* path 보호. invariant 는 zero-value 를 *skip* 처리
  (예: `if size.IsZero() { return nil }` — it46 step 7 패턴).
  → 유지.
- **Type C — Non-zero violation (정상)**: CRD default 가 *zero value 와
  동일* 또는 *부재* 함. invariant 가 zero 외 다른 무효값 검증.
  → 유지.

### 적용 범위

- **MUST** (RFC 2119): 새 invariant 추가 PR.
- **SHOULD**: 기존 webhook invariant 의 retroactive audit (1회 sweep).
- **MAY**: 다른 admission 가드 (mutating webhook 등).

### 자동화

`scripts/governance-report` (글로벌 standards) 에 *invariant unreachable
ratio* 메트릭 추가 candidate:

- 분모: 모든 webhook invariant.
- 분자: zero-value 거부 + 대상 field 의 CRD default 가 non-zero 인 invariant.
- 임계값 5% 초과 시 적색.

## Consequences

### 긍정

- *false sense of security* 제거 — 가드 진위 명확화.
- audit 표 의 "guard exists?" 컬럼이 *실제 동작* 입증.
- envtest round-trip 의 가치 명시: unit-only 통과는 *불충분 증거*.

### 부정

- audit 비용 ↑ — 매 invariant PR 시 CRD field marker 점검.
- 기존 invariant 의 retroactive sweep 시 *기존 dead code* 다수 발견 가능
  (정리 부담).

### 트레이드오프

audit 비용 < dead-code 으로 인한 *implicit 운영 risk*. 정리 부담 > 운영
신뢰성.

### 후속 작업

- sister operator 의 `autoFailover + ReplicasPerShard=0` invariant 정리
  (별 PR, type A 분류 명시).
- 3 operator 의 기존 invariant retroactive sweep (별 cycle, type 분류).
- ADR-0016 의 cross-cut audit 표에 *unreachable column* 추가.

## Alternatives Considered

### A. CRD default 제거하고 webhook 으로 default 적용

`+kubebuilder:default=1` 제거 후 webhook 의 mutating 분기에서 default 적용.

**거절 사유**: K8s convention 위반 — *static default 는 CRD marker* 가 표준
방법. mutating webhook 으로 옮기면 *runtime overhead* (admission chain 1회
추가) + *자동 cert-manager 의존* + *기존 사용자 영향*. 본 케이스의 *invariant
하나* 살리기 위한 over-engineering.

### B. unit test 만 유지하고 dead-code 그대로

unit test coverage metric 유지. envtest 부재 시 *발견 불가*.

**거절 사유**: 본 ADR 의 trigger 가 envtest. *real-apiserver 통합 검증* 이
unit-only false positive 보다 우선. coverage metric 은 *실제 runtime 가드*
와 일치해야 가치.

### C. linter rule 추가 — `+kubebuilder:default` + `if X == 0` 조합 검출

custom golangci-lint plugin 으로 *automated detection*.

**거절 사유**: tool 작성 비용 큼. *PR-time audit 표* (cheap) 가 자동화 도입
전 과도기 방법. 자동화는 별 cycle (P2 governance-report 메트릭).
