# Docker Hub 설정 가이드

## 개요

MongoDB Operator의 CI/CD 파이프라인은 Docker 이미지를 자동으로 빌드하고 Docker Hub에 게시합니다. 이를 위해 GitHub Secrets에 Docker Hub 인증 정보를 설정해야 합니다.

## 필수 사전 조건

1. **Docker Hub 계정**
   - Docker Hub 계정이 없다면 https://hub.docker.com 에서 생성
   - 무료 계정으로도 public 이미지 호스팅 가능

2. **Docker Hub Access Token**
   - 보안을 위해 비밀번호 대신 Access Token 사용 권장
   - Settings > Security > Access Tokens에서 생성

## GitHub Secrets 설정

### 1. Access Token 생성 (Docker Hub)

```bash
# Docker Hub 웹사이트에서:
1. https://hub.docker.com/settings/security 방문
2. "New Access Token" 클릭
3. Description: "GitHub Actions - MongoDB Operator"
4. Access permissions: "Read, Write, Delete" 선택
5. "Generate" 클릭
6. 생성된 토큰을 안전한 곳에 복사 (다시 볼 수 없음!)
```

### 2. GitHub Secrets 추가

GitHub 저장소에서:

```bash
1. Settings 탭 클릭
2. 왼쪽 메뉴에서 "Secrets and variables" > "Actions" 선택
3. "New repository secret" 클릭

# DOCKER_USERNAME 추가
- Name: DOCKER_USERNAME
- Value: [Docker Hub 사용자명]

# DOCKER_PASSWORD 추가
- Name: DOCKER_PASSWORD
- Value: [생성한 Access Token]
```

**명령줄에서 설정 (gh CLI 사용):**

```bash
# Docker Hub 사용자명 설정
gh secret set DOCKER_USERNAME --body "<YOUR_DOCKER_USERNAME>"

# Access Token 설정 (대화형)
gh secret set DOCKER_PASSWORD

# 또는 파일에서 읽기
echo "dckr_pat_xxxxxxxxxxxxxxxxxxxxx" | gh secret set DOCKER_PASSWORD
```

### 3. Secrets 확인

```bash
# GitHub Secrets 목록 확인
gh secret list

# 예상 출력:
# DOCKER_PASSWORD  Updated 2026-01-22
# DOCKER_USERNAME  Updated 2026-01-22
```

## Docker 이미지 수동 빌드 (대안)

GitHub Actions가 실패한 경우, 로컬에서 수동으로 빌드하고 푸시할 수 있습니다:

### 전제 조건

```bash
# Docker Buildx 활성화 (linux/amd64 단일 아키 빌드용, RFC-0002 §2)
docker buildx create --name multiarch --use
docker buildx inspect --bootstrap
```

### 빌드 및 푸시

```bash
# Docker Hub 로그인
docker login -u <YOUR_DOCKER_USERNAME>

# linux/amd64 단일 아키 이미지 빌드 및 푸시 (RFC-0002 §2)
docker buildx build \
  --platform linux/amd64 \
  --tag ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0 \
  --tag ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:latest \
  --push \
  .

# 빌드 성공 확인
docker manifest inspect ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0
```

**간단한 단일 아키텍처 빌드:**

```bash
# 로컬 아키텍처만 빌드 (빠름)
docker build -t ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0 .
docker tag ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0 ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:latest

# Docker Hub에 푸시
docker push ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0
docker push ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:latest
```

## GitHub Actions 워크플로우 재실행

Secrets 설정 후 실패한 워크플로우를 재실행할 수 있습니다:

### 웹 인터페이스에서

```bash
1. https://github.com/<YOUR_DOCKER_USERNAME>/mongodb-operator/actions 방문
2. 실패한 "Release" 워크플로우 클릭
3. 우측 상단 "Re-run jobs" > "Re-run failed jobs" 클릭
```

### 명령줄에서

```bash
# 최근 실패한 워크플로우 확인
gh run list --limit 5

# 특정 워크플로우 재실행 (Run ID 사용)
gh run rerun 21248135426

# 또는 실패한 작업만 재실행
gh run rerun 21248135426 --failed
```

## 이미지 태그 전략

MongoDB Operator는 다음 태그를 사용합니다:

```bash
# 버전별 태그
ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0
<YOUR_DOCKER_USERNAME>/mongodb-operator:0.0.7

# 최신 태그
ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:latest

# 개발 태그 (main 브랜치 커밋 시)
<YOUR_DOCKER_USERNAME>/mongodb-operator:dev
<YOUR_DOCKER_USERNAME>/mongodb-operator:sha-b35177c
```

**태그 규칙:**
- `latest`: 최신 안정 릴리스 (v1.0.0)
- `X.Y.Z`: 특정 버전 (시맨틱 버저닝)
- `dev`: 최신 main 브랜치 빌드
- `sha-XXXXXXX`: 특정 커밋 SHA

## 검증

이미지가 올바르게 푸시되었는지 확인:

```bash
# Docker Hub에서 이미지 확인
docker pull ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0

# 이미지 정보 확인
docker inspect ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0

# linux/amd64 단일 아키 매니페스트 확인 (RFC-0002 §2)
docker manifest inspect ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0 | jq '.manifests[].platform'

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

**웹에서 확인:**
- `https://hub.docker.com/r/<YOUR_DOCKER_USERNAME>/mongodb-operator`
- Tags 탭에서 1.0.0 태그 확인
- OS/Architecture 섹션에서 linux/amd64 확인 (multi-arch 금지 — RFC-0002 §2)

## 트러블슈팅

### 문제 1: "denied: requested access to the resource is denied"

**원인:** Docker Hub 인증 실패

**해결:**
```bash
# Docker Hub에 로그인했는지 확인
docker login -u <YOUR_DOCKER_USERNAME>

# GitHub Secrets가 올바르게 설정되었는지 확인
gh secret list

# Secrets 재설정
gh secret set DOCKER_USERNAME
gh secret set DOCKER_PASSWORD
```

### 문제 2: "manifest unknown"

**원인:** 이미지가 아직 푸시되지 않음

**해결:**
```bash
# 로컬에서 빌드 및 푸시
docker buildx build --platform linux/amd64 \
  -t ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0 --push .
```

### 문제 3: "rate limit exceeded"

**원인:** Docker Hub pull rate limit (익명: 100/6시간, 무료: 200/6시간)

**해결:**
```bash
# 인증된 상태에서 pull
docker login -u <YOUR_DOCKER_USERNAME>
docker pull ghcr.io/<YOUR_DOCKER_USERNAME>/mongodb-operator:1.0.0

# 또는 GitHub Container Registry 사용 고려
# (향후 구현 예정)
```

### 문제 4: 빌드 시 "no space left on device"

**원인:** Docker 빌드 캐시가 디스크 공간을 많이 사용

**해결:**
```bash
# Docker 시스템 정리
docker system prune -a --volumes

# 빌드 캐시 제거
docker builder prune -a
```

## 보안 모범 사례

1. **Access Token 사용**
   - 비밀번호 대신 Access Token 사용
   - Token은 필요한 권한만 부여 (Read, Write)
   - Token은 정기적으로 갱신

2. **Token 저장**
   - Token은 GitHub Secrets에만 저장
   - 절대 코드나 로그에 포함하지 말 것
   - `.env` 파일은 `.gitignore`에 추가

3. **접근 제한**
   - Repository Secrets는 해당 저장소에서만 사용
   - Organization Secrets 사용 시 필요한 저장소만 선택

4. **감사 로깅**
   - Docker Hub Settings > Audit Log에서 활동 모니터링
   - GitHub Actions 로그에서 이미지 푸시 활동 확인

## 참고 자료

- [Docker Hub 공식 문서](https://docs.docker.com/docker-hub/)
- [GitHub Actions Secrets](https://docs.github.com/en/actions/security-guides/encrypted-secrets)
- [Docker Buildx 문서](https://docs.docker.com/buildx/working-with-buildx/)
- [MongoDB Operator CI/CD 문서](./v1.0.0-release-guide.md)

## 다음 단계

Docker Hub 설정 완료 후:

1. ✅ GitHub Actions 워크플로우 재실행
2. ✅ Docker 이미지 빌드 성공 확인
3. ✅ GitHub Release 생성
4. ✅ Artifact Hub에서 이미지 정보 업데이트 확인

모든 단계가 완료되면 v1.0.0 릴리스가 완전히 배포됩니다!
