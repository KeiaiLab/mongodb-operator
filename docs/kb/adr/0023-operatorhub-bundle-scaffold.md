# ADR-0023: OperatorHub.io bundle scaffold (PR-B9 cross-cut)

- Date: 2026-05-10
- Status: Accepted
- Authors: @eightynine01

## Context

valkey-operator PR-B9 (ADR-0037 in valkey repo) + postgres-operator PR-B9
(ADR-0013 in postgres repo) 가 OperatorHub.io 등록 기술 전제를 갖췄다. mongodb
도 동일 cross-cut bundle scaffolding 으로 외부 OperatorHub 발견성 확보. ADR-0016
(Cross-cut Audit Pattern) 정합.

## Decision

valkey ADR-0037 + postgres ADR-0013 패턴 이식:
1. `config/manifests/bases/mongodb-operator.clusterserviceversion.yaml` —
   3 CRD owned (MongoDB / MongoDBSharded / MongoDBBackup), 메타데이터
   (description / keywords / maintainers / provider / maturity=alpha /
   minKubeVersion=1.26.0 / containerImage=v1.4.19).
2. `config/manifests/kustomization.yaml` — CSV + crd + rbac + manager.
   webhook + samples 디렉토리는 kustomization.yaml 부재로 제외.
3. Makefile `bundle` / `bundle-build` 타겟. **차이점**: `operator-sdk
   generate kustomize manifests` 단계 제거 (CSV base 의 hand-written 메타데이터
   보존). valkey + postgres 도 별 PR 로 동일 정합 후속.
4. alm-examples — 3 sample (replicaset / sharded / backup) inline JSON.

## Consequences

긍정:
- 3 operator (valkey + postgres + mongodb) cross-cut OperatorHub 등록 전제 완료.
- 3 CRD 의 customresourcedefinitions.owned 명시 + 한국어 description.
- alm-examples 3 sample 으로 OperatorHub UI 'Try it' 폼 자동 생성.

부정:
- valkey + postgres 의 Makefile bundle 타겟 도 동일 generate kustomize manifests
  제거 필요 — 별 PR-B9.4 로 후속.
- samples 디렉토리 kustomization.yaml 부재 (kubebuilder regenerate 호환성 영향)
  — 별 작업.

## Alternatives Considered

1. **operator-sdk generate kustomize manifests 보존 + interactive=true 사용**:
   거절. 사용자 input 요구로 자동화 차단.
2. **CSV base description 자동 생성에 의존**: 거절. "is the Schema for the
   <kind> API" 류 generic 설명은 OperatorHub 발견성 ↓.

## References

- valkey ADR-0037 / postgres ADR-0013 (동일 cross-cut).
- ADR-0016: Cross-cut Audit Pattern.
- 후속: PR-B9.3 community-operators repo PR (3 operator 동시 또는 분리).
