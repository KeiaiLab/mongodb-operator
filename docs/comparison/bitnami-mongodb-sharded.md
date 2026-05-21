# Bitnami `mongodb-sharded` Helm Chart 동등성 분석

## 개요

본 문서는 [Bitnami `mongodb-sharded` Helm chart 9.4.12](https://artifacthub.io/packages/helm/bitnami/mongodb-sharded)와 본 프로젝트(`mongodb-operator` v1.4.23)가 **동일한 오픈소스 기능을 제공하는지** 평가한다. 두 프로젝트는 같은 도메인(MongoDB Sharded Cluster on Kubernetes)을 다루지만 추상화 계층이 다르다.

> cycle 0 (2026-05-12) 갱신: operator version reference 만 v1.0.1 → v1.4.23. 본문 기능 갭 9건 + 우위 6건 의 본질은 그대로 유효 — ROADMAP `[~]`/`[ ]` 항목으로 cycle 1+ 인계. 추가 reference: [`cloudpirates-mongodb.md`](./cloudpirates-mongodb.md), [`three-way-summary.md`](./three-way-summary.md).

| 구분 | Bitnami `mongodb-sharded` | 본 프로젝트 `mongodb-operator` |
|---|---|---|
| 추상화 | Helm chart 기반 stateful 배포 | Kubernetes Operator (CRD reconciliation) |
| 배포 단위 | Helm release | `MongoDB`, `MongoDBSharded`, `MongoDBBackup` CR |
| Lifecycle 자동화 | `helm upgrade` 트리거 | spec 변경 시 자동 reconcile |
| 컨테이너 베이스 | Bitnami Secure Images (Photon) | 공식 `mongo:8.2` + `ghcr.io/keiailab/mongodb-operator` |

**판정 한 줄 요약**: 핵심 기능(SCRAM, TLS, Sharded 토폴로지, Prometheus, PVC)은 **동등**하다. 운영 사이드카·NetworkPolicy·arbiter/hidden 멤버 같은 **편의·보안·토폴로지 옵션 9건이 갭**으로 남아있다. 반대로 **백업 CRD, 선언적 sharding, PrometheusRule 자동 생성, mongo-go-driver 기반 보안 모델** 6건은 본 프로젝트가 우위다.

## 비교 기준 축

1. 토폴로지 (컴포넌트별 모델링과 스케일 차원)
2. 인증/보안 (SCRAM, X.509, LDAP, Kerberos, keyfile)
3. TLS / cert-manager
4. 영속성 (PVC, storageClass, retention)
5. 메트릭 (Prometheus exporter, ServiceMonitor / PodMonitor)
6. 백업/복구 (built-in vs Velero)
7. 네트워크 (Service type, NetworkPolicy, externalAccess)
8. 스케줄링 (affinity, anti-affinity, topology spread, priorityClass)
9. 가용성 (PDB, podManagementPolicy)
10. RBAC / ServiceAccount
11. 확장성 사이드카 (initScripts, sidecars, lifecycleHooks, extraVolumes)
12. 이미지/리소스 운영 (presets, pullSecrets, debug)
13. 라이프사이클 자동화 (scale-out, version upgrade, password rotation)
14. 라이선스/지원 모델
15. 제약/한계/Breaking change

## 동등성 매트릭스 (44행)

범례: ✅ 동등 또는 우위 · ⚠️ 부분 지원 · ❌ 미지원 · ⚪ 양쪽 모두 미지원(동급)

| # | 기능 축 | Bitnami `mongodb-sharded` 9.4.12 | `mongodb-operator` 1.4.23 | 동등성 | 비고 |
|---|---|---|---|---|---|
| 1 | Sharded 토폴로지 | shards(=2 default), configsvr, mongos, **arbiter**, **hidden node** | `MongoDBSharded` CRD: shards.count, configServer, mongos. **arbiter는 ReplicaSet CRD에만**, hidden node 없음 | ⚠️ 부분 | sharded용 arbiter/hidden member 미구현 |
| 2 | ReplicaSet 단독 토폴로지 | shardsvr를 ReplicaSet으로 취급 (sharded 1차 시나리오) | `MongoDB` CRD가 ReplicaSet 1급 객체 (arbiter 지원) | ✅ 우위 | Operator가 더 명확한 모델 |
| 3 | mongos Deployment vs StatefulSet | `mongos.useStatefulSet: false` 토글 | Deployment 고정 | ❌ 미지원 | StatefulSet mongos 옵션 부재 |
| 4 | mongos service-per-replica | `mongos.servicePerReplica.enabled` | 미지원 | ❌ 미지원 | external 직접 라우팅 시 갭 |
| 5 | External configsvr | `configsvr.external.host` | 미지원 | ❌ 미지원 | 항상 in-cluster |
| 6 | SCRAM auth | `auth.rootUser/rootPassword`, replicaSetKey | `AuthSpec.Mechanism: SCRAM-SHA-256/-1`, `adminCredentialsSecretRef` | ✅ 동등 | mongo-go-driver로 직접 검증 |
| 7 | X.509 auth | 부분 지원 | `AuthSpec.Mechanism: X509` | ✅ 동등~우위 | cert-manager 통합 |
| 8 | LDAP / Kerberos | 미명시(엔터프라이즈 영역) | 미지원 (ROADMAP Phase 2) | ⚪ 동등(미지원) | 양쪽 모두 OSS 한계 |
| 9 | TLS / cert-manager | `tls.enabled`, autoGen | `TLSSpec` (cert-manager + CustomCert) | ✅ 동등~우위 | 표준 cert-manager 통합 |
| 10 | Persistence | `persistence.{storageClass,size,accessModes,subPath,selector}` | `StorageSpec.{size, storageClassName, dataDirPath}` | ⚠️ 부분 | accessModes/subPath/selector 미노출 |
| 11 | PVC retention policy | `persistentVolumeClaimRetentionPolicy.{whenScaled,whenDeleted}` | 미노출 (StatefulSet 기본값) | ❌ 미지원 | scale-down 시 PVC 정책 제어 불가 |
| 12 | Volume permissions init container | `volumePermissions.enabled` (os-shell) | 미지원 | ❌ 미지원 | non-root 클러스터에서 갭 |
| 13 | Prometheus exporter | sidecar percona/mongodb_exporter | sidecar Percona 0.40 (`MonitoringSpec.Exporter`) | ✅ 동등 | 동일 익스포터 |
| 14 | ServiceMonitor / PodMonitor | `metrics.podMonitor.enabled` | `MonitoringSpec.ServiceMonitor`, `PrometheusRules` | ✅ 우위 | PrometheusRule까지 자동 |
| 15 | Backup 내장 | ❌ 없음 (Velero 가이드 링크) | `MongoDBBackup` CRD (mongodump, S3/PVC, full/incremental, schedule) | ✅ 우위 | 1급 백업 리소스 |
| 16 | PITR | 없음 | ROADMAP Phase 1.1 (`oplogRetentionHours` 필드 정의됨, 실행은 미구현) | ⚠️ 부분 | 스펙은 있고 실행 미완 |
| 17 | Service type | ClusterIP/NodePort/LB + headless + sessionAffinity + externalIPs | `MongosServiceSpec.{type,port,loadBalancerIP}` | ⚠️ 부분 | sessionAffinity/externalIPs/headless 토글 없음 |
| 18 | NetworkPolicy | `networkPolicy.{enabled,allowExternal,extraIngress,extraEgress,ingressNSMatchLabels}` | values.yaml placeholder만 (템플릿/CRD 미구현) | ❌ 미지원 | 보안 게이트 갭 |
| 19 | Affinity / anti-affinity preset | preset(soft/hard) + custom affinity | `PodSpec.Affinity` (raw corev1) | ⚠️ 부분 | preset 헬퍼 부재 |
| 20 | Topology spread / nodeSelector / tolerations | 모든 컴포넌트에 노출 | `PodSpec` raw 노출 | ✅ 동등 | |
| 21 | priorityClassName | 컴포넌트별 노출 | `PodSpec.PriorityClassName` | ✅ 동등 | |
| 22 | PodDisruptionBudget | `pdb.{create,minAvailable,maxUnavailable}` 컴포넌트별 | charts/templates/pdb.yaml(operator 자체) + 워크로드 PDB는 미노출 | ⚠️ 부분 | 워크로드 PDB 자동화 갭 |
| 23 | Pod management policy | `podManagementPolicy: OrderedReady` | 미노출(기본값) | ❌ 미노출 | Parallel 토글 부재 |
| 24 | ServiceAccount per component | configsvr/mongos 별 SA, automount false | operator 자체 SA만 | ❌ 미지원 | 워크로드 SA 분리 미구현 |
| 25 | Init scripts | `common.initScriptsCM` (.sh/.js) | 미노출 (postStart hook은 admin bootstrap 전용) | ❌ 미지원 | 사용자 스크립트 주입 갭 |
| 26 | Extra sidecars / initContainers | 컴포넌트별 노출 | 미노출 | ❌ 미지원 | exporter 외 사이드카 주입 불가 |
| 27 | Lifecycle hooks | 컴포넌트별 노출 | admin bootstrap postStart는 자동 주입(사용자 정의 미지원) | ❌ 미지원 | 사용자 lifecycle 갭 |
| 28 | extraEnvVars / extraVolumes / extraVolumeMounts | 컴포넌트별 노출 | 미노출 | ❌ 미지원 | 커스텀 마운트 갭 |
| 29 | Image registry/repo/tag/pullPolicy/pullSecrets | 컴포넌트별 + global override | `VersionSpec.version` + GHCR 고정 + `image.pullSecrets`(operator) | ⚠️ 부분 | 워크로드 image override 제한적 |
| 30 | Resource presets (none~2xlarge) | 8단계 preset | 미지원(직접 requests/limits) | ❌ 미지원 | 편의 갭 |
| 31 | Diagnostic mode (sleep infinity) | `diagnosticMode.enabled` | 미지원 | ❌ 미지원 | 트러블슈팅 편의 갭 |
| 32 | extraDeploy(추가 K8s 객체) | `extraDeploy: []` | 미노출 | ❌ 미지원 | |
| 33 | OpenShift SCC 호환 | `global.compatibility.openshift.adaptSecurityContext` | 미명시 | ⚠️ 부분 | OpenShift 검증 부재 |
| 34 | Horizontal scaling 자동 reconcile | helm upgrade 필요 | CRD spec 변경으로 reconcile + sh.addShard 자동 | ✅ 우위 | 핵심 우위 |
| 35 | Vertical scaling | helm upgrade로 rolling | resources 변경 시 rolling restart 자동 | ✅ 동등 | |
| 36 | Version upgrade | helm upgrade(이미지 tag 교체) | spec.version 변경, **자동 롤링 업그레이드는 ROADMAP Phase 1.3** | ⚠️ 부분 | 상태 머신 미완 |
| 37 | Scale-in (shard 제거) | helm scale 가능, removeShard 자동화 모호 | **미지원** (README 명시) | ❌ 미지원 | 알려진 한계 |
| 38 | ReplicaSet member 제거 | 사용자 책임 | **미지원** (rs.remove 미호출) | ❌ 미지원 | |
| 39 | Password rotation | 수동(secret 갱신 + db.changeUserPassword) | 자동화 미구현 | ⚪ 동등(둘 다 수동) | |
| 40 | 라이선스 | Apache-2.0 (단 BSI 일부 메타데이터는 Broadcom 상용 구독 가능성) | Apache-2.0 (`Chart.yaml`) | ✅ 동등 | Bitnami 운영 모델 변경 진행 중 |
| 41 | 컨테이너 이미지 출처 | bitnami/containers (Photon/BSI) | mongo:8.2 공식 + ghcr.io operator | ⚠️ 다름 | 공급망 모델 차이 |
| 42 | 의존 chart | `bitnami/common`, optional kube-prometheus-stack | 의존 없음 | ⚪ 동등 | |
| 43 | Velero 통합 가이드 | 명시 | 미명시(자체 백업 CRD 존재) | ❌ 부재 | |
| 44 | Prerequisites | K8s 1.23+, Helm 3.8+ | K8s 1.26+, cert-manager(권장) | ⚠️ 다름 | 더 높은 K8s 요구 |

## 갭 분석 — 우선순위별 (P0/P1/P2)

본 프로젝트가 Bitnami 대비 부재한 9건. ROADMAP에 동시 반영(Phase 4 신규 sub-phase).

### P0 — 보안 게이트 / 핵심 토폴로지

1. **NetworkPolicy 자동 생성** (#18) — Bitnami는 기본 enabled. CRD에 `network.policy.enabled` + `allowExternal/extraIngress/extraEgress` 필드 추가, Helm 템플릿 생성.
2. **Sharded용 Arbiter / Hidden member** (#1) — 비용 최적화 토폴로지(arbiter)와 분석/백업 격리(hidden) 양대 시나리오 누락.

### P1 — 운영 통합 필수

3. **워크로드 사이드카 / initContainer / extraVolumes / extraEnvVars 주입** (#26-28) — audit 로그 사이드카, fluentbit, oplog tailer 등 운영 표준 주입 갭.
4. **PVC retention policy** (#11) — `whenScaled`/`whenDeleted: Retain|Delete` 노출 필요. scale-down 시 데이터 손실 방지.
5. **volumePermissions init container** (#12) — non-root/restricted PSA 클러스터에서 fsGroup만으로 부족한 경우 대응.

### P2 — 편의·완성도

6. **Init scripts ConfigMap** (#25) — 인덱스/시드 자동화. `.sh`/`.js` 주입 경로.
7. **Service 옵션 확장** (#17) — sessionAffinity, headless, externalIPs, nodePort 토글.
8. **Diagnostic mode + resourcePresets** (#30, #31) — 트러블슈팅 편의 + Bitnami preset 호환 매핑.
9. **Scale-in / member removal** (#37, #38) — `rs.remove`, `removeShard` 자동화. 현재 README가 명시한 한계 직접 해소.

## Operator 우위 항목 (6건)

마케팅·README 보완 후보. Helm chart 기반 사용자가 본 Operator로 전환할 동기를 만든다.

1. **Built-in Backup CRD** (#15) — Bitnami는 Velero 외부 의존, 본 프로젝트는 `MongoDBBackup` CRD로 mongodump/S3/PVC/full-incremental 1급 지원.
2. **선언적 horizontal scaling + sh.addShard 자동** (#34) — 사용자가 `spec.shards.count`만 변경하면 reconciler가 `addShard` 호출까지 자동화.
3. **PrometheusRule 자동 생성** (#14) — Bitnami는 PodMonitor만 제공. 본 프로젝트는 알람 규칙까지 자동 배포.
4. **mongo-go-driver v2 기반(pods/exec 권한 0)** — Bitnami는 컨테이너 entrypoint shell script 의존. 본 프로젝트는 driver 직접 호출로 JS injection 표면 제거.
5. **cert-manager 1급 통합** (#9) — `TLSSpec`에서 cert-manager Issuer 직접 참조.
6. **`MongoDB` (ReplicaSet) CRD 단독 모델** (#2) — Bitnami `mongodb-sharded`는 sharded 전용. ReplicaSet은 별도 chart(`bitnami/mongodb`)로 분리 관리해야 함.

## 라이선스 및 공급망 비교

| 항목 | Bitnami | 본 프로젝트 |
|---|---|---|
| Helm chart 라이선스 | Apache-2.0 | Apache-2.0 |
| 컨테이너 이미지 | `bitnami/mongodb-sharded` (Photon, BSI) | `mongo:8.2`(공식) + `ghcr.io/keiailab/mongodb-operator`(Apache-2.0) |
| 운영 모델 변화 | 2024-08 Bitnami Secure Images(BSI) 도입, Broadcom 인수 후 일부 카탈로그 상용 구독 전환 진행 | 자체 발행, 공개 GHCR |
| 공급망 메타데이터 | SBOM/in-toto/VEX/EPSS 제공(BSI) | GHCR 표준 (provenance/SBOM은 향후 작업) |

**시사점**: Bitnami의 운영 모델이 변동 중이므로 OSS 사용자는 장기적으로 자체 발행 이미지를 쓰는 본 프로젝트가 공급망 통제 면에서 단순하다. 단 BSI 수준의 보안 메타데이터는 별도 작업(Sigstore, SLSA Provenance)으로 보강이 필요하다.

## 마이그레이션 가이드 — Bitnami `values.yaml` → 본 프로젝트 CRD 매핑

Bitnami chart 사용자가 본 Operator로 전환할 때 자주 쓰이는 필드 매핑.

| Bitnami `values.yaml` | 본 프로젝트 CRD 필드 | 비고 |
|---|---|---|
| `shards: 2` | `MongoDBSharded.spec.shards.count` | |
| `shardsvr.dataNode.replicaCount: 3` | `MongoDBSharded.spec.shards.membersPerShard` | |
| `configsvr.replicaCount: 3` | `MongoDBSharded.spec.configServer.members` | |
| `mongos.replicaCount: 2` | `MongoDBSharded.spec.mongos.replicas` | |
| `auth.rootUser`, `auth.rootPassword` | `auth.adminCredentialsSecretRef.name` (Secret으로 분리) | |
| `auth.replicaSetKey` | Operator가 자동 생성·관리 | |
| `auth.existingSecret` | `auth.adminCredentialsSecretRef.name` | |
| `tls.enabled: true` | `tls.enabled: true` + `tls.certManager.issuerRef` | cert-manager 권장 |
| `metrics.enabled: true` | `monitoring.enabled: true` | |
| `metrics.podMonitor.enabled: true` | `monitoring.serviceMonitor.enabled: true` | PodMonitor → ServiceMonitor 전환 |
| `persistence.size: 8Gi` | `storage.size: 8Gi` | |
| `persistence.storageClass: ""` | `storage.storageClassName: ""` | |
| `service.type: LoadBalancer` | `mongos.service.type: LoadBalancer` | |
| `shardsvr.arbiter.replicaCount` | **(본 프로젝트 미지원, ROADMAP Phase 4.2)** | 갭 #1 |
| `networkPolicy.enabled: true` | **(미지원, ROADMAP Phase 4.1)** | 갭 #18 |
| `volumePermissions.enabled: true` | **(미지원, ROADMAP Phase 4.5)** | 갭 #12 |
| `common.initScriptsCM` | **(미지원, ROADMAP Phase 4.6)** | 갭 #25 |
| `XXX.sidecars`, `XXX.extraVolumes` | **(미지원, ROADMAP Phase 4.3)** | 갭 #26-28 |
| (Velero 가이드) | `MongoDBBackup` CRD 사용 | 본 프로젝트 우위 |

## 검증 절차 (사실 정확성 확인)

1. **CRD 사실 확인**: `make manifests` 실행 후 `config/crd/bases/*.yaml`에서 매트릭스 행 #1~#10, #16, #17 필드 존재 여부 grep.
2. **Bitnami 최신 values 재확인**: `helm repo add bitnami https://charts.bitnami.com/bitnami && helm show values bitnami/mongodb-sharded --version 9.4.12`로 우측 컬럼 갱신.
3. **ROADMAP 매핑**: 본 문서의 갭 9건이 `ROADMAP.md` Phase 4에 모두 반영되었는지 확인.
4. **라이선스 변경 추적**: Bitnami → Broadcom 정책 변경(2024-08 BSI 도입) 영향이 OSS 사용자에게 미치는 부분을 분기별로 재확인.

## 참고 자료

- Bitnami chart 소스: https://github.com/bitnami/charts/tree/main/bitnami/mongodb-sharded
- ArtifactHub: https://artifacthub.io/packages/helm/bitnami/mongodb-sharded
- 본 프로젝트 ROADMAP: [`ROADMAP.md`](../../ROADMAP.md)
- CRD 정의: `api/v1alpha1/{mongodb,mongodbsharded,mongodbbackup,common}_types.go`
- Driver 매니저: `internal/mongodb/{client,replicaset,sharding,auth}.go`
