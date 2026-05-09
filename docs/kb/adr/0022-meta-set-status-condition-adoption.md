# ADR-0022: meta.SetStatusCondition 전면 채택 + ShardDraining 백오프 regression fix

- Date: 2026-05-10
- Status: Accepted
- Authors: @eightynine01

## Context

ADR-0013 은 `LastTransitionTime` 의 K8s convention 정합 — 즉 *Status 전이 시에만*
갱신 — 을 위해 upstream `meta.SetStatusCondition` 위임을 helpers.go 에 도입했다.
그러나 다음 6 site 가 본 표준에서 *비정상 분기* 로 남아 `filterConditionsByType +
append` 패턴 (수동 dedup + 매 호출 `LastTransitionTime: metav1.Now()`) 을 유지하고
있었다:

- `mongodb_controller.go` 의 `setPrimaryUnreachableCondition`,
  `clearPrimaryUnreachableCondition`, `recordPrimaryUnreachable` (3 site)
- `mongodbsharded_controller.go` 의 ShardDraining cleanup 2 site +
  `recordShardDrainingCondition` 1 site

본 패턴 의 *기능적 결함* 발견:

```go
// scaleInPollInterval (mongodbsharded_controller.go:975-991)
elapsed := time.Since(c.LastTransitionTime.Time)
switch {
case elapsed < 5*time.Minute:  return 30 * time.Second
case elapsed < 30*time.Minute: return 1 * time.Minute
default:                       return 5 * time.Minute
}
```

`recordShardDrainingCondition` 이 매 reconcile `LastTransitionTime: metav1.Now()`
로 갱신하므로 `elapsed` 는 항상 reconcile 주기 (~10s) 이하 → 백오프 분기 *항상
첫 절 (30s)* 만 반환. 의도된 5min/30min 백오프 이행 불가.

장시간 drain (수십 분 ~ 수 시간 — chunks 마이그레이션 부하) 시 mongos 에 30s
간격 polling 이 누적 → mongos 부하 증대. *production 운영 잠재 결함*.

## Decision

6 site 모두 `meta.SetStatusCondition` (write) 또는 `meta.RemoveStatusCondition`
(cleanup) 위임. `LastTransitionTime` 명시 설정 제거 (apimachinery 가 Status 전이
시점만 갱신).

`filterConditionsByType` 헬퍼 제거 (사용처 0 — orphan).

## Consequences

긍정:
- ShardDraining 백오프 정상 동작 회복: drain 5분 미만 30s, 30분 미만 1min,
  그 이상 5min — mongos 부하 분산.
- ADR-0013 정합 6 site 추가 — code path 단일화.
- helpers.go 의 `applyErrorCondition` (이미 meta.SetStatusCondition 사용) 와 동일
  패턴 — 가독성 ↑.

부정:
- LastTransitionTime semantic 변경 — 외부 alert/dashboard 가 *매 reconcile* 갱신
  타임스탬프를 기대했으면 영향. 검증: 본 condition 들은 기능적 status 표시용이며
  alert key 는 별도 (ReconcileError) 이므로 외부 영향 없음.

## Alternatives Considered

1. **scaleInPollInterval 측을 elapsed 대신 별도 timestamp 필드로 측정** — Status
   subresource 에 `drainStartedAt` 추가. 거절 사유: API 표면 확장. 본 ADR 의
   *condition 단일 사용* 보다 비용 ↑.
2. **ShardDraining 만 fix** (Primary* 3 site 보존) — 거절 사유: ADR-0013 정합 가치
   (단일 패턴) 가 partial fix 보다 우위.

## References

- 이전 ADR-0013: K8s convention LastTransitionTime fix.
- iteration 33 helpers.go meta.SetStatusCondition 도입 commit (helpers.go:130).
- apimachinery v0.35 `meta.SetStatusCondition` / `meta.RemoveStatusCondition`.
