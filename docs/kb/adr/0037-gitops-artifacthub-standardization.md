# ADR-0037: GitOps overlay + ArtifactHub 검증 파이프라인 표준화

- Date: 2026-06-02
- Status: Accepted
- Authors: @phil

## Context

keiailab operator 4종(mongodb-operator / postgres-operator / valkey-operator + operator-commons
라이브러리)의 cross-repo 표준이 비일치 상태였다:

- **GitOps overlay 경로 drift**: mongodb는 `examples/gitops/`를 사용하는 반면
  postgres/valkey는 `deploy/overlays/prod/`를 사용 — 동일 GitOps 패턴이 서로 다른
  경로에 분산.
- **ArtifactHub 검증 자동화 부재**: ArtifactHub Signed badge 전제 조건인 PGP
  signingKey(`F1A6893583E632A757FF6767F3CC8C6AEC9CEB08`) 메타데이터 검증이 수동에만
  의존.
- **CI 게이트 비대칭**: valkey가 가장 성숙한 reference 구현(ADR-0024 수기 chart 패턴
  + ADR-0044 Signed/Official trust badge).

mongodb-operator의 핵심 문제: GitOps overlay 경로가 `examples/gitops/`에 위치하여
org-wide `deploy/overlays/prod/` 표준과 drift. base 경로 심도(`../../../config` 3단계)
및 base namespace(`mongodb-operator-system`) 표기를 표준화해야 한다.

## Decision

**2-레이어 분리**를 전 4종에 적용한다:

- **Layer 1 — ArtifactHub publish** (4종 모두): helm chart(`charts/<name>/`) → gh-pages
  → ArtifactHub Signed badge. 공통 PGP signingKey fingerprint
  `F1A6893583E632A757FF6767F3CC8C6AEC9CEB08`를 `charts/artifacthub-repo.yml`에 등록.
- **Layer 2 — GitOps 배포 overlay** (operator 3종만, operator-commons 제외):
  kustomize(`deploy/overlays/prod/`), namespace=`data`, base namespace delete patch 적용.
  operator-commons는 `type: library`로 배포 대상이 아니므로 Layer 2에서 제외.

**ArtifactHub 검증 파이프라인**:
- `.github/workflows/artifacthub-verify.yml`: `ah lint`(메타데이터 린트) + smoke 테스트
  (gh-pages 인덱싱 확인 + ArtifactHub REST 등록 확인 + `.tgz.prov` 도달성 검증).

**서명 구분**:
- `charts/artifacthub-repo.yml` PGP signingKey → ArtifactHub `Signed` badge.
- cosign(`release.yml`) → GitHub Release `Verified` 레이블.
- 두 서명은 **완전히 별개**다 — 혼동 금지.

**mongodb-operator 특이사항**:
- GitOps overlay를 `examples/gitops/` → `deploy/overlays/prod/`로 이전.
- base 경로 심도: `../../../config`(3단계) — mongodb-operator 디렉토리 구조 정합.
- base namespace: `mongodb-operator-system` (operator CRD/RBAC가 위치하는 네임스페이스).
- `deploy/overlays/prod/kustomization.yaml`에 namespace=`data` patch + base namespace
  delete patch 포함.
- `charts/artifacthub-repo.yml`에 signingKey 등록 + `artifacthub-verify.yml` 추가.

**전파 방식**: Approach A(self-contained) — valkey reference를 각 repo에 복사+적응.
org-level reusable workflow(`uses:`) 방식은 배제. 이유: OSS fork 가능성 +
`keiailab/.github` org repo 2026-05-27 제거됨.

**GH Actions 사용 정당화**: RFC-0002(GitHub Actions 영구 금지)는 GitLab/인프라
closed-source org billing SPOF(2026-04-28 트리거) 컨텍스트의 결정이다. 본 대상은
**GitHub OSS public repo** + **사용자 명시 지시**("GHActions 통해서 artifacthub.io
파이프라인 검증"). 거버넌스 우선순위(사용자 명시 > Tier-1 글로벌)상 OSS public repo의
GH Actions 사용은 정책 위반이 아니다. ADR-0035(RFC-0002 GHA block hook)는 GitLab
repo 대상이며 본 GitHub OSS repo에는 미적용.

## Consequences

**긍정적**:
- overlay 경로 통일(`deploy/overlays/prod/`)로 cross-repo GitOps 패턴 일관성 확보.
- `ah lint` + smoke 자동 검증으로 ArtifactHub 등록 회귀 방지.
- cosign ↔ ArtifactHub Signed badge 혼동 제거 — 각 badge 역할 명확화.
- `examples/gitops/` 구버전 경로 제거로 디렉토리 클린업.

**부정적 / 트레이드오프**:
- `examples/gitops/` 경로를 참조하는 외부 문서/사용자가 있다면 마이그레이션 필요.
- `.tgz.prov` 생성은 현재 로컬 `helm-publish.sh --sign`에서만 동작 — CI 자동화는
  GPG private key secret 결정 후 후속 적용.
- ArtifactHub REST smoke는 gh-pages publish → ArtifactHub 인덱싱 지연(수 분)으로
  flaky 가능 → 재시도 로직 필요.

## Alternatives Considered

**`examples/gitops/` 경로 유지**: 배제. org-wide `deploy/overlays/prod/` 표준과 drift
지속. cross-repo 참조 시 경로 혼동 발생.

**org-level reusable workflow(`uses:` 호출)**: 배제. `keiailab/.github` org repo가
2026-05-27 제거됨. OSS repo는 self-contained를 선호(fork 시 의존성 없음). valkey
ADR-0024가 이미 self-contained manual pattern 확립.

**GH Actions 완전 배제(로컬 4계층만)**: ArtifactHub smoke는 gh-pages publish 후
원격 상태를 확인해야 하므로 로컬에서 실행 불가. ADR-0035가 GitLab repo 대상임을 명확히
하고 GitHub OSS repo에서는 ADR-0022 narrow exception 패턴을 따른다.

## Refs

- ADR-0035: RFC-0002 GitHub Actions block hook (GitLab repo 대상)
- ADR-0036: v3.x-stable baseline
- valkey-operator ADR-0024: Helm chart manual pattern + ArtifactHub
- valkey-operator ADR-0044: ArtifactHub Signed + Official trust badges
- RFC-0002: GitHub Actions 영구 금지(GitLab/인프라 closed-source 한정)
