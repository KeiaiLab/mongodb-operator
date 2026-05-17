# ADR-0028: OLM 번들 외부 사용자 운영 수준 (External-User Production Readiness)

- Date: 2026-05-14
- Status: Accepted
- Authors: @keiailab
- Refs: ADR-0023 (bundle scaffold), ADR-0027 (community-operators sync), CHANGELOG v1.5.0

## Context

mongodb-operator 의 OLM 번들 (`bundle/` + `config/manifests/`) 은 ADR-0023 이후 *기술 골격* 만 갖춘 상태로 유지되어 왔다. 라이브 감사 (2026-05-14, 5-축 평가) 결과 외부 사용자 노출 시 다음 5 결격이 식별되었다:

| # | 결격 | 영향 |
|---|---|---|
| 1 | CSV `containerImage` ↔ `spec.version` drift (`v1.4.19` vs `1.5.0`) | OperatorHub UI 가 실제 배포될 image 와 다른 tag 노출 → 사용자 혼란 |
| 2 | `alm-examples: '[]'` (빈 배열) | OperatorHub "Try it" 샘플 0 — 신규 사용자 onboarding 차단 |
| 3 | `replaces` + `olm.skipRange` 부재 | OLM Subscription 자동 업그레이드 경로 미정의, 1.4.x → 1.5.0 사용자가 *수동 재설치* 강제 |
| 4 | 채널 = `alpha` 단일 (stable 부재) | 외부 운영자가 신뢰할 채널 없음 — 정책상 alpha 채널은 production 권장 안 됨 |
| 5 | `maturity: alpha` | OperatorHub 검색 필터 (`maturity=stable`) 에서 제외, "Don't use in production" 배지 표시 |

본 ADR 은 외부 사용자 운영 (OperatorHub.io / community-operators 등록 사용자) 을 1차 노출 대상으로 승격하기 위해 5 결격을 *동시에* 해소하는 결정을 기록한다.

본 결정은 standards/adr.md §2 트리거 *외부 SDK·운영 표면 변경* + *AI 자가수정이 프레임워크 내부 설정을 바꾼 경우* 에 해당한다.

## Decision

다음 5 변경을 *동일 commit* 에 묶어 적용한다:

1. **Makefile `bundle` target — kustomize image 매칭 정정**
   - `kustomize edit set image controller=...` → `kustomize edit set image ghcr.io/keiailab/mongodb-operator=...` 으로 변경.
   - 사유: manager.yaml 의 실 image 명이 `ghcr.io/keiailab/mongodb-operator` 이며 `controller` placeholder 가 없어 이전 호출은 *no-op* 였다. 이 drift 가 결격 1 의 RCA.

2. **Makefile `bundle` target — 양 채널 동시 push, default = stable**
   - `--channels stable,alpha --default-channel stable` 로 변경.
   - alpha 사용자도 보존 (canary), 신규 외부 사용자 default = stable.

3. **Makefile `bundle` target — operatorframework suite validation**
   - `operator-sdk bundle validate ./bundle --select-optional suite=operatorframework` 추가.
   - OperatorHub.io 가 요구하는 community + olm suite 모두 실행.

4. **base CSV `metadata.annotations.olm.skipRange: '>=0.3.0 <1.5.0'`**
   - OperatorHub 에 이미 published 된 0.3.0 + 본 repo v1.0.0~v1.4.23 모든 사용자 포함.
   - skipRange 의 의미: 본 버전 (1.5.0) 이 *해당 범위의 모든 버전을 무파괴 대체* 한다는 contract.

5. **base CSV `spec.replaces: mongodb-operator.v1.4.23`**
   - 직전 정식 OLM bundle 명. 점진 업그레이드 경로 유지.

6. **base CSV `spec.maturity: stable`**
   - GA (semver v1.0) 와 무관 — *OLM 사용자 노출 readiness* 만 의미.
   - 기준 charter (별도 commit/RFC 없이 본 ADR 으로 확정):
     - v1.4.x 누적 14 release 안정성 (CHANGELOG)
     - v1.5.0 Sharded GA + e2e 통과 (`test/e2e/`)
     - webhook validation (G-11 standalone-aware, G-12 mongos integration)
     - cosign 서명 (G-13, container image + Helm chart + SBOM)
     - SLSA-3 build provenance (release.yml id-token: write)
     - CRD v1alpha1 무파괴 변경 정책 (RFC-0019)

## Consequences

**긍정**:
- OperatorHub.io 사용자가 `stable` 채널 구독 시 1.4.x → 1.5.0 자동 업그레이드.
- 0.3.0 OperatorHub-published 사용자도 skipRange 로 1-step 점프 가능.
- alm-examples 309 줄이 OperatorHub UI 에 노출 → onboarding 즉시 가능.
- containerImage drift 해소 — kustomize 매칭이 영구 정정되어 향후 cycle 자동.
- maturity stable 로 OperatorHub 검색 필터 노출.

**부정 / 트레이드오프**:
- maturity stable 이 SemVer 0.x.x (= pre-1.0 by spec) 와 *문자상 모순*. 본 ADR §Decision 6 에서 해석을 명시했지만 사용자에 따라 혼란 가능. mitigation: README `Stability` 섹션에 본 ADR 링크.
- replaces v1.4.23 는 *해당 OLM bundle 이 published* 라는 전제 — 실제 community-operators 에 v1.4.23 번들은 없을 수 있다 (ADR-0027 에 따르면 0.3.0 만 published). 이 경우 OLM 은 replaces 를 *경고 후 skip* 처리 — skipRange 가 backup 경로. 따라서 안전.
- stable + alpha 양 채널 push 는 community-operators PR diff 증가 (file 2 set). 검토 부담 +.

**후속 작업** (별도 commit / 본 ADR 참조):
- `make bundle VERSION=1.5.0` 재생성 + 5 결격 모두 해소 verify (본 commit 에 포함).
- ROADMAP.md 의 OperatorHub 항목 `[~]` → `[x]` 정정.
- community-operators upstream PR 으로 v1.5.0 sync (ADR-0027 자동화 deferred 상태 — 본 commit 은 수동 fallback path 가능 상태 까지만).
- README `Stability` 섹션 추가 (별 commit).

## Alternatives Considered

**A. maturity 를 alpha 유지 + stable channel 만 추가** — 결격 5 미해소. 외부 사용자가 검색 필터 `maturity=stable` 에서 본 operator 를 발견 못함. Reject.

**B. v1.0.0 으로 semver bump 후 stable 승격** — semver 의미상 정합. 단 v1.5.0 이 이미 release tag 로 존재 (artifacthub published). 역방향 bump 는 더 큰 사용자 혼란. Reject.

**C. base CSV 직접 edit 대신 kustomize patch overlay** — 더 깨끗하지만 operator-sdk generate bundle 의 입력 단계에서 적용되는지 검증 필요 + 단일 file 변경으로 충분. Reject (§2 Simplicity).

**D. manager.yaml 의 `image: ghcr.io/...:latest` 를 `image: controller:latest` 로 변경 후 첫 kustomize entry 유효화** — operator-sdk 표준 placeholder 정합. 단 helm chart / dev workflow 가 `ghcr.io/...:latest` 를 직접 참조하므로 영향 범위가 큼. Reject (§3 Surgical).

## Verification

본 ADR 의 결정이 적용되었음을 확인하는 명령:

```bash
# 5 결격 모두 해소 확인 (재생성 후)
make bundle VERSION=1.5.0
grep "containerImage:" bundle/manifests/mongodb-operator.clusterserviceversion.yaml
  # → ghcr.io/keiailab/mongodb-operator:v1.5.0 (결격 1)
grep -c '"kind":' bundle/manifests/mongodb-operator.clusterserviceversion.yaml
  # → 6 이상 (alm-examples 3 CRD + 내장 ClusterIssuer ref 2 + MongoDBBackup ref 1, 결격 2)
grep -E "(replaces|olm\.skipRange):" bundle/manifests/mongodb-operator.clusterserviceversion.yaml
  # → replaces: mongodb-operator.v1.4.23 + olm.skipRange: '>=0.3.0 <1.5.0' (결격 3)
grep "channels.v1:" bundle/metadata/annotations.yaml
  # → stable,alpha + default stable (결격 4)
grep "^  maturity:" bundle/manifests/mongodb-operator.clusterserviceversion.yaml
  # → maturity: stable (결격 5)
operator-sdk bundle validate ./bundle --select-optional suite=operatorframework
  # → PASS
```

## Phase D — OLM v0 only path 폐기 (2026-05-17)

**결정**: 본 ADR 의 Phase A/B/C 가 정립한 OLM v0 외부 사용자 운영 수준 (CSV 5 결격 해소 + KeiaiLab cluster v0 install + community-operators upstream sync) 중 *v0 cluster install path* 와 *community-operators sync 자동화* 영구 폐기. mongodb-operator distribution = OLM v1 + Helm 의 2-path matrix 로 단순화.

**근거**:
- ADR-0029 의 OLM v1 (operator-controller v1.8.0) 라이브 검증 완료 → KeiaiLab cluster 의 single canonical install path 로 채택
- community-operators v0.3.0 등록이 8 개월 stale — OperatorHub.io 채널 실질 작동 안 함 (사용자 영향 미미)
- OLM v0 / v1 두 cluster 자원 (`CatalogSource` vs `ClusterCatalog`) 동시 유지 = 중복 cluster 자원 + maintainer fork PR auth dependency 누적

**제거 대상**:
- `deploy/olm/` 디렉토리 전체 (CatalogSource + Subscription + OperatorGroup + NetworkPolicies + ArgoCD App + namespace + kustomization + README) — *영구 폐기*
- `.github/workflows/release.yml` 의 `sync-community-operators` job (line 176~228) — *영구 폐기* (ADR-0027 Deprecated)
- README.md / AGENTS.md / GOVERNANCE.md / ARCHITECTURE.md / INSTALL.md 의 OLM v0 path 참조 — 모두 정리

**유지 대상** (v0/v1 공유 input):
- `bundle/` + `bundle.Dockerfile` + `config/manifests/` — OLM v1 ClusterCatalog 의 backing bundle (registry+v1 mediatype 은 v1 controller 가 그대로 소비)
- `Makefile` 의 `bundle` / `bundle-build` / `bundle-push` / `catalog-build` / `catalog-push` target — v1 ClusterCatalog image build chain
- `deploy/catalog/` (Phase D 사전 commit `3bd0406` 에서 `deploy/olm/catalog/` 에서 이동) — FBC catalog source

**Consequences**:
- 외부 사용자 distribution 경로: ① helm chart (`helm install mongodb-operator/mongodb-operator`) ② OLM v1 (`kubectl apply -f deploy/olm-v1/`). 기존 OperatorHub.io 사용자는 두 경로 중 선택해서 마이그레이션.
- repo 크기 ~350 lines + 60 lines 문서 감소. bundle/ 유지로 OperatorHub 재등록 *옵션* 보존 (정책 reversal 가능).
- ADR-0027 Status: `Accepted` → `Deprecated (superseded by ADR-0028 Phase D)`.
- ADR-0029 Status: `Proposed/In-Progress` → `Accepted` (실 cutover 적용).
- `CHANGELOG.md` Unreleased 섹션에 BREAKING CHANGES 블록 추가.

**Verification**:
```bash
# 1. v0 자원 cluster 잔존 사전 cleanup (사용자 실행 의무)
kubectl delete subscription mongodb-operator -n olm --ignore-not-found
kubectl delete operatorgroup mongodb-operator -n olm --ignore-not-found
kubectl delete catalogsource keiailab-operators -n olm --ignore-not-found

# 2. OLM v1 single path 가동
kubectl get clusterextension mongodb-operator \
  -o jsonpath='{.status.conditions[?(@.type=="Installed")].status}{"\n"}'
# 기대: True
kubectl get clustercatalog keiailab-operators \
  -o jsonpath='{.status.conditions[?(@.type=="Serving")].status}{"\n"}'
# 기대: True

# 3. ArgoCD reconcile
kubectl get application -n argocd mongodb-operator-olm-v1 \
  -o jsonpath='{.status.sync.status}/{.status.health.status}{"\n"}'
# 기대: Synced/Healthy

# 4. release pipeline 정합
gh workflow view release.yml --repo keiailab/mongodb-operator
# sync-community-operators job 미존재 확인
```

<!-- live-verified: 2026-05-17 -->
Phase D plan: `~/.claude/plans/olm-v1-only-lucky-sloth.md`.
