# ADR-0024: INC-0001 cross-cut audit — mongodb 의 once-shot init flag 패턴

- Date: 2026-05-10
- Status: Accepted
- Authors: @eightynine01

## Context

valkey-operator INC-0001 (2026-05-09 운영 keiailab-valkey-prod 19h cluster fail)
의 root cause: controller 의 `status.ClusterInitialized=true` flag 가 *cluster
fail 상태에서도 reset 안 됨* — bootstrap *once-shot* 가정으로 자가치유 차단.
영구 fix: ADR-0039 (valkey-operator) post-init self-heal — flag 무관 *실제 cluster
state* 검증 후 ensureClusterMeet 재호출.

ADR-0016 (Cross-cut Audit Pattern) 정합으로 mongodb-operator 의 비슷한 패턴 audit.

## Audit Findings

mongodb-operator 의 동일 패턴:

| Site | Flag | Once-shot 분기 | Mitigation |
|---|---|---|---|
| `mongodb_controller.go:165` | `!mdb.Status.ReplicaSetInitialized` | `reconcileReplicaSetInitialization` skip 시 RS state query 안 함 | **부분** — line 191 의 `hasPrimary` check 가 primary 부재 시 PrimaryUnreachable condition 발현 + requeue. 단 *RS membership 깨진* 시나리오 (노드 forget) 는 hasPrimary 만으로 회복 불가. |
| `mongodb_controller.go:510` | `ReplicaSetInitialized && AdminUserCreated` 의 *Ready* 판정 | Ready=True 가 status 만 보고 결정 — *RS member 누락* 시 Ready=True 그대로. | **부분** — 다른 condition (PrimaryUnreachable, AuthenticationReady) 가 진단 신호. |

**INC-0001 동등 시나리오 (mongodb)**: pod 재시작 후 mongo 인스턴스의 RS 멤버십이
깨진 상태 (예: node 가 다른 RS member 들의 host 변경 미감지). 현재는:
- `IsInitialized` (line 359) 가 *first pod 만* query → first pod 이 자기 자신은
  initialized 로 인지 → `mdb.Status.ReplicaSetInitialized=true` 설정 → 다음 reconcile
  에서 line 165 에서 *bootstrap skip*.
- `hasPrimary` (line 191) 가 primary 못 찾으면 PrimaryUnreachable condition + retry.
  하지만 *RS reconfig* (rs.reconfig 호출로 멤버 hosts 갱신) 는 이루어지지 않음.

즉 *milder* 형태의 동일 anti-pattern. 운영 영향:
- RS member host 변경 (e.g. headless service DNS 갱신 지연) 시 hasPrimary 가
  long-term fail → PrimaryUnreachable 운영자 알람.
- RS reconfig 자동화 부재 — 운영자 manual `rs.reconfig()` 필요.

valkey 와 다른 점: mongodb 의 ReplicaSet 은 valkey Cluster 처럼 *slot ownership*
이 없음. RS membership reconfig 는 *primary 1 차례 호출* 로 즉시 적용. 회복 비용
저렴.

## Decision

**Phase 1 (본 ADR)**: audit 결과 + 영향 분석 기록. 즉시 fix 는 *deferred*.
- 근거: 운영 영향이 *milder* (PrimaryUnreachable 알람 + 운영자 대응 가능). valkey
  의 19h fail 같은 stuck 안 됨.
- mongodb HA 운영 패턴 (deliberate scaling, ADR-0008/0009/0010) 이 *manual operator
  awareness* 를 가정 — auto self-heal 도입은 별 RFC.

**Phase 2 (별 PR)**: RS reconfig 자동화 도입 — `mongodb_controller.go` 의
`hasPrimary` 가 fail 했을 때 *RS member host 검증 + reconfig* 시도. 별 ADR + RFC.

## Consequences

긍정:
- INC-0001 cross-cut audit 의무 (ADR-0016) 충족.
- mongodb-operator 의 *milder anti-pattern* 영향 평가 + 후속 트랙 명시.
- 운영자 대응 시 PrimaryUnreachable condition 의 의미 정확히 인지.

부정:
- 즉시 fix 부재 — 동일 시나리오 발생 시 mongodb 도 운영자 manual intervention
  필요. 단 valkey 19h stuck 같은 *침묵 stuck* 은 안 됨 (PrimaryUnreachable 알람).

## References

- valkey INC-0001 / ADR-0039 (post-init self-heal, 2026-05-10).
- ADR-0016 (mongodb): Cross-cut Audit Pattern.
- ADR-0010 (mongodb): RS deliberate scaling — manual operator awareness 전제.
- 후속 트랙: RFC (가칭) "Auto RS reconfig on host change detection".
