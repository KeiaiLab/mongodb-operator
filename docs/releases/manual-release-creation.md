# GitHub Release 수동 생성 가이드

## 개요

GitHub Actions 워크플로우가 실패한 경우, GitHub Release를 수동으로 생성할 수 있습니다. 이 가이드는 v1.0.0 릴리스를 수동으로 생성하는 방법을 설명합니다.

## 전제 조건

1. **Git 태그 존재 확인**
   ```bash
   git tag -l | grep v1.0.0
   # 출력: v1.0.0
   ```

2. **GitHub CLI 설치** (선택사항, 권장)
   ```bash
   gh --version
   # 또는 설치:
   # macOS: brew install gh
   # Linux: https://github.com/cli/cli#installation
   ```

## 방법 1: GitHub CLI 사용 (권장)

### 기본 릴리스 생성

```bash
# CHANGELOG.md에서 릴리스 노트 추출
cat > /tmp/release-notes-v1.0.0.md <<'EOF'
## Summary
Initial stable (GA) release with comprehensive CI/CD, documentation, and examples. This release marks production-readiness of the MongoDB Operator with full enterprise-grade infrastructure for open source maintenance.

## What's New

### Core Features
- **MongoDB ReplicaSet**: Deploy highly available 3+ member replica sets with automatic failover
- **Sharded Cluster**: Deploy distributed clusters with config servers, shards, and mongos routers
- **TLS Encryption**: Automatic TLS certificate management with cert-manager integration
- **Authentication**: SCRAM-SHA-256 authentication with keyfile support for internal cluster communication
- **Monitoring**: Prometheus metrics export with ServiceMonitor support
- **Backup/Restore**: Automated backups to S3-compatible storage or PVC
- **Auto-scaling**: Horizontal Pod Autoscaler support for Mongos routers

### CI/CD & Infrastructure (6 Workflows)
- **CI Pipeline**: Continuous integration with Go tests, linting, and Docker build verification
- **Docker Build**: Automated multi-arch image building (linux/amd64, linux/arm64) and publishing to Docker Hub
- **Release Automation**: Complete release automation for GitHub releases with binary artifacts
- **Helm Publishing**: Helm chart packaging and publishing to gh-pages branch
- **Security Scanning**: Comprehensive security scanning (dependencies, containers, licenses)
- **Helm Testing**: Automated Helm chart linting and testing

### Documentation (12+ Documents)
- **Getting Started**: Quick installation and basic usage guide
- **Advanced Guides**: TLS, backup/restore, monitoring, scaling
- **Developer Docs**: Architecture, development setup, testing strategy
- **Release Guides**: v1.0.0 release process, Docker Hub setup
- **Repository Docs**: GitHub settings, issue management, PR process

### Examples (7 Production-Ready)
- **Minimal**: Simple ReplicaSet and Sharded Cluster deployments
- **Production**: Full-featured deployments with TLS, monitoring, backup
- **Monitoring**: Prometheus stack integration examples
- **Backups**: S3 backup configuration examples

### Project Infrastructure
- **Issue Templates**: Bug report, feature request, question templates
- **PR Template**: Comprehensive pull request checklist
- **Dependabot**: Automated dependency updates for Go modules and GitHub Actions
- **Pre-commit Hooks**: Code quality enforcement with automated formatting
- **Codecov Integration**: Automated code coverage tracking
- **Security Policy**: SECURITY.md with vulnerability reporting guidelines
- **Code of Conduct**: Contributor Covenant code of conduct
- **Contributing Guide**: Detailed contribution guidelines

## Breaking Changes
None. This is a major release representing stabilization of the project with all features from previous pre-releases. No deprecations or breaking changes introduced.

## Installation

### Helm Repository
```bash
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

helm install mongodb-operator mongodb-operator/mongodb-operator \
  --version 1.0.0 \
  --namespace mongodb-operator-system \
  --create-namespace
```

### Artifact Hub
Visit: https://artifacthub.io/packages/helm/mongodb-operator/mongodb-operator

## Upgrade from 0.0.x

```bash
# Backup existing MongoDB clusters
kubectl get mongodb -A -o yaml > mongodb-backup.yaml
kubectl get mongodbsharded -A -o yaml > mongodbsharded-backup.yaml

# Upgrade operator
helm upgrade mongodb-operator mongodb-operator/mongodb-operator \
  --version 1.0.0 \
  --namespace mongodb-operator-system

# Existing MongoDB clusters will continue running
# No manual intervention required
```

## Verification

```bash
# Check operator version
kubectl get deployment -n mongodb-operator-system \
  mongodb-operator-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].image}'

# Expected: ghcr.io/eightynine01/mongodb-operator:1.0.0

# Verify CRDs
kubectl get crd | grep mongodb
# mongodbs.mongodb.keiailab.com
# mongodbshardeds.mongodb.keiailab.com
# mongodbbackups.mongodb.keiailab.com
```

## Known Issues

1. **Docker Hub Authentication**: Initial release workflow requires Docker Hub secrets to be configured. See [Docker Hub Setup Guide](./docker-hub-setup.md)

## What's Next

See [Roadmap](../ROADMAP.md) for planned features in future releases:
- Point-in-Time Recovery (PITR)
- LDAP/OIDC authentication
- Multi-region support
- Automated version upgrades
- Performance advisor

## Contributors

Special thanks to all contributors who made this release possible!

---

**Full Changelog**: https://github.com/eightynine01/mongodb-operator/compare/v0.0.5...v1.0.0
EOF

# GitHub Release 생성
gh release create v1.0.0 \
  --title "v1.0.0 - MongoDB Operator GA Release" \
  --notes-file /tmp/release-notes-v1.0.0.md \
  --verify-tag

# 릴리스 확인
gh release view v1.0.0
```

### 바이너리 아티팩트 추가 (선택사항)

GitHub Actions 워크플로우에서 빌드된 아티팩트를 릴리스에 추가:

```bash
# 워크플로우 아티팩트 다운로드
gh run download 21248135426

# 바이너리를 릴리스에 업로드
gh release upload v1.0.0 \
  binary-linux-arm64/mongodb-operator-linux-arm64 \
  binary-darwin-amd64/mongodb-operator-darwin-amd64 \
  checksums/checksums.txt
```

### Helm 차트 추가

```bash
# Helm 차트 패키징 (이미 gh-pages에 있음)
curl -LO https://keiailab.github.io/mongodb-operator/mongodb-operator-1.0.0.tgz

# 릴리스에 업로드
gh release upload v1.0.0 mongodb-operator-1.0.0.tgz
```

## 방법 2: 웹 인터페이스 사용

### 1. GitHub Releases 페이지 접속

```
https://github.com/eightynine01/mongodb-operator/releases/new
```

### 2. 릴리스 정보 입력

**Tag version:**
```
v1.0.0
```

**Release title:**
```
v1.0.0 - MongoDB Operator GA Release
```

**Describe this release:**

위의 "방법 1"에서 작성한 릴리스 노트를 복사하여 붙여넣기

### 3. 바이너리 첨부 (선택사항)

"Attach binaries by dropping them here or selecting them." 영역에 파일 드래그:
- mongodb-operator-linux-amd64
- mongodb-operator-linux-arm64
- mongodb-operator-darwin-amd64
- mongodb-operator-darwin-arm64
- checksums.txt
- mongodb-operator-1.0.0.tgz (Helm 차트)

### 4. 릴리스 옵션 설정

- ☐ Set as a pre-release (체크 해제)
- ☑ Set as the latest release (체크)
- ☐ Create a discussion for this release (선택사항)

### 5. Publish release

"Publish release" 버튼 클릭

## 방법 3: API 사용

```bash
# GitHub Personal Access Token 필요
export GITHUB_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"

# 릴리스 노트 파일 준비
RELEASE_NOTES=$(cat /tmp/release-notes-v1.0.0.md)

# API로 릴리스 생성
curl -X POST \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer $GITHUB_TOKEN" \
  https://api.github.com/repos/eightynine01/mongodb-operator/releases \
  -d @- <<EOF
{
  "tag_name": "v1.0.0",
  "name": "v1.0.0 - MongoDB Operator GA Release",
  "body": $(echo "$RELEASE_NOTES" | jq -Rs .),
  "draft": false,
  "prerelease": false,
  "make_latest": "true"
}
EOF
```

## 릴리스 검증

### 1. 릴리스 페이지 확인

```bash
# CLI에서
gh release view v1.0.0

# 웹에서
https://github.com/eightynine01/mongodb-operator/releases/tag/v1.0.0
```

**검증 항목:**
- ✅ 릴리스 제목: "v1.0.0 - MongoDB Operator GA Release"
- ✅ 릴리스 노트: CHANGELOG 내용 포함
- ✅ 바이너리 첨부: 4개 플랫폼 바이너리
- ✅ Helm 차트 첨부: mongodb-operator-1.0.0.tgz
- ✅ "Latest" 배지 표시
- ✅ 릴리스 날짜: 2026-01-22

### 2. 다운로드 테스트

```bash
# 바이너리 다운로드
curl -LO https://github.com/eightynine01/mongodb-operator/releases/download/v1.0.0/mongodb-operator-linux-amd64

# 실행 권한 부여
chmod +x mongodb-operator-linux-amd64

# 버전 확인
./mongodb-operator-linux-amd64 version
# 예상 출력: mongodb-operator version 1.0.0
```

### 3. Helm 차트 설치 테스트

```bash
# Helm 레포지토리 업데이트
helm repo update

# v1.0.0 버전 확인
helm search repo mongodb-operator --versions | grep 1.0.0

# 테스트 설치
helm install test-release mongodb-operator/mongodb-operator \
  --version 1.0.0 \
  --namespace test \
  --create-namespace \
  --dry-run
```

## 릴리스 편집 (필요 시)

### CLI에서

```bash
# 릴리스 노트 수정
gh release edit v1.0.0 --notes-file /tmp/updated-notes.md

# 제목 수정
gh release edit v1.0.0 --title "v1.0.0 - Updated Title"

# 바이너리 추가
gh release upload v1.0.0 new-binary.tar.gz

# 바이너리 제거
gh release delete-asset v1.0.0 old-binary.tar.gz
```

### 웹에서

```
1. https://github.com/eightynine01/mongodb-operator/releases 접속
2. v1.0.0 릴리스 우측 "Edit" 버튼 클릭
3. 내용 수정 후 "Update release" 클릭
```

## 릴리스 삭제 (주의!)

**경고:** 릴리스 삭제는 신중하게 수행해야 합니다. 사용자가 이미 다운로드했을 수 있습니다.

```bash
# 릴리스 삭제 (태그는 유지)
gh release delete v1.0.0 --yes

# 릴리스와 태그 모두 삭제
gh release delete v1.0.0 --yes
git push --delete origin v1.0.0
```

## 체크리스트

릴리스 생성 전:
- [ ] Git 태그 v1.0.0이 푸시됨
- [ ] CHANGELOG.md에 v1.0.0 섹션 작성됨
- [ ] Chart.yaml에 version 1.0.0 설정됨
- [ ] Helm 차트가 gh-pages에 게시됨
- [ ] Docker 이미지가 Docker Hub에 푸시됨 (선택사항)

릴리스 생성 후:
- [ ] GitHub Release 페이지에서 v1.0.0 표시됨
- [ ] "Latest" 배지가 v1.0.0에 있음
- [ ] 릴리스 노트가 올바르게 렌더링됨
- [ ] 바이너리를 다운로드하고 실행 가능
- [ ] Helm 차트 설치 테스트 성공

알림:
- [ ] Twitter/소셜 미디어에 릴리스 공지
- [ ] Artifact Hub에서 업데이트 확인
- [ ] 커뮤니티 포럼에 공지 (선택사항)

## 트러블슈팅

### 문제 1: "tag does not exist"

**원인:** Git 태그가 원격 저장소에 없음

**해결:**
```bash
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

### 문제 2: "release already exists"

**원인:** 동일한 태그로 릴리스가 이미 존재

**해결:**
```bash
# 기존 릴리스 확인
gh release view v1.0.0

# 기존 릴리스 삭제 후 재생성
gh release delete v1.0.0 --yes
# 다시 생성...
```

### 문제 3: 바이너리 업로드 실패

**원인:** 파일 크기 제한 또는 권한 문제

**해결:**
```bash
# 파일 압축
gzip mongodb-operator-linux-amd64

# 재업로드
gh release upload v1.0.0 mongodb-operator-linux-amd64.gz

# 또는 웹 인터페이스 사용
```

## 참고 자료

- [GitHub Releases 문서](https://docs.github.com/en/repositories/releasing-projects-on-github)
- [GitHub CLI 릴리스 명령어](https://cli.github.com/manual/gh_release)
- [시맨틱 버저닝](https://semver.org/)
- [Keep a Changelog](https://keepachangelog.com/)

## 다음 단계

릴리스 생성 완료 후:

1. ✅ Artifact Hub에서 릴리스 확인 (30-60분 후)
2. ✅ Docker Hub에서 이미지 태그 확인
3. ✅ Helm 레포지토리에서 차트 버전 확인
4. ✅ 소셜 미디어/블로그에 릴리스 공지
5. ✅ 다음 릴리스 계획 수립 (ROADMAP.md 참조)
