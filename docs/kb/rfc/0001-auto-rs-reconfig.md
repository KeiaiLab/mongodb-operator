# RFC-0001: Auto RS reconfig on host change detection

- Status: Draft
- Date: 2026-05-10
- Authors: @keiailab
- Related: ADR-0024 (INC-0001 cross-cut audit Phase 2 후속)

## Summary

mongodb-operator 의 `MongoDB` reconciler 가 ReplicaSet member host 변경 (pod 재시작 →
새 IP 또는 DNS 갱신 지연) 자동 감지 + `rs.reconfig()` 호출 추가. 운영자 manual
intervention 제거.

## Motivation

valkey-operator INC-0001 (2026-05-09 19h cluster fail) 의 root cause `ClusterInitialized=
true` once-shot pattern. ADR-0024 (mongodb cross-cut audit) 에서 mongodb-operator 의
유사 패턴 `!ReplicaSetInitialized` 분기 (mongodb_controller.go:165) + `hasPrimary` 부분
mitigation 만 보유 — RS member host *변경* 시 자동 reconfig 부재.

운영 영향:
- Pod 재시작 + headless service DNS 갱신 지연 시 hasPrimary fail → PrimaryUnreachable
  condition 발현 + retry. 운영자 알람.
- 운영자가 manual `rs.reconfig()` 호출하여 member hosts 갱신.
- *milder* 형태이지만 운영자 awareness 부담.

## Design

### 트리거 조건

```
ReplicaSetInitialized=true
  AND hasPrimary fail (3 consecutive reconcile)
  AND pod IPs 의 *최소 1 개 변경 감지*
```

### Reconfig 시퀀스

1. *running* primary 찾기 — anonRSManager (pre-bootstrap) 또는 authedRSManager.
2. `rs.config()` 으로 현재 RS config 추출.
3. 각 member host 의 IP 검증 — pod IP 와 비교.
4. drift 감지된 host 들의 새 IP 로 `rs.reconfig()` 시도.
5. status condition `RSReconfigInProgress` / `RSReconfigSuccess` / `RSReconfigFailed` 발현.

### Idempotency

`rs.reconfig` 멱등 — 같은 config 호출 시 no-op. 무한 호출 방지 위해 *backoff
+ status condition lastTransitionTime* 검증.

### 안전 가드

- Primary 1 차례 fail-over 가능 — reconfig 시 election 잠시 진행. application
  downtime ~1s.
- *Replica majority* 가용해야 reconfig 성공. minority 만 가용 시 skip + 알람.

## Implementation Plan

### Phase 1: Detection (별 PR)

`mongodb_controller.go` 의 `hasPrimary` fail 분기 확장:

```go
if hasPrimary == false && consecutive_fail >= 3 {
    if r.detectIPDrift(ctx, mdb) {
        // RFC-0001: trigger auto reconfig
        return r.reconcileRSReconfig(ctx, mdb)
    }
}
```

`detectIPDrift`: 모든 RS member 의 `rs.status()` 응답에서 host:port 추출 → pod IP 와
비교 → drift 감지.

### Phase 2: Reconfig (별 PR)

`reconcileRSReconfig`:
1. authedRSManager.GetConfig
2. UpdateMembersHosts (drift 만)
3. rs.reconfig
4. status condition update

### Phase 3: e2e regression test

```go
// test/e2e/rs_reconfig_test.go
//   1. Bootstrap MongoDB (3 member RS)
//   2. Pod IP 변경 시뮬레이션 (delete + recreate)
//   3. hasPrimary 3 consecutive fail
//   4. controller auto reconfig trigger
//   5. rs.config 의 hosts 갱신 확인
//   6. application 연결 회복 검증
```

## Alternatives

1. **Headless service 의 publishNotReadyAddresses=true** — DNS 가 not-ready pod 도
   advertise. 단 *bootstrap 단계* 에서는 위험 (split-brain 가능). 거절.
2. **Sidecar mongo-side init**: PodPostStart hook 으로 self-reconfig. 거절 — 분산
   coordination 어려움 (어느 pod 가 trigger?).
3. **valkey ADR-0039 패턴 그대로** (cluster_state != ok 시 재호출): mongodb 는
   *cluster info* 가 아닌 *RS topology query* 라 패턴 차이. 본 RFC 가 적절.

## Open Questions

1. `consecutive_fail` 임계값 — 3? 5?
2. reconfig 타이밍 — primary fail-over 1s downtime 허용 가능?
3. minority majority detection — 어떻게 알람?

## References

- ADR-0024: INC-0001 cross-cut audit (mongodb).
- valkey ADR-0039: post-init self-heal pattern.
- INC-0001 (valkey): 운영 19h fail 사례.
- MongoDB docs: <https://www.mongodb.com/docs/manual/reference/command/replSetReconfig/>.
