# ADR-0009: shard / cfg HPA의 RS 부작용과 이중 가드

- Date: 2026-04-29
- Status: Accepted
- Authors: @keiailab

## Context

ADR-0007에서 mongos는 stateless router로 표준 HPA로 안전하게 scale 가능함을
명시했고, RS / cfg / shard 멤버는 RS reconfig 부작용으로 본 ADR 범위 외라고
이연했다. v1.3.0 사이클에서 이 영역을 *제한적으로* 다룬다.

`MongoDBShardedSpec`에는 두 종류의 자동화 spec이 있다.

1. `Shards.AutoScaling *ShardAutoScalingSpec{minShards, maxShards, metrics}`
   — *shard 갯수* 자동 조정.
2. `ConfigServer.AutoScaling`(신규) — cfg 멤버 수.
3. `Shards.ScalePolicy`(신규) — shard 멤버 수(MembersPerShard) 변경 가드.

## Decision

### 1. `ConfigServer.AutoScaling` + `ConfigServer.ScalePolicy` 이중 가드 (ADR-0008과 동일)

cfg는 보통 작은 멤버 수(3-7)이고 변동이 거의 없으므로 *기본 비활성*. opt-in
시 `AutoScaling.Enabled=true` + `ScalePolicy.Deliberate=true` 둘 다 만족해야
HPA 활성화. `BuildConfigServerHPA`에서 강제.

### 2. `Shards.AutoScaling`(*shard 갯수*) — 명시 거절, reconcile 안 함

shard 갯수 변경(`addShard`/`removeShard`)은:
- chunk migration이 동반된다(이미 sharded collection이 있으면).
- balancer가 *수 분~수 시간* 동안 chunk를 새 shard로 이동하면서 cluster 부하
  급증.
- removeShard는 *모든 chunk가 다른 shard로 옮겨지기 전*까지 차단되어 사용자
  명령어에 *수십 분 hang*.
- mongo의 표준 HPA의 short-window metric(보통 1-5분)으로 trigger되면 cluster
  안정성을 *심각하게* 훼손한다.

본 ADR에서는 `ShardAutoScalingSpec` spec을 *보존*(API 호환성)하되 operator의
reconcile loop은 이를 *읽지 않는다*. CRD validation도 변경하지 않으나 README에
명시 비활성으로 표기. 운영자는 수동으로 `spec.Shards.Count`를 조정해야 하며
이는 ADR-0008의 deliberate 가드 적용 대상이 *아니다*(현재 `ShardSpec.Count`는
자체 가드 없이 operator가 즉시 reconcile, 별도 사이클에서 deliberate 적용
검토).

### 3. `Shards.ScalePolicy` — `MembersPerShard` 가드 (ADR-0008 적용)

shard *멤버 수 per shard*는 RS reconfig 동반이지만 chunk migration은 없다 —
표준 RS reconfig 패턴. 따라서 ADR-0008의 deliberate 가드를 그대로 적용.

## Consequences

긍정:
- cfg HPA를 안전한 opt-in 모델로 제공.
- shard 갯수 자동화는 *명시 거절*로 운영 안정성 보장.
- `Shards.MembersPerShard` 변경을 deliberate 가드로 보호.

부정:
- 사용자 spec에 `ShardAutoScalingSpec` 필드가 *작동하지 않는* 잔여물로 남음.
  README에 명시 + spec 코멘트(이미 추가)로 안내. 향후 사이클에서 별도
  *deliberate sharding API*(spec.shardCount 변경에 deliberate 가드 + 별도
  rebalance window 설정)로 대체 가능.
- cfg HPA가 metric 변동에 따라 cfg 멤버 수를 변경하면 *config DB의 RS
  reconfig*가 발생 — chunk metadata는 cfg에 저장되므로 mongos가 일시 router
  table을 다시 받아야 한다. 정상 RS에서는 ms 단위지만 운영자가 ScalePolicy.
  Deliberate를 의도하고 켰다는 전제하에 허용.

후속 작업:
- *deliberate sharding API* — spec.Shards.Count 변경에 deliberate + drain
  window 동반.
- HPAStatus를 CR `Status.ConfigServer.HPA`에 노출 (현재 spec 정의 완료, sync
  로직 별도 commit).

## Alternatives Considered

1. **`ShardAutoScalingSpec` 필드 deprecate + 삭제**: backward-compat 깨짐
   (kubectl apply 시 Unknown field 경고/거부 가능성). API 호환성 위해 필드
   보존 + 무시 전략 채택. 거절.
2. **cfg HPA를 mongos처럼 단일 가드로**: cfg는 stateful이라 standalone에선
   안전하지 않음. 이중 가드 유지. 거절.
3. **shard 갯수 자동화를 deliberate + drain window로 구현**: 본 사이클 범위
   외. 별도 사이클로 이연.
