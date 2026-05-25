# ADR-0011: Hook 도구로 pre-commit 채택 (글로벌 lefthook 표준 분기)

- Date: 2026-05-06
- Status: Accepted
- Authors: @keiailab

## Context

글로벌 표준 `~/Documents/ai-dev/standards/enforcement.md §1.1` 은 git hook 관리 도구로 **lefthook** (Go 단일 바이너리, 언어 중립) 을 명시한다. 본 repo 는 `.pre-commit-config.yaml` 기반의 **pre-commit** (Python 기반) 을 운영 중이며, 표준과 분기한 상태로 `mongodb-operator` / `postgresql-operator` 두 repo 가 동일 패턴을 사용하고 있다 (3-repo 중 sister operator 만 lefthook).
<!-- live-verified: 2026-05-09 -->

## Decision

본 repo 는 **pre-commit 을 유지**한다. 마이그레이션 일정 미정 (트리거 조건은 Consequences 절 참조).

## Rationale

1. **pre-commit 은 GitHub-recognized 표준 hook 매니저** — 광범위한 hook 생태계 (trailing-whitespace, end-of-file-fixer, check-merge-conflict 등 built-in) 를 지원하며 본 repo 는 이미 안정 운영.
2. **기능 동일** — 둘 다 pre-commit / pre-push hook 을 등록하고 실패 시 차단하는 동일 메커니즘. RFC 0002 의 4 계층 게이트 (`standards/ci.md §1`) 매핑이 둘 모두 가능.
3. **마이그레이션 비용 vs 가치 낮음** — `.pre-commit-config.yaml` 의 모든 hook 이 *현재* 통과 중이며 lefthook 마이그레이션은 hook 재작성 + 검증 비용 발생 (회귀 위험 존재). lefthook 의 강점 (Go 단일 바이너리, 언어 중립) 은 본 *Go 프로젝트* 에서 결정적 이점이 아님.
4. **3-repo 분기 단점**: sister operator 만 lefthook → 신규 기여자가 양쪽 도구를 모두 알아야 함. 본 ADR 이 해당 분기를 *명시 추적* 하여 onboarding 문서 부담을 감소.

## Consequences

### 긍정
- 기존 hook 인프라 재사용 — 회귀 위험 0.
- pre-commit 의 autofix_prs CI 통합 활용 가능.

### 부정
- 글로벌 표준 (`enforcement.md §1.1`) 과 분기 — `governance-report` 의 P0 정합 컬럼에서 분기 표시 가능.
- sister operator 와 도구 일관성 부족.

### 마이그레이션 트리거 (lefthook 으로 전환 시점)

다음 중 하나 발생 시 본 ADR 을 *Superseded* 로 변경하고 lefthook 마이그레이션:

1. sister operator 의 lefthook 운영이 6개월 이상 안정적이고 *명백한 우위* 발견.
2. pre-commit 자체에 보안 이슈 / 유지보수 중단 시그널.
3. 글로벌 RFC 가 lefthook 강제 적용 (예외 없이) 으로 갱신.
4. 신규 hook 추가 시 lefthook 만 지원하는 기능이 필요해짐.

## Alternatives Considered

### A. lefthook 으로 즉시 마이그레이션 (표준 정합)
- pros: 글로벌 표준 100% 정합, 3-repo 도구 통일.
- cons: 회귀 위험 + 마이그레이션 비용. *동작 중인 인프라 교체* 의 정당화 부족.
- 거절 사유: 가치 < 비용.

### B. sister operator 를 pre-commit 으로 통일
- pros: 3-repo 도구 통일.
- cons: valkey 가 글로벌 표준 정합 (lefthook) 인데 그 정합을 *깨는* 방향. 글로벌 RFC 갱신 시 다시 마이그레이션 필요.
- 거절 사유: 표준 정합을 깨는 변경은 ADR 로 정당화 어려움.

### C. (채택) ADR 로 분기 명시 + 마이그레이션 트리거 정의
- pros: 명시적 추적성, 나중에 통일 결정 시 trigger 발동.
- cons: 단기적으로 도구 분기 잔존.

## 글로벌 참조

- 표준: `~/Documents/ai-dev/standards/enforcement.md §1.1`
- 정합 사례: `sister operator/.lefthook.yml`
- 본 repo 운영: `.pre-commit-config.yaml`

<!-- live-verified: 2026-05-09 -->
