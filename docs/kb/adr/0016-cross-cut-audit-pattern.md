# ADR-0016: Cross-cut Audit Pattern — invariant 도입 시 3 operator 동시 점검 의무화

- Date: 2026-05-07
- Status: Accepted
- Authors: @claude (it46)
- Refs: it46 commit `e6a238b` (mongodb TLS/Backup invariant) + `1d83880` (valkey cross-cut fix)

## Context

iteration 46 에서 mongodb-operator webhook 에 *omitempty trap* 가드 invariant
4 건 추가 (TLS issuerRef.name / customCert.secretName / s3.bucket /
s3.credentialsRef.name). 추가 후 *grep cross-cut audit* 결과 동일 trap 이
valkey-operator 의 `valkey_webhook.go` + `valkeycluster_webhook.go` 양쪽에 잠재
함이 발견되어 별도 commit (`1d83880`) 으로 fix.

본 사례는 *3 operator 의 공통 spec 영역* (TLSSpec / CertManagerSpec / AuthSpec /
LocalObjectReference 등) 에서 *single-operator 발견 → cross-cut transferable* 한
패턴이 일반적임을 입증.

### 배경 사실

- mongodb / valkey / postgres 가 동일 `operator-commons` 의 webhook /
  security / labels 패키지를 공유 (Plan Phase 0 의 결과).
- 3 operator 의 `api/v1alpha1/common_types.go` 가 거의 동일한 TLSSpec /
  CertManagerSpec / CertIssuerRef / CustomCertSpec 정의 (valkey ↔ mongodb 는
  field 명까지 동일).
- single-operator audit 만으로는 cross-operator 잠재 함정 미발견 — 본 사례는
  운 좋게 이전 cycle 의 `★ Insight` 메모로 파악.

### 문제

새 invariant 추가 시 *체계적 cross-cut 검토* 가 부재하면:

- 동일 trap 이 다른 operator 에서 silently 잠재 → 운영 incident 시점에 발견.
- "fix 1 operator at a time" 패턴은 *시간 차이* 동안 다른 operator 사용자 노출.
- knowledge transfer 가 *우연* (cycle-by-cycle insight) 에 의존 — 자동화 없음.

## Decision

**새 webhook invariant 또는 validation rule 도입 시 다음 cross-cut audit
체크리스트 의무 수행.**

### Cross-cut audit 체크리스트

1. **Spec 구조 grep**: 추가하는 invariant 의 대상 type (e.g. `TLSSpec`,
   `LocalObjectReference`, `CertIssuerRef`) 이 다른 operator 의 `api/v1alpha1/`
   에 존재하는지:
   ```bash
   grep -rn "TYPE_NAME\b" /path/to/{mongodb,valkey,postgres}-operator/api/
   ```

2. **Invariant 가드 위치 grep**: 다른 operator 의 webhook 이 동일 trap 을
   가드하는지:
   ```bash
   grep -rn "FIELD_NAME != \"\"" /path/to/*/internal/webhook/
   ```

3. **3 operator 비교 표**: PR 본문에 다음 형식으로 결과 명시:
   | operator | trap 존재 여부 | 가드 상태 | action |
   |---|---|---|---|
   | mongodb | ✓ | ✓ (본 PR) | — |
   | valkey | ✓ | ✗ | follow-up commit `XXX` |
   | postgres | N/A | N/A | 해당 영역 미구현, 도입 시 재audit |

4. **N/A 영역 명시**: 다른 operator 가 해당 영역 미구현이면 *future
   invariant 작성 계약* 을 ADR 또는 plan 에 기록.

5. **Docs accuracy audit (Errata, it cluster-ops cycle 발견)**: 코드/ADR
   변경 시 *user-facing docs* 가 *동일 사실 진술* 인지 검증. mongodb-operator
   의 `docs/advanced/monitoring.md` 가 commit `edbb35b` 이전 *"operator
   automatically creates ServiceMonitor"* false claim 보유 — controller 실제
   미구현 (I16 orphan). 이는 *docs ↔ 코드 drift* 의 사례.
   - **검증 명령**:
     ```bash
     # 변경 영역의 docs grep — 잘못된 "automatic" / "controller-managed"
     # 진술이 실제 controller 코드에 대응하는지.
     grep -nrE "automatically (create|manage|reconcile)" docs/
     ```
   - **MUST**: 코드 동작 변경 (controller add/remove, spec deprecation,
     invariant 추가) 시 docs 동시 정정. 별 PR 분리 가능지만 같은 cycle 내.
   - **검증**: ADR 의 "후속 작업" 에 `docs/<path>.md 정정` 명시.

- **MUST**: webhook validating invariant, security 표면 변경, 데이터 영역
  검증, struct value omitempty trap 류.
- **SHOULD**: 일반 controller 로직 변경, 새 controller 추가.
- **MAY**: 단일 operator 고유 기능 (e.g. mongodb 의 sharded-only 영역, valkey
  의 cluster slot migration).

### 자동화 (P2 measurable)

향후 `scripts/governance-report` (글로벌 standards) 에 *cross-cut audit
ratio* 메트릭 추가:

- 기준: 마지막 30일 commit 중 webhook/security 변경 commit 수.
- 분자: cross-cut audit 표 (3 operator 비교) 가 PR 본문 또는 commit body 에
  포함된 commit 수.
- 임계값 70% 미만 시 governance-report 적색.

## Consequences

### 긍정

- Invariant gap 의 cross-operator 잠재 시간 *0* 보장.
- ADR 작성 시점부터 *체계적 검토 강제* — "운 좋게 발견" 의존 제거.
- operator-commons 으로 helper 승격 candidate *명시화* (3 operator 가 동일
  audit 필요한 영역 = commons 후보).

### 부정

- PR 작성 비용 ↑ (cross-cut grep + 표 작성).
- N/A 영역 표시가 cross-operator parity 부담을 *상시 visible* 하게 만들어
  사용자가 의도적 미구현 영역을 *결함* 으로 오인 가능 (해결: N/A 사유 명시).

### 트레이드오프

PR 작성 비용 ↑ < incident 1건 비용. 운영 사고 대비 cheap.

### 후속 작업

- AGENTS.md / CLAUDE.md 의 webhook 관련 섹션에 본 ADR 링크 추가 (별 commit).
- operator-commons v0.5.0 release 시 *cross-cut audit-eligible helper* (e.g.
  `webhook.ValidateNonEmptyRef`, `webhook.ValidateTLSSpec`) 식별 + 승격.

## Alternatives Considered

### A. cross-cut audit 를 운 (cycle insight) 에 맡김

본 cycle 처럼 *발견되면 fix*. 체계화 없음.

**거절 사유**: insight 메모가 cycle compaction 에서 손실되거나 다음 작업자가
다른 시각이면 audit 누락. *우연 의존* 은 정의상 governance 아님.

### B. cross-cut helper 를 commons 에 *항상* 승격

3 operator 가 한 번이라도 비슷한 코드를 쓰면 즉시 commons 으로 이동.

**거절 사유**: §2 Simplicity First 위반. 3-of-3 사용 명확하기 전 commons
승격은 *premature abstraction* — 첫 사용 operator 만 적합한 design 일 수
있음 (예: postgres 의 단일 validate 함수가 mongodb 의 multi-CR 와 다른 구조).
*audit 가 먼저, 승격은 별 cycle*.

### C. e2e 시나리오로 cross-cut 가드

각 operator 에 동일 시나리오의 e2e 추가. webhook 호출 거부 검증.

**거절 사유**: e2e cost 큼 (kind cluster + 시나리오 5건/operator * 3 operator
= 15 시나리오). *unit-level cross-cut audit* 가 90% 가치를 cheap 하게 제공.
e2e 는 critical path 만.
