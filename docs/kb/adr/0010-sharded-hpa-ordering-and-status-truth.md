# ADR-0010: Sharded HPA — informer cache + reconcile ordering + status truth source

- Date: 2026-04-30
- Status: Accepted
- Authors: @eightynine01

## Context

v1.4.0-rc.1 까지 `features.sharded.enabled` 가 베타 carve-out 으로 기본 비활성화 되어 있었다. `charts/mongodb-operator/values.yaml` 의 작성자(본인) 주석에 두 P1 결함이 명시되어 있었으며 24h soak 환경에서 재현 가능했다.

### Issue #3 — HPA informer cache timeout (가장 본질적 결함)

`SetupWithManager` 가 `Owns(...)` 에 `autoscalingv2.HorizontalPodAutoscaler` 를 등록하지 않았다. controller-runtime 의 default cached reader 는 `For` + `Owns` 로 사전 등록된 GVK 만 informer 를 set up 한다. Sharded controller 가 HPA 를 watch 하지 않으면 다음 동작이 발생:

1. `r.Get(... HPA ...)` 호출 → cached reader 가 HPA informer 검색 → 없음
2. controller-runtime 이 lazy informer 생성 시도 → `cache sync wait` 시작
3. timeout (default 2분) → `r.Get` 이 영구 hang 또는 `cache.ErrCacheNotStarted` 반환
4. reconcile cycle 이 매번 timeout → controller pod 가 사실상 정지

RS controller (`mongodb_controller.go:724`) 는 v1.2.0 HPA 도입 시 정확히 추가됐으나 Sharded 는 *누락*. 즉시 운영 영향이 큼: 사용자가 발견한 "HPA informer cache timeout" 은 정확히 이 결함이다.

### Issue #1 — ConfigServer init/HPA ordering

`Reconcile()` 의 단계 순서가 다음과 같았다:

```
6. Mongos resources (Deployment 생성)
6.7 Mongos HPA reconcile           ← HPA 생성
6.8 ConfigServer HPA reconcile     ← HPA 생성
7. ConfigServerInit (rs.initiate)  ← cfg PRIMARY 선출
8. ShardsInit (rs.initiate)
```

문제는 단계 7/8 의 RS init 이 *완료되기 전*에 HPA controller 가 mongos Deployment 와 cfg StatefulSet 에 부착된다는 것이다. cfg replica set 이 미초기화 상태이므로:

1. mongos pod 가 cfg shards 에 connect 시도 → connection failure → CrashLoopBackOff
2. HPA controller 가 metrics-server 로부터 CrashLoopBackOff pod 의 (실패한) CPU/memory metric 을 읽음
3. metric 이 부정확 → HPA 가 잘못된 desired replicas 산출 → mongos `.spec.replicas` 를 무작위로 patch
4. cfg RS 가 결국 init 되었을 때 mongos count 는 이미 drift 한 상태 — 운영자가 인지 불가

### Issue #2 — Status.Mongos/ConfigServer Total 영구 divergence

`updateStatus()` 의 라인 619-620 (cfg) 과 644-645 (mongos):

```go
mdbsh.Status.ConfigServer = ComponentStatus{
    Ready: cfgSts.Status.ReadyReplicas,
    Total: mdbsh.Spec.ConfigServer.Members,  // ← CR.Spec 에서 읽음
    ...
}
mdbsh.Status.Mongos = ComponentStatus{
    Ready: mongosDeploy.Status.ReadyReplicas,
    Total: mdbsh.Spec.Mongos.Replicas,  // ← CR.Spec 에서 읽음
    ...
}
```

HPA 가 활성화되면 `Deployment/StatefulSet.Spec.Replicas` 의 owner 는 HPA controller 로 넘어간다 (K8s 컨벤션 — server-side apply field manager 가 다름). CR.Spec 은 변하지 않으므로 24h 동안 HPA 가 traffic 변동에 따라 scale-up/down 한 결과는 **`Status.Mongos.Total` 에 반영되지 않는다**. 동시에 `Ready` 는 실제 Deployment.Status 에서 가져오므로 **두 필드가 서로 다른 source-of-truth 를 사용** → 영구 divergence.

## Decision

### Issue #3 — `SetupWithManager` Owns 보정

```go
return ctrl.NewControllerManagedBy(mgr).
    For(&mongodbv1alpha1.MongoDBSharded{}).
    Owns(&appsv1.StatefulSet{}).
    Owns(&appsv1.Deployment{}).
    // ... 기존 Owns 6개 ...
    Owns(&autoscalingv2.HorizontalPodAutoscaler{}).  // ← 추가
    Complete(r)
```

이 한 줄이 informer cache 를 정상 부팅시키며, 부수 효과로 HPA 객체 변경 (외부 kubectl edit, replica patch 등) 이 reconcile 을 자연스럽게 trigger 함. RS controller 와 정확히 동일 패턴.

### Issue #1 — 단계 순서 재배치 + readiness gate 이중 가드

`Reconcile()` 의 HPA reconcile 두 블록을 단계 11.5/11.6 (AdminUser → ScaleIn → AddShards 이후) 로 이동:

```
7. ConfigServerInit (rs.initiate)
8. ShardsInit (rs.initiate)
9. isMongosReady
10. AdminUser
10.5 ScaleIn / 11. AddShards
11.5 Mongos HPA                    ← 새 위치
11.6 ConfigServer HPA              ← 새 위치
12. updateStatus
```

추가로 `reconcileMongosHPA` / `reconcileConfigServerHPA` 시작부에 readiness gate:

```go
if !mdbsh.Status.ConfigServerInitialized || !r.areShardsInitialized(mdbsh) {
    return nil  // 다음 reconcile 에서 재시도
}
```

이는 *Reconcile() 의 단계 순서가 깨지더라도 (외부 호출/리팩터링 회귀)* HPA 가 조기 활성화되지 않게 하는 *이중 가드*다.

### Issue #2 — HPA-aware source-of-truth 분기

`updateStatus()` 에서 HPA active 여부에 따라 Total 결정:

```go
total := mdbsh.Spec.Mongos.Replicas
if resources.IsMongosHPAActive(mdbsh) && mongosDeploy.Spec.Replicas != nil {
    total = *mongosDeploy.Spec.Replicas  // HPA-managed truth
}
```

K8s 컨벤션과 일치: **HPA 가 `.spec.replicas` 의 owner 이면 controller 는 그것을 read-only source 로 취급해야 한다**. `Spec.X.Replicas` 는 HPA 가 *없을 때만* 의미가 있다.

또한 `isClusterReady` 의 ready 비교 기준도 HPA active 시 `Status.Total` (위 분기로 결정된 desired) 을 사용. 이전에는 `Status.Ready != Spec.Replicas` → HPA scale-down 직후 ready 가 새 desired 와 일치해도 cluster 가 영구 `Initializing` phase 에 갇히는 secondary 결함이 있었다.

`updateStatus` 의 `r.Get` 도 NotFound 만 silent skip 하고 *다른 transient error 는 propagate* 하도록 변경 — 이전엔 모든 error 를 silent 무시했다.

## Consequences

### 긍정

- Sharded GA 진입 — `features.sharded.enabled` 기본값 true.
- HPA 가 stale/crashloop pod 의 metric 을 sample 하지 않음 → 부정확 스케일링 제거.
- `Status.Mongos/ConfigServer.Total` 이 24h soak 동안 실제 desired 와 정확히 일치.
- HPA scale-down 후에도 cluster 가 정상 `Running` phase 유지.

### 부정

- HPA 활성화 시점이 *모든 RS init + AdminUser + AddShards 완료 후* 로 늦춰짐. 첫 cluster bootstrap 부터 HPA 가 부착되기까지 ~30s-2min 추가 지연. 운영 환경에서 traffic 이 cluster bootstrap 동안 polled 되어도 HPA 가 미부착이므로 mongos 는 spec 의 고정 replicas 로 운영. spec 의 mongos.replicas 가 충분히 크게 설정되어 있어야 함 (운영 권장 minimum=2).
- `reconcileMongosHPA` 에 readiness gate 가 추가되어 HPA 가 *생성 안 된 상태로* 다음 reconcile 까지 기다림. controller-runtime 의 자연 backoff 로 수렴하며, 가시 지연은 수 초 단위.
- v1.4.0 → v1.4.1 업그레이드 시 기존 HPA 객체는 그대로 보존됨 (gate fail 시 nil return — *기존 HPA 삭제하지 않음*). 안전한 in-place 업그레이드.

### 트레이드오프

- *대안 A*: 단계 순서를 그대로 두고 HPA min/max 를 spec 으로 강제 freeze 하는 방법 — 거절. HPA 의 본질적 가치를 훼손.
- *대안 B*: Total 을 `Status.Replicas` (실제 pod 수) 에서 가져오는 방법 — 거절. HPA 가 스케일링하는 *과정 중*에는 desired 와 actual 이 일시 불일치 (`maxSurge/maxUnavailable` 윈도). desired (`Spec.Replicas`) 가 더 안정적인 source.
- *대안 C*: Owns(&autoscalingv2.HorizontalPodAutoscaler{}) watch 추가 — 별개 개선. 본 ADR 은 ordering + truth source 만 다룸.

## Alternatives Considered

- **State machine 도입** (Phase: Bootstrapping → Initializing → Running → Scaling): 큰 리팩터로 회귀 표면 과다. 단계 순서 재배치 + readiness gate 만으로 충분.
- **HPA 에 별도 controller 분리**: K8s autoscaling/v2 가 이미 분리된 controller 임. 우리 operator 의 `reconcileMongosHPA` 는 단순 "spec → HPA 객체" 변환자이므로 분리 불필요.
- **`Status.Phase` enum 확장 (HPAActive 추가)**: `isClusterReady` 의 minReplicas 비교만으로 동등 효과. 추가 enum 은 API 호환성 부담.

## Refs

- 관련 ADR: ADR-0007 (mongos HPA), ADR-0008 (RS deliberate), ADR-0009 (cfg HPA 이중 가드)
- 관련 commit: v1.4.1 release
- 회귀 테스트: `internal/controller/mongodbsharded_p1_unit_test.go` 6 케이스
