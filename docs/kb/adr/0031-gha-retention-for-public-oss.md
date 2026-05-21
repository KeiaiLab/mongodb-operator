# ADR-0031: GitHub Actions 보존 — Public OSS Operator 외부 신뢰 게이트

| Meta | Value |
|---|---|
| Status | Superseded by ADR-0032 |
| Date | 2026-05-21 |
| Author | keiailab |
| Supersedes | (none) |
| Superseded by | ADR-0032 (GHA 전면 제거 → 로컬 4계층 단일 운영) |
| Related | ADR-0012 (Renovate 단일 의존성 봇 채택), ADR-0027 (community-operators sync 자동화 — Deprecated by ADR-0028 Phase D) |

> **2026-05-21 supersede**: maintainer 의 S7 cycle 결정으로 본 ADR 의 *유지* 노선은 폐기되고, ADR-0032 (RFC-0002 strict 적용 + 12 workflow 전면 제거) 로 대체되었다. 본 문서는 history 보존 용도로만 유지된다.

## Context

글로벌 RFC-0002 (GitHub Actions 영구 금지, 2026-04-29) 는 2026-04-28
organization billing 사고 — 단일 billing 실패가 전 저장소 전 PR 의 머지를
24시간+ 차단 — 로 트리거됐다. 본 정책의 의도: *내부 인프라 저장소* 의
단일-SaaS SPOF 회피.

그러나 public 오픈소스 K8s operator 는 *다른* 요구를 가진다:

1. 외부 contributor 가 PR 을 검증하려면 *신뢰 가능한 자동 게이트* 가
   필요하다. 외부 contributor 는 maintainer 의 사설 lefthook 프로필이나
   `make verify` 를 비-Linux 노트북에서 합리적 시간 내에 실행할 수 없다.
2. 보안 스캐너 (CodeQL, OpenSSF Scorecard, Trivy) 는 downstream 소비자가
   인정하는 *외부 신뢰 신호* 다. Artifact Hub / OperatorHub 의 Scorecard
   배지는 패키지 메타데이터의 일부.
3. Helm chart auto-publish (GitHub Pages) 와 release artifact 서명
   (cosign + SLSA) 은 *public release 파이프라인의 일부* — Pages 가
   chart 의 유일한 canonical URL.
4. dependabot/renovate 는 PR cadence 를 구동하기 위해 GHA 호환
   `package-ecosystem` 스캔이 필요하다.

본 ADR 은 본 저장소의 기존 부분 예외 ADR — **ADR-0012** (Renovate 단일
의존성 봇 채택; RFC-0002 §7 예외의 일관성 정리) 와 **ADR-0027**
(community-operators sync 자동화; ADR-0028 Phase D 로 Deprecated 됐으나
history 보존) — 를 통합된 *단일 rationale* 로 흡수한다. 이전 부분 ADR 들이
각각 일탈의 한 슬라이스를 정당화했다면, 본 ADR 은 본 저장소의
`.github/workflows/` *전체* (12개 파일) 보존에 대한 SSOT 다.

## Decision

`.github/workflows/` (12개 워크플로) 를 **이중 운영** — GitHub Actions
primary 게이트 + 로컬 4계층 (pre-commit, pre-push, Makefile, PR 리뷰어
증거 확인) fallback — 으로 보존한다. 이 depth-defense 패턴이 RFC-0002 의
모티브였던 SPOF 위험을 완화한다.

### 워크플로 분류 (본 저장소 12개 파일)

| 분류 | 워크플로 (본 저장소) | Rationale |
|---|---|---|
| **External Trust Gate** | `codeql.yml`, `scorecard.yml`, `dco.yml`, `dependency-review.yml`, `kube-linter.yml`, `security-scan.yml` | 외부 인정 보안/컴플라이언스 신호. downstream 소비자는 Scorecard 배지를 검증; CodeQL 의 deep dataflow 정적 분석은 로컬 `gosec` 가 표현할 수 없는 영역; DCO 는 community-operators upstream 에 필수인 Signed-off-by 트레일을 유지. |
| **Auto Deploy** | `helm-publish.yml`, `release.yml` | RFC-0002 §7 예외 ① (GitHub Pages) + 예외 ③ (release tag → GitHub Release body). `gh-pages` 로의 Helm chart auto-publish 와 cosign 서명된 release artifact. (`release.yml` 은 ADR-0027 의 community-operators sync job 의 *history* 를 보유했으나 ADR-0028 Phase D 로 해당 job 폐기됨 — workflow 본체는 release tag 처리용으로 유지). |
| **Local 4-Tier Backup** | `ci.yml` (lint+test+build), `helm-lint.yml`, `helm-install-test.yml`, `markdown-link-check.yml` | 동일 검사가 pre-commit / pre-push / Makefile 에서도 강제됨 (ADR-0011 정합). GHA = primary, 로컬 = depth-defense. GHA 가 다운돼도 maintainer 는 `make verify` + 로컬 hook 로 머지 가능. |
| **Ops Tools** | (본 저장소 없음 — `stale.yml` 미보유) | mongodb-operator 는 issue/PR lifecycle 자동화를 GHA 로 운영하지 않음. sister postgres/valkey 는 `stale.yml` 보유 (분류 슬롯 유지). |

### Branch protection 정합

`main` branch protection 은 **External Trust Gate** 와 **Local 4-Tier
Backup** 분류의 GHA job 이름을 `required_status_checks` 로 나열한다.
워크플로 파일 내 job 이름을 변경할 때 maintainer 는 이 리스트를 함께
동기화해야 한다 — 미동기화는 운영 결함으로 간주한다 (§Consequences 의
운영 규율 항목 참조).

## Consequences

**긍정**:

- 외부 contributor 가 로컬 환경 동등성 없이 명확한 자동 PR 게이트를 본다.
- downstream 소비자가 외부 보안 신호 — Security 탭의 CodeQL findings,
  Scorecard 배지, DCO 컴플라이언스 트레일 — 를 검증.
- Helm chart 의 GitHub Pages auto-publish 가 release velocity 를 유지
  (매 release 마다 수동 `helm package` + `gh-pages` push 단계 제거).
- dependabot/renovate 가 별도 runner SaaS 없이 운영 (ADR-0012 의 Renovate
  단일 채택 정합).
- 이전 부분 예외 ADR (0012, 0027) 가 PR 리뷰에서 참조하기 쉬운 통합 결정을
  upstream 으로 갖게 됨.

**부정**:

- GHA SPOF 위험은 잔존. 로컬 4계층 fallback 으로 완화 — External Trust
  Gate 와 Local 4-Tier Backup 분류의 모든 게이트는 maintainer 가 GHA 다운
  시 실행 가능한 로컬 등가물을 갖는다.
- 일부 워크플로 (특히 `ci.yml`, `helm-lint.yml`) 가 로컬 hook 와 중복.
  depth-defense 명목으로 수용 — 워크플로 YAML 의 동기화 유지 비용은 작다.
- branch protection 의 `required_status_checks` 리스트가 워크플로 job
  이름과 동기화 유지 필요. 운영 규율로 처리 — `ci.yml` 의 job 을 branch
  protection 갱신 없이 rename 하면 해당 게이트가 silent 로 비활성화되므로
  rename 은 PR 리뷰를 거친다.

**중립**:

- RFC-0002 §7 의 명시 예외 3종 (Pages, dependabot, release) 는 이미 모두
  포함. 본 ADR 은 *더 넓은 통합 rationale* 로, 12파일 보존 *전체* 가
  *이 종류의 저장소* (public OSS operator) 에 대해 정당함을 설명한다.
  새 예외 추가 요청이 아님.

## Alternatives Considered

1. **엄격 RFC-0002 (모든 워크플로 제거)** — 거부. 외부 contributor 신뢰
   상실; Artifact Hub 의 Scorecard 배지가 사라짐; Security 탭의 CodeQL
   findings 가 비워짐; release 자동화 regression. sister `valkey-operator`
   가 ADR-0045 의 평행 history 에서 commit `3c69429` 로 시도한 바 있으며,
   결과가 심각하여 워크플로가 복원됐다.
2. **부분 제거 (External Trust Gate 만 유지, `ci.yml` + `helm-lint.yml`
   제거)** — 거부. sister operator (`postgres-operator`, `valkey-operator`)
   가 전체 셋을 유지하는데 본 저장소만 outlier; 로컬 4계층 중복이
   maintenance burden 을 더하지만 명확한 이득 없음. depth-defense 가치는
   작지만 0 이 아니며 비용도 낮다.
3. **GHA-only (로컬 4계층 폐기)** — 거부. RFC-0002 가 막으려 한 정확한
   SPOF 를 재도입; 2026-04-28 사고가 이 실패 모드 (24h+ org-wide 머지
   동결) 를 이미 실증.

## References

- **RFC-0002** (2026-04-29) — 글로벌 GHA 영구 금지 (내부-인프라 의도).
- **Sister ADRs (본 저장소)**:
  - [ADR-0012](0012-renovate-as-single-dependency-bot.md) — Renovate 단일
    채택. RFC-0002 §7 예외의 일관성 정리.
  - [ADR-0027](0027-community-operators-sync-automation.md) — Deprecated by
    ADR-0028 Phase D. history 보존; 본 통합 ADR 의 *upstream sync 자동화
    부분* 은 폐기됐으나 14파일 retention 결정 자체는 본 ADR 로 흡수.
- **Cross-operator ADRs (통합 rationale parity)**:
  - `postgres-operator/docs/kb/adr/0017-gha-retention-for-public-oss.md`
  - `valkey-operator/docs/kb/adr/0048-gha-retention-for-public-oss.md`
- **Incident KB**: I-2026-04-28 (GHA billing 사고; RFC-0002 트리거).
- **Related 본 저장소 정책**: [ADR-0011](0011-pre-commit-instead-of-lefthook.md)
  는 본 ADR 의 Local 4-Tier Backup fallback 의 hook 도구를 정의한다.

## Implementation

코드 변경 없음. 본 ADR 머지 시점에 `Proposed` → `Accepted`. `.github/workflows/`
의 기존 12개 워크플로 파일은 그대로 유지 — 본 ADR 은 그 보존의 *이유* 를
문서화한다. branch protection 의 `required_status_checks` 리스트는 불변.
