# GitHub Container Registry (GHCR) 설정 가이드

## 개요

MongoDB Operator는 Docker Hub 대신 **GitHub Container Registry (GHCR)**를 사용하여 Docker 이미지를 게시합니다. GHCR은 GitHub Actions와 완전히 통합되어 있어 별도의 Secrets 설정이 필요하지 않습니다.

## GHCR의 장점

### Docker Hub 대비 우위
- ✅ **자동 인증**: GITHUB_TOKEN 자동 제공, Secrets 설정 불필요
- ✅ **무료 공개 이미지**: 용량 제한 없음
- ✅ **GitHub 통합**: Repository와 연결, 권한 관리 통합
- ✅ **Rate Limit 없음**: 인증된 사용자는 무제한 Pull
- ✅ **OCI 표준**: Docker, containerd, Podman 모두 지원
- ✅ **보안 스캔**: GitHub Security 자동 통합

### 비용 비교
| 항목 | Docker Hub Free | GHCR |
|------|----------------|------|
| 공개 이미지 | 1개 | 무제한 |
| Pull 제한 | 익명: 100/6시간<br>인증: 200/6시간 | 인증: 무제한 |
| 저장소 크기 | 제한 없음 | 제한 없음 |
| 빌드 속도 | 표준 | 표준 |

## 자동 설정 (GitHub Actions)

### 권한 설정

GitHub Actions 워크플로우에서는 `permissions` 섹션만 추가하면 됩니다:

```yaml
permissions:
  contents: read
  packages: write  # GHCR에 푸시하기 위한 권한
```

### 인증

GITHUB_TOKEN은 자동으로 제공되므로 별도 설정 불필요:

```yaml
- name: Log in to GitHub Container Registry
  uses: docker/login-action@v3
  with:
    registry: ghcr.io
    username: ${{ github.actor }}
    password: ${{ secrets.GITHUB_TOKEN }}
```

### 이미지 경로

```yaml
env:
  REGISTRY: ghcr.io
  IMAGE_NAME: keiailab/mongodb-operator
```

## 로컬 개발자 설정

### Personal Access Token (PAT) 생성

로컬에서 GHCR에 이미지를 푸시하려면 PAT가 필요합니다:

```bash
# GitHub 웹사이트에서:
1. Settings > Developer settings > Personal access tokens > Tokens (classic)
2. "Generate new token (classic)" 클릭
3. Note: "GHCR Access - MongoDB Operator"
4. Scopes 선택:
   - ✅ write:packages (이미지 푸시용)
   - ✅ read:packages (이미지 풀용)
   - ✅ delete:packages (이미지 삭제용, 선택사항)
5. "Generate token" 클릭
6. 토큰을 안전한 곳에 복사 (다시 볼 수 없음!)
```

### GHCR 로그인

```bash
# PAT를 사용하여 로그인
echo "ghp_xxxxxxxxxxxxxxxxxxxx" | docker login ghcr.io -u keiailab --password-stdin

# 로그인 확인
docker info | grep -A 5 "Registry"
```

### 이미지 빌드 및 푸시

```bash
# linux/amd64 단일 아키 이미지 빌드 (RFC-0002 §2)
docker buildx build \
  --platform linux/amd64 \
  --tag ghcr.io/keiailab/mongodb-operator:1.0.0 \
  --tag ghcr.io/keiailab/mongodb-operator:latest \
  --push \
  .

# 빌드 성공 확인
docker manifest inspect ghcr.io/keiailab/mongodb-operator:1.0.0
```

**단일 아키텍처 빌드 (빠름):**

```bash
# 로컬 아키텍처만 빌드
docker build -t ghcr.io/keiailab/mongodb-operator:1.0.0 .

# GHCR에 푸시
docker push ghcr.io/keiailab/mongodb-operator:1.0.0
```

## 공개 이미지 설정

기본적으로 GHCR 이미지는 비공개입니다. 공개로 설정하려면:

### 웹 인터페이스에서

```bash
1. https://github.com/orgs/keiailab/packages 방문
   # 또는 개인 계정: https://github.com/keiailab?tab=packages

2. "mongodb-operator" 패키지 클릭

3. "Package settings" 클릭

4. "Danger Zone" 섹션에서:
   - "Change visibility" 클릭
   - "Public" 선택
   - 패키지 이름 확인 입력
   - "I understand, change package visibility" 클릭
```

### GitHub CLI 사용

```bash
# 패키지를 공개로 설정
gh api \
  --method PATCH \
  -H "Accept: application/vnd.github+json" \
  /user/packages/container/mongodb-operator \
  -f visibility=public
```

## 이미지 사용

### Kubernetes에서 사용

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mongodb-operator
spec:
  template:
    spec:
      containers:
      - name: manager
        image: ghcr.io/keiailab/mongodb-operator:1.0.0
        imagePullPolicy: IfNotPresent
```

### Helm에서 사용

```bash
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --set image.repository=ghcr.io/keiailab/mongodb-operator \
  --set image.tag=1.0.0
```

**values.yaml에서:**

```yaml
image:
  repository: ghcr.io/keiailab/mongodb-operator
  pullPolicy: IfNotPresent
  tag: "1.0.0"
```

## 이미지 태그 전략

```bash
# 버전별 태그
ghcr.io/keiailab/mongodb-operator:1.0.0
ghcr.io/keiailab/mongodb-operator:1.0
ghcr.io/keiailab/mongodb-operator:1

# 최신 태그
ghcr.io/keiailab/mongodb-operator:latest

# 브랜치 태그
ghcr.io/keiailab/mongodb-operator:main

# 커밋 SHA 태그
ghcr.io/keiailab/mongodb-operator:sha-b35177c
```

## 검증

### 이미지 Pull 테스트

```bash
# 공개 이미지 Pull (인증 불필요)
docker pull ghcr.io/keiailab/mongodb-operator:1.0.0

# 이미지 정보 확인
docker inspect ghcr.io/keiailab/mongodb-operator:1.0.0

# linux/amd64 단일 아키 매니페스트 확인 (RFC-0002 §2)
docker manifest inspect ghcr.io/keiailab/mongodb-operator:1.0.0 | jq '.manifests[].platform'

# 예상 출력:
# {
#   "architecture": "amd64",
#   "os": "linux"
# }
# {
#   "architecture": "arm64",
#   "os": "linux"
# }
```

### 웹에서 확인

```bash
# 패키지 페이지
https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator

# 확인 항목:
- ✅ Visibility: Public
- ✅ Tags: 1.0.0, latest
- ✅ OS/Arch: linux/amd64 (단일 아키, RFC-0002 §2)
- ✅ Linked to repository
- ✅ README 표시
```

## 권한 관리

### Organization 패키지

Organization에서 패키지를 관리하는 경우:

```bash
# Organization 멤버에게 권한 부여
1. Package settings > Manage access
2. "Invite teams or people" 클릭
3. 멤버 선택 및 역할 부여:
   - Read: Pull만 가능
   - Write: Pull, Push 가능
   - Admin: 모든 작업 가능
```

### Repository 연결

패키지를 특정 Repository에 연결:

```bash
1. Package settings
2. "Connect repository" 클릭
3. "keiailab/mongodb-operator" 선택
4. "Connect repository" 클릭

# 이제 Repository에서 Packages 탭에 표시됨
```

## 보안

### 취약점 스캔

GitHub는 자동으로 이미지를 스캔합니다:

```bash
# 웹에서 확인:
https://github.com/keiailab/mongodb-operator/security

# 또는 CLI:
gh api /repos/keiailab/mongodb-operator/code-scanning/alerts
```

### 서명 (Sigstore/Cosign)

이미지 서명 지원 (선택사항):

```bash
# Cosign 설치
brew install cosign

# 이미지 서명
cosign sign ghcr.io/keiailab/mongodb-operator:1.0.0

# 서명 검증
cosign verify ghcr.io/keiailab/mongodb-operator:1.0.0
```

## 마이그레이션 체크리스트

Docker Hub에서 GHCR로 마이그레이션 시:

- [x] `.github/workflows/docker-build.yml` 업데이트
- [x] `.github/workflows/release.yml` 업데이트
- [x] `charts/mongodb-operator/Chart.yaml` 이미지 경로 변경
- [x] `charts/mongodb-operator/values.yaml` repository 변경
- [x] `README.md` 배지 및 설명 업데이트
- [x] 문서 전체에서 이미지 경로 업데이트
- [ ] GHCR 패키지를 Public으로 설정
- [ ] 기존 사용자에게 마이그레이션 공지
- [ ] v1.0.0 태그 재푸시하여 GHCR 이미지 빌드 트리거

## 사용자 마이그레이션 가이드

기존 사용자를 위한 안내:

```bash
# 기존 (Docker Hub)
image: keiailab/mongodb-operator:1.0.0

# 새로운 (GHCR)
image: ghcr.io/keiailab/mongodb-operator:1.0.0

# Helm values 업데이트
helm upgrade mongodb-operator mongodb-operator/mongodb-operator \
  --reuse-values \
  --set image.repository=ghcr.io/keiailab/mongodb-operator
```

## 트러블슈팅

### 문제 1: "unauthorized: unauthenticated"

**원인**: GHCR 로그인 필요 (비공개 이미지인 경우)

**해결**:
```bash
# PAT로 로그인
echo "ghp_xxxxxxxxxxxxxxxxxxxx" | docker login ghcr.io -u keiailab --password-stdin

# 또는 패키지를 Public으로 설정
```

### 문제 2: "denied: permission_denied"

**원인**: GITHUB_TOKEN 권한 부족

**해결**:
```yaml
# 워크플로우에 permissions 추가
permissions:
  packages: write
```

### 문제 3: "manifest unknown"

**원인**: 태그가 존재하지 않음

**해결**:
```bash
# 사용 가능한 태그 확인
docker pull ghcr.io/keiailab/mongodb-operator:latest

# 또는 웹에서 확인
# https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator
```

## 참고 자료

- [GitHub Container Registry 문서](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [GitHub Actions와 GHCR](https://docs.github.com/en/packages/managing-github-packages-using-github-actions-workflows/publishing-and-installing-a-package-with-github-actions)
- [GHCR 공개 설정](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)

## 다음 단계

GHCR 설정 완료 후:

1. ✅ 패키지를 Public으로 설정
2. ✅ v1.0.0 워크플로우 재실행
3. ✅ GHCR에서 이미지 확인
4. ✅ Helm 차트로 설치 테스트
5. ✅ 기존 사용자에게 마이그레이션 공지

**장점**: Docker Hub Secrets 오류 없이 완전 자동화된 릴리스 프로세스!
