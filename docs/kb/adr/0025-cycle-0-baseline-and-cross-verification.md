# ADR-0025: Cycle 0 — Baseline 검증 + 3-way Cross-Verification + Program 분해

- Date: 2026-05-12
- Status: Accepted
- Authors: @keiailab (사용자 명시 목표 driven)

## Context

사용자 목표 (세션 `/goal`):
> `mongodb-operator` 를 Bitnami `mongodb-sharded` + CloudPirates `mongodb` 두 Helm chart 과 교차검증하고, 기능·품질·운영 안정성을 확보한 뒤 *모든 기능 테스트가 통과할 때까지* 진행한다.

본 ROADMAP (cycle 0 직전 상태):
- v1.4.23 release
- `[x]` 20건 / `[~]` 4건 / `[ ]` 81건 = *완성도 ~23%*
- 두 reference Helm chart 대비 cross-verification 갭: Bitnami 9건 + CloudPirates 추가 3건 = *최소 12 신규 갭 후보*

**조건**:
- 사용자 환경 = public github-only repo. 사설 GitLab/keiailab 라이브 게이트 부적용.
- 단일 세션 토큰 예산 ~500K (T3) 으로는 81+12 항목 모두 처리 불가능.

## Decision

본 cycle 0 은 *baseline + cross-verification matrix + program 분해 SSOT* 확립에 집중하고, 코드 변경은 *F-IMP-04 (DiagnosticMode sharded 확장)* 1건만 surgical 으로 처리한다. 나머지 항목 (F01..F85) 은 **12-cycle program** 으로 분해하여 `HANDOFF.md` + `TASKS.md` + `docs/comparison/three-way-summary.md` 3축 SSOT 로 인계한다.

구체적 분해:

| Cycle | 주제 | 핵심 산출물 |
|---|---|---|
| 0 (본 cycle) | baseline + matrix + F-IMP-04 | three-way-summary.md / cloudpirates-mongodb.md / HANDOFF.md / TASKS.md / ADR-0025 |
| 1 | PITR 완전 구현 | F01-F05 |
| 2 | Grafana dashboard 5종 | F06-F10 |
| 3 | Cluster용 Helm chart | F85 |
| 4 | LDAP/OIDC | F23-F32 |
| 5 | Federation | F33-F37 |
| 6 | KMS encryption | F38-F42 + F61-F65 |
| 7 | Upgrade automation + Insights | F11-F16 + F51-F55 |
| 8 | ClusterGroup | F56-F60 |
| 9 | Scale-in safety | F74 + F43-F50 |
| 10 | Bitnami/CloudPirates parity polish | F66-F79 |
| 11 | Architecture standalone + supply chain | F17-F22 + F80-F84 |
| 12 | Final parity 재검증 + ROADMAP 100% | three-way-summary 재산출 |

## Consequences

### 긍정

1. **명확한 SSOT 3축** — HANDOFF.md (컨텍스트), TASKS.md (작업 목록), three-way-summary.md (Gap → cycle 매핑) 로 cross-session 재개 가능.
2. **Baseline 회귀 가드 확립** — cycle 0 의 `make gate` + `make test` PASS 가 *cycle 1+ 의 회귀 기준*.
3. **Cross-verification 완전성** — 두 reference chart 의 모든 documented feature 가 26 Gap-ID 로 추적됨. cycle 종료 시 Gap-NN 매트릭스 갱신만으로 진척 가시화.
4. **F-IMP-04 cycle 0 처리** — `DiagnosticMode` 가 sharded ConfigServer / Shard / Mongos 까지 확장. ROADMAP L223 `[~]` → `[x]`. cycle 0 의 *진본 코드 1건* 보장 (CLAUDE.md §9 noise anti-pattern 차단 — closure-only commit 금지).

### 부정 / 트레이드오프

1. **단일 세션에 81+ 항목 완료 불가** — 사용자가 "모든 기능 테스트 통과까지" 요구. 12-cycle 분해는 *수개월 단위 program*. 사용자 expectation alignment 의무.
2. **F-IMP-01/02/03 cycle 0 미처리** — 원 plan 의 4건 [~] 모두 cycle 0 처리 의도는 simplicity-first (`standards/principles.md §2`) 위배. F-IMP-04 1건으로 축소.
3. **e2e shell runner stale** — `test/e2e/run-all-tests.sh` 가 reference 하는 `0[1-9]-*.sh` 부재. cycle 1 진입 시 정리 (Go `//go:build e2e` 5 파일이 진정한 e2e SSOT).
4. **keiailab cluster 라이브 게이트 N/A** — public github-only repo 정책 (사용자 명시). RFC-0004 §3 라이브 게이트는 본 program 의 어느 cycle 에도 적용 안 됨.

### 후속 작업 (action items)

- [ ] cycle 1 진입: F01-F05 (PITR 완전 구현) — *다음 세션*
- [ ] cycle 1 종료 시 본 ADR 의 `cycle 진행 상황` 표 갱신
- [ ] cycle 12 종료 시점 본 ADR Status: `Accepted` → `Implemented` 전환 + 회고 ADR (ADR-00NN cycle 0-12 retrospective) 신규

## Alternatives Considered

### A. 단일 세션에 모든 [~] / [ ] 처리 시도 (rejected)

- 장: 사용자 목표 단일 세션 충족 외관
- 단: 토큰 예산 초과 → 컨텍스트 압축 다발 → 품질 저하 + 회귀 위험 폭증
- Rejected: `standards/token-budget.md §1` T3 권장 분해 위반

### B. Cycle 분해 하되 docs 만 작성 (no code change, rejected)

- 장: 가장 안전
- 단: CLAUDE.md §9 noise anti-pattern — *진본 작업 0인 closure 자기 적분*. cycle 0 의 *진본 변경 1건 의무*.
- Rejected

### C. keiailab 클러스터 강제 사용 (rejected)

- 장: 라이브 검증 정합 (RFC-0004 §3)
- 단: 사용자 명시 "public 은 오픈소스 github 만 사용". 사설 GitLab 의존 부정.
- Rejected: 사용자 instruction 우선 (CLAUDE.md §3 충돌 시 우선순위)

### D. 다른 reference chart 추가 (e.g., percona, mongodb-community-operator) (deferred)

- 장: 더 광범위한 parity
- 단: cycle 12 종료 후 program 회고 시점에 평가하는 편이 적절
- Deferred to retrospective ADR

## Refs

- Plan: `/Users/phil/.claude/plans/nifty-snuggling-ocean.md`
- HANDOFF.md (cross-session SSOT)
- TASKS.md (F01..F85 작업 목록)
- docs/comparison/three-way-summary.md (Gap-NN → cycle 매핑)
- docs/comparison/cloudpirates-mongodb.md (CloudPirates 28행 분석)
- docs/comparison/bitnami-mongodb-sharded.md (Bitnami 44행 분석, v1.4.23 reference 갱신)
- 글로벌 표준: `standards/workflow.md §8` (Audit-Driven Evolution), `standards/token-budget.md §1` (T 등급 분해)
- 사용자 instruction: 본 세션 `/goal` (Stop hook session-scoped)
