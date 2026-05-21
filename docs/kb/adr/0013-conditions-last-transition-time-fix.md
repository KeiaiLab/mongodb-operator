# ADR-0013: Conditions LastTransitionTime — K8s convention 정합 (upstream 위임)

- Date: 2026-05-07
- Status: Accepted
- Authors: @keiailab
- Refs: ADR-0008 (operator-commons 채택), valkey iteration 32 (cb9b807) 패턴

## Context

`internal/controller/helpers.go` 의 두 함수가 status conditions 를 매 호출마다
`LastTransitionTime: metav1.Now()` 로 명시 갱신:

```go
// applyErrorCondition (line 124-130)
*conds = filterConditionsByType(*conds, conditionTypeReconcileError)
*conds = append(*conds, metav1.Condition{
    Type:               conditionTypeReconcileError,
    Status:             metav1.ConditionTrue,
    LastTransitionTime: metav1.Now(),  // 매 호출마다 Now() — semantics 위반
    Reason:             "ReconcileFailed",
    Message:            ...,
})

// clearReconcileErrorCondition (line 155-160) — 동일 패턴
*conds = filterConditionsByType(*conds, conditionTypeReconcileError)
return append(conds, metav1.Condition{
    Type:               conditionTypeReconcileError,
    Status:             metav1.ConditionFalse,
    ObservedGeneration: generation,
    LastTransitionTime: metav1.Now(),  // 매 호출마다 Now() — semantics 위반
    ...
})
```

K8s convention (`k8s.io/apimachinery/pkg/api/meta.SetStatusCondition` 의
documented behavior):

> LastTransitionTime *only updates if Status changed*. 동일 Status 재설정 시
> 기존 LastTransitionTime 보존.

mongodb 의 *매 호출 갱신* 패턴은:
- **deviation 가능성 ↑**: K8s ecosystem 의 alerting / observability stack 이
  `LastTransitionTime` 을 *상태 변경 시점* 으로 가정 — 매 reconcile cycle 마다
  *false transition* 으로 잘못 판독.
- git history 검토 (`git log -- internal/controller/helpers.go`) 에서 *intentional
  reasoning 의 commit message 흔적 부재* — *unconscious bug* 추정.

valkey-operator 는 iteration 32 (`cb9b807`) 에서 동일 패턴 발견 후 *upstream
meta.SetStatusCondition 위임* 으로 K8s convention 정합화. mongodb 도 동일 fix 필요.

## Decision

`applyErrorCondition` / `clearReconcileErrorCondition` 의 *인라인 filter+append*
패턴 → `k8s.io/apimachinery/pkg/api/meta.SetStatusCondition` 위임. upstream 이
*Status 변경 시만 LastTransitionTime 갱신* + *Type-별 1건 유지* (filter 불필요)
+ *append-or-update* 자동 처리.

### 변경 영향

| 시나리오 | 이전 동작 | 신규 동작 |
|---|---|---|
| 첫 ReconcileError 발생 | append + LastTransitionTime=Now | append + LastTransitionTime=Now (동등) |
| 동일 Status 재설정 (error 지속) | LastTransitionTime 갱신 (deviation) | **보존** (K8s convention) |
| Status 변경 (True→False, error 해소) | LastTransitionTime 갱신 | **갱신** (동등) |
| Reason / Message 만 변경 | LastTransitionTime 갱신 (deviation) | **보존** (K8s convention) |

### 영향받는 사용자

`status.conditions[*].lastTransitionTime` 을 *상태 변경 시점* (K8s convention)
으로 가정한 *모든* 외부 도구. 본 ADR 적용 후:
- Prometheus alerting / Grafana dashboard 의 `condition_age` 메트릭 정확성 ↑
- `kubectl describe mongodb` 의 condition timeline 정확성 ↑
- 실제 Status 미변경 reconcile cycle 에서 *false transition 사라짐*

### 영향받지 않는 사용자

`status.conditions` 의 *Type / Status / Reason / Message / ObservedGeneration*
필드만 사용하는 도구. mongodb 의 기존 unit test (`TestClearReconcileErrorCondition`,
`TestShardedUpdateStatusError`) 가 모두 이 영역만 검증 — *회귀 가드 자연 PASS*.

## Consequences

### Positive
- K8s ecosystem convention 정합 (Prometheus / Grafana / kubectl 자연 동작).
- valkey + mongodb 동일 패턴 (cross-operator drift 차단).
- LoC 감소 (filter 함수 호출 제거).

### Negative
- *매 reconcile timestamp* 에 의존한 외부 도구 (있다면) 동작 변경 — 본 ADR 의
  영향 매트릭스 표 참조.
- ObservedGeneration 처리 — mongodb 의 `clearReconcileErrorCondition` 가
  `ObservedGeneration: generation` 명시 설정. upstream `SetStatusCondition` 도
  *입력 condition 의 ObservedGeneration 보존* — 동등.

### Trade-offs
- *K8s convention 정합* (본 ADR) vs *기존 동작 보존* (do-nothing): convention
  정합이 *외부 도구 호환성 ↑*. 기존 동작에 의존한 도구는 *deviation 의존* 이라
  *우리가 수정* 의무.

## Alternatives Considered

1. **operator-commons 신규 conditions 패키지** — 거절: upstream `meta.SetStatusCondition`
   이 *동등 기능 제공*. wrapper-only 패키지는 over-engineering (HANDOFF iteration
   32 의 boundary 분석).
2. **인라인 패턴 보존 + `LastTransitionTime` 비-Now 조건 추가** — 거절: upstream
   호출 1줄로 동등 logic. 자체 reimplementation 가치 zero.
3. **deviation 인정 + 보존** — 거절: K8s convention 위반의 *적극 유지* 는
   *기술 부채*. valkey 가 이미 fix.

## Implementation

```go
// helpers.go before
*conds = filterConditionsByType(*conds, conditionTypeReconcileError)
*conds = append(*conds, metav1.Condition{
    Type: conditionTypeReconcileError, Status: metav1.ConditionTrue,
    LastTransitionTime: metav1.Now(), Reason: "ReconcileFailed", Message: ...,
})

// after (upstream 위임)
meta.SetStatusCondition(conds, metav1.Condition{
    Type: conditionTypeReconcileError, Status: metav1.ConditionTrue,
    Reason: "ReconcileFailed", Message: ...,
    // LastTransitionTime 미명시 — upstream 이 자동 처리
})
```

## Verification

```bash
go test ./internal/controller/ -count=1
# TestClearReconcileErrorCondition + TestShardedUpdateStatusError 모두 PASS
# 두 test 가 Type / Status / Reason / ObservedGeneration 만 검증 — LastTransitionTime
# semantics 변경 영향 없음.
```

## Refs

- valkey iteration 32 (cb9b807) — 동일 패턴 fix
- HANDOFF iteration 32 — 3-way boundary 분석 (commons / upstream / 자체 보존)
- k8s.io/apimachinery/pkg/api/meta.SetStatusCondition GoDoc
- ADR-0008 (operator-commons 채택)
