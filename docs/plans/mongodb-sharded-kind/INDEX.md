# Plan — MongoDBSharded kind ProfileFetcher 지원 (cycle 9 P4)

## Context

ROADMAP §3.2 cycle 9 P1~P3 적용 후 MongoDBInsights ProfileFetcher 는 `Kind=MongoDB` 만 지원. `MongoDBSharded` kind 입력 시 `unsupported ClusterRef.Kind` error → Failed phase.

본 cycle (P4) = mongos 라우터 경유 system.profile 수집 *기본 path* 추가.

## T 등급

**T2** — 단일 패키지 (`internal/insights/fetcher.go`) + unit test + envtest 회귀.

## 범위

### 변경

1. **`internal/insights/fetcher.go`** Fetch 메서드:
   - ClusterRef.Kind == "MongoDB" → 기존 path 유지
   - ClusterRef.Kind == "MongoDBSharded" → 신규 path:
     * `MongoDBSharded` CR 조회 (`*mongodbv1alpha1.MongoDBSharded`)
     * host = `<name>-mongos.<ns>.svc.cluster.local:27017` (`BuildMongosService` 정합)
     * ConnectOpts: ReplicaSet="" (mongos = router, RS opt 부재)
     * 나머지 동일: loadCredentials + applyProfilingLevel + collectProfileDocs

2. **`internal/insights/fetcher_test.go`**: 신규 `TestMongoProfileFetcher_AcceptsShardedKind` — Sharded kind 시 `unsupported` error 제거 verify (실 connect 는 k8s client nil 경로로 fail-fast, 다른 error로).

3. **`internal/insights/analyzer.go`** 무변경.

### 비범위

- per-shard 직접 connect (각 shard StatefulSet headless 경유) — 후속 sub-task (정확도 향상, mongos routing 한계 우회)
- system.profile 의 sharded routing 검증 — e2e 필요, 별도 sub-task
- chunk 분포 + balancer 메트릭 — 별도 sub-task

## codex-review

codex-review: 4 challenges 수신 (critical 2 + major 2). 처리 매핑:

| # | category | challenge | Claude 처리 |
|---|---|---|---|
| 1 | critical | mongos 는 profiler enable 불가 (level 1/2 invalid). MongoDBSharded 에서 applyProfilingLevel 호출이 silent no-op | 수용. `kind == "MongoDB"` 조건으로 applyProfilingLevel 호출 가드. per-shard 직접 적용은 후속 P5. |
| 2 | critical | mongos system.profile 은 cluster-wide aggregate 아님. 일부 shard 만 보는 incomplete data | **부분 수용** — 본 P4 는 *mongos 경유 기본 path* 만 지원 (현재 동작 명시). per-shard fan-out (shard list → RS connect → merge + shard 라벨 보존) 은 *후속 P5 의무*. plan body 에 incomplete data 사실 명시. |
| 3 | major | `isProfileAbsent` 가 "system.profile" substring 만 봐서 auth/routing error 가 namespace 포함 시 silent skip | 수용. `mongo.CommandError` type assertion + code 26 (NamespaceNotFound) 정밀 매칭. fallback marker 에서 "system.profile" 제거 — "ns not found" / "namespacenotfound" 만 인정. |
| 4 | major | 신규 테스트가 negative path 만 검증 (nil client, unknown kind). positive path (host, ReplicaSet="", secret fallback) 미검증 | **부분 수용** — 본 P4 는 *unsupported reject 해제* 검증 + nil-client fail-fast 까지. positive path = fake k8s client + MongoDBSharded CR + Secret 주입 = 후속 P5 의무. |

#2 + #4 후속 sub-task (P5) 명시:
- **P5 (T2-T3)**: per-shard ProfileFetcher fan-out — `MongoDBSharded.Spec.Shards.Count` → shard RS DNS 추정 (`<name>-shard-N-headless`) → RS connect → system.profile 수집 + shard label 보존. fake k8s client positive path test 동반.

## DoD

- `grep -nE 'MongoDBSharded' internal/insights/fetcher.go` 2+ matches
- `go build ./...` PASS
- `go vet ./internal/...` PASS
- `go test ./internal/insights/...` PASS (기존 + 신규 test)
- `KUBEBUILDER_ASSETS=... go test ./internal/...` 9/9 PASS

## Refs

- `BuildMongosService` (`internal/resources/builder.go:1473`) — mongos service name pattern (`<name>-mongos`)
- 직전 main HEAD: `bb83aca` (cycle 9 P3 profile auto-apply)
- Goal v2: stable(main) 사이클 + dev 폐기 (2026-05-17)
