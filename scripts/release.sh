#!/usr/bin/env bash
#
# mongodb-operator 수동 release 스크립트.
#
# 사용:
#   bash scripts/release.sh v1.3.2 [<registry>]
#
# 동작:
#   1. tag 형식 검증 (vMAJOR.MINOR.PATCH[-prerelease]).
#   2. working tree clean 검증.
#   3. 로컬 게이트 (`make gate`).
#   4. Chart.yaml + CHANGELOG version 정합 검증.
#   5. linux/amd64 단일 아키 이미지 빌드 + push (docker buildx default builder, RFC 0002 §2).
#   6. git tag + push.
#   7. gh release create — chart .tgz + (옵션) SBOM 첨부.
#   8. helm-publish (gh-pages).
#
# 사전조건:
#   - git remote 'origin' 설정.
#   - docker buildx 활성화 (기본 빌더).
#   - 컨테이너 레지스트리 로그인 (docker login ghcr.io).
#   - gh CLI 인증 (gh auth status).
#   - (선택) git-cliff: brew install git-cliff (CHANGELOG 자동 생성).
#   - (선택) syft: brew install syft (SBOM 생성).
#
# CLAUDE.md §2 (RFC 0002 — GHA 영구 금지) 준수. 본 스크립트는 *수동* 실행.
# 기존 Makefile `release` target 의 본문을 scripts/ 로 추출 — 외부 호출자가
# Makefile 의존 없이 release pipeline 을 직접 호출 가능. Makefile target 호환 유지.

set -euo pipefail

usage() {
  echo "Usage: $0 <version> [registry]"
  echo "  version: vMAJOR.MINOR.PATCH[-prerelease] (e.g. v1.3.2 또는 v1.3.2-beta.6)"
  echo "  registry: image registry prefix (default: ghcr.io/keiailab)"
  exit 1
}

[[ $# -lt 1 ]] && usage

VERSION="$1"
REGISTRY="${2:-ghcr.io/keiailab}"
IMAGE_REPOSITORY="${REGISTRY}/mongodb-operator"
HELM_CHART="${HELM_CHART:-charts/mongodb-operator}"
RELEASE_TMP="${RELEASE_TMP:-/tmp/mongodb-operator-release}"
PLATFORMS="${PLATFORMS:-linux/amd64}"

# 1. tag 형식 검증.
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
  echo "ERROR: version must be vMAJOR.MINOR.PATCH[-prerelease] (got: $VERSION)" >&2
  exit 1
fi
TARGET_VER="${VERSION#v}"

# 2. working tree clean.
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "ERROR: working tree dirty. Commit or stash first." >&2
  git status --short
  exit 1
fi

# 3. 로컬 게이트.
echo "==> Step 1/6: 로컬 게이트 (make gate)"
make gate

# 4. version 정합 검증.
echo "==> Step 2/6: Chart.yaml version 정합 검증"
CHART_VER="$(awk '/^version:/ { print $2; exit }' "${HELM_CHART}/Chart.yaml")"
if [[ "$CHART_VER" != "$TARGET_VER" ]]; then
  echo "ERROR: Chart.yaml version=$CHART_VER, 기대=$TARGET_VER" >&2
  echo "  먼저 ${HELM_CHART}/Chart.yaml 의 version + appVersion 갱신" >&2
  exit 1
fi
echo "OK Chart.yaml version=${TARGET_VER}"

# 5. 이미지 빌드 + push.
echo "==> Step 3/6: Docker image build + push (${PLATFORMS}, default builder)"
docker --context=default buildx build --platform "${PLATFORMS}" \
  -t "${IMAGE_REPOSITORY}:${VERSION}" \
  -t "${IMAGE_REPOSITORY}:${TARGET_VER}" \
  --push .

# 6. git tag + push.
echo "==> Step 4/6: Git tag + push"
if git tag -l "$VERSION" | grep -q .; then
  echo "WARN: tag $VERSION 이미 존재 — skip tag 생성"
else
  git tag -a "$VERSION" -m "$VERSION"
fi
git push origin "$VERSION"

# 7. gh release.
echo "==> Step 5/6: GitHub Release"
PREFLAG=""
case "$VERSION" in
  *beta*|*rc*|*alpha*) PREFLAG="--prerelease" ;;
esac
mkdir -p "${RELEASE_TMP}"
helm package "${HELM_CHART}" -d "${RELEASE_TMP}/"

NOTES_FLAG="--notes \"Release ${VERSION}. 변경 내역은 CHANGELOG.md 참조.\""
if command -v git-cliff >/dev/null 2>&1; then
  if git-cliff --strip all --tag "$VERSION" --unreleased > "/tmp/release-notes-${VERSION}.md" 2>/dev/null; then
    NOTES_FLAG="--notes-file /tmp/release-notes-${VERSION}.md"
  fi
fi

SBOM_ASSET=""
if command -v syft >/dev/null 2>&1; then
  echo "==> syft SBOM 생성"
  if syft scan "${IMAGE_REPOSITORY}:${VERSION}" -o spdx-json -q > "/tmp/mongodb-operator-${VERSION}.spdx.json" 2>/dev/null; then
    SBOM_ASSET="/tmp/mongodb-operator-${VERSION}.spdx.json"
  fi
fi

if gh release view "$VERSION" -R keiailab/mongodb-operator >/dev/null 2>&1; then
  echo "WARN: GH release $VERSION 이미 존재 — skip"
else
  eval gh release create "$VERSION" -R keiailab/mongodb-operator $PREFLAG \
    --title "$VERSION" \
    $NOTES_FLAG \
    "${RELEASE_TMP}/mongodb-operator-${TARGET_VER}.tgz" \
    $SBOM_ASSET
fi

rm -rf "${RELEASE_TMP}"

# 8. helm-publish.
echo "==> Step 6/6: Helm chart publish to gh-pages"
bash scripts/helm-publish.sh

echo
echo "==================================================="
echo "릴리스 완료: $VERSION"
echo "==================================================="
echo "  이미지: ${IMAGE_REPOSITORY}:${VERSION}"
echo "  chart:  ${HELM_CHART} (${TARGET_VER})"
echo "  helm:   https://keiailab.github.io/mongodb-operator"
echo "  GH release: https://github.com/keiailab/mongodb-operator/releases/tag/${VERSION}"
echo "  ArtifactHub: ~30분 내 인덱싱"
