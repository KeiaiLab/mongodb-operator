# Cluster Snapshots

운영 클러스터의 *git 추적 부재* CR/manifest 의 disaster recovery snapshot.
*임시 보관소* — 본 디렉토리의 spec 들은 *keiailab-platform-data 등 적절한 GitOps
repo 로 마이그레이션* 될 때까지의 *bridge*.

## 정책

- **저장 시점**: cluster ops audit 에서 git 추적 0 인 CR 발견 시 즉시.
- **저장 형식**: `<date>/<resource-name>.yaml` — kubectl get -o yaml 산출
  + ephemeral field 제거 (creationTimestamp / resourceVersion / uid /
  managedFields / status).
- **마이그레이션**: 적절한 GitOps repo (keiailab-platform-data 등) 로 옮긴
  뒤 본 디렉토리에서 *제거* (git 추적 단일화).
- **scope**: mongodb-operator repo 가 *cluster ops cross-cut 영역* 커버 —
  본 cycle 의 cluster-wide audit 결과 보관 candidate.

## 인덱스

| Date | Resource | Status | Migration target |
|---|---|---|---|
| _(none)_ | — | — | — |

**마지막 cleanup**: 2026-05-21 — `2026-05-07/keiailab-valkey-prod.yaml` 마이그레이션 완료 후 제거 (production-grade-sprint.md Phase B7 완수). 본 디렉토리는 *임시 보관소* 정합 유지 — 향후 git 추적 0 인 CR 발견 시 같은 정책으로 재사용.
