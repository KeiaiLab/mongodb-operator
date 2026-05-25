# ADR-0017: RFC-0018 채택 — pkg/finalizer migration (controllerutil → commons)

- Date: 2026-05-09
- Status: Accepted (PR-A5 first cut — finalizer only, status migration 별 PR)
- Authors: @keiailab
- Refs: RFC-0018 (operator-commons/docs/kb/rfc/0018-status-finalizer-standard.md), ADR-0003 (commons), Plan §2 D10

## Context

RFC-0018 §3.2 의 mongodb-operator 측 채택 (sister operator PR-A6 / ADR-0038
패턴 동일 적용). 본 ADR 작성 시점:

| 호출 위치 | 변경 전 | 변경 후 |
|---|---|---|
| `internal/controller/helpers.go:81,91` | `controllerutil.ContainsFinalizer / RemoveFinalizer` | `commonsfinalizer.Has / Remove` |
| `internal/controller/mongodb_controller.go:100,101` | `Contains / Add` | `Has / Add` |
| `internal/controller/mongodbsharded_controller.go:101,102` | 동일 | 동일 |
| `internal/controller/mongodbbackup_controller.go:80,81` | 동일 | 동일 |

핵심 보존:
- **wire contract** (`mongodbFinalizer` / `mongodbShardedFinalizer` /
  `mongodbBackupFinalizer` 상수) 변경 없음 — 외부 사용자 (kubectl jsonpath,
  ArgoCD finalizer cleanup) 의존성 보호.
- 호출 시그니처 동등 (`metav1.Object` 가 `client.Object` 의 superset, 호환).

## Decision

1. **4 controller 의 finalizer 호출** 을 `controllerutil` 에서
   `github.com/keiailab/operator-commons/pkg/finalizer` (alias
   `commonsfinalizer`) 로 위임.

2. **API 매핑**:
   - `controllerutil.ContainsFinalizer(o, name)` → `commonsfinalizer.Has(o, name)`
   - `controllerutil.AddFinalizer(o, name)` → `commonsfinalizer.Add(o, name)`
   - `controllerutil.RemoveFinalizer(o, name)` → `commonsfinalizer.Remove(o, name)`

3. **wire contract 불변**: mongodb finalizer 상수 그대로.

4. **status migration 분리**: 본 PR 은 *finalizer 만*. `setCondition` 등
   status 호출의 commons 위임은 *별 PR* (PR-A5.2). ADR-0013 (Conditions
   LastTransitionTime — meta.SetStatusCondition 위임) 와 정합 방향.

## Consequences

### Positive

- 4-repo 정합성 (sister operator ADR-0038 / sister operator ADR-0011 와
  동일 패턴).
- commons `pkg/finalizer` 채택률 25% (valkey) → 50% (mongodb 추가).
  postgres 비대칭 보존 시 최종 67% (3 of 4 repo, 의도된 비대칭).

### Negative

- 의존성 표면 +1 (operator-commons/pkg/finalizer). v0.6.0 bump 후속
  (chore/bump-commons-v0.6.0 #117 머지 완료).

### Trade-offs

- *4 controller 일괄 migration* (본 PR) vs *PR 분할* — 4 파일 동일 패턴
  일괄이 review 부담 < 4 PR 분할.
- *finalizer + status 통합* vs *분리 (본 PR)* — 후자 채택. status
  migration 은 ADR-0013 (LastTransitionTime) 정합 분석 필요로 분리.

## Alternatives Considered

1. **`commonsfinalizer.Add` 의 return value 활용 단순화** — 보류.
   - 기존 `if !ContainsFinalizer { AddFinalizer; Update; }` 패턴이
     `commonsfinalizer.Add` 의 return 활용 시 단순화 가능.
   - 본 PR 은 *최소 변경* 우선.

2. **commons `pkg/finalizer/runtime.EnsureRemoval` 신설** — 거부 (ADR-0003).
   - controller-runtime client 의존 도입 → commons zero-dep 원칙 위반.

## Refs

- RFC-0018: operator-commons/docs/kb/rfc/0018-status-finalizer-standard.md
- ADR-0003 (commons): pkg/status 슈가 + pkg/finalizer 변경 없음 결정.
- sister operator ADR-0038: 동일 패턴 (5 controller 변경).
- sister operator ADR-0011: pkg/status 부분 채택 + finalizer 비대칭.
- Plan §2 D10/D11.
- 후속 PR-A5.2 (별도): setCondition → commonsstatus 위임.
