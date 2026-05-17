# Plan — MongoDBInsights 실 분석 엔진

> ROADMAP Phase 3.2 의 cycle 9 강화. skeleton 컨트롤러 (78 줄, Phase 전이만) → 실 system.profile 쿼리 기반 분석 엔진.

## 배경

- 현 상태 (`internal/controller/mongodbinsights_controller.go`): CRD watch + Pending → Analyzing → Ready 전이. 실 mongo 호출 0, Recommendation 슬롯 0.
- API (`api/v1alpha1/mongodbinsights_types.go`) 는 완성. `Recommendation` 4 타입 (MissingIndex/UnusedIndex/SlowQueryPattern/SchemaHint) 정의.
- mongo 드라이버: `go.mongodb.org/mongo-driver/v2 v2.6.0` (`internal/mongodb/client.go` 의 `NewClient` 재사용).

## 목표 (goal)

`kubectl get mongodbinsights <name> -o yaml` 의 `.status.recommendations` 가 *비어있지 않음* (ROADMAP §3.2 Verify) — 실 cluster system.profile 기반.

## T 등급

**T2 → T3 분할**:
- **본 plan (T2)**: 첫 sub-task — 순수 분석 함수 (`internal/insights/analyzer.go`) + unit test + controller wiring (skeleton 호출). 외부 mongo 의존성 없이 unit-testable.
- **후속 (T3, 별도 plan)**: e2e (실 mongo cluster + profile injection + Recommendations 검증), UnusedIndex 분석 (serverStatus.metrics.queryExecutor), Status.Conditions 표준화.

## codex-review

codex-review: 5 challenge 수신 (critical 2 + major 3) → 모두 본 plan 본문 + 구현에 반영. 후속 commit (challenge fix) 가 verify gate.

| # | category | challenge | Claude 처리 |
|---|---|---|---|
| 1 | critical | `Find(..., nil)` 가 limit/sort 미적용 → DB 전체 메모리 적재 + 최신 보장 X | 수용. `options.Find().SetSort(bson.D{{"ts",-1}}).SetLimit(perDBLimit)` + global cap streaming decode 으로 수정 (controller `collectProfileDocs`) |
| 2 | critical | BSON variance 과소가정 — `bson.A`/`bson.D`/legacy `command.q`/`command.pipeline[0].$match`/`command.sort` 누락 | 수용. `normalizeMap` + `normalizeSlice` helper + `convertProfile` 4 path (top-level / command.filter / command.q / pipeline $match) + 7 unit test 추가 |
| 3 | major | SlowQueryPattern: plan = "그룹 평균 ≥ threshold", 구현은 threshold 미만 doc 사전 제외 → mixed sample false negative | 수용. `detectSlowQueryPatterns` 가 모든 doc 그룹화 후 `count≥3 && avg≥threshold` 판정으로 수정 + `TestAnalyze_SlowQueryPatternUsesGroupAverage` 회귀 가드 |
| 4 | major | MissingIndex ratio: `NReturned>0` 가드가 *0건 반환 + 대량 scan* 케이스 누락 | 수용. `looksLikeMissingIndex` 가 `denom=max(NReturned,1)` 사용 + `TestAnalyze_ZeroReturnedHighScanEmitsMissingIndex` 회귀 가드 |
| 5 | major | `AnalysisCredentialsSecretRef` 가 username/authDB 무시하고 admin/admin 고정 + `AnalyzeOverride` DI 가 너무 광범위 | 부분 수용. `loadAnalysisCredentials` 가 secret 의 `username`/`authDB`/`authSource` 읽도록 수정 (custom ref 시 username 필수, fallback 시 admin/admin). `AnalyzeOverride` 좁은 interface 화는 *follow-up* (e2e 도입 시 ProfileFetcher interface 분리 — 후속 sub-task) |


## 본 plan (T2) 범위

### 1. `internal/insights/analyzer.go` (신규)

순수 함수. mongo 의존성 없음.

```go
package insights

type ProfileDoc struct {
    Op           string   // "query" | "command" | "update" ...
    NS           string   // "<db>.<coll>"
    Millis       int32    // latency
    PlanSummary  string   // "COLLSCAN" | "IXSCAN { ... }"
    Filter       map[string]any
    Sort         map[string]any
    DocsExamined int64
    NReturned    int64
    KeysExamined int64
}

func Analyze(docs []ProfileDoc, slowThresholdMs int32) []v1alpha1.Recommendation
```

검출 규칙:
- **MissingIndex**: `PlanSummary == "COLLSCAN"` OR `DocsExamined > 1000 && DocsExamined/max(NReturned,1) > 100` → suggest BSON index on `keys(Filter)` 정렬 (equality 우선 → range → sort 키).
- **SlowQueryPattern**: `(ns, filterShape)` 그룹화 (filterShape = filter 키 set 정렬 join). 그룹 평균 `Millis >= slowThresholdMs` + 발생 ≥3회 → emit.
- **SchemaHint**: 단일 query 가 5+ `$or` / `$nor` 절 → "복합 키 인덱스 또는 schema 정규화 검토".
- **UnusedIndex**: 본 plan 범위 외 (별도 sub-task).

Severity:
- avg latency ≥ 1000ms → "critical"
- ≥ 500ms → "warning"
- 그 외 → "info"

### 2. `internal/insights/analyzer_test.go` (신규)

synthetic ProfileDoc fixture 로 4 시나리오:
- COLLSCAN → MissingIndex emit
- 빈 docs → 빈 결과
- 3건 slow 동일 filterShape → SlowQueryPattern 1건 (그룹화 검증)
- 6 절 $or → SchemaHint emit

### 3. `internal/controller/mongodbinsights_controller.go` (편집)

Analyzing phase 에서:
- ClusterRef → Service DNS resolve (`<cluster>-svc.<ns>.svc.cluster.local`)
- `internal/mongodb.NewClient` 로 connect (credentials = `AnalysisCredentialsSecretRef` 또는 cluster admin secret)
- `db.system.profile` find(limit=SampleSize) 으로 docs 수집
- `insights.Analyze(docs, SlowQueryThresholdMs)` 호출 → `Status.Recommendations` 갱신
- connect/find 실패 시 phase=Failed + Condition, requeue with backoff

connect 실패는 *plan 진행 차단점 아님* — `Failed` phase + 다음 interval 재시도.

### 4. Verify (DoD)

- `go test ./internal/insights/... -run TestAnalyze -v` PASS (4 시나리오)
- `go test ./internal/controller/... -run TestMongoDBInsights -v` PASS (skeleton 회귀 0)
- `go build ./...` 성공
- `golangci-lint run ./internal/insights/...` 0 error

## 비범위 (Non-Goals)

- e2e 실 cluster round-trip (별도 T3 plan)
- UnusedIndex 검출 (serverStatus 통합 필요)
- ProfilingLevel 동적 변경 (현재 spec 만, 다음 sub-task)
- profile 컬렉션이 capped 일 때 retention (mongo 자체 처리)

## 실패 정의

- analyzer unit test 1 시나리오라도 FAIL → 즉시 stop + RCA
- skeleton 회귀 (기존 controller test 깨짐) → 즉시 stop + rollback

## 후속 sub-task (별도 plan)

- e2e: kind cluster + profile data injection + Recommendations assertion
- UnusedIndex via serverStatus
- Prometheus 메트릭: `mongodb_insights_recommendations_total{type}`
- Status.Conditions 표준 (`AnalysisHealthy`, `MongoReachable`)

## Refs

- ROADMAP §3.2 (verify 인용)
- `api/v1alpha1/mongodbinsights_types.go`
- `internal/controller/mongodbinsights_controller.go` (78줄 skeleton)
- `internal/mongodb/client.go` (`NewClient` 재사용)
- §2.5 Plan Adversarial Review (codex challenge → 본 plan hook)
