# ADR-0032: GHA 전면 제거 → 로컬 4계층 단일 운영 (RFC-0002 strict)

| Meta | Value |
|---|---|
| Status | Accepted |
| Date | 2026-05-21 |
| Author | keiailab |
| Supersedes | ADR-0031 (GHA Retention for Public OSS Operator) |
| Related | RFC-0002 (GHA 영구 금지), ADR-0012 (Renovate 단일 의존성 봇), ADR-0027 (community-operators sync — Deprecated by ADR-0028 Phase D), sister postgres-operator ADR-0018, sister valkey-operator ADR-0048 (operator family trade-off 다름 — GHA 유지 노선) |

## Context

ADR-0031 (Proposed, 2026-05-21) 은 12개 `.github/workflows/` 의 *유지* 결정을 시도했다. 그 근거는:

1. External contributor 의 trusted automated gate
2. OpenSSF Scorecard / CodeQL 등 external trust signal
3. Helm chart auto-publish (GitHub Pages) + release supply-chain 자동화
4. dependabot/renovate 의 GHA `package-ecosystem` 의존

그러나 사용자 (maintainer) 는 2026-05-21 S7 cycle 에서 다음을 명시:

- mongodb-operator 본 repo 는 **RFC-0002 strict** 적용 — GHA 전면 제거 + 로컬 4계층 단일 운영
- 2026-04-28 incident (org billing → 전 repo 24h+ merge freeze) 의 *재발 회피* 가 external trust signal 손실보다 우선
- sister postgres-operator 도 ADR-0018 Accepted 로 동일 노선 선택 — operator family 정합
- sister valkey-operator (ADR-0048 Accepted) 는 GHA 유지 노선 별도 선택 — operator 별 trade-off 다르게 처리

이로써 ADR-0031 의 *유지* 노선은 폐기되고, 본 ADR 이 strict 적용을 SSOT 화한다.

## Decision

`.github/workflows/` 전면 제거 (12 파일) + 모든 게이트를 **로컬 4계층** 으로 일원화 + 배포 자동화는 `scripts/*.sh` 로 대체.

### 로컬 4계층 매핑

| Layer | 메커니즘 | 본 repo 의 구체 도구 |
|---|---|---|
| L1 pre-commit hook | `.lefthook.yml pre-commit` | gofmt, govet, golangci-lint, helm-lint (chart 변경 시), adr-phantom-check, orphan-plan-files-block |
| L2 pre-push hook | `.lefthook.yml pre-push` | unit-test (envtest), full-lint, helm-template, govulncheck, gitleaks, platforms-amd64-guard, version-sync, go-mod-tidy, **kube-linter** (NEW), **go-licenses** (NEW), **markdown-link-check** (NEW) |
| L3 Makefile | `Makefile` target | `make lint test build audit validate gate release helm-publish kube-lint go-licenses md-link-check` |
| L4 리뷰어 증거 확인 | PR description | 로컬 `lefthook run pre-push` + `make gate` 실행 로그 첨부 |

### 제거된 12 workflow + 대체 위치

| 제거 workflow | 대체 |
|---|---|
| `ci.yml` (lint+test+build) | L1+L2 (golangci-lint + unit-test) + L3 (`make lint test build`) |
| `codeql.yml` | L2 (gosec via `make audit` HIGH severity) + L3 (`make audit`) — CodeQL deep-dataflow 손실 인정 (RFC-0002 trade-off) |
| `dco.yml` | L1 commit-msg hook (`dco-signoff`, `DCO_STRICT=1` 기본) |
| `dependency-review.yml` | L2 (`go-mod-tidy` drift 차단) + L3 (`make audit` govulncheck + trivy) |
| `helm-install-test.yml` | L2 (`helm-template` render 검증) — 실 install test 는 release 시 `scripts/release-smoke-test.sh` |
| `helm-lint.yml` | L1 (`helm-lint` chart 변경 시) + L2 (`helm-lint`) |
| `helm-publish.yml` | **`scripts/helm-publish.sh`** (수동 실행, gh-pages 브랜치 push) |
| `kube-linter.yml` | L2 (`kube-linter` hook, NEW) + L3 (`make kube-lint`, NEW) |
| `markdown-link-check.yml` | L2 (`markdown-link-check` hook, NEW) + L3 (`make md-link-check`, NEW) |
| `release.yml` | **`scripts/release.sh`** (수동 실행, tag push + helm publish + cosign sign + scripts/release-smoke-test 검증) |
| `scorecard.yml` | 외부 metadata only — 머지 게이트 아님. 제거 시 badge 손실 인정 |
| `security-scan.yml` | L3 (`make audit` govulncheck + trivy fs/image) |

### 신규 scripts (배포 자동화)

| Script | 책임 |
|---|---|
| `scripts/helm-publish.sh` | Helm chart package + gh-pages 브랜치 publish (이전 helm-publish.yml 대체) |
| `scripts/release.sh` | 본 release flow — tag → docker buildx → helm-publish → cosign sign → release-smoke-test → gh release create |
| `scripts/release-smoke-test.sh` | release 후 smoke test — Pull image → helm install --dry-run → CR sample apply |
| `scripts/audit-cluster-state.sh` | 사용자 cluster 의 운영 상태 audit (drift 감지) |

## Consequences

**Positive**:
- **SPOF 제거** — 2026-04-28 incident 재발 시 즉시 영향 없음 (로컬 4계층 + scripts)
- **RFC-0002 정합** — Global 규약 strict 준수
- **maintainer 운영 자율** — release 시점 / 빈도를 사용자가 직접 결정 (수동 trigger)
- **로컬 게이트 보강** — kube-linter, go-licenses, markdown-link-check 3 종 NEW 추가로 게이트 누락 0

**Negative**:
- **외부 신뢰 signal 손실** — OpenSSF Scorecard badge 미동작 / CodeQL deep-dataflow 분석 손실. 부분 보완: `make audit` (gosec + govulncheck + trivy) 가 1차 방어.
- **외부 기여자 PR 게이트 미시각화** — local hook 통과 증거 첨부 의무 (L4). 외부 기여자는 즉시 통과 시각화 안 됨 — PR review 시간 ↑
- **dependabot/renovate 비활성** — go module / docker base image 업데이트는 수동 (`go get -u && go mod tidy` 또는 `make deps-update`) 또는 Renovate self-hosted 검토 (별 ADR)
- **release 자동화 사용자 시간** — release 시점 사용자가 직접 `scripts/release.sh` 실행

**Neutral**:
- sister valkey-operator (ADR-0048 Accepted) 는 GHA 유지 — operator family 내 *trade-off 다르게* 인정. 두 모델의 *비교 데이터* (수개월 운영 후) 로 향후 합의점 모색

## Alternatives Considered

1. **ADR-0031 (Retention) 유지** — Rejected: SPOF 위험 정합 우선
2. **Partial removal (외부 신뢰 게이트만 유지, ci.yml 만 제거)** — Rejected: GHA 자체에 의존성 잔존, mongodb sister postgres 와 정합성 깨짐
3. **valkey-operator 노선 (GHA 유지 + 로컬 이중 운영)** — operator 별 trade-off 다름. valkey 의 *외부 신뢰 게이트 우선* 결정은 별 operator family member 의 선택. mongodb 는 SPOF 회피 우선

## References

- **RFC-0002** (2026-04-29) — Global GHA permanent ban (internal infra repo intent + public OSS 적용 확장)
- **Incident KB**: I-2026-04-28 (GHA billing outage) — see `~/.codex/standards/incident-kb.md`
- **Sister ADRs**:
  - postgres-operator: ADR-0018 (GHA strict removal, 동일 노선)
  - valkey-operator: ADR-0048 (GHA retention, 다른 노선 — operator family trade-off)
- **본 repo previous ADRs**:
  - ADR-0027 (community-operators sync automation — Deprecated by ADR-0028 Phase D)
  - ADR-0028 (Phase D — community-operators sync 수동화)

## Implementation

본 ADR 의 결정은 사전 작업으로 이미 실행됨:

| Step | Commit | PR |
|---|---|---|
| 1. workflow 12 파일 제거 + 로컬 이관 | cdd34f2 | #194 |
| 2. scripts/release.sh + scripts/helm-publish.sh 신규 | ea65265 | #195 |
| 3. 로컬 4계층 보강 (kube-linter + go-licenses + md-link-check) | 9f97104 | #196 |
| 4. 본 ADR Accepted (이 PR) | (TBD) | (TBD) |

검증:
- `.github/workflows/` 부재 확인: `test ! -d .github/workflows`
- 신규 scripts 실행 가능: `bash scripts/release.sh --dry-run`
- 로컬 4계층 통과: `lefthook run pre-push` + `make gate`
- PR description 에 위 통과 로그 첨부 의무 (L4)
