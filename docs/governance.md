<p align="center">
  <b>English</b> |
  <a href="GOVERNANCE.ko.md">한국어</a> |
  <a href="GOVERNANCE.ja.md">日本語</a> |
  <a href="GOVERNANCE.zh.md">中文</a>
</p>

# Governance

본 문서는 keiailab/mongodb-operator 프로젝트의 의사결정 절차를 정의합니다.

## 원칙

1. **개방성**: 모든 의사결정은 공개 채널(GitHub issue/PR/RFC)에서 이뤄집니다.
2. **최소 합의(Lazy Consensus)**: 일상적 변경은 반대 없으면 진행됩니다.
3. **명시적 합의(Explicit Consensus)**: 아키텍처 변경, CRD 변경, 보안 모델 변경, 라이선스 변경은 RFC/ADR 후 메인테이너 **2/3 supermajority** 승인. 일반 RFC (단일 컴포넌트 / 도구 채택 / 정책 보강) 는 **simple majority (>50%)**. GOVERNANCE 자체 변경 (§ "본 문서 변경") 은 항상 2/3 supermajority.
4. **공동 책임**: 메인테이너는 코드 품질, 사용자 안전, 커뮤니티 건강에 대해 공동 책임을 집니다.

## 의사결정 분류

### 일상 변경 (Lazy Consensus)
- 버그 픽스, 문서 개선, 테스트 추가, 의존성 minor/patch 업그레이드, 리팩터링(공개 API 무변경)
- 절차: PR → 1명 이상 메인테이너 LGTM → 머지
- 시한: 별도 코멘트 윈도우 없음 (로컬 게이트 통과 시 즉시 머지 가능 — RFC-0002 에 따라 GitHub Actions 미사용, pre-commit/pre-push hook + Makefile 로 검증)

### 중간 변경 (Explicit Consensus)
- 새 CRD 필드 추가, 새 reconciler, 의존성 major 업그레이드, 공개 API 변경
- 절차: 이슈로 제안 → 7일 코멘트 윈도우 → 메인테이너 다수 LGTM → 머지
- 거부 1건이 있을 시 메인테이너 회의에서 토론

### 아키텍처 변경 (RFC/ADR 필수)
- 새 컴포넌트 도입, 보안 모델 변경, 라이선스 변경, 호환성 깨는 변경
- 절차:
  1. `docs/kb/adr/NNNN-title.md` 에 ADR 또는 RFC 제출
  2. 14일 코멘트 윈도우
  3. 메인테이너 2/3 이상 찬성
  4. ADR/RFC Status: `Draft → Accepted` 후 구현 PR 진입

### Deployment 모델 변경 (ADR + 사용자 명시 cluster apply, 2026-05-15 추가)

OLM v0 ↔ OLM v1 ↔ Helm chart 의 *기본 권장 변경* 또는 *cluster apply* 는 본 영역.

| 결정 | 도구 | 게이트 |
|---|---|---|
| bundle / catalog manifest 변경 | PR + ADR | Conventional Commits + bundle validate PASS |
| OLM v0 → v1 migration 같은 *모델 전환* | ADR (cluster-side) | ADR + 메인테이너 2/3 + 사용자 명시 cluster apply |
| installer RBAC 변경 (cluster-admin ↔ narrow) | PR + ADR | bundle CSV derive 검증 + cluster apply 사용자 명시 |
| NetworkPolicy 신설/변경 | PR + ADR | OPRUN-3923 reference + cluster apply 사용자 명시 |
| 외부 사용자 *recommended install path* 변경 | RFC | (install.md) §1 matrix 갱신 + 14일 코멘트 |

본 영역의 ADR chain: ADR-0028 (외부 사용자 운영 수준) → ADR-0029 (OLM v1 채택) → ADR-0030 (narrow RBAC + NP). 후속 ADR 의 *cluster apply* 는 사용자 명시 영역 (글로벌 §2.0 자율 헌법 ② 조건 — 돌이킬 수 없는 운영 작업).

## 보안 결정

CVE 보고, 시크릿/인증 모델 변경은 [Security](security.md) 절차에 따라 비공개 채널에서 우선 처리한 뒤, 패치 릴리스 후 공개 합의를 거칩니다.

### Installer RBAC scope (ADR-0030, 2026-05-15)

- **production cluster**: `clusterextension-narrow-rbac.yaml` (bundle CSV derive) 권장.
- **PoC / dev cluster**: `clusterextension.yaml` (cluster-admin) 허용 — 단순함 우선.
- **cluster-admin binding 영구 운영 금지** — bundle 조작 시 cluster 전체 영향. PR review 시 cluster-admin 사용 시 *production 외* 명시 확인.

### Network surface (ADR-0030, 2026-05-15)

- **default-deny cluster**: olmv1-system + mongodb-system 의 NetworkPolicy *명시 적용* 의무. `deploy/olm-v1/networkpolicies.yaml` reference (OLM v0 path 는 ADR-0028 Phase D 로 영구 폐기).
- **default-allow cluster**: NP optional, 단 적용 권장 (security baseline).

## 릴리스 결정

릴리스 분기 / 버전 bump 는 메인테이너 1 인이 lazy consensus 로 진행 가능. 단 LTS 라인 신설 / EOL 선언 / Sharded GA 졸업 등 *주요 마일스톤* 은 explicit consensus 필수.

### Release artifact 의 도달 가능성 (ADR-0028 후속)

매 release 시 *3 deployment models* 모두에 도달 가능해야:
- **operator container**: ghcr.io public — 모든 path 공통
- **Helm chart**: gh-pages → artifacthub.io 자동 publish
- **OLM bundle + catalog**: `make bundle-push + catalog-push` 후 ArtifactHub + community-operators upstream PR (ADR-0027 자동화 deferred — 수동 fallback)

위 3 artifact 중 *하나라도 missing* 시 release tag 금지.

## 변경 이력

| Date | Change | Refs |
|---|---|---|
| 2026-05-15 | Deployment 모델 변경 분류 + Installer RBAC scope + Network surface + Release artifact 도달 가능성 신설 | ADR-0028, ADR-0029, ADR-0030 |
| 2026-05-07 | 본 문서 신설 — 3-repo (mongodb / postgresql / valkey) 거버넌스 자산 정합 | INC-2026-05-07 |

