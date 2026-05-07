# ADR-0014: Controller Create 패턴 boundary — CreateOrUpdate vs intentional 수동

- Date: 2026-05-07
- Status: Accepted
- Authors: @eightynine01
- Refs: ADR-0008 (operator-commons 채택), ADR-0013 (conditions LastTransitionTime),
  HANDOFF iteration 41-43 (3 operator race-tolerant audit + CreateOrUpdate 마이그레이션)

## Context

iteration 41 의 cross-operator audit 에서 발견:
- valkey iteration 40 (`ac1421f`): backup Job *Get NotFound + Create AlreadyExists*
  race condition → *수동 IsAlreadyExists guard* 추가
- mongodb iteration 41 (`a0a0cff`): 2 호출 사이트 (mongodbbackup + helpers) *동일
  deviation* → 동일 *수동 guard* 적용
- postgres-operator: `controllerutil.CreateOrUpdate` 만 사용 — *우월한 추상화*
  (controller-runtime 의 *AlreadyExists 자동 retry*)

iteration 42 (`aa56f48`) + iteration 43 (`85451ef`) 에서 *mongodb mongodbbackup +
valkey valkeybackup × 2* → CreateOrUpdate 마이그레이션 (postgres 패턴 차용).
~25줄 → ~5줄 단순화.

본 ADR 은 *남은 2 호출 사이트* (mongodb bootstrap_lease + helpers) 에 대한
*마이그레이션 vs 보존* 결정.

## Decision

**bootstrap_lease + helpers 의 *수동 Get + Create + IsAlreadyExists 패턴 보존***.
CreateOrUpdate 마이그레이션 *부적합* — 각 사이트 별 *intentional design* 의미가
CreateOrUpdate 의 *update mutate* semantic 과 충돌.

### 1. bootstrap_lease.go (`acquireBootstrapLease`)

**Lease busy/holder detection logic**:

```go
existing := &coordinationv1.Lease{}
err := r.Get(...)
if apierrors.IsNotFound(err) {
    // fresh Lease 생성 — 본 reconcile 이 leader 됨
    if err := r.Create(ctx, fresh); err != nil {
        if apierrors.IsAlreadyExists(err) {
            // 다른 reconcile 이 동시에 막 만든 상태 — busy
            return nil, false, nil
        }
        return nil, false, err
    }
    return fresh, true, nil  // ← create 성공 → leader
}
// existing.Spec.HolderIdentity 검사 — 다른 holder 가 valid lease 보유 시 busy
if *existing.Spec.HolderIdentity != holder && time.Now().Before(deadline) {
    return nil, false, nil  // busy
}
```

**Why preserve**: Lease 의 *busy detection* 은 *holder identity 비교* 가 핵심.
CreateOrUpdate 로 변환 시:
- mutate fn 안에서 `existing.Spec.HolderIdentity = &holder` 설정 → *다른 holder 의
  Lease 를 자기 holder 로 덮어씀* → busy detection 의미 위반.
- *Atomic create-or-busy 검사* 가 *race-tolerance* 의 *명시적 design* — 자동 retry 보다
  *세밀한 제어* 필요.

ADR-0002 (Admin user 부트스트랩 race-free) 와 일관 — *Lease 분산락* 의 *intentional
design*.

### 2. helpers.go (`ensureSecret`)

**Random password 1회 generate 보장**:

```go
existing := &corev1.Secret{}
if err := r.Get(...); err == nil {
    return nil  // 기존 secret 보존 — random password 변경 안 함
}
if !errors.IsNotFound(err) { return err }

secret := build()  // ← random password 생성 (1회만)
controllerutil.SetControllerReference(...)
if err := c.Create(ctx, secret); err != nil && !errors.IsAlreadyExists(err) {
    return err
}
```

**Why preserve**: `build()` 가 *random 32-byte password* 생성 — 매 reconcile 마다
호출 시 *password 매번 변경* 위험. 현재 패턴은 *Get success 시 build() 호출 skip*
— *secret data 보존* 보장.

CreateOrUpdate 로 변환 시 *mutate fn 매 reconcile 호출* — *build() in mutate fn*
패턴 가능하지만:
- mutate fn 안에서 `if secret.CreationTimestamp.IsZero()` 체크 + build() 호출
- mongodb 의 *명시적 *build() 1회 호출* design* 보다 *복잡도 ↑*
- 가독성 ↓ + future contributor 의 *random password 매번 변경* 실수 위험 ↑

따라서 *수동 패턴 보존* — *intentional design* + *future-proof simplicity*.

## Consequences

### Positive
- bootstrap_lease 의 *Lease 분산락 의미론* 유지 (ADR-0002 정합).
- secret 의 *random password 1회 generate* 보장 (보안 invariant).
- *수동 패턴* 이 *명시적 → 가독성 ↑*.

### Negative
- 3 operator 추상화 *완전 통일* 미달 — mongodb 가 *mixed pattern* 유지.
- 향후 *비슷한 사이트 추가* 시 *어느 패턴* 선택 결정 비용.

### Trade-offs
- *추상화 통일* (CreateOrUpdate everywhere) vs *intentional design 보존* (mixed):
  본 ADR 은 *intentional 우선*. **추상화 일관성보다 *명시적 의도* 우선**.

## Alternatives Considered

1. **bootstrap_lease 도 CreateOrUpdate 마이그레이션** — 거절: Lease busy
   detection logic 손실. ADR-0002 정합 위반.
2. **helpers 도 CreateOrUpdate + mutate fn 안 build()** — 거절: 복잡도 ↑ +
   *random password 매번 변경* 잠재 bug 위험.
3. ***추상화 통일* 위해 두 사이트 모두 변환 + ADR 없이 Implementation note** — 거절:
   *intentional design* 위반은 ADR 의 *주요 트리거 케이스*.

## Decision Matrix (3 operator post-it44)

| Operator | Sites | Pattern | Reason |
|---|---|---|---|
| **mongodb** | mongodbbackup (it42) | CreateOrUpdate | Job spec immutable 안전 |
| **mongodb** | bootstrap_lease | 수동 (보존) | Lease busy/holder logic (ADR-0002) |
| **mongodb** | helpers (ensureSecret) | 수동 (보존) | random password 1회 generate |
| **valkey** | valkeybackup × 2 (it43) | CreateOrUpdate | Job spec immutable 안전 |
| **postgres** | postgrescluster | CreateOrUpdate | controller-runtime 표준 |

3 operator 의 *대부분 사이트* CreateOrUpdate 통일 + *intentional 영역 2 사이트
보존* — *추상화 우월 + design 의도 보존* 균형.

## Verification

```bash
$ go test ./internal/controller/... -count=1
ok  github.com/keiailab/mongodb-operator/internal/controller  ~19s

# bootstrap_lease 의 race-free invariant 검증 (기존 unit test)
$ go test ./internal/controller/ -run TestBootstrapLease -count=1
ok

# secret invariant 검증
$ go test ./internal/controller/ -run TestEnsureSecret -count=1
ok
```

## Refs

- ADR-0002 (Admin user 부트스트랩 race-free Lease 분산락)
- ADR-0013 (conditions LastTransitionTime — upstream meta.SetStatusCondition 위임)
- valkey iteration 40 `ac1421f` (수동 IsAlreadyExists guard 도입)
- mongodb iteration 41 `a0a0cff` (cross-cut audit fix)
- mongodb iteration 42 `aa56f48` (mongodbbackup CreateOrUpdate 마이그레이션)
- valkey iteration 43 `85451ef` (valkeybackup CreateOrUpdate 마이그레이션)
- HANDOFF iteration 32 (3-way boundary: commons / upstream / 자체 보존)
- postgres-operator: `controllerutil.CreateOrUpdate` 표준 활용
