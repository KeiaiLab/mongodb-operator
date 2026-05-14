---
adr: 0028
title: Restore GitHub Actions workflows for OSS CI (deviation from RFC-0002)
status: Accepted
date: 2026-05-14
deciders: keiailab/maintainers
deviates_from: ai-dev/rfcs/0002-no-github-actions.md
sister_adr: keiailab/valkey-operator ADR-0045 (canonical, 2026-05-12)
---

# ADR-0028: Restore GitHub Actions workflows for OSS CI

## Status

Accepted — 2026-05-14

## Context

`mongodb-operator` 는 ghcr.io 와 Artifact Hub 에 publish 되는 공개 OSS
Kubernetes operator 다. 본 repo 가 RFC-0002 (GitHub Actions permanently
banned) 의 *infra repo 전제* 와 다른 4 차이는 valkey-operator ADR-0045
§Context 와 동일하다. 본 ADR 은 그 sister pattern 으로서 mongodb-operator
의 13 workflow 도입을 *명시 일탈* 로 봉인한다.

차이점 요약 (sister of ADR-0045 §Context 1~4):

1. **External contributor surface** — fork PR 가 lefthook 미설치 환경에서도
   동일 게이트 통과해야 함.
2. **Trust signals** — OpenSSF Scorecard `Branch-Protection` /
   `CI-Tests` / `Token-Permissions` / `Signed-Releases` 가 Actions 기반.
3. **Already-installed conventions** — README badge, CODEOWNERS routing,
   `.github/PULL_REQUEST_TEMPLATE.md`, Artifact Hub publication 모두
   Actions-driven 가정.
4. **Required status checks** — branch protection 의 `required_status_checks`
   집합이 비면 dependabot sweep 회귀 hole 재발.

RFC-0002 §7 narrow exception 3종 (Pages / Renovate-Dependabot / release tag
1-step) 은 본 13 workflow 중 *0건* 완전 fit (라이브 audit 2026-05-14):
- `helm-publish.yml` (3 jobs) → §7 ① Pages 정합이나 *1-step 아님*
- `release.yml` (7 jobs: preflight / image / sbom / notes / chart-tgz /
  github-release / sync-community-operators) → §7 ③ 1-step *의도적 일탈*

## Decision

다음 13 workflow 를 *scoped deviation* 으로 유지:

| Workflow | Trigger | Purpose |
|---|---|---|
| `ci.yml` | PR + push main | golangci-lint + go test |
| `codeql.yml` | PR + push + weekly | CodeQL semgrep static analysis |
| `dco.yml` | PR | Signed-off-by 검증 |
| `dependency-review.yml` | PR | actions/dependency-review-action |
| `helm-install-test.yml` | PR + weekly | helm install smoke |
| `helm-lint.yml` | PR | helm template + chart-testing |
| `helm-publish.yml` | tag `v*` | gh-pages chart repository |
| `kube-linter.yml` | PR + push + weekly | kube-linter chart scan |
| `markdown-link-check.yml` | PR + weekly | link rot 검사 |
| `release.yml` | tag `v*` | image + sbom + notes + chart-tgz + community-operators sync |
| `scorecard.yml` | weekly + branch_protection_rule | OpenSSF Scorecard |
| `security-scan.yml` | PR + weekly | trivy-fs + govulncheck |

`main` branch protection 의 `required_status_checks` 최소 집합:
- `golangci-lint`
- `go test` (envtest 포함)
- `helm-lint`
- `kube-linter`
- `dco`
- `dependency-review`

내부 인프라 repo (`force-*`, `keiailab/platform-*`) 는 RFC-0002 §1 그대로
유지 — 본 일탈은 *공개 OSS operator surface* 에 한정한다.

## Scope of the deviation

valkey-operator ADR-0045 §Scope sister repo 매트릭스 중 본 repo (mongodb-
operator) 에 해당한다:

- `keiailab/valkey-operator` (ADR-0045, canonical)
- `keiailab/mongodb-operator` (본 ADR)
- `keiailab/postgres-operator` (sister ADR — 동일 cycle 작성)
- `keiailab/operator-commons` (no workflow yet, ADR 불필요)

## Consequences

### Positive

- 외부 fork PR 즉시 게이트 통과 신호.
- `required_status_checks` 강제 가능 → dependabot sweep 회귀 hole 차단.
- OpenSSF Scorecard score `CI-Tests` / `Branch-Protection` /
  `Signed-Releases` 회복.
- SLSA-3 provenance + cosign keyless (`release.yml` 의 `sbom` + `image` job)
  자연 통합.
- 글로벌 governance-report 에 `gha_workflow_count` 메트릭 추가 시 본 repo
  값 = 13 으로 *명시 audit 대상*.

### Negative

- RFC-0002 가 차단했던 *GitHub Actions billing SPOF* 재진입. 완화:
  - 모든 workflow 는 *반드시* `make` target 으로도 실행 가능해야 함
    (lefthook + Makefile parity — RFC-0002 §2 evidence pattern 동일).
  - Actions 장애 시 PR 본문에 `make verify` 출력 인용 + maintainer 승인
    으로 머지 가능 (valkey ADR-0045 §Negative mitigation 정합).
- `release.yml` 7-job 구조는 §7 ③ "1-step" 명시 일탈. SBOM / cosign /
  community-operators sync 각 job 의 *분리 불가능성* (산출물 의존 그래프
  체인) 이 근거.

### Trade-offs explicitly considered

| Alternative | Rejected because |
|---|---|
| Keep `.github/workflows/` removed, lefthook only | fork PR 에 게이트 신호 0 |
| Mirror to GitLab CI | OSS contribution surface 분기, 유지비 2x |
| Only restore `release.yml` (§7 ③ argue) | required_status_checks 강제 불가 + PR CI 신호 부재 |
| Self-host runners | PR volume 대비 비용 정당화 안 됨 |

## Compliance

- 13 workflow 중 *cross-merge-queue dependency 없음* (각 workflow 독립
  실행 — RFC-0002 §1 sweep 시나리오 회피).
- `make test-unit` + `make lint` + `make audit` + `make validate` 모두
  로컬에서 Actions 없이 동등 실행 가능 (Makefile RFC-0002 §2 evidence).
- governance-report 갱신 시 `gha_workflow_count: 13` + `gha_adr_link: 0028`
  pair 로 *exception 명시* 추적.

## Follow-ups

- 본 ADR 머지 후 `governance-report` 에 `gha_workflow_count` 컬럼 추가
  (검출 도구 강화).
- postgres-operator sister ADR 동일 cycle 작성 — 14 workflow scope.
- ai-dev/rfcs/0002 본문 *clarification footnote* — "public OSS operator
  repositories" 가 scope 외임을 명시.

## References

- valkey-operator ADR-0045 (canonical sister, 2026-05-12)
- ai-dev/rfcs/0002-no-github-actions.md §7 (narrow exceptions)
- ai-dev/standards/adr.md §2 (글로벌 standards 일탈 시 ADR 필수)
- 2026-05-14 cross-repo audit (mongodb 13 workflow trigger 분류 evidence)
