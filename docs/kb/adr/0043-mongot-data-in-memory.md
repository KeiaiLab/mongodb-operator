# ADR-0043: mongot data 를 메모리 emptyDir 에 둘 수 있게 한다

- 상태: Accepted
- 날짜: 2026-11-20
- 관련: ADR-0039 · ADR-0040 (sharded search) / `internal/resources/mongot.go`

## Context

mongot data(`/var/lib/mongot`)는 mongod data PVC 의 `search-index` subPath 다. emptyDir 이던 것을
옮긴 이유는 노드 루트 디스크 압박 때 mongot 이 disk-usage pause 로 조용히 멈췄기 때문이다.

라이브 실측(2026-11-20, 5 shard × 3 멤버, ceph-rbd · size 3, mongot 0.69.1):

- shard 멤버의 볼륨 쓰기는 조용한 shard 도 17건/s · 233 KiB/s 였다. mongod 저널 sync 는
  1.1회/s 뿐이다.
- `bpftrace`: 그 볼륨에 들어간 bio 약 1,730건/30s 중 약 1,590건이 `jbd2`(ext4 저널 커밋)이고,
  fsync 는 mongot `config-monitor` 스레드 150회 · mongod `JournalFlusher` 33회였다.
- `strace`: mongot 은 12초에 30번 `configJournal.json.tmp` 를 쓰고 fsync → rename →
  디렉터리 fsync 한다. 파일 내용의 해시는 그동안 같다.
- 격리 재현(docker, 단일 RS): 색인 0개 · 정상 색인 1개 · 컬렉션이 사라진 색인 어느 쪽이든
  초당 약 1회 다시 쓴다. 1.70.6 · 1.79.1 도 같다. 설정 파일에는 주기를 바꾸는 칸이 없다
  (`CommunityConfig` 필드 전수).
- 그 결과 Ceph NVMe OSD 는 장치당 flush 280회/s × 2.1 ms 로 사용률의 58 %p 를 flush 가
  차지했고, 풀 쓰기 건수(약 380–490/s)의 절반가량이 15개 멤버의 mongot 이었다.

data 를 잃었을 때: 색인 정의는 mongod 쪽 카탈로그에 있고 mongot 은 거기서 다시 읽어 initial
sync 한다. 42만 건 · 72차원 벡터 색인이 36초에 READY 로 돌아왔고, 처음 수 초 동안 질의는 빈
결과가 아니라 오류(`cannot query vector index`)였다. 그 뒤 READY 전 약 15초는 질의가 결과를
돌려준다 — 그 구간의 결과가 완전한지는 재지 않았다.

## Decision

annotation `search.mongodb.keiailab.com/mongot-data-memory` 를 더한다. 값이 양의 quantity 면
mongot 컨테이너의 `/var/lib/mongot` 을 `emptyDir{medium: Memory, sizeLimit: <값>}` 로 mount 하고
PVC subPath 는 mount 하지 않는다. 없거나 틀린 값이면 지금과 같다(영속, 무롤링).

- 메모리 매체를 쓴다 — 노드 루트 디스크와 무관해 옮긴 이유(disk-usage pause)가 되살아나지
  않는다. mongot 이 보는 사용률은 자기 data ÷ sizeLimit 이다.
- 기본은 바꾸지 않는다. 로컬 디스크 PVC 에서는 이 fsync 가 문제가 아니고 영속이 낫다.
- 기존 annotation 과 같은 길(source CR 의 annotation → builder)이다. CRD 는 바뀌지 않는다.

## Consequences

- annotation 을 붙이면 pod template 이 바뀌어 mongot 을 가진 멤버가 한 번 롤링한다.
- pod 재시작마다 그 멤버의 색인을 다시 만든다. router shard 는 readiness 가 첫 Ready 를 색인
  queryable 에 걸어 두므로(1.16.16) 롤링이 재구축을 기다린다. 다른 shard 는 기다리지 않는다.
- 색인은 mongot 컨테이너의 메모리로 계산된다. sizeLimit 과 컨테이너 memory limit 을 색인
  크기에 맞춰야 한다. 넘치면 mongot 이 쓰기 실패 또는 OOM 으로 재시작한다.
- PVC 의 `search-index` 디렉터리는 남는다(쓰이지 않는다). 지우는 것은 운영자 몫이다.

## Alternatives Considered

- **mongot 판올림** — 1.70.6 · 1.79.1 도 같은 주기로 다시 쓴다.
- **고아 색인 정리** — 색인이 0개여도 다시 쓴다. 원인이 아니다.
- **디스크 emptyDir** — fsync 는 Ceph 를 떠나지만 노드 디스크 압박 문제가 돌아온다.
- **fsync 무력화(LD_PRELOAD)** — 이미지를 따로 지어야 하고 Lucene 커밋의 내구성도 함께 잃는다.
- **CRD 칸(`spec.storage.medium`)** — 맞는 자리지만 CRD 전달이 따로 필요하다. annotation 으로
  먼저 열고, 칸은 뒤에 annotation 을 채우는 식으로 얹을 수 있다.
