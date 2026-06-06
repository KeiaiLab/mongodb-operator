# Artifact Hub 검증 가이드

## 개요

이 가이드는 MongoDB Operator가 Artifact Hub에 올바르게 게시되었는지 확인하는 방법을 설명합니다.

## Artifact Hub란?

Artifact Hub는 Kubernetes 패키지(Helm 차트, Operators, Plugins 등)의 중앙 집중식 레지스트리입니다. 사용자가 쉽게 패키지를 검색하고 설치할 수 있도록 합니다.

## 자동 게시 프로세스

MongoDB Operator는 다음 단계를 통해 자동으로 Artifact Hub에 게시됩니다:

1. **Git 태그 생성**: `git push origin vX.Y.Z`
2. **GitHub Actions 트리거**: `helm-publish.yml` 워크플로우 실행
3. **Helm 차트 패키징**: `helm package` 실행
4. **gh-pages 브랜치 업데이트**: 차트 및 index.yaml 푸시
5. **Artifact Hub 자동 동기화**: 30-60분 이내

## 검증 절차

### 1. Helm 레포지토리 확인

```bash
# Helm 레포지토리 인덱스 확인
curl -s https://keiailab.github.io/mongodb-operator/index.yaml | grep -A 10 "version: 1.0.0"

# 예상 출력:
#   version: 1.0.0
#   created: "2026-01-22T12:17:01Z"
#   digest: sha256:...
#   urls:
#   - https://keiailab.github.io/mongodb-operator/mongodb-operator-1.0.0.tgz
```

### 2. Artifact Hub 웹 페이지 확인

**URL**: https://artifacthub.io/packages/helm/mongodb-operator/mongodb-operator

#### 검증 체크리스트

- [ ] **버전 표시**: v1.0.0이 최신 버전으로 표시됨
- [ ] **메타데이터 정확성**:
  - Description: "A Kubernetes Operator for managing MongoDB..."
  - License: MIT
  - Category: database
  - Operator Capabilities: Full Lifecycle
- [ ] **CRDs 표시**: 3개 CRD (MongoDB, MongoDBSharded, MongoDBBackup)
- [ ] **설치 명령어**: Helm install 명령어 표시
- [ ] **예제 YAML**: 3개 CRD 예제 렌더링
- [ ] **링크 작동**:
  - Documentation → GitHub README
  - Source Code → GitHub Repository
  - MongoDB Documentation → mongodb.com
- [ ] **이미지 정보**:
  - ghcr.io/keiailab/mongodb-operator:1.0.0
  - mongo:8.2
  - percona/mongodb_exporter:0.40
- [ ] **변경사항**: CHANGELOG 내용 렌더링
- [ ] **보안 배지**: 취약점 스캔 결과 (있는 경우)

### 3. Helm 레포지토리 추가 및 설치 테스트

```bash
# Helm 레포지토리 추가
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

# v1.0.0 버전 확인
helm search repo mongodb-operator/mongodb-operator --versions

# 예상 출력:
# NAME                              CHART VERSION  APP VERSION  DESCRIPTION
# mongodb-operator/mongodb-operator 1.0.0          1.0.0        A Kubernetes Operator for...
# mongodb-operator/mongodb-operator 0.0.7          0.0.7        A Kubernetes Operator for...

# Dry-run 설치 테스트
helm install test-release mongodb-operator/mongodb-operator \
  --version 1.0.0 \
  --namespace mongodb-operator-system \
  --create-namespace \
  --dry-run
```

### 4. 차트 다운로드 및 검증

```bash
# 차트 다운로드
helm pull mongodb-operator/mongodb-operator --version 1.0.0

# 압축 해제
tar -xzf mongodb-operator-1.0.0.tgz

# Chart.yaml 확인
cat mongodb-operator/Chart.yaml

# Values.yaml 확인
cat mongodb-operator/values.yaml

# CRD 확인
ls -la mongodb-operator/crds/
```

## 트러블슈팅

### 문제 1: Artifact Hub에 새 버전이 표시되지 않음

**원인**: Artifact Hub 동기화 지연 (최대 1시간)

**해결**:
```bash
# 1. gh-pages 브랜치 확인
git fetch origin gh-pages
git log origin/gh-pages --oneline | head -5

# 2. index.yaml에 버전 확인
curl -s https://keiailab.github.io/mongodb-operator/index.yaml | grep "version: 1.0.0"

# 3. 60분 후에도 표시되지 않으면, Artifact Hub에 문의
# https://github.com/artifacthub/hub/issues
```

### 문제 2: CRD 예제가 렌더링되지 않음

**원인**: Chart.yaml의 `artifacthub.io/crdsExamples` 형식 오류

**해결**:
```bash
# Chart.yaml 검증
helm lint charts/mongodb-operator

# YAML 형식 확인
yq eval '.annotations."artifacthub.io/crdsExamples"' charts/mongodb-operator/Chart.yaml
```

### 문제 3: 이미지 정보 누락

**원인**: `artifacthub.io/images` 어노테이션 누락

**해결**:
```yaml
# Chart.yaml에 추가
annotations:
  artifacthub.io/images: |
    - name: mongodb-operator
      image: ghcr.io/keiailab/mongodb-operator:1.0.0
    - name: mongodb
      image: mongo:8.2
```

### 문제 4: 설치 명령어 실패

**원인**: Helm 레포지토리 캐시 문제

**해결**:
```bash
# 캐시 삭제
rm -rf ~/.cache/helm/repository/*

# 레포지토리 재추가
helm repo remove mongodb-operator
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update
```

## Artifact Hub 메타데이터 관리

### Chart.yaml 어노테이션

모든 Artifact Hub 메타데이터는 `Chart.yaml`의 `annotations` 섹션에 정의됩니다:

```yaml
annotations:
  # 라이센스
  artifacthub.io/license: MIT
  
  # 카테고리
  artifacthub.io/category: database
  
  # Operator 능력
  artifacthub.io/operatorCapabilities: Full Lifecycle
  
  # 링크
  artifacthub.io/links: |
    - name: Documentation
      url: https://github.com/keiailab/mongodb-operator/blob/main/README.md
    - name: Source Code
      url: https://github.com/keiailab/mongodb-operator
  
  # CRD 정의
  artifacthub.io/crds: |
    - kind: MongoDB
      version: v1alpha1
      name: mongodbs.mongodb.keiailab.com
      displayName: MongoDB ReplicaSet
      description: Deploys and manages a MongoDB ReplicaSet cluster
  
  # 이미지
  artifacthub.io/images: |
    - name: mongodb-operator
      image: ghcr.io/keiailab/mongodb-operator:1.0.0
  
  # 변경사항
  artifacthub.io/changes: |
    - kind: added
      description: Initial stable (GA) release
```

### artifacthub-repo.yml

저장소 레벨 구성은 `charts/artifacthub-repo.yml`에 정의됩니다:

```yaml
repositoryID: XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX
owners:
  - name: Keiailab
    email: support@keiailab.com
```

## 보안 스캐닝

Artifact Hub는 자동으로 보안 취약점을 스캔합니다:

### Trivy 스캐닝

```bash
# 로컬에서 Trivy 스캔 실행
trivy image ghcr.io/keiailab/mongodb-operator:1.0.0

# 결과는 Artifact Hub 웹 페이지의 "Security Report" 섹션에 표시됨
```

### Snyk 통합 (선택사항)

Artifact Hub는 Snyk와 통합할 수 있습니다:

1. https://snyk.io 에서 계정 생성
2. Artifact Hub Settings에서 Snyk 토큰 추가
3. 자동으로 의존성 스캔

## 배지 추가

README.md에 Artifact Hub 배지 추가:

```markdown
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/mongodb-operator)](https://artifacthub.io/packages/helm/mongodb-operator/mongodb-operator)
```

## 통계 확인

Artifact Hub는 다음 통계를 제공합니다:

- **다운로드 수**: Helm pull 횟수
- **스타 수**: 사용자가 즐겨찾기에 추가한 횟수
- **검색 순위**: 검색 결과에서의 순위

```bash
# Artifact Hub API로 통계 확인
curl -s https://artifacthub.io/api/v1/packages/helm/mongodb-operator/mongodb-operator | jq '.stats'
```

## 참고 자료

- [Artifact Hub 문서](https://artifacthub.io/docs/)
- [Helm 차트 어노테이션](https://artifacthub.io/docs/topics/annotations/)
- [CRD 예제](https://artifacthub.io/docs/topics/annotations/helm/#supported-annotations)

## 다음 단계

Artifact Hub 검증 완료 후:

1. ✅ README.md에 Artifact Hub 배지 추가
2. ✅ 소셜 미디어에 릴리스 공지
3. ✅ 커뮤니티 포럼에 공지
4. ✅ 사용자 피드백 수집
