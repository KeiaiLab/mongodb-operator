# ADR-0039: sharded MongoDB Search multi-shard 검색 = Community mongot 0.69.1 upstream 한계; config server search-sync 비번 drift fix

- 상태: Accepted
- 날짜: 2026-06-24
- 관련: ADR-0038 (OLM v1 Path1), RFC-0070 (OSS GitHub canonical)

## Context

prod `keiailab-mongo`(5-shard sharded cluster, ns=data, OLM ClusterExtension)에서 sharded MongoDB Search(`$vectorSearch`) 활성화를 라이브 디버깅하며 결함 체인을 실측 규명했다:

- **#5** mongos가 search setParameter 없어 `$search`/`$vectorSearch`를 parse 거부(`"Atlas ... requires additional configuration"`). operator `builder.go:1527` 주석 *"mongos 가 $search 를 shard 별 mongot 으로 fan-out(mongos setParameter 불요)"* 가정이 실측과 불일치 — mongos에 4개 setParameter(mongotHost/searchIndexManagementHostAndPort/searchTLSMode/useGrpcForSearch) 주입 시에만 parse 통과.
- **#6** config server의 `search-sync` user 비번이 secret과 drift → mongot의 router(config server) 인증 실패 → index catalog 조회 불가(`"Failed calling authoritative index catalog ... Exception authenticating userName='search-sync'"`). shard-local 비번은 맞아 replicaSet sync(데이터)는 정상이나 config server 비번만 어긋남.
- **#7** mongos에 단일 `mongotHost`를 주면 그 **단일 mongot endpoint로 직접 연결**(broadcast/fan-out 아님). dummy endpoint(`dummy:27028`) 실측 → `errCode:125 "Error connecting to Search Index Management service"`(broadcast였다면 dummy 무시하고 shard로 위임했을 것). 데이터가 다른 shard에 있으면 빈 결과(`VS:[]`).
- **upstream 제약** Community mongot 0.69.1의 `MongodTopologyMonitor`가 syncSource 무관 `localhost:27017` 하드코딩 연결 → mongot=sidecar 강제, 별도 StatefulSet 불가. 공식 `{shardName}` fan-out은 **Enterprise mongot=StatefulSet+LoadBalancer** 모델 전용이라 Community sidecar에 적용 불가.

mongos가 OLM ClusterExtension(MongoDBSharded owner)이라 라이브 패치는 operator reconcile이 즉시 원복(operator scale 0도 OLM이 복원) → 코드 수정만 유효.

## Decision

1. **config server search-sync 비번 drift fix** (v1.16.3): `internal/mongodb/auth.go::EnsureSearchCoordinatorUser`의 precheck `userHasDualSCRAMAndRole`(mechanisms+role만 검사, **비번 미검증**)를 제거 → 매 reconcile `createUser`→(존재 시)`updateUser`로 pwd+mechanisms+roles를 secret 기준 보정(멱등). orphan `userHasDualSCRAMAndRole` 삭제.
2. **multi-shard sharded search는 upstream(Community mongot) 한계로 보류**: mongos 단일 endpoint 직접 라우팅 + mongot localhost 하드코딩으로 operator 구조적 불가(operator 결함 아님). multi-shard 필요 시 Enterprise mongot 전환 / mongot community 버전업(sharded fan-out 지원 시) / search 데이터를 별도 RS 클러스터로 분리.

## Consequences

- ✅ RS 클러스터 search GA 정상(변경 없음).
- ✅ sharded 클러스터의 **단일 primary shard**에 모든 search 데이터가 있는 경우(unsharded collection on sharded cluster) 검색 가능(mongos mongotHost=그 shard).
- ⛔ sharded collection(여러 shard 분산)의 search는 불가(Community mongot 한계).
- config server 비번 drift 재발 방지(precheck 제거로 매 reconcile 동기화 — updateUser oplog write는 search reconcile 30s 빈도상 미미).

## 라이브 검증

- 라이브 수동 `updateUser`(config server search-sync 비번 → secret 값) → `AUTH_OK:1`(인증 복구).
- `go test ./internal/mongodb/` → ok(회귀 0) / `go build ./...` → OK / `golangci-lint` → 0 issues.

<!-- live-verified: 2026-06-24 -->
