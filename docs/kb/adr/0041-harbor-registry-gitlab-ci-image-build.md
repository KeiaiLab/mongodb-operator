# ADR-0041: 컨테이너 이미지 빌드·발행 경로 이관 — GHCR 로컬 push → GitLab CI + Harbor

- 상태: Accepted
- 날짜: 2026-07-15
- 관련: RFC-0125 (Harbor registry 이관) / RFC-0127 (remote buildkitd) / RFC-0070 (OSS dual-remote — GitHub canonical) / GOVERNANCE §2.3 (linux/amd64 단일)

## Context

v1.16.6 릴리스가 두 지점에서 막혔다.

**1) `make validate` 가 선재 결함으로 항상 FAIL** — `validate` 타깃이 존재하지 않는
`charts/mongodb-cluster` 를 lint 했다:

```
$ helm lint charts/mongodb-cluster
Error: ... charts/mongodb-cluster/Chart.yaml: no such file or directory
```

`charts/` 에는 `mongodb-operator` 단 하나만 실재한다. `validate` 는 `gate` 의 구성원이고
`gate` 는 `release` 의 Step 1 이므로, **릴리스 파이프라인 전체가 이 한 줄에 막혀 있었다.**

**2) 이미지 push 가 사람의 키체인에 종속** — `release` 의 Step 3 는
`docker buildx build --push ghcr.io/keiailab/mongodb-operator:...` 였다. GHCR 자격이 로컬
macOS 키체인에 묶여 있어 push 마다 **GUI 승인 프롬프트**가 뜬다. 즉 릴리스가 *사람이 앉아 있는
특정 머신*에 종속된 SPOF 였고, 어떤 CI/자동화 경로로도 이미지를 낼 수 없었다.

한편 keiailab 플릿은 이미 이미지 빌드/발행 표준을 갖고 있다:

- **RFC-0127** — 클러스터 상주 rootless buildkitd Deployment(mTLS). CI job pod 는 `buildctl`
  클라이언트일 뿐 무권한. (kaniko 는 upstream archived 로 폐기.)
- **RFC-0125** — registry = `harbor.keiailab.dev`, 경로 규약 `harbor.keiailab.dev/$CI_PROJECT_PATH`,
  push 자격 = Harbor robot(OpenBao `apps/harbor` → ESO → runner `harbor-push` secret mount).

즉 *자격이 이미 클러스터 안에 있다*. 로컬 키체인을 살려두는 것이 유일한 병목이었다.

## Decision

**1. `validate` 는 실재하는 차트만 순회한다.** 하드코딩 목록 대신 `charts/*/Chart.yaml` wildcard
순회 (`sync-crds` 와 동일 패턴) — 차트 추가/삭제에 자동 정합, 유령 차트 lint 재발 불가.

**2. 컨테이너 이미지의 빌드·발행 SSOT = GitLab CI + Harbor.**

| 대상 | 신규 경로 |
|---|---|
| operator manager | `harbor.keiailab.dev/keiailab/platform/mongodb-operator` |
| OLM bundle | `harbor.keiailab.dev/keiailab/platform/mongodb-operator-bundle` |
| OLM catalog (FBC) | `harbor.keiailab.dev/keiailab/platform/mongodb-operator-catalog` |

- GitLab 프로젝트 경로 = **`keiailab/platform/mongodb-operator`** — §14.2 카테고리 결정트리상
  "클러스터 *안에서* 도는 워크로드" = `platform`(cf. `infra` = 클러스터를 *만드는* 것). 라이브에서도
  operator 는 `data` ns 안에서 돈다. Harbor 경로는 `$CI_PROJECT_PATH` 규약을 그대로 따른다.
- `.gitlab-ci.yml` `build:image` — `tags:[k8s]`, `buildctl` → `tcp://buildkitd.gitlab-runner.svc:1234`(mTLS),
  `linux/amd64` 단일(GOVERNANCE §2.3). MR-event = build-only(`PUSH_IMAGE=false`, 미머지 코드 태그 차단),
  `stable` push = 발행. `PUSH_IMAGE` 의 **variables 기본값은 `"false"`**(fail-closed — 신규 rule 이
  설정을 누락해도 조용한 push 회귀가 없다). 태그 = `:$CI_COMMIT_SHA` · `:$APP_VERSION`(Chart.yaml
  `appVersion` — Go repo 라 pyproject 앵커 부재) · `:latest`.
- `harbor-auth`/`buildkit-certs` 부재 시 **exit 1** — 익명 push·무TLS 로의 침묵 회귀 차단.

**3. `make release` 는 더 이상 이미지를 push 하지 않는다.** Step 3 은 대상 태그가 Harbor 에
실재하는지 *확인만* 한다(미도달 시 warn). 태그 / GitHub Release / helm package 는 로컬 유지.

**4. 범위는 *이미지* 한정 — RFC-0070 은 그대로 유효 (좁은 해석).**
코드 canonical 은 **GitHub 유지**(`github.com/keiailab/mongodb-operator`), helm **chart** 발행도
**GHCR OCI + ArtifactHub 유지**(`oci://ghcr.io/keiailab/charts`). 본 ADR 이 옮기는 것은
**컨테이너 이미지의 빌드·발행 경로 하나뿐**이다. GitLab 은 (미러 생성 후) *빌드 실행 평면*이지
개발 진입점이 아니다. 현 지시("케플은 gitlab + harbor 로 처리")만으로 개발 경로까지 GitLab 으로
옮긴다고 확정할 수 없어 **좁게 해석**했다 — 확장하려면 별도 RFC/ADR.

## Consequences

**긍정**
- 릴리스가 사람의 키체인·머신에서 분리된다. `stable` push → 이미지 자동 발행.
- 빌드 자원이 클러스터 buildkitd(레이어 캐시 PVC 상주)로 이동 → 로컬 빌드 시간·디스크 소거.
- MR 파이프라인이 build-only 로 Dockerfile 회귀를 잡는다(현재 GitLab 에는 그 게이트가 아예 없었다).

**부정 / residual (정직 표기)**
- **`harbor.keiailab.dev` 는 NetBird 내부망 전용**(RFC-0082 — `*.keiailab.dev` = grey, public DNS
  A/AAAA 부재). 따라서 **외부 OSS 사용자는 Harbor 에서 이미지를 pull 할 수 없다.** 그런데 본 cycle 에
  차트 기본값(`image.repository`) · CSV `containerImage` · `config/manager` 까지 Harbor 로 바꿨으므로,
  **공개 소비 경로는 현재 gate_gap** 이다.
- **단, GHCR 공개 발행은 로컬 키체인과 무관하게 이미 살아 있다** — `.github/workflows/release.yml` 이
  `v*` 태그 push 시 `secrets.GITHUB_TOKEN` 으로 `ghcr.io/keiailab/mongodb-operator` 를 빌드·push 한다
  (`linux/amd64`, 로컬 자격 불요). 즉 막혀 있던 것은 **`make release` Step 3 의 *중복* 로컬 push 하나**
  였고, 공개 이미지 자체는 태그만 밀면 나온다. 따라서 공개 배포를 유지하려면 **신규 미러 잡이 아니라**
  다음 1-line 결정이면 된다:
  - `charts/mongodb-operator/values.yaml` 의 `image.repository` 를 **GHCR 로 되돌린다**
    (= 공개 소비자는 GHCR / 내부 클러스터는 `--set image.repository=harbor...` 또는 values override).
  - 이 경우 registry 는 *2-plane* 이 된다: **GHCR = 공개 발행(GH Actions, 태그 트리거)** /
    **Harbor = 내부 CI·클러스터 소비(GitLab CI, stable push)**. 본 ADR 은 사용자 지시("operator 이미지도
    harbor")를 그대로 반영해 **단일 Harbor** 로 두었고, 위 2-plane 전환은 *열려 있는 결정*이다.
- `deploy/catalog/catalog/mongodb-operator/catalog.yaml` 의 bundle digest 는 구 GHCR 시절
  digest 다. `make bundle-push && make catalog-build` 로 Harbor digest 재주입 전까지 **dangling**.
- `deploy/olm-v1/` 의 imagePullSecret 이름은 여전히 `ghcr-keiailab-pull` — Harbor robot pull
  secret 으로의 교체는 라이브 클러스터 작업이라 본 cycle 범위 밖(수동 후속).
- GitLab 미러 프로젝트 `keiailab/platform/mongodb-operator` 는 **아직 미생성** — 생성 전까지
  `build:image` 는 돌지 않는다(코드만 준비된 상태).

## Alternatives Considered

- **GHCR 유지 + GitHub Actions 로 push** — GOVERNANCE §2.3 GitHub Actions 영구 금지(RFC-0002).
  기각.
- **GHCR 유지 + GitLab CI 에서 GHCR PAT 로 push** — 자격이 GitLab Variables 로 옮겨가 자동화는
  되지만, 플릿 표준 registry(Harbor)와 이원화되고 buildkitd 레이어 캐시·robot 자격 인프라를
  중복 구성해야 한다(§2.6 복잡성 최소화 위반). 기각 — 단 *공개 미러*로는 여전히 유효한 후속 경로.
- **로컬 키체인 자격 문제만 우회(credential helper 교체)** — SPOF 를 사람 머신에 남긴 채
  증상만 가린다. 기각.
