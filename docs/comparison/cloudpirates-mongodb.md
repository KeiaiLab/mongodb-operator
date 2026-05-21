# CloudPirates `mongodb` Helm Chart 동등성 분석

## 개요

본 문서는 [CloudPirates `mongodb` Helm chart 0.17.1 (MongoDB 8.3.1)](https://artifacthub.io/packages/helm/cloudpirates-mongodb/mongodb) 와 본 프로젝트 `mongodb-operator` v1.4.23 의 *오픈소스 기능 동등성* 을 평가한다. Bitnami 분석([`bitnami-mongodb-sharded.md`](./bitnami-mongodb-sharded.md)) 의 자매 문서로, 3-way summary 는 [`three-way-summary.md`](./three-way-summary.md) 참조.

| 구분 | CloudPirates `mongodb` | 본 프로젝트 `mongodb-operator` |
|---|---|---|
| 추상화 | Helm chart (`architecture` 모드 분기) | Kubernetes Operator (CRD reconciliation) |
| Architecture 모드 | `standalone` / `replicaset` / `sharded` 1 chart 3 토폴로지 | `MongoDB` (RS) + `MongoDBSharded` 2 CRD |
| 배포 단위 | Helm release | CR (`MongoDB`, `MongoDBSharded`, `MongoDBBackup`) |
| Lifecycle 자동화 | `helm upgrade` 트리거 | spec 변경 시 자동 reconcile |
| 컨테이너 베이스 | non-root, read-only FS, Cosign 서명 이미지 | 공식 `mongo:8.2` + 자체 발행 `ghcr.io/keiailab/mongodb-operator` |

**판정 한 줄 요약**: CloudPirates 는 Helm-native 사용성 + 보안 baseline (non-root, Cosign) 에 강하다. 본 Operator 는 *선언적 reconciliation + Backup CRD + cert-manager 1급 통합* 에 강하다. **Hidden replica, lifecycleHooks, externalAccess** 3건이 CloudPirates 우위이며 Operator ROADMAP cycle 1+ 으로 인계 대상.

## 출처

- ArtifactHub: https://artifacthub.io/packages/helm/cloudpirates-mongodb/mongodb
- GitHub 소스: https://github.com/CloudPirates-io/helm-charts (mongodb chart)
- Chart version: 0.17.1, App version: MongoDB 8.3.1
- 본 분석 작성일: 2026-05-12 (cycle 0)

## 비교 기준 축 (Bitnami 분석과 동일)

1. 토폴로지 / Architecture 모드
2. 인증/보안 (SCRAM, X.509, custom user)
3. TLS / cert-manager
4. 영속성 (PVC, storageClass)
5. 메트릭 (Prometheus exporter, ServiceMonitor, PrometheusRule)
6. 백업/복구
7. 네트워크 (Service type, NetworkPolicy, externalAccess)
8. 스케줄링 (affinity, anti-affinity)
9. 가용성 (PDB, replica 역할)
10. 확장성 사이드카 (initContainer, lifecycleHooks, extraObjects)
11. 이미지/리소스 운영 (presets, pullSecrets)
12. 라이프사이클 (upgradeStrategy, podManagementPolicy)
13. 라이선스/공급망

## 동등성 매트릭스 (28행)

범례: ✅ 동등 또는 우위 · ⚠️ 부분 지원 · ❌ 미지원 · ⚪ 양쪽 모두 미지원

| # | 기능 축 | CloudPirates `mongodb` 0.17.1 | `mongodb-operator` 1.4.23 | 동등성 | 비고 |
|---|---|---|---|---|---|
| 1 | Architecture 모드 (standalone) | ✅ `architecture: standalone` | ❌ 단독 인스턴스 미지원 (RS-only) | ❌ 미지원 | dev/test 1-pod 시나리오 갭 |
| 2 | Architecture 모드 (replicaset) | ✅ `architecture: replicaset` | ✅ `MongoDB` CRD 1급 | ✅ 동등 | — |
| 3 | Architecture 모드 (sharded) | ✅ `architecture: sharded` | ✅ `MongoDBSharded` CRD | ✅ 동등 | — |
| 4 | SCRAM auth | ✅ `auth.rootPassword`, custom users | ✅ `AuthSpec.Mechanism: SCRAM-SHA-256` | ✅ 동등 | — |
| 5 | Existing secret 참조 | ✅ `auth.existingSecret` | ✅ `auth.adminCredentialsSecretRef.name` | ✅ 동등 | — |
| 6 | X.509 auth | ⚠️ 수동 cert + config | ✅ `AuthSpec.Mechanism: X509` + cert-manager | ✅ 우위 | Operator 통합 우위 |
| 7 | LDAP / Kerberos | ❌ 미지원 | ❌ 미지원 (ROADMAP Phase 2) | ⚪ 동급 | OSS 한계 |
| 8 | TLS / cert-manager | ⚠️ `config.content` 수동 설정 | ✅ `TLSSpec` cert-manager 자동 | ✅ 우위 | Operator 표준 통합 |
| 9 | Persistence (PVC) | ✅ `persistence.{storageClass,size,accessModes}` | ✅ `StorageSpec.{size,storageClassName}` | ⚠️ 부분 | accessModes 미노출 |
| 10 | Metrics exporter | ✅ Percona MongoDB exporter sidecar | ✅ mongodb-exporter sidecar | ✅ 동등 | exporter 종류 차이 |
| 11 | ServiceMonitor | ✅ Prometheus Operator 통합 | ✅ `monitoring.serviceMonitor.enabled` | ✅ 동등 | — |
| 12 | PrometheusRule | ⚠️ `extraObjects` 로 수동 추가 | ✅ 자동 생성 | ✅ 우위 | alarm rule 자동화 |
| 13 | Backup / restore | ⚠️ `extraObjects` CronJob 수동 | ✅ `MongoDBBackup` CRD (S3/PVC, full/incr) | ✅ 우위 | Operator 1급 |
| 14 | PITR | ❌ 미지원 | ⚠️ CRD 필드 정의만 (ROADMAP cycle 1) | ⚠️ 동급(부분) | 양쪽 미완 |
| 15 | NetworkPolicy | ✅ opt-in (`networkPolicy.enabled`) | ✅ `network.policy.enabled` | ✅ 동등 | — |
| 16 | PodDisruptionBudget | ⚠️ `extraObjects` 수동 | ✅ `pdb.enabled` | ✅ 우위 | — |
| 17 | externalAccess (Ingress/LB) | ✅ `externalAccess` 1급 옵션 | ⚠️ LB 부분 + Ingress 미지원 | ❌ 미지원 | cycle 10+ |
| 18 | Arbiter | ✅ replica set 모드 | ✅ `MongoDB.spec.arbiter` (RS only) | ⚠️ 부분 | sharded arbiter 미지원 |
| 19 | Hidden replica | ✅ `hidden` 옵션 | ❌ 미지원 | ❌ 미지원 | cycle 10 candidate |
| 20 | Delayed replica | ⚠️ 수동 config | ❌ 미지원 | ⚪ 동급(부분) | rare use case |
| 21 | initContainer / customInit | ✅ `initContainer.resources`, `customInit` | ❌ 미지원 | ❌ 미지원 | cycle 10 candidate |
| 22 | lifecycleHooks | ✅ `lifecycleHooks` (postStart/preStop) | ⚠️ ReplicaSet PostStart bootstrap 만 | ⚠️ 부분 | cycle 10 candidate |
| 23 | extraObjects 주입 | ✅ chart 차원 supplementary | ❌ Operator 외부 GitOps 권장 | ⚪ 모델 차이 | — |
| 24 | HPA / autoscaling | ❌ 미지원 | ✅ HPA 통합 | ✅ 우위 | — |
| 25 | Cosign 서명 | ✅ chart + image | ⚠️ image 만 (chart provenance cycle 11+) | ⚠️ 부분 | 공급망 보강 필요 |
| 26 | non-root + readOnlyRootFilesystem | ✅ 기본 | ✅ securityContext + PSA restricted | ✅ 동등 | — |
| 27 | UpdateStrategy | ✅ `updateStrategyType: RollingUpdate` | ✅ StatefulSet 자동 | ✅ 동등 | — |
| 28 | Version upgrade automation | ❌ 사용자 수동 | ⚠️ 기본 호환성 매트릭스 (cycle 7 rollback) | ⚠️ 부분 | Operator 우위 진행 중 |

## CloudPirates 우위 항목 (3건)

ROADMAP cycle 매핑 — 후속 cycle 진입 대상:

1. **Hidden replica member** (#19) — analytics/backup 격리 시나리오. CloudPirates 는 `replicaset` 모드에서 `hidden` 옵션 노출. Operator 는 ROADMAP cycle 10 `Phase 4 polish` 로 인계.
2. **externalAccess Ingress + LB 1급 옵션** (#17) — CloudPirates 는 chart level 에서 외부 노출 추상화. Operator 는 LB 부분 지원, Ingress 미지원 → cycle 10.
3. **initContainer / customInit / lifecycleHooks 확장** (#21, #22) — Bitnami 와 마찬가지로 사이드카 주입 갭. cycle 10.

## Operator 우위 항목 (6건)

1. **Built-in Backup CRD** (#13) — S3/PVC + full/incremental.
2. **PrometheusRule 자동 생성** (#12).
3. **cert-manager 1급 통합** (#8).
4. **HPA / autoscaling** (#24).
5. **PodDisruptionBudget 자동** (#16).
6. **선언적 reconciliation** — spec 변경만으로 자동 적용.

## 라이선스 / 공급망 비교

| 항목 | CloudPirates | 본 프로젝트 |
|---|---|---|
| Helm chart 라이선스 | Apache-2.0 (추정 — GitHub 소스 확인 필요) | Apache-2.0 |
| 컨테이너 이미지 | non-root, read-only FS, Cosign 서명 | `mongo:8.2` + `ghcr.io/keiailab/mongodb-operator` |
| 공급망 메타데이터 | Cosign (image + chart) | GHCR 표준 (provenance/SBOM 향후 작업) |
| 운영 모델 | 신생 OSS 조직 | keiailab OSS, 자체 발행 |

**시사점**: CloudPirates 는 Cosign 서명을 chart 까지 확장한 점이 인상적. Operator 는 image-only — cycle 11+ provenance 강화 작업으로 인계.

## 마이그레이션 가이드 — CloudPirates `values.yaml` → 본 프로젝트 CRD

| CloudPirates `values.yaml` | 본 프로젝트 CRD 필드 | 비고 |
|---|---|---|
| `architecture: standalone` | (미지원 — RS members=1 우회) | 갭 #1 |
| `architecture: replicaset` | `MongoDB.spec.members: 3+` | 동등 |
| `architecture: sharded` | `MongoDBSharded.spec.{configServer,shards,mongos}` | 동등 |
| `auth.rootPassword` | `auth.adminCredentialsSecretRef.name` (Secret) | Secret 분리 권장 |
| `auth.existingSecret` | `auth.adminCredentialsSecretRef.name` | 직접 매핑 |
| `tls.enabled` + `config.content` | `tls.enabled: true` + `tls.certManager.issuerRef` | cert-manager 권장 |
| `persistence.size: 8Gi` | `storage.size: 8Gi` | 동등 |
| `metrics.enabled: true` | `monitoring.enabled: true` | 동등 |
| `metrics.serviceMonitor.enabled: true` | `monitoring.serviceMonitor.enabled: true` | 동등 |
| `networkPolicy.enabled: true` | `network.policy.enabled: true` | 동등 |
| `externalAccess.enabled: true` | (Ingress 미지원, LB 부분 — ROADMAP cycle 10) | 갭 #17 |
| `arbiter.enabled: true` | `MongoDB.spec.arbiter.enabled: true` | RS 동등, sharded 갭 |
| `hidden.enabled: true` | (미지원 — ROADMAP cycle 10) | 갭 #19 |
| `lifecycleHooks.postStart/preStop` | (RS PostStart bootstrap 만) | 갭 #22 |
| `initContainer.image` / `customInit` | (미지원) | 갭 #21 |
| `extraObjects` | (Operator 외부 GitOps 권장) | 모델 차이 |
| `pdb` (extraObjects) | `pdb.enabled: true` | Operator 자동 |
| `metrics.prometheusRule` (extraObjects) | (자동 생성) | Operator 자동 |
| Backup (extraObjects CronJob) | `MongoDBBackup` CRD | Operator 우위 |

## 검증 절차

1. **CRD 사실 확인**: `make manifests` 후 `config/crd/bases/*.yaml` 에서 본 매트릭스 행 #2~#16 grep.
2. **CloudPirates 최신 chart 재확인**: `helm repo add cloudpirates https://cloudpirates-io.github.io/helm-charts && helm show values cloudpirates/mongodb --version 0.17.1` 로 우측 컬럼 갱신.
3. **3-way summary 정합**: 본 문서 갭 3건 (#17, #19, #21) 이 [`three-way-summary.md`](./three-way-summary.md) 의 cycle 매핑 표에 모두 반영되었는지 확인.

## 참고 자료

- ArtifactHub: https://artifacthub.io/packages/helm/cloudpirates-mongodb/mongodb
- GitHub: https://github.com/CloudPirates-io/helm-charts
- 자매 분석: [`bitnami-mongodb-sharded.md`](./bitnami-mongodb-sharded.md)
- 3-way 요약: [`three-way-summary.md`](./three-way-summary.md)
- ROADMAP: [`../../ROADMAP.md`](../../ROADMAP.md)
