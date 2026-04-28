# ADR-0001: 자체 createOrUpdate 헬퍼 폐기 + controllerutil.CreateOrUpdate(mutateFn) 채택

- Date: 2026-04-28
- Status: Accepted
- Authors: @phil

## Context

mongodb-operator의 3개 컨트롤러(MongoDB / MongoDBSharded / MongoDBBackup)는
모두 자체 `createOrUpdate(ctx, owner, obj)` 헬퍼를 가지고 있었다. 동작:

1. `controllerutil.SetControllerReference(owner, obj, scheme)` — owner ref 설정.
2. `obj.DeepCopyObject()`로 fetch 대상 사본 생성.
3. `r.Get(...)` → `IsNotFound`면 `r.Create(ctx, obj)`.
4. 존재하면 `obj.SetResourceVersion(existing.GetResourceVersion())` 후
   `r.Update(ctx, obj)`.

문제: 4단계에서 *호출자가 전달한 obj 그대로* update. 호출자가 desired 객체를
*partial mutation*으로 만든 경우(예: STS의 Replicas만 변경하고 다른 필드는
미설정), `r.Update`는 *기존 spec을 obj 값으로 덮어쓴다*. 결과:

- StatefulSet의 Template이 비어있는 채로 Update → spec drift.
- Service의 ClusterIP가 빈 string으로 Update → API server가 immutable 위반으로 거부.
- ConfigMap의 Data 일부만 mutation → 나머지 키 silent 삭제.

검수에서 P0로 식별 (mongodb_controller.go:367-388, mongodbsharded_controller.go:604-625).

## Decision

자체 createOrUpdate 헬퍼를 *완전히 폐기*하고 controller-runtime 표준
`controllerutil.CreateOrUpdate(ctx, c, obj, mutateFn)` 패턴으로 마이그레이션.

도메인 타입별 apply helper(`internal/controller/resources_apply.go`)를 도입:

- `applyConfigMap`, `applyService`, `applyStatefulSet`, `applyDeployment`,
  `applyPDB`, `applyNetworkPolicy`.
- 각 helper는 `mutateFn` 안에서 *어떤 필드를 갱신할지 명시적으로 선언*.
- immutable 필드(Service.ClusterIP, STS.Selector/ServiceName/
  VolumeClaimTemplates, Deployment.Selector)는 `target.CreationTimestamp.IsZero()`
  분기로 *Create 시점에만* 설정.

호출자(reconcile* 함수들)는 desired를 builder로 만들고 apply helper를 호출.

적용 제외:

- `reconcileKeyfileSecret`: keyfile은 immutable(첫 Create만, 이후 갱신 시 RS
  auth 깨짐) → 자체 helper 유지(Get → exists면 skip → 없으면 Create).
- `mongodbbackup_controller.createOrUpdate`: Job spec immutable이라 update 무
  의미 → create-only 자체 helper 유지.

## Consequences

긍정:

- spec 손실 위험 차단 — partial mutation으로 desired를 만들어도 mutateFn이
  명시한 필드만 갱신되어 기존 spec 보존.
- immutable 필드 update 시도가 명시적 분기로 차단되어 K8s API server 거부 위험 ↓.
- mutateFn 패턴이 *도메인 의미*를 코드로 표현 → 새 호출 사이트 추가 시 어떤
  필드를 동기화해야 하는지 명확.
- D1 envtest 회귀 봉쇄망과 결합해 향후 마이그레이션 회귀를 즉시 검출 가능.

부정:

- 코드 양 ↑ (helper 6개 추가, 각 4-15 LOC). 단 응집도가 더 높아 trade-off 가치.
- mutateFn 안에서 깜빡 누락한 필드는 update 시 동기화 안 됨 — D1 envtest가
  케이스별로 검증.

후속 작업:

- 후속 사이클에서 Owns(...) 추가 등 controller wiring 변경 시 helper 패턴
  재사용 (이미 PDB, NetworkPolicy 트랙에서 적용).
- backup controller도 *create-only가 진짜 옳은가* 재검토 — Job 재실행 시나리오
  (재시도, 재스케줄)가 필요해지면 별도 ADR.

## Alternatives Considered

1. **자체 createOrUpdate 유지 + 호출자가 항상 full spec 전달**: 호출자 책임이
   분산되어 누락 위험 ↑. 새 reconcile* 함수 추가할 때마다 동일 실수 반복.

2. **Server-side apply (SSA) 도입**: K8s 1.22+ 표준. fieldManager 기반으로
   immutable 필드 충돌이 명시적. 그러나 mongodb-operator의 모든 reconcile
   호출 사이트를 한 번에 SSA로 옮기는 건 파급 범위 ↑(테스트, controller-runtime
   client 옵션 변경). controllerutil.CreateOrUpdate가 *최소 변경 범위*로
   동일 안전성 확보 가능 → 본 사이클 채택. SSA는 후속 ADR 후보.

3. **client.Patch + StrategicMergePatch**: spec drift는 차단하지만 mutateFn
   패턴보다 declarative하지 않고, K8s SDK 변경에 더 민감. controller-runtime의
   추천 패턴인 CreateOrUpdate가 가장 future-proof.
