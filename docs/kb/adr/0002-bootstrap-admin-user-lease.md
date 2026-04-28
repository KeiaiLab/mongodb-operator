# ADR-0002: Admin user 부트스트랩 race-free를 위한 K8s Lease 분산락 도입

- Date: 2026-04-28
- Status: Accepted
- Authors: @phil

## Context

`MongoDBReconciler.reconcileAdminUser`는 RS init 직후 *유일한 익명 접근 창*에서
첫 admin user를 생성한다 (MongoDB의 localhost exception). 호출 흐름:

1. anonymous connect로 RS의 임의 멤버에 붙음.
2. `replSetGetStatus`로 PRIMARY 멤버 발견.
3. PRIMARY에 createUser 명령.
4. `Status.AdminUserCreated=true`로 영속화.

검수에서 식별된 P0:

- 두 reconcile loop이 동일 CR에 대해 동시에 진입하면(예: leader pod이 두
  worker goroutine으로 reconcile 동시 실행), 둘 다 익명 접근 창에서 진행.
  첫 번째가 createUser 성공 후 두 번째가 도달하면 익명 접근이 *이미 차단된*
  상태이므로 typed `AuthenticationFailed` 또는 `requires authentication` 에러
  반환. `isAuthRequiredErr`가 이 신호를 "user 이미 존재"로 해석해 두 reconcile
  모두 nil 반환 → 둘 다 success 처리.

- 위 시나리오의 *진짜* 위험: 첫 번째가 `createUser` 호출 직전에 멈췄고 두 번째
  reconcile이 그 사이에 진입해 *createUser 실패* 후 `isAuthRequiredErr` 분기로
  nil 반환. 결과: user가 *0명*인데 둘 다 success 보고.

또한 `BootstrapAdminUser` 직후 `Status.AdminUserCreated=true`로 영속화하는
시점에 *user가 실제로 만들어졌는지* 검증이 없었다 — partial-failure도 success로
위장될 가능성.

## Decision

K8s `coordination.k8s.io/v1.Lease` 자원을 mdb 단위 분산락으로 사용한다.

구현(`internal/controller/bootstrap_lease.go`):

- Lease 이름: `mongodb-bootstrap-{cr-name}` (mdb namespace).
- Holder: `{hostname}-{pid}` (process-unique).
- LeaseDurationSeconds: 30s (정상 path에서 충분 + holder pod 사망 시 takeover).
- 흐름: `Get → 다른 holder가 valid면 busy(false, nil) → 점유 또는 expired면
  Update(CAS)`. ResourceVersion conflict는 다른 reconcile이 동시 갱신했다는
  의미로 busy로 해석.
- AlreadyExists / Conflict / valid-other-holder 모두 busy로 일원화 → 호출자
  가 5s requeue로 양보.

`reconcileAdminUser`는 lease 점유 후 BootstrapAdminUser → 인증된 매니저로
`usersInfo` ping → 통과해야만 `Status.AdminUserCreated=true` 영속화. lease는
defer로 release.

## Consequences

긍정:

- 동일 CR에 대한 동시 reconcile race 차단.
- post-bootstrap verify로 partial-failure가 success로 위장되는 silent bug 봉쇄.
- controller-runtime leader-election과 *별개*의 resource-level lock — leader
  pod이 동일 CR에 대해 두 reconcile loop을 동시에 돌리는 시나리오까지 차단.
- Lease는 owner ref가 mdb이므로 mdb 삭제 시 자동 GC.
- 후속에서 sharded controller, backup controller도 동일 패턴 재사용 가능
  (현재는 mongodb_controller만 적용).

부정:

- RBAC 권한 추가: `coordination.k8s.io/leases` (get/list/watch/create/update/
  patch/delete).
- 부트스트랩 latency가 5s requeue × N (lease busy 시) 만큼 증가할 수 있음.
  단 정상 path에서는 첫 reconcile이 즉시 점유.
- Lease가 expired인데 holder pod이 살아있는 corner case: 30s TTL을 짧게
  잡았으므로 takeover한 두 번째 reconcile이 createUser를 다시 시도. 이때
  isUserAlreadyExistsErr가 idempotent 처리하므로 안전 — 단 이를 보장하기 위해
  ADR-0003-related typed-error fallback 패턴이 필요.

후속 작업:

- D2-D4(race 시나리오 테스트)에서 lease busy → backoff → 단일 호출 검증을
  envtest 또는 fake client 단위로 추가.
- sharded `reconcileShardedAdminUser`에도 동일 패턴 적용 (별도 사이클).

## Alternatives Considered

1. **process-level mutex (sync.Mutex)**: 단일 process 안에서는 race 차단되지만
   leader-election fallback 시점이나 멀티 controller pod 환경에서 보장 안 됨.
   K8s native distributed lock이 표준.

2. **Optimistic concurrency on mdb.Status**: `Status.AdminUserCreated=true`를
   먼저 영속화 시도하고 `IsConflict`면 다른 reconcile이 진행 중이라 판단.
   문제: createUser 호출 전후 race 창이 그대로 남아 있어 위 P0 시나리오 미해결.

3. **controller-runtime leader-election에 의존**: leader-election은 process
   단위 coordination만 제공. 동일 leader 안에서 같은 CR의 두 reconcile loop이
   동시 실행되는 시나리오는 차단 안 됨. resource-level lock이 보완.

4. **MongoDB의 idempotent createUser에만 의존**: server가 11000(DuplicateKey)
   반환 시 idempotent 처리. 하지만 *createUser 호출 직전* race(`replSetGetStatus`
   ~ createUser 사이)는 보장 안 됨.
