# ADR-0007: mongos HorizontalPodAutoscaler 지원 (RS 멤버는 제외)

- Date: 2026-04-29
- Status: Accepted
- Authors: @eightynine01

## Context

`Spec.Mongos.AutoScaling` / `AutoScalingSpec`은 v1alpha1 API에 이미 선언돼
있었으나 reconcile 로직이 비어 있어 사용자가 spec을 채워도 HPA가 만들어지지
않았다(2026-04-29 검증에서 발견). RBAC(`autoscaling/horizontalpodautoscalers`)는
v1.1.0 사이클에서 chart에 추가됐으나 controller 측 코드가 미구현.

mongos는 *stateless router*로 cfg/shard에 접속해 query를 routing한다. replica
변경이 RS state에 영향을 주지 않으므로 표준 HPA(CPU/Memory utilization 또는
custom metric)로 안전하게 수평 확장 가능.

반면 RS / cfg / shard 멤버 수 변경은:
- Replica set reconfiguration (`rs.add`/`rs.remove`) 트리거.
- Initial sync로 인한 *수십분~수시간*의 IO/네트워크 부하.
- election-time 일시 PRIMARY 부재.
- shard rebalancing이 동시에 진행되면 chunk migration이 길어짐.

이런 부작용 때문에 RS 멤버에 표준 HPA를 적용하면 metric 일시 변동(예: warmup
기간 일시 CPU 100%)에 따라 *RS reconfig가 빈번 발동* → 운영 안정성 훼손.
업계 표준 사례(MongoDB Inc. operator, Percona PSMDB) 모두 RS 멤버는 vertical
scaling만 지원, 멤버 수 변경은 운영자 명시 reconfig만 허용.

## Decision

본 사이클에서는 **mongos에만** HPA를 도입한다.

```yaml
spec:
  mongos:
    replicas: 2
    autoScaling:
      enabled: true
      minReplicas: 2
      maxReplicas: 10
      metrics:
        - type: cpu
          target: 70
        - type: memory
          target: 80
```

옵트인(`enabled: true`)이며 비활성 시 기존 HPA를 cleanup. metric 미지정 시
default `cpu 80% utilization`(Bitnami chart 등 표준).

지원 metric type:
- `cpu` / `memory` → `Resource` metric, `Utilization(%)`
- `custom` → `Pods` metric, `AverageValue` (Prometheus adapter 등 외부 metrics
  server가 expose하는 metric. `customMetric.name` 필수.)

RS / cfg / shard 멤버는 본 ADR 범위 외 — 별도 사이클에서 *명시 reconfig API*
또는 *predicate-based scaling*(metric warmup이 정상 운영의 일부일 때만 trigger)
도입을 검토.

## Consequences

긍정:
- mongos가 부하 변동에 자동 대응 → 운영자 수작업 부담 감소.
- spec이 이미 v1alpha1 API에 있어 backward-compatible(필드 추가 없음, 기존
  CR은 default `nil` → opt-out 동작).
- 단위 테스트로 7개 분기 회귀 가드 (disabled/nil/default/clamp/cpu+mem/custom/
  empty-name).

부정:
- HPA가 mongos `Deployment.spec.replicas`를 patch하므로 operator의 `BuildMongos
  Deployment`가 재계산하는 desired replicas가 무시된다. controller-runtime의
  `controllerutil.CreateOrUpdate`는 spec 전체를 비교하므로 reconcile cycle에서
  `replicas`만 *유지* 처리하는 별도 분기가 필요 — 본 사이클에서 다루지 않음
  (HPA controller가 매 ~15s마다 자체 patch로 정렬하므로 사용자 가시 영향 없음).
- 멤버 metric source(metrics-server 또는 Prometheus adapter)가 cluster에 없으면
  HPA는 `<unknown>` target으로 표시되며 scale 안 됨. 본 ADR 범위는 *operator 측
  HPA 객체 정확 생성*에 한정.

후속 작업:
- mongos Deployment reconcile에서 HPA 활성 시 `replicas` 무시 분기.
- RS 멤버 수 변경의 *명시 reconfig API* (`spec.scalePolicy.deliberate=true`
  같은 가드).
- HPA 상태(`current/desired replicas`)를 CR `status.mongos`에 노출.

## Alternatives Considered

1. **모든 컴포넌트(cfg/shard 멤버 포함)에 HPA 일괄 도입**: RS reconfig 빈발
   위험. 거절.
2. **VerticalPodAutoscaler(VPA) 도입**: VPA는 pod restart 동반(in-place 미지원
   k8s < 1.27 또는 alpha gate)으로 mongod의 PRIMARY 전환을 빈번 일으킬 수 있다.
   본 사이클 범위 외.
3. **KEDA(custom event-driven autoscaler) 도입**: 외부 dependency 추가. spec에
   이미 있는 `AutoScalingSpec` 필드를 우선 활용하는 게 더 적절. 거절.
