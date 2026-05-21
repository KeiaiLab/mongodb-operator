# ADR-0034: Sprint 1 — operator-commons pkg/pvc + pkg/topology 채택 (-327 LOC)

- Date: 2026-05-21
- Status: Accepted
- Authors: @keiailab (Codex Major #7 — Sprint 1 Phase 2)
- Refs: operator-commons ADR-0012 (commons-side decisions)

## Context

mongodb-operator 의 `internal/controller/pvc_resize.go` (~119 LOC) +
`internal/resources/topology_spread.go` (~45 LOC) + 각 테스트 (124 + 41
LOC) 가 postgres / valkey 와 거의 동일 cross-repo 중복. commons Sprint 1
(ADR-0012) 에서 `pkg/pvc` + `pkg/topology` 신규 추출.

## Decision

1. **pkg/pvc 어댑션** — `internal/controller/pvc_resize.go` (119 LOC) +
   test (124 LOC) 삭제. `mongodb_controller.go:159` 의 `expandDataPVCs(...)`
   호출을 `commonspvc.ExpandDataPVCs(...)` 로 교체.

2. **pkg/topology 어댑션** — `internal/resources/topology_spread.go`
   (45 LOC) + test (41 LOC) 삭제. `builder.go:771` 의
   `defaultedTopologySpread(nil, mdb.Spec.Members, labels)` 호출을
   `commonstopology.Defaulted(nil, mdb.Spec.Members, labels)` 로 교체.
   mongodb 는 `Members >= 2` 의미론 → commons 의 **default**
   `WithMinReplicas(2)` 와 동일 → 옵션 미지정.

3. **go.mod**: `operator-commons v0.8.0 → v0.8.1-0.20260521045707-85a46ba80952`
   (commons PR #52 pre-merge). v0.9.0 tag 후 본 ADR 갱신.

## Consequences

### Positive

- LOC 감축: -327 LOC.
- 단일 SSOT: PVC expansion + TSC default 로직이 commons 에서만 갱신됨.
- mongodb 가 commons default semantics 그대로 사용 — 옵션 0건 (가장 단순).

### Negative

- Beta tier 채택 — commons API breaking 위험 (ADR-0012 §Alternatives 의
  명시적 거부로 안정성 확보).
- commit hash 의존 — v0.9.0 tag 후 갱신 필요.

## Alternatives Considered

1. **부분 채택 (pkg/pvc 만)** — 거부. 함께 머지가 회귀 통과 신뢰성 우위.
2. **commons 머지 후 머지** — 본 PR 의 머지 정책 (사용자 결정).

## Refs

- operator-commons PR #52, ADR-0012.
- 삭제된 원본:
  - `internal/controller/pvc_resize.go` (-119 LOC).
  - `internal/controller/pvc_resize_test.go` (-124 LOC).
  - `internal/resources/topology_spread.go` (-45 LOC).
  - `internal/resources/topology_spread_test.go` (-41 LOC).
- 수정된 callsite:
  - `internal/controller/mongodb_controller.go:159`.
  - `internal/resources/builder.go:771`.
