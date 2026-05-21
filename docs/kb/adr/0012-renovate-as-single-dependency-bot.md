# ADR-0012: 의존성 봇은 Renovate 단일 채택 (Dependabot 제거)

- Date: 2026-05-07
- Status: Accepted
- Authors: @keiailab

## Context

본 repo 는 `.github/dependabot.yml` 과 `renovate.json` 을 *동시에* 보유한 상태였다. 두 봇 모두 weekly 일정으로 의존성 PR 을 생성하므로:

1. 동일 의존성에 대한 *중복 PR* 발생 가능 (예: `k8s.io/api` minor 업데이트 → Dependabot PR + Renovate PR).
2. 머지 충돌 / 리뷰 노이즈 / CI 중복 실행 (RFC 0002 의 GH Actions 폐기 후 로컬 게이트만 운영하나, 둘 다 PR 만들면 사용자 리뷰 시간 2배 소비).
3. 3-repo 정합 부족: `postgresql-operator` / `valkey-operator` 는 `renovate.json` 만 운영. 본 repo 만 outlier.

## Decision

**Renovate 단일 채택**. `.github/dependabot.yml` 제거.

## Rationale

1. **Renovate 가 기능적으로 superset** —
   - `vulnerabilityAlerts` (security label + 자동 assign) — Dependabot 는 별도 Security Alerts 인터페이스, repo config 일원화 불가.
   - `packageRules` 의 정교한 매칭 (`matchManagers` + `matchDepTypes` + `matchUpdateTypes` 조합) — Dependabot 의 `groups` 보다 표현력 높음.
   - `lockFileMaintenance` 자동 — go.sum / package-lock.json 등 lockfile 의 indirect 갱신을 별도 PR 로 분리 가능.
   - `prHourlyLimit` / `prConcurrentLimit` — PR 폭증 차단 (Dependabot 는 `open-pull-requests-limit` 만 지원).
   - `rangeStrategy: bump` — Go module direct 의 minor 업데이트를 정확한 버전으로 bump (Dependabot 는 range 변경 미세 조정 어려움).
2. **3-repo 정합** — postgres + valkey 가 이미 Renovate 단일. 본 ADR 로 mongodb 도 동일 정합.
3. **RFC 0002 §7 예외 정합** — 글로벌 RFC 가 Dependabot/Renovate 둘 다 GH Actions 예외로 허용. 그러나 본 ADR 은 *둘 다 운영하지 않고 하나만* 를 강조.
4. **유지보수 일관성** — Renovate config 변경은 `renovate.json` 한 곳만 갱신하면 됨.

## Consequences

### 긍정
- 중복 PR 0건 → 리뷰 시간 절감.
- 3-repo 도구 정합 ✓.
- Renovate 의 `vulnerabilityAlerts` 자동 assign + `security` label 활용 가능.

### 부정
- Dependabot 의 `version: 2` 표준 호환성 상실 — 단, GitHub 의 *Dependabot Security Updates* 는 repo 설정과 무관하게 `dependabot.yml` 없이도 동작 (Vulnerability Alerts 는 GitHub UI level).
- Dependabot 의 `groups` 정의 (kubernetes-dependencies, controller-runtime, test-dependencies) 가 Renovate 의 `packageRules` 로 *명시 이전* 필요 — 현재 `renovate.json` 의 `extends: group:linters / group:test` 가 일부 커버하나, kubernetes-dependencies 그룹은 별도 packageRule 추가 권장 (별 트랙).

### Migration

본 ADR 채택과 동시에:
1. `git rm .github/dependabot.yml`.
2. README / CONTRIBUTING 의 Dependabot 언급 제거 (현재 없음).
3. ~~Renovate `packageRules` 에 `kubernetes-dependencies` 그룹 추가~~ — *이미 적용 완료* (3-repo 의 `renovate.json` 모두 `groupName: "kubernetes"` + `matchPackagePrefixes: ["k8s.io/", "sigs.k8s.io/"]` 보유. opentelemetry 그룹 + dockerfile golang 룰도 동일 정합).

## Alternatives Considered

### A. Dependabot 단일
- pros: GitHub 네이티브.
- cons: 3-repo 정합 깨짐 (postgres+valkey Renovate 운영). 기능 빈곤.
- 거절 사유: 정합 + 기능 부족.

### B. (채택) Renovate 단일
- pros: 기능 풍부 + 3-repo 정합.
- cons: 외부 GitHub App 의존 — 단, RFC 0002 §7 예외에 명시 허용.

### C. 둘 다 운영
- pros: 양쪽 기능 활용.
- cons: 중복 PR + 리뷰 노이즈 — 본 ADR 의 트리거.
- 거절 사유: 운영 비용 > 효용.

## 글로벌 참조

- RFC 0002 §7 (GH Actions 금지의 3 예외): Dependabot / Renovate 도구 자체 허용.
- 표준: `~/Documents/ai-dev/standards/enforcement.md §1.5` (Security scan).
- 사례: `postgresql-operator/renovate.json`, `valkey-operator/renovate.json`.
