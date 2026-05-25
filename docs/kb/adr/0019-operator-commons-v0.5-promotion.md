# ADR-0019: operator-commons v0.5.0 helper 승격 — validateStorageSize + apiError 통일

- Date: 2026-05-07
- Status: Proposed
- Authors: @claude (it cluster-ops cycle 16+)
- Refs: ADR-0016 cross-cut audit pattern (alternatives B 거절 사유 해소 trigger), commits `8b2414f` / `33c7eab` / `0e91499` / `b564fe7`

## Context

iteration 46-47 cluster-ops audit 결과 *3 operator (mongodb / valkey /
postgres) 가 동일 helper 패턴 보유* 사례 다수. ADR-0016 의 "B) 항상 commons
승격" alternatives 거절 사유는 *3-of-3 사용 입증 전 premature abstraction*
방지 — **본 ADR 시점 = 3-of-3 입증 완료**.

### 승격 candidates

#### 1. `validateStorageSize` (3-of-3 사용)

| operator | commit | path |
|---|---|---|
| mongodb | `8b2414f` | `internal/webhook/v1alpha1/mongodb_webhook.go:validateStorageSize` |
| valkey  | `33c7eab` | `internal/webhook/v1alpha1/valkeycluster_webhook.go:validateStorageSizeMin` |
| postgres| `0e91499` | `internal/webhook/v1alpha1/postgrescluster_webhook.go` (inline 분기) |

3 operator 모두 *zero IsZero() skip + Cmp(min) < 0 reject* 동일 패턴.
구현 byte 차이 = 함수명 (`validateStorageSize` vs `validateStorageSizeMin`)
+ 에러 메시지 (data dir + oplog vs RDB+AOF vs WAL+temp). *core logic 동일*.

#### 2. `apiError` helper (3-of-3 비대칭)

| operator | 패턴 |
|---|---|
| mongodb | `internal/webhook/v1alpha1/error.go` 별 helper (commit `b564fe7` 의 `GroupVersion.Group` 참조) |
| valkey  | `internal/webhook/v1alpha1/valkeycluster_webhook.go` 내부 helper (동일 패턴) |
| postgres| inline (별 helper 없음, validate 함수 내 직접 호출) |

승격 가치: *4번째 operator 추가 시 즉시 재사용*. postgres 의 inline 패턴도
helper 사용으로 일관화.

#### 3. ADR-0017 Type 분류 docs (3-of-3 audit 의무)

ADR-0016 + ADR-0017 의 *cross-cut audit 표 형식* 이 commons repo 의 docs 로
이전 — 3 operator 가 동일 ADR 형식 사용. operator-commons 의 README 또는
별 문서.

#### 4. `validateUsersSecretRefs` (1-of-3, 거절)

sister operator 만 보유 (commit `6b2dbf0`). mongodb / postgres 의 *Auth.Users*
spec 부재 (mongodb 도 `Spec.Auth.Users` 정의 있지만 운영 미사용 + valkey 와
다른 SecretKeySelector vs LocalObjectReference 구조). **거절** — 1-of-3 은
ADR-0016 alternatives B 의 premature abstraction.

## Decision

**operator-commons v0.5.0 release 시 승격 (Phase A):**

1. `pkg/webhook/validate_storage_size.go` 신규 — `ValidateStorageSizeMin(path,
   size, min)` 시그너처. 기본 min=1Gi, 호출자가 override 가능 (postgres 의
   "WAL+temp" 같은 *operator-specific reasoning* 은 wrapper 에서).
2. `pkg/webhook/apierror.go` 신규 — `APIError(group, kind, name string, errs
   field.ErrorList) error`. 3 operator 통일 형식 (`GroupVersion.Group` 참조
   호출자 책임).

**거절 (Phase B 보류):**

- `validateUsersSecretRefs` — 1-of-3 사용. 4번째 operator 추가 시 재고.
- ADR docs 통일 — operator-commons repo 자체 ADR section 도입은 *governance
  복잡도* ↑. 별 RFC.

### Migration plan (3 operator 동시)

#### Step 1: operator-commons v0.5.0 publish (별 cycle)

- `pkg/webhook/validate_storage_size.go` + 단위 테스트 (100% coverage).
- `pkg/webhook/apierror.go` + 단위 테스트.
- v0.5.0 tag + module proxy 등록.

#### Step 2: 3 operator go.mod bump (PR 3건 동시)

- mongodb-operator: v0.4.0 → v0.5.0.
- sister operator: v0.4.0 → v0.5.0.
- sister operator: v0.4.0 → v0.5.0.

#### Step 3: 3 operator 코드 마이그레이션 (PR 3건 동시)

- 각 operator 의 `validateStorageSize` 또는 `validateStorageSizeMin` 제거 →
  `commons.webhook.ValidateStorageSizeMin` 호출.
- `apiError` helper 제거 → `commons.webhook.APIError(GroupVersion.Group, ...)` 호출.
- 기존 unit test + envtest round-trip 통과 검증.

#### Step 4: 통합 검증 (3 operator)

- 3 operator 의 `go test ./...` PASS.
- `golangci-lint run`: 0 issues.
- `helm lint`: PASS.

## Consequences

### 긍정

- DRY 격차 해소 — 3 operator 의 *동일 helper 4 위치 정의* → *commons 1 위치*.
- 4번째 operator 추가 시 즉시 재사용 (e.g. 가상 `keiailab/elasticsearch-operator`).
- postgres 의 inline 패턴 *helper 사용 일관화*.
- ADR-0016 cross-cut audit 의 *완성된 진화 사례* — alternatives B 거절 사유
  해소 후 승격.

### 부정

- 3 operator go.mod bump + PR 동시성 — *partial migration* 시 cross-version
  drift. *Step 1-4 동시 진행* 의무.
- operator-commons v0.x 의 *breaking change 가능성* — v1.0 미달 단계라 API
  shape 변경 자유. 단 본 ADR 의 helper 들은 *core stable* 추정.

### 트레이드오프

migration cost (3 PR 동시) < DRY 가치. 또한 *cross-cut audit pattern 의
완성형* — 발견 → 통일 → 승격 의 자연 진화.

### 후속 작업

- 별 cycle 에 operator-commons v0.5.0 release.
- 3 operator 마이그레이션 PR (동시).
- 운영 검증 후 v0.5.0 stable 선언.

## Alternatives Considered

### A. 무행동 (각 operator 독립 helper 유지)

3 operator 가 각자 helper 보유. ADR-0016 alternatives A 의 거절 사유
("audit 결과 등록 의무 위반") 와 동일.

**거절 사유**: 4번째 operator 추가 시 *4 helper 정의 + 3 operator 와 drift
가능성*. *cross-cut audit pattern* 의 자연 종착점이 *commons 승격* — 본 ADR
이 그 *완성형* 명시.

### B. v0.5.0 대신 v1.0.0 stable 승격

본 시점에 operator-commons v1.0.0 stable 선언.

**거절 사유**: v1.0 은 *API shape 변경 동결* 의무. 향후 추가 helper 후보
(예: `validateAuthSecretRef`, `validateBackupSpec`) 가 *3-of-3 입증 전*
이라 v0.x 단계 유지가 *evolution flexibility* 보장. v1.0 은 4-5 helper 누적
+ 1년 운영 후 (별 cycle).

### C. validateUsersSecretRefs 도 함께 승격

1-of-3 이지만 *futureproofing* 위해 미리 commons.

**거절 사유**: ADR-0016 alternatives B (premature abstraction) 위반. *3 of 3
입증 후 승격* 원칙 유지.
