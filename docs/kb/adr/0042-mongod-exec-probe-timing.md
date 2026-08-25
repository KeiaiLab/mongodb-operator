# ADR-0042: mongod exec 프로브 시간축 — 노드 exec 지연을 DB 장애로 오독하지 않는다

- 상태: Accepted
- 날짜: 2026-08-25
- 관련: ADR-0039 (mongot fanout) / 본 repo `internal/resources/builder.go` (프로브 정의 SSOT)

## Context

라이브 클러스터에서 mongod 파드가 만성적으로 재시작했다. 실측(2026-08-25):

```
POD                       CONTAINER  reason  exit  RESTARTS
keiailab-mongo-shard-4-1  mongodb    Error   137   148
keiailab-mongo-shard-0-2  mongodb    Error   137   147
```

`exit 137` 이지만 `reason` 은 `OOMKilled` 가 **아니라** `Error` 다 — cgroup OOM 킬러가 아니라
바깥에서 SIGKILL 이 왔다는 뜻이다. 이벤트가 출처를 지목했다:

```
Killing    Container mongodb failed liveness probe, will be restarted
Unhealthy  Liveness probe failed: command timed out:
           "mongosh --quiet --port 27018 --eval db.adminCommand('ping')" timed out after 5s
```

**mongod 는 멀쩡했다.** 멈춘 것은 프로브였다. 판별에 쓴 3개 사실:

1. **exec 프로브만 실패한다.** 문제 노드 e22 의 파드 70개 중 Unhealthy 를 낸 것은 5건뿐이고
   전부 무거운 바이너리를 띄우는 exec 프로브였다(mongosh = Node.js 런타임, `bao` = Go).
   httpGet·tcpSocket 프로브를 쓰는 나머지 65개는 **0건**.
2. **한 노드에 몰려 있다.** 클러스터 전체 exec 타임아웃 10,007건 중 **9,964건(99.6%)이 e22**.
   같은 프로브가 나머지 8개 노드에서는 멀쩡하다.
3. **CPU 스로틀링이 아니다.** 해당 컨테이너의 cgroup `cpu.stat` 스로틀 비율은 1.0~1.2%.
   평상시 프로브 왕복은 ~0.6s 로, 5s 타임아웃 대비 8배 여유가 있다.

즉 원인은 mongod 도 컨테이너 자원도 아니고, **노드의 컨테이너 exec 경로가 간헐적으로 막히는
것**이다(e22 는 RL GPU 워커와 rook-ceph OSD 를 함께 이고 있다). 그런데 프로브 창이 좁아서
그 스파이크가 곧바로 "DB 사망" 판정이 됐다.

데이터베이스 재시작은 싸지 않다 — 선거, 초기 동기화 위험, mongos 커넥션 순단이 따라온다.
스테이트풀 워크로드의 liveness 는 **확실할 때만** 발화해야 한다.

같은 파일의 mongos 는 이미 이 교훈을 반영하고 있었다: liveness 는 TCP, readiness 는
`--norc` + 타임아웃 10s. cfg 와 shard 만 구 설정에 남아 있었다.

## Decision

mongod(ReplicaSet / configServer / shard) 프로브 시간축을 `internal/resources/builder.go` 의
명명 상수 하나로 모으고 창을 넓힌다. liveness 는 exec 을 **유지**한다 — hang 판정에는 실제
ping 이 필요하고, TCP 는 멈춘 mongod 도 받아주기 때문이다.

| 축 | 구 | 신 | 판정까지 |
|---|---|---|---|
| liveness period / timeout / failures | 10s / 5s / 6 | **30s / 15s / 4** | 60s → **120s** |
| readiness period / timeout | 10s / 5s | **15s / 10s** | 30s → **45s** |

mongosh 를 부르는 모든 프로브에 `--norc` 를 붙인다(rc 파일 로딩 생략 — mongos 가 이미 그렇다).

회귀 가드 = `internal/resources/builder_probe_timing_test.go`. 창을 다시 좁히면 실패한다.

## Consequences

- 진짜 hang 을 감지해 재시작하기까지 60s → 120s 로 늦어진다. 스테이트풀 DB 에서 이 방향의
  오차(늦게 죽임)는 반대 방향(건강한 primary 를 죽임)보다 싸다.
- 엔드포인트 제외도 30s → 45s 로 늦어진다. mongos 는 자체 서버 모니터링을 하므로 k8s
  readiness 는 DNS·엔드포인트 갱신 축에만 관여한다.
- 노드 exec 경로 지연 자체는 **남는다**. 본 ADR 은 그 지연을 DB 장애로 오독하지 않게 할 뿐이다.
  노드 축 수리는 별건이다.

## Alternatives Considered

- **liveness 를 tcpSocket 으로 교체** — 가장 싸지만 hang 감지를 잃는다. 멈춘 mongod 도 TCP
  는 accept 한다. mongos(무상태 라우터)엔 맞지만 mongod 엔 맞지 않는다.
- **타임아웃만 올리고 주기는 유지** — 프로브 실행이 주기보다 길어질 수 있어 중첩이 생긴다.
  주기를 함께 늘려 겹침을 없앴다.
- **mongosh 대신 경량 헬스체크 바이너리 탑재** — `mongo:8.3.x` 이미지에 그런 바이너리가 없다.
  별도 이미지를 만드는 비용이 이득보다 크다.
- **문제 노드에서 mongod 를 쫓아냄(anti-affinity)** — 증상 회피일 뿐이고, 어느 노드든 부하가
  몰리면 재발한다. 프로브가 부하에 견디는 것이 옳다.
