# ADR-0040: sharded search 컨트롤면 배선 — mongos → mongot ClusterIP Service

- 상태: Accepted
- 날짜: 2026-07-14
- 관련: ADR-0039 (sharded search mongot fan-out 한계 — 본 ADR 이 #5 를 코드로 해소)

## Context

prod `keiailab-mongo`(5-shard, ns=data)에서 `MongoDBSearchIndex searchval-vec-idx` 가 19일째 `Pending` 고착. 라이브 실측 근본 원인:

1. operator 는 mongot 연동 setParameter 를 **shard mongod 에만** 주입한다(`builder.go` `BuildShardStatefulSet`).
   라이브 shard args 실측:
   ```
   --setParameter mongotHost=localhost:27028
   --setParameter searchIndexManagementHostAndPort=localhost:27028
   --setParameter searchTLSMode=disabled
   --setParameter useGrpcForSearch=true
   ```
2. **mongos 에는 `--setParameter` 가 하나도 없다.** `builder.go` 주석의 *"mongos setParameter 불요"* 가정이 실측과 불일치(ADR-0039 #5 가 이미 지적).
3. `MongoDBSearchIndex` 컨트롤러는 인덱스 관리 명령($listSearchIndexes / createSearchIndex)을 **mongos 경유**로 보낸다(`mongodbsearchindex_controller.go` `NewServiceConnectFactory(<name>-mongos, ...)`). mongos 에 mongot 엔드포인트가 비어 있어 `SearchNotEnabled` 로 거부 → CR Pending 고착.
4. 인증 문제 아님(mongot 로그 unauthorized 0건, mongot 사이드카 15/15 Ready — 데이터면 정상, 컨트롤면만 단절).

라이브 mongos(mongo 8.3.4 Community) `getParameter` 실측 — 4 파라미터 모두 **정식 등록**(값만 공백):

```
{"mongotHost":"","searchIndexManagementHostAndPort":"","searchTLSMode":"globalTLS","useGrpcForSearch":false,"ok":1}
```

mongos pod 에는 mongot 사이드카가 없다(컨테이너 = mongos, exporter) → mongos 는 **pod 밖 엔드포인트**를 통해서만 mongot 에 도달할 수 있다. mongot 컨테이너는 `27028`(mongot-grpc) + `8080`(health)을 containerPort 로 선언하므로 Service 노출이 가능하다.

## Decision

1. **mongot ClusterIP Service 신설** — `BuildMongotService(mdbsh)` → `<cluster>-mongot:27028`(targetPort=`mongot-grpc`).
   - selector: shard pod 는 `app.kubernetes.io/component=shard-N` 으로 shard 마다 달라 equality-only Service selector 로 전 shard 를 묶을 수 없다. 따라서 mongot 사이드카가 주입되는 **shard pod template 에만** 공통 표식 라벨 `search.mongodb.keiailab.com/mongot=true` 를 붙이고 이를 selector 로 쓴다.
   - STS `Selector`(immutable)에는 넣지 않는다 — pod template label 만 추가(기존 STS in-place 갱신 가능).
   - annotation(`search.mongodb.keiailab.com/mongot-image`) 부재 = search 비활성 → Service 미생성(기존 opt-in 규율).
   - owner = MongoDBSearch CR(mongot ConfigMap 과 동일 수명, search 삭제 시 GC).
2. **mongos setParameter 주입** — annotation 활성 시 `BuildMongosDeployment` 가 mongos args 에 4 파라미터를 붙인다(엔드포인트 = mongot Service FQDN). annotation 부재 시 args **byte-identical** → mongos 무롤링(회귀 테스트 `TestShardedMongot_MongosNoRoll` 고정).
3. shard 주입(`localhost:27028`)은 **불변** — 데이터면(각 shard mongod ↔ 자기 사이드카)은 그대로.

## Consequences

- ✅ mongos 가 인덱스 관리 명령을 mongot 으로 전달 가능 → `MongoDBSearchIndex` Pending 고착 해소(컨트롤면 복구).
- ✅ search 비활성 클러스터: mongos/shard template 무변경 = 무롤링.
- ⚠️ search 활성 클러스터: shard pod template 에 표식 라벨이 추가되므로 **shard STS 1회 롤링**(operator 업그레이드 시). RS 멤버 순차 롤링이라 무중단이나 비용 0 은 아니다.
- ⚠️ **미검증 영역(정직 표기)**: ADR-0039 #7 은 dummy 엔드포인트 실측(`errCode:125`)을 근거로 *"mongos 는 단일 mongot 엔드포인트에 직접 연결(broadcast 아님)"* 이라 결론지었다. 그 실측 오류는 **인덱스 관리 경로**(Search Index Management service)의 것이므로, 질의($search/$vectorSearch)가 shard 로 fan-out 되는지 여부까지 확정하지는 못한다. ClusterIP 는 연결마다 임의의 shard mongot 으로 로드밸런싱하므로:
  - 인덱스 관리: 인덱스 정의가 cluster-wide authoritative catalog(`__mdb_internal_search` DB — 라이브 존재 확인)에 기록되면 어느 mongot 이 받아도 전 shard 로 전파된다(**가정 — 배포 후 실측 필요**).
  - 질의: 만약 ADR-0039 #7 대로 mongos 가 단일 mongot 에 직접 질의한다면, 데이터가 다른 shard 에 있을 때 빈 결과가 나올 수 있고 LB 때문에 **비결정적**이 된다. 이 경우 후속으로 shard 별 Service(`<cluster>-mongot-shard-N`) + 검색 DB 의 primary shard 로 pin 하는 방식이 필요하다.
- 배포 후 검증 순서: ① SearchIndex CR `Ready` 도달 ② 전 shard mongot 이 인덱스 정의를 받았는지(카탈로그 전파) ③ `$vectorSearch` 결과가 shard 분포와 무관하게 안정적인지. ②·③ 이 실패하면 ADR-0039 #7 확정 → primary shard pin 설계로 전환.

## 라이브 검증(코드)

- `make test` → 전 패키지 `ok`(exit=0), `internal/resources` coverage 73.0%.
- `make lint` → `0 issues`(exit=0).
- 신규 회귀 가드: `TestShardedMongot_MongosNoRoll` / `TestShardedMongot_MongosSetParameterInjected` / `TestShardedMongot_ShardInjectionUnchanged` / `TestShardedMongot_Service`.

<!-- live-verified: 2026-07-14 (라이브 = 진단·읽기 전용. 배포 후 재검증 필요 — 위 ⚠ 항목) -->
