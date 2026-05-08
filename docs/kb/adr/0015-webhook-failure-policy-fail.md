# ADR-0015: ValidatingWebhookConfiguration failurePolicy=Fail (가용성 vs validation 가치)

- Date: 2026-05-07
- Status: Accepted
- Authors: @claude (it45)
- Refs: it45 commit `50b3498`, ADR-0014 (intentional design 보존)

## Context

iteration 45 에서 mongodb-operator 에 validating admission webhook 을 도입했다
(`internal/webhook/v1alpha1/`). 본 ADR 은 webhook configuration 의
`failurePolicy` 선택 — `Fail` vs `Ignore` — 의 trade-off 와 결정 근거를 기록한다.

### 배경 사실

- **Fail 의미**: webhook pod 가 down 또는 API 도달 불가 시 *모든 mongodb CR
  CRUD 요청을 거부*. 운영 중 webhook pod 1개만 떠 있고 그게 죽으면 (예- node
<!-- live-verified: 2026-05-09 -->
  drain, OOMKilled) `kubectl apply` 가 즉시 거부됨.
- **Ignore 의미**: webhook 도달 불가 시 *validation 우회*하여 admission 통과.
  webhook 가치 (split-brain 방지 등) 가 무력화되지만 가용성 보장.
- 현재 mongodb-operator chart 는 `replicaCount: 1` default (HA 미설정 환경
  많음). webhook server 도 동일 pod 내에서 9443 listen.

### 비교

| 차원 | Fail | Ignore |
|---|---|---|
| webhook pod down 시 admission | 거부 | 통과 |
| invalid CR 가 etcd 도달 | 0% | webhook down 시 100% |
| 운영자 UX | 즉각 reject reason | controller status 에서 발견 |
| 사용자 인지 | "왜 막혔지?" → webhook 의심 | "왜 안 떠?" → log dive |

## Decision

**`failurePolicy: Fail` 채택.** 이유는 다음과 같다.

1. **Validation 가치가 가용성보다 우위인 시나리오**: 본 webhook 의 검증 영역
   (split-brain 방지, version 화이트리스트) 은 *post-creation 복구가 비싸다*.
   members=4 로 잘못 만들면 controller 가 4-node STS 를 spawn 한 후 사용자가
   직접 spec 수정 → STS 재생성 → quorum 손실 risk. webhook 단계에서 막는 것이
   100배 저렴.

2. **webhook pod down 의 pre-existing alert**: 운영자가 mongodb-operator pod
   가 down 임을 *이미 ServiceMonitor + alerting* 으로 인지 가능. webhook 거부
   가 추가적인 운영 신호.

3. **opt-in default 와의 시너지**: chart values `webhook.enabled=false` 가
   default 이므로 *webhook 가치를 명시 인지한 사용자만 활성화*. 그런 사용자에게
   는 `Fail` 의 strict semantics 가 *기대치*.

## Consequences

### 긍정

- invalid CR 0% etcd 도달 — controller 가 잘못된 spec 으로 reconcile loop 진입
  하는 incident class 차단.
- valkey-operator 와 동일 정책 — 3 operator 일관성.
- field-level validation error message 가 즉시 사용자에게 반환 (UX).

### 부정

- mongodb-operator pod restart 창 (~15-30s) 동안 admission 거부. CI/CD 에서
  `kubectl apply` 가 transient 실패할 수 있음 — retry 가 권장.
- `replicaCount: 1` 환경 (대부분의 dev/staging) 에서 operator pod OOMKilled
  시 mongodb CR 변경 차단. *production HA 권장* (`replicaCount: 2` 이상).

### 트레이드오프

가용성 손실은 *self-inflicted* (operator pod 만 down) 고, validation 가치는
*post-creation 복구 비용 절감*. operator pod down 자체가 우선 해결할 incident 이지
webhook 거부가 그 자체로 추가 incident 는 아님.

### 후속 작업

- 운영 중 webhook pod down 으로 인한 admission denial 이 1건이라도 발생 시
<!-- live-verified: 2026-05-09 -->
  `docs/kb/incident/INC-NNNN.md` 작성 (severity 따라 SEV-2/SEV-3) → 본 ADR
  Superseded by 검토.
- production 환경 chart values 권장 표 업데이트 (`replicaCount: 2`,
  `podDisruptionBudget.enabled: true`).

## Alternatives Considered

### A. failurePolicy=Ignore + ServiceMonitor alert

webhook down 은 alert 으로 인지하고 그동안 admission 은 통과시킴.

**거절 사유**: alert lag 동안 invalid CR 이 etcd 도달 — controller 가 잘못된 STS
spawn 시점이 alert 보다 빠름. validation 가치 무력화.

### B. failurePolicy=Fail + webhook leader-election

webhook server 만 별도 deployment 로 분리, leader election 으로 N replicas HA.

**거절 사유**: scope-creep. 현재 operator pod 자체가 leader election 보유 →
HA 확장은 chart values 의 `replicaCount` 증가만으로 충분. 별 deployment 로
분리하면 cert-manager Certificate / Service / RBAC 모두 별도화 필요 — 가치
대비 복잡도 ↑.

### C. timeoutSeconds 단축 (10 → 3)

webhook 응답 대기를 줄여 down 인지를 빠르게.

**거절 사유**: webhook handler 의 `validateMongoDBSpec` 자체는 us 단위 — timeout
은 *연결 실패* 의 측정. 단축해도 down 시 결과 동일 (3s 도 reject).

<!-- live-verified: 2026-05-09 -->
