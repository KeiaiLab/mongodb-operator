# ADR-0008: ReplicaSet 멤버 수 변경의 deliberate 가드

- Date: 2026-04-29
- Status: Accepted
- Authors: @keiailab

## Context

`MongoDB.Spec.Members`(RS), `ConfigServer.Members`(cfg), `Shards.MembersPerShard`
변경은 곧바로 *RS reconfiguration* + *initial sync*를 트리거한다. initial sync는
mongo 8.x에서 데이터 크기에 따라 수십 분~수 시간 소요되며 IO/네트워크/CPU를
대량 소비한다. 그동안:

- PRIMARY가 일시 부재(election 진행 중)이거나 *write concern majority*가 만족되지
  않아 client write가 실패할 수 있다.
- shard 환경에서는 chunk migration까지 동시 진행되어 query latency가 측정 가능
  하게 악화된다.
- 운영자가 spec을 *오타로* 변경하거나 git ops에서 잘못된 값으로 sync하면 즉시
  시작되어 *되돌리기 어려운 상태*로 빠진다.

특히 `Spec.AutoScaling`이 활성화된 상태에서 HPA controller가 metric 변동에 따라
Replicas를 patch하는 것도 구별 없이 똑같은 부작용을 일으킨다.

업계 사례:
- MongoDB Inc. 자체 operator는 `mongoDBVersionedAutoscaling.HumanReviewRequired`
  플래그로 자동 scale을 가드.
- Percona PSMDB는 `manualUpdate` 플래그로 STS 업데이트를 명시 승인 모델로 변경.
- Bitnami chart의 mongodb-sharded는 mongos 외 컴포넌트 HPA를 *기본 미지원*.

## Decision

`ScalePolicy{Deliberate bool}` 신규 타입을 도입해 RS / cfg / shard의 멤버 수 변경
을 *명시 승인 모델*로 전환한다. `MongoDB.Spec.ScalePolicy`,
`ConfigServer.ScalePolicy`, `Shards.ScalePolicy` 세 곳에 추가.

### 동작 매트릭스

| 조건 | 동작 |
|---|---|
| `ScalePolicy=nil` 또는 `Deliberate=false` | spec.Members 변경이 *적용되지 않음*. operator는 STS replicas를 그대로 두고 `Status.PendingScale={current,desired,requestedAt,reason}` 노출 + 로그 출력 |
| `ScalePolicy.Deliberate=true` | spec.Members 변경 즉시 STS replicas로 반영. HPA가 활성화되어 있으면 HPA controller의 patch도 즉시 반영 |

### HPA 활성화 조건 (이중 가드)

`AutoScaling.Enabled=true`만으로는 HPA를 *생성하지 않는다*. `ScalePolicy.
Deliberate=true`까지 둘 다 만족해야 `BuildReplicaSetHPA` / `BuildConfigServerHPA`가
HPA 객체를 반환한다. 둘 중 하나라도 false면 기존 HPA가 있으면 cleanup.

근거 — RS 멤버 자동 scale은 *되돌리기 어려운 작업*의 자동화이므로 *명시 의도*
임을 spec에 박는 것이 안전.

### applyStatefulSet preserveReplicas 분기

`applyStatefulSet`은 `preserveReplicas=true`일 때 운영 중인 STS의 spec.Replicas
<!-- live-verified: 2026-05-09 -->
를 desired로 덮어쓰지 않는다. 두 경우에 사용:

- **HPA 활성**: HPA controller가 patch한 값 보존(ADR-0007과 동일 의도).
- **Deliberate=false**: 보류 중인 spec.Members 변경을 STS에 반영하지 않음.

첫 Create 시점에는 desired.Spec.Replicas를 그대로 사용 — 첫 deploy를 막지 않는다.

## Consequences

긍정:
- 운영자 실수에 대한 안전망. spec 변경이 즉시 *되돌리기 어려운* RS reconfig를
  트리거하지 않는다.
- HPA 자동 scale을 *명시 opt-in*으로 전환 — 모르는 사이 RS reconfig가 발동되는
  운영 사고 차단.
- `Status.PendingScale`로 보류된 변경이 가시화되어 운영자가 한눈에 인지하고
  deliberate=true로 승인 가능.

부정:
- 사용자가 `spec.Members` 변경 후 동작이 즉시 안 일어나는 *놀라움*을 겪을 수
  있다 — `Status.PendingScale` 메시지에 명시 안내. 문서(README, helm values
  주석)에서도 강조 필요.
- HPA 첫 적용에 두 필드를 동시 설정해야 함 — values.yaml 예제에서 보이게.
- 이중 가드를 `Status` 단순 ‘진행’으로 오해할 가능성 — `Reason` 필드에 명확한
  가이드 메시지 박아둠("set spec.scalePolicy.deliberate=true to apply").

후속 작업:
- HPAStatus(`current/desired/min/max`) 노출 — 본 사이클에서 spec 정의는 완료,
  reconcile loop의 status sync 구현은 별도 commit 가능.
- shard count auto-scaling은 `ShardAutoScalingSpec` spec에 보존되되 *operator
  가 reconcile하지 않음* — addShard/removeShard는 chunk migration 동반이라
  표준 HPA로 다룰 수 없다는 별도 ADR-0009 결정에 따름.

## Alternatives Considered

1. **AdmissionWebhook으로 spec 변경 거부**: spec 자체는 받되 멀쩡해 보이는데
   효과가 없는 *침묵 동작*보다 webhook으로 reject하는 게 명확. 단, webhook은
   별도 인프라(cert-manager + service)가 필요하고 본 operator는 webhook 없이
   동작하는 게 설치 단순성에 부합. 거절(별도 사이클 검토).
2. **deliberate=true가 기본값**: 사용자 단순성 측면에서는 좋지만 안전망이
   없어진다. 거절.
3. **spec.scaleDirection만 설정 (up/down 명시)**: deliberate보다 세분화됐으나
   복잡도가 의미 없게 증가. 단순한 boolean이 더 명확. 거절.

<!-- live-verified: 2026-05-09 -->
