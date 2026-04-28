# ADR-0003: MongoDBSharded Arbiter/Hidden 토폴로지 후속 사이클 이연

- Date: 2026-04-28
- Status: Accepted (defer until next cycle)
- Authors: @phil

## Context

Bitnami `mongodb-sharded` 9.4.12 동등성 분석(`docs/comparison/bitnami-mongodb-
sharded.md`) 매트릭스 #1에서 식별된 P0 항목: MongoDBSharded의 cfg server 및
shard replica set에 *Arbiter*(저비용 quorum 멤버) 및 *Hidden*(분석/백업 격리
용) 멤버 토폴로지를 지원하지 않는다.

현 상태:

- `MongoDB` CRD(ReplicaSet 모델)에는 `spec.arbiter.{enabled,resources}` 필드가
  *이미 존재* (api/v1alpha1/mongodb_types.go:82-103). 단, builder.go의
  StatefulSet 생성 로직이 arbiter 멤버를 *별도 STS*로 분리해 구성하는지 또는
  단일 STS 내 마지막 멤버에 arbiterOnly 플래그를 부여하는지 추가 검증 필요.
- `MongoDBSharded` CRD(Sharded 모델)에는 Arbiter/Hidden 필드가 *부재*.

본 사이클(production-readiness)에서 식별된 우선순위:

1. P0 silent failure 봉쇄: createOrUpdate spec drift, isAuthRequiredErr string
   match, BootstrapAdminUser race.
2. P1 토폴로지 결손: NetworkPolicy 자동화, Sharded scale-in, 워크로드 PDB.
3. P2 회귀 봉쇄: envtest 활성화 + condition 누적 차단.

본 사이클의 토큰/리뷰 예산상 *모든 P0+P1*을 단일 PR로 봉쇄하면서 Sharded
Arbiter/Hidden까지 *완전 구현*하면 builder.go의 STS 생성 로직 분기(별도 STS
또는 멤버 type 옵션) 변경 + replicaset.go의 BuildShardReplicaSetConfig 확장 +
controller의 부분 STS reconcile 흐름 변경이 필요해 PR이 2K+ LOC로 폭주.

## Decision

본 사이클(2026-04-28)에서는 **MongoDBSharded Arbiter/Hidden 토폴로지를 미구현
한다**. 단:

- `MongoDB` ReplicaSet의 Arbiter 지원은 *현 상태 유지* (CRD 필드 존재). builder
  레벨 검증은 후속 사이클에서.
- README의 "Limitations"에 "Sharded Arbiter/Hidden topology — ⚠️ ReplicaSet
  only" 항목을 명시 추가 → 사용자 기대 관리.
- ROADMAP.md Phase 4.2 항목을 "⚠️ 부분 (ReplicaSet만)"으로 마킹.
- 후속 사이클 작업 범위 정의:
  - `MongoDBShardedSpec.ConfigServer.Arbiter *ArbiterSpec` 필드 추가.
  - `MongoDBShardedSpec.Shards.Arbiter *ArbiterSpec` 필드 추가.
  - `HiddenSpec{Replicas, Priority *float64, Tags map[string]string}` 신규 타입
    + cfg/shards에 `Hidden *HiddenSpec` 필드.
  - builder.go의 BuildConfigServerStatefulSet / BuildShardStatefulSet에서
    arbiter/hidden 멤버를 위한 별도 STS(`{name}-cfg-arbiter`, `{name}-shard-N-
    arbiter`) 생성 분기 또는 단일 STS 내 멤버 type 옵션.
  - replicaset.go의 BuildReplicaSetConfig에 `arbiterCount`/`hiddenCount` 옵션
    파라미터 + RS init 시 멤버별 type 매핑.
  - mongodbsharded_controller.go의 reconcileConfigServer / reconcileShard 흐름
    에 arbiter STS reconcile 단계 추가.

## Consequences

긍정:

- 본 사이클이 단일 PR 한계(머지 가능성, 리뷰 부담) 안에서 *집중*적으로 P0
  silent failure를 봉쇄하고 회귀 봉쇄망을 가동시키는 데 성공.
- 후속 사이클의 작업 범위가 명확해져 기간/리소스 추정 가능 (ROADMAP 추정 3-4주).
- 본 사이클의 변경(controllerutil.CreateOrUpdate, applyXxx helper)이 Sharded
  Arbiter 추가 시 *그대로 재사용 가능* — 후속 PR이 패턴 일관성으로 안전.

부정:

- Bitnami `mongodb-sharded` 사용자가 본 Operator로 1:1 전환할 때 매트릭스 #1
  갭이 *남아 있음* — 비용 최적화(Arbiter) 및 분석 격리(Hidden) 시나리오는 직접
  수동 STS 추가 필요. README에 명시.
- `MongoDB` ReplicaSet의 Arbiter는 spec에는 있지만 *실제 동작 검증* 미수행 —
  후속 사이클에서 envtest 또는 testcontainers로 검증 필요.

후속 트리거:

- Bitnami chart 사용자로부터 마이그레이션 관련 issue 보고 시.
- 또는 Phase 4 우선순위 재조정 시점(2027 Q1 예정).

## Alternatives Considered

1. **본 사이클에서 Arbiter/Hidden까지 완전 구현**: 단일 PR이 2K+ LOC가 되어
   리뷰 가능성 ↓, git bisect 추적 어려워짐. P0 봉쇄와 신규 토폴로지 추가가
   분리되지 않아 회귀 추적 비용 ↑.

2. **Arbiter/Hidden CRD 필드만 추가, builder는 미구현**: scaffold만 두고 실제
   동작은 후속. CRD validation 통과는 되지만 사용자가 spec을 채워도 무시되는
   *기만적인* 상태가 됨. README와 코드의 정합성이 깨짐.

3. **Sharded Arbiter만 본 사이클, Hidden은 후속**: Arbiter 단독 구현도 builder
   변경 범위가 충분히 크고(별도 STS 또는 멤버 분기 모두 동일 비용), 사용자
   가치도 두 기능 모두 갖춰져야 의미가 큼. 분할이 인위적.
