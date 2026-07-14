# ADR-0040: sharded search 컨트롤면 배선 — mongos → mongot Service (단일 shard pin)

- 상태: Accepted
- 날짜: 2026-07-14
- 관련: ADR-0039 (sharded search mongot fan-out 한계 — 본 ADR 이 #5 를 코드로 해소, **#7 은 그대로 유효**)

## Context

prod `keiailab-mongo`(5-shard, ns=data)에서 `MongoDBSearchIndex searchval-vec-idx` 가 19일째 `Pending` 고착. 라이브 실측 근본 원인:

1. operator 는 mongot 연동 setParameter 를 **shard mongod 에만** 주입한다(`BuildShardStatefulSet`).
   라이브 shard args 실측: `mongotHost=localhost:27028` / `searchIndexManagementHostAndPort=localhost:27028` / `searchTLSMode=disabled` / `useGrpcForSearch=true`.
2. **mongos 에는 `--setParameter` 가 하나도 없다.** `builder.go` 주석의 *"mongos setParameter 불요"* 가정이 실측과 불일치(ADR-0039 #5 가 이미 지적).
3. `MongoDBSearchIndex` 컨트롤러는 인덱스 관리 명령($listSearchIndexes / createSearchIndex)을 **mongos 경유**로 보낸다(`NewServiceConnectFactory(<name>-mongos, ...)`). mongos 에 mongot 엔드포인트가 비어 있어 `SearchNotEnabled` 로 거부 → CR Pending 고착.
4. 인증 문제 아님(mongot 사이드카 15/15 Ready, 데이터면 정상 — 컨트롤면만 단절).

라이브 mongos(mongo 8.3.4 Community) `getParameter` 실측 — 4 파라미터 모두 **정식 등록**(값만 공백):

```
{"mongotHost":"","searchIndexManagementHostAndPort":"","searchTLSMode":"globalTLS","useGrpcForSearch":false,"ok":1}
```

mongos pod 에는 mongot 사이드카가 없다(컨테이너 = mongos, exporter) → mongos 는 **pod 밖 엔드포인트**로만 mongot 에 도달할 수 있다. mongot 컨테이너는 `27028`(mongot-grpc)을 containerPort 로 선언하므로 Service 노출이 가능하다.

**결정적 제약 (ADR-0039 #7 — 실측 확정)**: mongos 에 단일 `mongotHost` 를 주면 그 **엔드포인트로 직접 연결**한다 — broadcast/fan-out 이 아니다(dummy endpoint → `errCode:125`, 데이터가 다른 shard 면 빈 결과 `VS:[]`). 따라서 전 shard mongot 을 묶은 **로드밸런싱 ClusterIP 는 금지**다: 연결마다 임의 shard 의 mongot 으로 라우팅되어 `$search`/`$vectorSearch` 가 **비결정적으로 빈 결과(조용한 오답)** 를 낸다 — 가장 나쁜 실패 모드.

## Decision

1. **mongot ClusterIP Service 신설 — 단일 shard pin** — `BuildMongotService(mdbsh)` → `<cluster>-mongot:27028`(targetPort=`mongot-grpc`).
   - selector = **pin 대상 shard 의 pod 만** (`app.kubernetes.io/component=shard-N` + mongot 표식 라벨 `search.mongodb.keiailab.com/mongot=true`). 전 shard 를 묶지 않는다 → 엔드포인트가 결정적.
   - mongot 표식 라벨은 사이드카가 주입되는 **shard pod template 에만** 부착(mongos/config server pod 제외). STS `Selector`(immutable)에는 넣지 않는다 — pod template label 만 추가.
   - annotation(`.../mongot-image`) 부재 = search 비활성 → Service 미생성(opt-in 규율).
   - owner = MongoDBSearch CR(mongot ConfigMap 과 동일 수명, GC).
2. **shard pin API** — `MongoDBSearch.spec.router.mongotShard`(optional, pattern `^shard-[0-9]+$`, **기본값 `shard-0`**). search controller 가 `search.mongodb.keiailab.com/router-shard` annotation 으로 전달 → Service selector 결정.
   - 기본값을 둔 이유: 미지정 CR 도 최소한 **결정적** 엔드포인트를 갖게 하기 위함(엔드포인트 부재 = 무조건 Pending 고착이므로 "명시 필수"보다 안전). 다만 **검색 대상 컬렉션이 상주하는 shard(= 그 database 의 primary shard)로 명시 지정하는 것이 원칙**이다.
   - CRD 스키마 변경이므로 `config/crd/bases` + `charts/*/crds` + **OLM bundle CRD** 를 함께 갱신했다(prod 는 OLM ClusterExtension 설치 → bundle CRD 가 stale 이면 `spec.router` 가 apiserver 에서 **pruning** 된다). `make manifests && make sync-crds && make bundle VERSION=1.16.5` → `verify-bundle-parity PASS`.
3. **mongos setParameter 주입** — annotation 활성 시 `BuildMongosDeployment` 가 mongos args 에 4 파라미터를 붙인다(엔드포인트 = mongot Service FQDN). annotation 부재 시 args **byte-identical** → mongos 무롤링(`TestShardedMongot_MongosNoRoll` 고정).
4. shard 주입(`localhost:27028`)은 **불변** — 각 shard mongod ↔ 자기 사이드카 경로 그대로.

## Consequences

본 수정의 범위(정직 표기):

- ✅ **(a) SearchIndex CR Pending 고착 해소** — mongos 가 인덱스 관리 명령을 mongot 으로 전달 가능(컨트롤면 복구).
- ✅ **(b) unsharded(단일 shard 상주) 컬렉션에 한한 `$search`/`$vectorSearch` 동작** — pin 한 shard 가 그 컬렉션의 primary shard 일 때만. 첫 소비자(케플 memory atom 벡터 회상)는 샤딩할 이유가 없는 작은 컬렉션이라 이 조건을 만족한다.
- ⛔ **(c) multi-shard(여러 shard 분산) 컬렉션 검색은 여전히 미해결** — Community mongot 0.69.1 upstream 한계로 **보류 유지**(ADR-0039 Decision #2 불변). mongos 단일 endpoint 직결 + mongot `localhost` 하드코딩 구조상 operator 로 해결 불가. 필요 시 Enterprise mongot 전환 / mongot 버전업(sharded fan-out 지원 시) / search 데이터를 별도 RS 클러스터로 분리. **본 ADR 은 이를 "해결"하지 않는다.**
- ⚠️ **오지정 시 조용한 오답** — `mongotShard` 를 데이터가 없는 shard 로 지정하면 인덱스 관리는 되지만 질의는 빈 결과. primary shard 확인: `mongosh --eval 'db.getSiblingDB("config").databases.find({},{_id:1,primary:1})'`.
- ⚠️ search 활성 클러스터: shard pod template 에 표식 라벨이 추가되므로 **shard STS 1회 롤링**(operator 업그레이드 시). RS 멤버 순차 롤링이라 무중단이나 비용 0 은 아니다.
- ✅ search 비활성 클러스터: mongos/shard template 무변경 = 무롤링.
- 미검증(배포 후 실측 필요): 인덱스 정의가 cluster-wide authoritative catalog(`__mdb_internal_search` — 라이브 존재 확인)를 통해 다른 shard mongot 으로도 전파되는지. 전파 여부와 무관하게 (a)·(b) 는 성립하고 (c) 판단은 불변.

## Alternatives Considered

- **전 shard mongot 을 묶은 ClusterIP LB** — **기각**. ADR-0039 #7 실측상 mongos 는 단일 엔드포인트 직결이라 연결마다 임의 shard 로 라우팅 → 비결정적 빈 결과(조용한 오답). 최악의 실패 모드.
- **primary shard 자동 해석**(operator 가 `config.databases` 조회 후 자동 pin) — 보류. MongoDBSearch 는 대상 database 를 모르고(그 정보는 MongoDBSearchIndex 소유), 검색 대상 DB 가 여럿이면 primary shard 가 갈려 단일 pin 이 성립하지 않는다(= (c) 한계와 동형). 명시 pin 이 단순·결정적.
- **mongot 을 별도 StatefulSet + LB 로 분리**(Enterprise 모델) — Community mongot 의 `MongodTopologyMonitor` 가 `localhost:27017` 하드코딩이라 불가(ADR-0039).

## 라이브 검증(코드)

- `make test` → 전 패키지 `ok`(exit=0).
- `make lint` → `0 issues`(exit=0).
- `make verify-bundle-parity` → `PASS`.
- 회귀 가드: `TestShardedMongot_MongosNoRoll` / `_MongosSetParameterInjected` / `_ShardInjectionUnchanged` / `_Service`(pin 된 shard 만 선택) / `_ServiceRouterShardPin`(pin 변경 시 다른 shard 미선택).

<!-- live-verified: 2026-07-14 (라이브 = 진단·읽기 전용. 배포 후 재검증 필요 — 위 ⚠ 항목) -->
