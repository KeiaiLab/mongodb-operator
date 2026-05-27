# Community Distribution Plan

> Status: Draft
> Issue: #237

## 목표

mongodb-operator 의 커뮤니티 배포 채널을 정의하고, 사용자가 쉽게
설치할 수 있는 다양한 경로를 제공한다.

## 배포 채널 (계획)

1. **Helm Chart** — charts/ 디렉토리의 Helm chart 를 ArtifactHub 에 등록
2. **OLM Bundle** — bundle/ 디렉토리의 OLM bundle 을 OperatorHub 에 등록
3. **Container Image** — GHCR / Docker Hub 이중 배포
4. **Kustomize** — config/ 디렉토리의 kustomize manifests 직접 사용

## 다음 단계

- [ ] ArtifactHub 메타데이터 최종 검증
- [ ] OLM bundle CSV 버전 갱신 자동화
- [ ] 릴리스 태그 시 이미지 자동 빌드 파이프라인 구성
- [ ] 설치 문서 (getting-started.md) 각 채널별 가이드 추가
