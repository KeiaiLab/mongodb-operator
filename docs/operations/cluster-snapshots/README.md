# Cluster Snapshots

운영 클러스터의 *git 추적 부재* CR/manifest 의 disaster recovery snapshot.
*임시 보관소* — 본 디렉토리의 spec 들은 *argos-platform-data 등 적절한 GitOps
repo 로 마이그레이션* 될 때까지의 *bridge*.

## 정책

- **저장 시점**: cluster ops audit 에서 git 추적 0 인 CR 발견 시 즉시.
- **저장 형식**: `<date>/<resource-name>.yaml` — kubectl get -o yaml 산출
  + ephemeral field 제거 (creationTimestamp / resourceVersion / uid /
  managedFields / status).
- **마이그레이션**: 적절한 GitOps repo (argos-platform-data 등) 로 옮긴
  뒤 본 디렉토리에서 *제거* (git 추적 단일화).
- **scope**: mongodb-operator repo 가 *cluster ops cross-cut 영역* 커버 —
  본 cycle 의 cluster-wide audit 결과 보관 candidate.

## 인덱스

| Date | Resource | Status | Migration target |
|---|---|---|---|
| 2026-05-07 | `keiailab-valkey-prod.yaml` (ValkeyCluster, data ns) | 임시 보관 | argos-platform-data (`valkey-operator/` umbrella, 별 cycle) |
