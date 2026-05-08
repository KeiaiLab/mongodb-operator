# ADR-0020: RFC-0017 operator tooling unification 채택

- Date: 2026-05-09
- Status: Proposed
- Authors: @eightynine01
- Tags: tooling, ci, hook, lint

## Context

ai-dev RFC-0017 (`~/Documents/ai-dev/rfcs/0017-operator-tooling-unification.md`) 가 4 keiailab operator repo 의 hook system / linter / Makefile / EventRecorder / Dockerfile HEALTHCHECK 통합을 제안한다. 본 ADR 은 mongodb-operator 측 채택 결정 + 본 repo 한정 변경 사항을 기록한다.

본 repo 의 현 상태 (2026-05-09 audit):
- Hook: `.pre-commit-config.yaml` (lefthook 아님) — RFC-0017 §3.1 위반
- `.golangci.yml`: 보유 (~12 linter) — RFC-0017 §3.2 의 18 linter 표준 미달
- Makefile: lint/test/validate/audit 모두 존재 — ✓
- Dockerfile: distroless static base — RFC-0017 §3.5 (HEALTHCHECK) 철회 후 N/A. helm chart 의 livenessProbe / readinessProbe 정합 확인 필요.
- EventRecorder: ✓ (mgr.GetEventRecorderFor 사용)

## Decision

RFC-0017 을 **Accepted** 상태로 채택하고 본 repo 에서 다음을 수행한다:

1. `.lefthook.yml` 신규 (valkey-operator 패턴 채택, mongodb 특이 hook 1:1 매핑)
2. `.pre-commit-config.yaml` 제거 (DAY 2)
3. `.golangci.yml` 보강 — postgres 의 18 linter 표준 준수, depguard 규칙은 mongodb 특이 (예: SDK boundary 가 있다면) 만 별도 추가
4. `.custom-gcl.yml` 신규 — logcheck plugin 빌드용
5. ~~Dockerfile HEALTHCHECK 추가~~ — 철회 (RFC-0017 §3.5 distroless 부적합). 대신 helm chart `charts/mongodb-operator/templates/deployment.yaml` 에 livenessProbe + readinessProbe 보강 검증.

EventRecorder 변경 없음 (이미 mongodb 가 표준 원본).

## Consequences

### 긍정
- 4 repo 도구 단일화 → audit 비용 감소
- govulncheck / gitleaks / go-mod-tidy drift 검사가 pre-push 로 진입 (현재 mongodb 에 부재)
- 신규 contributor 가 4 repo 어느 곳을 작업해도 동일 hook 명령

### 부정 / 트레이드오프
- 기존 contributor 환경에서 `lefthook install` 1회 실행 필요
- pre-commit 설정 자체가 익숙한 contributor 에게 학습 비용
- linter 18종 활성화 시 *기존 코드의 미해결 issue* 노출 가능 — 단계적 fix 필요

### 후속 작업
- [ ] AI-MO20-1: lefthook install 후 pre-commit run --all-files PASS 검증 (Owner: @eightynine01, Due: 2026-05-12)
- [ ] AI-MO20-2: 기존 .pre-commit-config.yaml 의 모든 hook 이 .lefthook.yml 에 매핑되었는지 diff 검증 (Owner: @eightynine01, Due: 2026-05-12)
- [ ] AI-MO20-3: golangci v2 18-linter 활성화 후 발견되는 issue 분류 + 단계 fix PR (Owner: @eightynine01, Due: 2026-05-19)
- [ ] AI-MO20-4: helm chart deployment.yaml 의 livenessProbe / readinessProbe 보강 검증 (`helm template ... | yq '.spec.template.spec.containers[].livenessProbe'` 부재 시 PR) (Owner: @eightynine01, Due: 2026-05-12)

## Alternatives Considered

| 대안 | 거절 사유 |
|------|----------|
| 현 상태 유지 | RFC-0017 §2 motivation 동일 — drift 누적 |
| .pre-commit-config.yaml 강화 (lefthook 미채택) | standards/enforcement.md §1.1 lefthook 단일화 의무 위반 |

## References

- 글로벌 RFC: `~/Documents/ai-dev/rfcs/0017-operator-tooling-unification.md`
- 관련 audit: `~/.claude/plans/mongodb-operator-operator-commons-postgr-tranquil-horizon.md`
- 관련 ADR: ADR-0011 (mongodb pre-commit policy — 본 ADR 로 일부 super-seded)
- 관련 PR: #(TBD)
