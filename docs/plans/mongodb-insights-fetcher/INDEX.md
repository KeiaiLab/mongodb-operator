# Plan — ProfileFetcher 좁은 interface refactor (Codex #5 근본 fix)

## Context

이전 sub-task (worktree-mongodb-insights-real-engine, dev 통합 완료) 의 Codex
adversarial review #5 challenge "partial 수용" 상태. 본 plan 은 *남은 근본
trap* fix:

- 기존: `MongoDBInsightsReconciler.AnalyzeOverride func(ctx, in) → (recs, sampled, err)` — ClusterRef 조회 + Secret 로드 + mongo connect + collectProfileDocs + convertProfile 5 책임 *모두* 우회. e2e 시 통합 결합도 검증 불가.
- 목표: `ProfileFetcher.Fetch(ctx, sampleSize) → ([]ProfileDoc, error)` 단일 책임 좁은 interface. controller 는 `fetcher.Fetch + insights.Analyze` 2-step.

## T 등급

**T2** — 다중 파일 (controller + 신규 fetcher.go + 신규 convert.go + 테스트 이전 + 신규 fetcher_test.go).

## 범위

### 변경

1. **신규 `internal/insights/fetcher.go`**: `ProfileFetcher` interface + `MongoProfileFetcher` 구현. `K8sClient + Insights` 필드. `Fetch(ctx, sampleSize)` 내부에서 ClusterRef 해석 → secret 로드 → mongo connect → collectProfileDocs → convertProfile 순차 호출.

2. **신규 `internal/insights/convert.go`**: `normalizeMap` + `normalizeSlice` + `convertProfile` + `readInt64Any` 를 controller 패키지에서 이전. internal/insights 가 자기-격리 단위.

3. **`internal/controller/mongodbinsights_controller.go` 축소**:
   - `AnalyzeOverride func(...)` 광범위 hook **삭제**
   - 새 필드 `Fetcher insights.ProfileFetcher` (nil 시 default `&insights.MongoProfileFetcher{K8sClient:r.Client, Insights:in}` 사용)
   - `runAnalysis` 가 `fetcher.Fetch + insights.Analyze` 2-step 으로 압축
   - convert helper / loadAnalysisCredentials / collectProfileDocs 호출 *모두* insights 패키지로 이전 (controller 에서 mongo driver imports 제거)

4. **`internal/insights/fetcher_test.go` 신규**: `fakeProfileFetcher` 가 합성 ProfileDoc 반환 → 통합 path 검증 (단순 sanity, e2e 대체 아님).

5. **`internal/controller/mongodbinsights_convert_test.go` → `internal/insights/convert_test.go` 이전**: BSON variance 8 test 패키지 이전.

6. **plan INDEX (직전 sub-task)** challenge #5 → "수용 완료" 매핑 갱신 (별도 commit 이슈로 본 plan 에서는 인용만).

### 비범위

- e2e (kind cluster + 실 profile injection) — 후속 sub-task
- MongoDBSharded kind 지원 — 후속 sub-task
- UnusedIndex / Prometheus 메트릭 / profiling level 자동화 — 후속 sub-task
- ROADMAP 사실 정정 — 본 sub-task 다음 cycle

## codex-review

codex-review: 4 challenge 수신 (critical 1 + major 2 + minor 1) → 모두 수용·fix. challenge 처리 매핑:

| # | category | challenge | Claude 처리 |
|---|---|---|---|
| 1 | critical | `FetcherFactory` 가 `ProfileFetcher` 전체 교체 가능 → ClusterRef/Secret/connect/collect/convert *모두* 우회 (#5 함정 재발) | 수용. `FetcherFactory` 필드 **완전 삭제**. controller 가 *항상* `MongoProfileFetcher` 사용. 테스트는 분리: analyzer unit (synthetic ProfileDoc) / fetcher unit (nil-guard + interface contract) / envtest controller suite (k8s lookup 통합) |
| 2 | major | `defer cli.Disconnect(ctx)` 가 reconcile context 사용 → timeout/cancel 후 cleanup 실패 | 수용. `context.WithTimeout(context.Background(), 5s)` 전용 disconnect ctx. 누수 방지 + idempotent. |
| 3 | major | `coll.Find` 에러 전부 profiling-disabled로 간주 + `cursor.Err()` 미검사 → auth/network/context error 가 Ready+0 docs로 silent 잠재 차단 | 수용. `isProfileAbsent(err)` helper (substring 매칭: "ns not found"/"NamespaceNotFound"/"system.profile"/"profiling is off") 만 skip, 그 외 surface. iteration 후 `cursor.Err()` 검사 추가. |
| 4 | minor | `NormalizeMap` nested bson.A 보존 + `countBoolClauses` 가 `[]any` 만 assertion → bson.A 형태 $or 누락 | 수용. `countBoolClauses` 가 `NormalizeSlice(v)` 사용 (bson.A | []any 둘 다 cover). `TestAnalyze_SchemaHintViaConvertProfileWithBsonA` 회귀 가드 추가. |

직전 sub-task 의 Codex #5 (partial 수용) 는 본 commit 으로 **완전 수용 완료** — controller-level coarse hook 제거 + 좁은 책임 fetcher 도입.

## DoD

- `grep -n "AnalyzeOverride" internal/controller/mongodbinsights_controller.go` exit 1
- `grep -n "ProfileFetcher" internal/insights/fetcher.go` ≥ 2 (interface + impl)
- `grep -nE "(bson\.|mongo\.|options\.Find)" internal/controller/mongodbinsights_controller.go` exit 1 (mongo driver imports 제거)
- `go build ./...` PASS
- `go vet ./internal/...` 0 issue
- `KUBEBUILDER_ASSETS=/Users/phil/WorkSpace/public/mongodb-operator/bin/k8s/1.31.0-darwin-arm64 go test ./internal/... -count=1` 9/9 packages PASS
- `go test ./internal/insights/... -v` ≥ 18 tests PASS (기존 10 analyzer + 이전 8 convert + fetcher 신규)
- Codex re-review challenge #5 "수용 완료" 확인

## Refs

- 이전 sub-task: dev 통합 commit `aea5ce6` + Codex challenge transcript (RFC-0045 §2.5)
- 이전 plan: `docs/plans/mongodb-insights-real-engine/INDEX.md`
- standards: §1 Think Before Coding (가정 명시) / §3 Surgical Changes (controller 의 mongo 의존성만 제거, 분석 로직 무변경)
