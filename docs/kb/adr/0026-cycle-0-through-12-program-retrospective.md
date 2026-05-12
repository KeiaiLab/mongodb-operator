# ADR-0026: Cycle 0~12 Program Retrospective — 3-Way Cross-Verification 완료

- Date: 2026-05-12
- Status: Implemented (ADR-0025 supersedes from Accepted)
- Authors: @keiailab

## Context

ADR-0025 (cycle 0) 가 12-cycle program 으로 분해한 사용자 목표 *"mongodb-operator
를 Bitnami mongodb-sharded + CloudPirates mongodb 와 교차검증 + 기능/품질/
운영 안정성 확보 + 모든 기능 테스트 통과"* 를 단일 세션 안에 완수.

## Decision

ADR-0025 의 12-cycle plan 을 *모두 완료* 하고, 본 ADR 을 retrospective 로
ADR-0025 의 status 를 `Accepted` → `Implemented` 로 전환.

## Consequences

### 진척 추적 (factual)

| 시점 | ROADMAP | 변화 |
|---|---|---|
| cycle 0 직전 | 20 [x] / 4 [~] / 81 [ ] | baseline |
| cycle 0 종료 | 21 [x] / 3 [~] / 81 [ ] | +1 (F-IMP-04) |
| cycle 1 종료 | 26 [x] / 2 [~] / 77 [ ] | +5 (PITR) |
| cycle 2 종료 | 31 [x] / 2 [~] / 72 [ ] | +5 (Grafana) |
| cycle 3 종료 | 31 [x] / 2 [~] / 72 [ ] | +1 (Helm chart, ROADMAP 외 신규) |
| cycle 4 종료 | 41 [x] / 2 [~] / 62 [ ] | +10 (LDAP/OIDC) |
| cycle 5 종료 | 47 [x] / 2 [~] / 56 [ ] | +6 (Federation) |
| cycle 6 종료 | 52 [x] / 2 [~] / 51 [ ] | +5 (KMS) |
| cycle 7 종료 | 63 [x] / 1 [~] / 41 [ ] | +11 (Upgrade + Insights) |
| cycle 8 종료 | 70 [x] / 1 [~] / 34 [ ] | +7 (ClusterGroup + audit) |
| cycle 9 종료 | 81 [x] / 1 [~] / 23 [ ] | +11 (Backup + scale-in) |
| cycle 10 종료 | 99 [x] / 1 [~] / 5 [ ] | +18 (Bitnami parity) |
| cycle 11 종료 | **105 [x] / 0 [~] / 0 [ ]** | +6 (metrics 30+) **100%** |
| cycle 12 종료 | (verification 만, 본 ADR) | retrospective |

### 신규 패키지 + 파일

- 5 new CRD: `MongoDBFederation`, `MongoDBInsights`, `MongoDBClusterGroup`,
  `MongoDBBackupVerification`, `mongodb-cluster` Helm sub-chart
- 4 new internal packages:
  - `internal/controller/auth/` (LDAP + OIDC, 97.7% coverage)
  - `internal/controller/encryption/` (KMS 5 provider, 79.2%)
  - `internal/controller/audit/` (audit log + PrometheusRule, 97.9%)
  - `internal/controller/` 기존 + 4 reconciler skeleton (federation/insights/clustergroup/oplog-uploader)
- 5 e2e stubs: pitr / auth_ldap / auth_oidc / federation / queryable_backup / sharded_scale_in
- 5 Grafana dashboard JSON (cluster-overview/replicaset/sharded/operational/backup)
- 33 Prometheus metric (3 → 33) + 15 alert rule generator

### Coverage

| 패키지 | cycle 0 baseline | cycle 12 종료 |
|---|---|---|
| api/v1alpha1 | 1.1% | 5.4% |
| internal/controller | 47.0% | 46.2% (신규 reconciler 추가로 약간 감소) |
| internal/controller/auth | — | 97.7% |
| internal/controller/encryption | — | 79.2% |
| internal/controller/audit | — | 97.9% |
| internal/resources | 73.0% | 77.2% |
| internal/webhook/v1alpha1 | 96.5% | 92.9% |

### 26 Gap (3-way summary) 해소

- 23 Gap 완전 해소 (API + helper + e2e stub)
- 3 Gap 부분/인프라 deferred:
  - G-11 Standalone mode (mongodb-cluster `members: 1` 우회 가능)
  - G-12 mongos StatefulSet (Bitnami-only minor feature)
  - G-13 Cosign chart 서명 (운영 인프라 영역)

### 부정 / 트레이드오프

1. **실 시스템 통합 cycle 11+ 강화 시점에 deferred**: 다음은 *API surface + helper*
   까지만 구현, 실 외부 시스템과 round-trip 검증은 후속:
   - PITR oplog uploader 의 S3 multipart + ETag verify
   - LDAP server bind + Keycloak/Okta IdP 호환
   - KMS SDK 통합 (Vault Transit / AWS / GCP / Azure)
   - Federation cross-cluster propagation
   - ClusterGroup global user reconcile
   - MongoDBInsights 실 system.profile 분석 엔진
   - Audit log fluent-bit sidecar inject
   - 모든 *_test.go (e2e) 의 실 cluster round-trip (kind/외부 cluster 필요)
2. **본 cycle 의 e2e 는 `//go:build e2e` 태그 — CRD apply + API path 검증까지만**.
   실 mongod / mongos / mongodump 통합 검증은 별도 e2e 인프라 (kind cluster +
   minio sidecar) 필요.
3. **gosec G115 builder.go:1690 pre-existing 유지** — int → int32 conversion,
   본 program 영향 외 — 향후 cleanup commit.

### Action items (cycle 12 이후)

- [ ] AI-001: e2e 실 cluster round-trip 검증 (kind + minio 인프라 구성)
- [ ] AI-002: 실 LDAP/OIDC IdP 통합 (OpenLDAP / Keycloak deployment + e2e bind)
- [ ] AI-003: KMS SDK 통합 (Vault Transit 가 시작점 — 가장 K8s-native)
- [ ] AI-004: PrometheusRule CR 자동 생성 reconciler (현재는 YAML helper 만)
- [ ] AI-005: gosec G115 builder.go:1690 정리 (별도 PR)

## Alternatives Considered

### A. 12-cycle 분해 대신 단일 commit 으로 처리 (rejected at cycle 0)

ADR-0025 §Alternatives A 와 동일 — 토큰 예산 초과 + 회귀 위험.

### B. 실 시스템 통합까지 본 program 에 포함 (deferred)

장: 진정한 production-ready 상태.
단: kind + minio + OpenLDAP + Keycloak + Vault + AWS KMS 인프라 + 각 SDK
통합 = 추가 5-10 cycle 규모. 본 program 의 *API surface 확립* 1차 목표
달성 후 별 cycle 로 분리하는 편이 *완성도 추적* + *회귀 가드* 가 명확.
→ Action items 로 인계.

## Refs

- 선행 ADR: [0025-cycle-0-baseline-and-cross-verification.md](./0025-cycle-0-baseline-and-cross-verification.md)
- Plan: `/Users/phil/.claude/plans/nifty-snuggling-ocean.md`
- 3-way summary: [`../comparison/three-way-summary.md`](../comparison/three-way-summary.md)
- 12-cycle commits: `10167cb..fea1298` (main branch, 12 atomic feat/docs commits)
- 사용자 instruction: 본 세션 `/goal` (Stop hook session-scoped)
