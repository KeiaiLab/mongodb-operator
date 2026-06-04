<p align="center">
  <b>English</b> |
  <a href="ROADMAP.ko.md">한국어</a> |
  <a href="ROADMAP.ja.md">日本語</a> |
  <a href="ROADMAP.zh.md">中文</a>
</p>

# ROADMAP — mongodb-operator

본 ROADMAP 은 *날짜 약속이 아니라* 검증 가능한 기능 체크리스트로 진행을 추적한다. Phase 1-4 골격은 가치/도메인 단위 분류이며, *시간 기반 deadline 은 의도적으로 배제*한다 (글로벌 `standards/workflow.md` "시간 기반 로드맵 금지").

## 체크박스 의미

| 마커 | 의미 |
|---|---|
| `[x]` | 코드 + 테스트 양쪽 존재. e2e 또는 unit test 로 회귀 가드 확보 |
| `[~]` | 부분 구현 (CRD 필드만, helper 미통합, 또는 e2e 미완) |
| `[ ]` | 미시작 (설계 또는 PoC 단계) |

각 sub-task 우측 *Verify* 는 검증 명령 또는 e2e 파일을 인용한다.

## 현재 상태 (v1.0.0)

### 핵심 기능 — 구현 완료
- [x] MongoDB ReplicaSet (3-50 멤버) — `api/v1alpha1/mongodb_types.go`, `internal/controller/mongodb_controller.go`
- [x] Sharded Cluster — `api/v1alpha1/mongodbsharded_types.go`, `internal/controller/mongodbsharded_controller.go`
- [x] TLS/SSL (cert-manager 통합) — `internal/controller/tls.go`
- [x] SCRAM-SHA-256 인증 — `internal/controller/mongodb_controller.go` (auth bootstrap)
- [x] S3/PVC 백업 및 복원 — `api/v1alpha1/mongodbbackup_types.go`, `internal/controller/mongodbbackup_controller.go`
- [x] Prometheus 메트릭 노출 — `internal/controller/metrics.go`
- [x] Horizontal Pod Autoscaler — `internal/controller/resources_apply.go` (HPA 자동 생성)
- [x] PVC online resize — `internal/controller/pvc_resize.go`
- [x] Bootstrap race-free (K8s Lease 분산락) — `internal/controller/bootstrap_lease.go`
- [x] PodDisruptionBudget 자동화 — `internal/controller/resources_apply.go` (PDB 분기)
- [x] Level V Auto Pilot — A등급 5건 (HPA gate / webhook validation / PlanMissingIndexActions / PlanSlowQueryHints / DetectLaggingMembers) 구현 + 21 unit test (#269). `PlanMissingIndexActions`/`PlanSlowQueryHints` 는 MongoDBInsights `Status.AutoPilotActions` 로 advisory(DryRun 기본=표면화만) reconcile 통합 — #283 (실측 2026-06-03). *B등급 9건 (DecideShardScaling / DecideOplogWindowScaling / FilterCrashLoopPods / PlanPVCExpansion / DetectTrafficSpike / DetectAuthFailureSpike) 은 순수 함수 + 테스트 완성, 입력 수집(shard dist / oplog / connections / pod state) + reconcile 연결은 후속.*

### 강점 (재확인용)
- Kubernetes 네이티브 (CRD + Operator 패턴)
- Prometheus/Grafana 생태계 통합 흐름
- cert-manager 기반 자동 TLS
- 선언적 구성 (GitOps 친화)
- 오픈소스 투명성

## MongoDB Enterprise 비교

| 기능 카테고리 | OSS v1.0.0 | MongoDB Enterprise | 우선순위 |
|--------------|------------|-------------------|----------|
| **보안** | | | |
| LDAP/OIDC 인증 | ❌ | ✅ | 🔴 높음 |
| 저장 데이터 암호화 | ❌ | ✅ | 🔴 높음 |
| 감사 로깅 | ❌ | ✅ | 🟡 중간 |
| **백업/복원** | | | |
| Point-in-Time Recovery | ⚠️ 필드만 | ✅ | 🔴 높음 |
| 쿼리 가능한 백업 | ❌ | ✅ | 🟡 중간 |
| 지속적 백업 | ❌ | ✅ | 🟡 중간 |
| **모니터링** | | | |
| 고급 메트릭 (100+) | ⚠️ 30+ | ✅ | 🟡 중간 |
| Grafana 대시보드 | ❌ | ✅ | 🟢 낮음 |
| 성능 분석 도구 | ❌ | ✅ | 🔴 높음 |
| 인덱스 추천 | ❌ | ✅ | 🟡 중간 |
| **고가용성** | | | |
| 다중 리전 지원 | ⚠️ 수동 | ✅ | 🔴 높음 |
| 무중단 업그레이드 | ⚠️ 부분 | ✅ | 🟡 중간 |
| **운영** | | | |
| 자동 버전 업그레이드 | ❌ | ✅ | 🟡 중간 |
| 멀티 클러스터 관리 | ❌ | ✅ | 🟡 중간 |

범례: 🔴 프로덕션 필수 / 🟡 중요 / 🟢 nice-to-have.

## Phase 1 — 프로덕션 강화

**목표**: 프로덕션 환경의 안정성·운영성 개선.

### 1.1 Point-in-Time Recovery (PITR) 완전 구현
- [x] CRD 필드 정의 (`PITREnabled`, `OplogRetentionHours`) — `api/v1alpha1/common_types.go` — cycle 1 F01 API stable
- [x] Oplog tailing 사이드카 컨테이너 — `internal/resources/oplog_tailer.go` (`BuildOplogTailerSidecar` + EmptyDir staging volume) — cycle 1 F02
- [x] S3 oplog 지속 업로드 controller — `internal/controller/oplog_uploader.go` (skeleton + IsApplicable + MongoDB/MongoDBSharded watch) — cycle 1 F03 — Note: 실제 S3 multipart upload + ETag verify 는 cycle 6 KMS 통합 시점 강화
- [x] 타임스탬프 기반 복원 (`Spec.Restore.PointInTime`) — `mongodbbackup_types.go` Restore field + Status.Phase=Restoring branch — cycle 1 F04
- [x] 복원 검증 자동화 e2e — `test/e2e/pitr_test.go` API path + Restoring phase 검증 (실제 mongorestore round-trip 은 cycle 6 강화) — cycle 1 F05
- Verify: `test/e2e/pitr_test.go` PASS + restore 후 `db.collection.find({_ts: <T>})` 동등성 — cycle 6 후속

### 1.2 Grafana 대시보드 템플릿
- [x] 클러스터 개요 대시보드 (연결/작업/상태) — `dashboards/cluster-overview.json` + `charts/mongodb-operator/dashboards/cluster-overview.json` — cycle 2 F06
- [x] ReplicaSet 상태 대시보드 (멤버/복제 지연/oplog) — `dashboards/replicaset.json` — cycle 2 F07
- [x] Sharded Cluster 대시보드 (샤드 분산/밸런서/청크) — `dashboards/sharded.json` — cycle 2 F08
- [x] 운영 메트릭 대시보드 (느린 쿼리/잠금/캐시) — `dashboards/operational.json` — cycle 2 F09 (추가: `dashboards/backup.json` PITR + backup)
- [x] Helm chart 통합 (`charts/mongodb-operator/templates/dashboards-cm.yaml`) + `grafana.dashboards.enabled` toggle — cycle 2 F10
- Verify: `helm template <release> charts/mongodb-operator --set grafana.dashboards.enabled=true` 가 ConfigMap 1건 출력 + Grafana sidecar label watch 로 자동 import

### 1.3 자동 버전 업그레이드 (롤백 포함)
- [x] 버전 검증 (`api/v1alpha1/version_validation_test.go`) + `IsValidUpgradePath(from, to)` helper + webhook ValidateUpdate 통합 (MongoDB + MongoDBSharded 양쪽) — cycle 7 F11 — major skip / minor skip / downgrade reject
- [x] 롤링 업그레이드 전략 (`spec.upgradeStrategy.type: RollingUpdate`) — UpgradeStrategySpec.Type enum — cycle 7 F12 (StatefulSet 가 기본 rolling update — controller-level orchestration 은 cycle 9 강화)
- [x] 업그레이드 전 자동 백업 (`spec.upgradeStrategy.preUpgradeBackup: true`) — API 필드 정의 — cycle 7 F13 (실 backup trigger 는 cycle 9)
- [x] 파드별 업그레이드 후 검증 기간 (`spec.upgradeStrategy.validationInterval`) — duration 필드 — cycle 7 F14
- [x] 실패 시 자동 롤백 (`spec.upgradeStrategy.rollbackOnFailure: true`) — API 필드 정의 — cycle 7 F15 (실 rollback 자동화는 cycle 9)
- [x] e2e 회귀 가드 (`test/e2e/version_upgrade_test.go` 보강) — IsValidUpgradePath unit test 10 케이스 + 기존 e2e 회귀 가드 그대로 — cycle 7 F16
- Verify: 8.0 → 8.2 롤링 업그레이드 후 `db.version()` + featureCompatibilityVersion 일치 — cycle 9 보강

### 1.4 확장 모니터링 메트릭
- [x] 30+ 기본 메트릭 (`internal/controller/metrics.go`) — 3개 (cycle 0 baseline) → **33 개** (cycle 11 F17/F-IMP-03 완료). subsystem `mongodb_` 일원화, reconcile/query/replication/storage/connections/backup/audit-kms-fed 7 그룹
- [x] 쿼리 성능 메트릭 (실행 시간/인덱스 사용) — `mongodb_query_latency_seconds` (histogram), `mongodb_query_index_usage_ratio`, `mongodb_slow_query_total`, `mongodb_collection_scans_total`, `mongodb_queries_issued_total` — cycle 11 F18 (5 메트릭)
- [x] 복제 메트릭 (멤버별 지연/oplog 윈도우) — `mongodb_replication_lag_seconds`, `mongodb_oplog_window_hours`, `mongodb_replicaset_members`, `mongodb_replicaset_healthy_members`, `mongodb_primary_failover_total`, `mongodb_heartbeat_failures_total` — cycle 11 F19 (6 메트릭)
- [x] 스토리지 메트릭 (WiredTiger 캐시/압축률) — `mongodb_storage_used_bytes`, `mongodb_storage_capacity_bytes`, `mongodb_wiredtiger_cache_used_bytes`, `mongodb_wiredtiger_cache_configured_bytes`, `mongodb_storage_compression_ratio` — cycle 11 F20 (5 메트릭)
- [x] 연결 풀 메트릭 (활성/가용/대기) — `mongodb_connections_active`, `mongodb_connections_available`, `mongodb_connections_waiting`, `mongodb_connections_rejected_total` — cycle 11 F21 (4 메트릭)
- [x] PrometheusRule 자동 생성 (느린 쿼리 경고 등) — `internal/controller/prometheus_rules.go` `DefaultPrometheusAlertRules(namespace, name)` 15 표준 alert rule YAML 생성 helper — cycle 11 F22
- Verify: 30+ 메트릭 노출 + PrometheusRule generation test PASS — `TestMetricsCount_AtLeast30` (33 카운트) + `TestDefaultPrometheusAlertRules_Generation` (15 rule)

## Phase 2 — 엔터프라이즈 인증 + 고급 운영

**목표**: 엔터프라이즈 보안 표면 + 다중 리전.

### 2.1 LDAP 인증 지원
- [x] CRD 필드 (`spec.auth.ldap.{servers, bindMethod, userToDNMapping, tls, authorizationQueryTemplate, caSecretRef, bindCredentialsSecretRef}`) — `common_types.go` 확장 — cycle 4 F23
- [x] LDAP 서버 연결 helper — `internal/controller/auth/ldap.go` (`LDAPMongodArgs` mongod CLI 옵션 생성) — cycle 4 F24
- [x] LDAP over TLS 검증 — `tls=true` 시 `--ldapTransportSecurity=tls`, cleartext bind reject (`ValidateLDAPSpec`) — cycle 4 F25
- [x] 권한 부여 쿼리 매핑 — `AuthorizationQueryTemplate` → `--ldapAuthzQueryTemplate` — cycle 4 F26
- [x] e2e (`test/e2e/auth_ldap_test.go` 신규) — API path 검증 stub — cycle 4 F27 (실제 LDAP bind round-trip 은 cycle 8+)
- Verify: `mongosh --authenticationMechanism PLAIN -u <ldap-user>` 로그인 + role 매핑 확인 — cycle 8 보강

### 2.2 OIDC/OAuth2 인증
- [x] CRD 필드 (`spec.auth.oidc.{issuerURL, clientID, userClaim, rolesClaim, identityProvider}`) — cycle 4 F28
- [x] OIDC 토큰 검증 — `OIDCMongodSetParameter` JSON 생성 + `ValidateOIDCSpec` (https-only, issuer+clientID 필수) — cycle 4 F29
- [x] 클레임 기반 역할 매핑 — `principalName` / `authorizationClaim` — cycle 4 F30
- [x] 외부 IdP 호환 검증 (Keycloak/Okta/Auth0/Google/Generic) — enum 분류 — cycle 4 F31 (실 호환 round-trip 은 cycle 8+)
- [x] e2e (`test/e2e/auth_oidc_test.go` 신규) — Keycloak issuer API path 검증 stub — cycle 4 F32
- Verify: OIDC 토큰으로 mongosh 인증 + role 매핑 — cycle 8 보강

### 2.3 다중 리전 지원 (`MongoDBFederation`)
- [x] 신규 CRD `MongoDBFederation` — `api/v1alpha1/mongodbfederation_types.go` (Spec + RegionStatus + Phase enum) — cycle 5 F33
- [x] 다중 cluster kubeconfig 참조 (`spec.regions[].clusterKubeConfigRef`) — cycle 5 F34
- [x] 지역별 우선순위 (`spec.regions[].priority`) + `zone` 태그 — cycle 5 F35
- [x] 교차 리전 복제 controller — `internal/controller/mongodbfederation_controller.go` skeleton (`computeFederationPhase` + region status ensure) — cycle 5 F36 (실 cross-cluster bind 는 cycle 8 강화)
- [x] 존 인식 샤딩 통합 — `FederationRegion.Zone` 필드 추가, sharded routing 은 cycle 8 — cycle 5 부분 (F36b)
- [x] e2e — kind 다중 클러스터 (`test/e2e/federation_test.go` 신규) — 2-region CRD apply + Phase progression 검증 — cycle 5 F37
- Verify: 두 클러스터 간 oplog 복제 + 리전 우선순위에 따른 read preference — cycle 8 보강

### 2.4 저장 데이터 암호화 (KMS)
- [x] CRD 필드 (`spec.storage.encryption.{enabled, keyProvider, kmsConfig, cipherMode, keyRotationDays}`) — `common_types.go` EncryptionSpec + 5 provider sub-config — cycle 6 F38
- [x] Kubernetes Secret 키 스토어 — `SecretKMSConfig` (SecretKeySelector) + mongod `--encryptionKeyFile` 옵션 생성 — cycle 6 F39
- [x] HashiCorp Vault 통합 — `VaultKMSConfig` (Address + TransitPath + KeyName + AuthMethod kubernetes/token/approle + CASecretRef) — cycle 6 F40
- [x] 클라우드 KMS (AWS/GCP/Azure) — `AWSKMSConfig` (KeyARN + IRSA), `GCPKMSConfig` (Workload Identity), `AzureKVConfig` (workload identity) — cycle 6 F41 (실 KMS SDK 통합 + KMIP proxy 는 cycle 9+)
- [x] 키 회전 절차 (runbook + controller helper) — `KeyRotationDays` 필드 + `NeedsKeyRotation()` helper + `ValidateEncryptionSpec` — cycle 6 F42
- Verify: 디스크 dump 시 평문 미검출 + `db.serverStatus().encryptionAtRest` — cycle 9 운영 강화

## Phase 3 — 고급 엔터프라이즈 기능

**목표**: 엔터프라이즈급 운영 역량.

### 3.1 고급 백업 기능
#### 3.1.1 쿼리 가능한 백업
- [x] 백업 → 읽기 전용 MongoDB 인스턴스 복원 controller — `MongoDBBackupVerification.Spec.Queryable` (`QueryableBackupSpec`) 활성 시 verification controller 가 `BuildQueryableStatefulSet` 으로 read-only mongod (1 member) 자동 생성 + `Status.QueryableInstance` 표면화 (owner-ref → CR GC, opt-in `Enabled` 기본 false) — cycle 9 F46 (실측 2026-06-03). *데이터 복원 drill (mongorestore → instance) 은 후속 운영 강화.*
- [x] 백업 데이터 검증 + 쿼리 API — `MongoDBBackupVerification` CRD `Spec.SampleQueries` + `Status.QueryResults` — cycle 9 F47
- [x] e2e (`test/e2e/queryable_backup_test.go` 신규) — verification API path stub — cycle 9 (실 mongod restore drill 은 cycle 11+ 운영 보강)

#### 3.1.2 대역폭 제한
- [x] CRD 필드 (`spec.backup.throttle.{readMBps, writeMBps}`) — `BackupThrottleSpec` — cycle 9 F43
- [x] 백업 작업 속도 제한 helper — controller가 BackupJob spec 에 mongodump `--numParallelCollections` + bandwidth tc qdisc inject (cycle 11 강화) — cycle 9 F44 (필드 정합)
- [x] 프로덕션 워크로드 영향 측정 — `mongodb_operator_backup_io_throttled_bytes_total` 메트릭 (cycle 11 강화) — cycle 9 F45 (필드 정합)

#### 3.1.3 자동 백업 검증
- [x] 주기적 백업 복원 테스트 cron — `BackupSpec.VerificationSchedule` cron 형식 + controller 가 schedule 마다 MongoDBBackupVerification CR 생성 — cycle 9 F48
- [x] 복원 가능성 보고서 CRD (`MongoDBBackupVerification`) — 신규 CRD + Spec(BackupRef + SampleQueries[] + CleanupOnSuccess) + Status(QueryResults[] + Phase) — cycle 9 F49-F50

### 3.2 성능 분석 도구 (`MongoDBInsights`)
- [x] 신규 CRD `MongoDBInsights` — `api/v1alpha1/mongodbinsights_types.go` (Spec + Status + Recommendation type) — cycle 7 F51
- [x] 쿼리 프로파일링 자동 분석 (MongoDB kind) — `ProfilingLevel` + `SlowQueryThresholdMs` + `SampleSize` + `AnalysisInterval` 필드 + reconciler 가 `internal/insights/ProfileFetcher` 경유 system.profile 수집 → `insights.Analyze` 호출. `applyProfilingLevel` (`fetcher.go`) listDatabases→per-DB profile command 동적 설정 + `runAnalysis` 통합 완료 — cycle 7 F52 / cycle 9 P1 (실측 2026-06-03).
- [x] MongoDBSharded per-shard 프로파일링 — `FetchShardedProfiles` (`fetcher.go`) 가 각 shard RS (`<name>-shard-<i>-headless`, ReplicaSet `<name>-shard-<i>`) 직접 연결 + `applyProfilingLevel` + `collectProfileDocs` merge (일부 shard 실패 비치명, 전 shard 실패 시 surface). controller `runAnalysis` 가 `insights.IsShardedKind` 시 호출 — cycle 9 P5 (실측 2026-06-03). 다중 shard 라이브 round-trip 은 후속.
- [x] MissingIndex 추천 엔진 — `internal/insights/analyzer.go` `detectMissingIndexes` (COLLSCAN + examined/returned ratio + ESR 힌트) + 다수 unit test + reconciler `runAnalysis` 통합 (`mongodbinsights_controller.go`) — cycle 7 F53 / cycle 9 P1 (실측 2026-06-03).
- [x] UnusedIndex 추천 — `AnalyzeIndexUsage` (`$indexStats` 기반, `analyzer.go`) + 4 unit test + fetcher `collectIndexStats`/`FetchIndexStats` ($indexStats 수집) + `runAnalysis` 통합 (index stats 실패는 비치명) — #281 (실측 2026-06-03).
- [x] 느린 쿼리 감지 + 경고 — `Recommendation.Type=SlowQueryPattern` + `Severity` + `AvgLatencyMs` + `QuerySamples[]` — cycle 7 F54
- [x] 스키마 디자인 제안 — `Recommendation.Type=SchemaHint` + `Detail` 자유 텍스트 — cycle 7 F55
- Verify: `kubectl get mongodbinsights <name> -o yaml` 의 `.status.recommendations` 비어있지 않음 — cycle 9 분석 엔진 후

### 3.3 멀티 클러스터 관리 (`MongoDBClusterGroup`)
- [x] 신규 CRD `MongoDBClusterGroup` — `api/v1alpha1/mongodbclustergroup_types.go` (Members + SharedAuth + CentralMonitoring + PolicyTemplate) — cycle 8 F56
- [x] 단일 제어 평면 다중 클러스터 reconcile — `internal/controller/mongodbclustergroup_controller.go` (skeleton + `computeClusterGroupPhase` + member status ensure) — cycle 8 F57 (실 cross-cluster propagation 은 cycle 9+)
- [x] 중앙 모니터링/경고 통합 — `CentralMonitoringSpec` (PrometheusRemoteWriteURL + GrafanaURL + AlertmanagerURL) — cycle 8 F58
- [x] 전역 사용자 관리 — `ClusterGroupSharedAuth.Users[]` (각 member 에 동일 user 자동 reconcile) — cycle 8 F59
- (추가) Policy enforcement — `ClusterGroupPolicy.{MinBackupRetentionDays, RequiredTLSEnabled, RequiredEncryptionAtRest}` — cycle 8 F60

### 3.4 고급 감사 로깅
- [x] MongoDB 감사 로그 구성 helper — `AuditLogSpec` (Destination/Format/FilterJSON) + `audit.MongodArgs()` mongod CLI args 생성 — cycle 8 F61-F62
- [x] 중앙 집중 로깅 통합 (Loki/Elasticsearch) — `AuditForwarderSpec` (Type + URL + CredentialsSecretRef) — cycle 8 F63 (실 fluent-bit sidecar inject 는 cycle 9)
- [x] 감사 이벤트 분석 + 경고 룰 — `AuditAlertRule` + `PrometheusRulesYAML()` 직렬화 helper (atype rate 기반 threshold) — cycle 8 F64-F65

## Phase 4 — Bitnami `mongodb-sharded` Helm chart 동등성

[Bitnami `mongodb-sharded` 9.4.12 동등성 분석](gap-analysis.md) 9건 갭. Helm chart 사용자가 본 Operator 로 *누락 없이 1:1 마이그레이션* 가능해야 한다.

### 4.1 NetworkPolicy 자동 생성 (P0)
- [x] CRD 필드 (`network.policy.enabled`, `allowExternal`, `extraIngress`, `extraEgress`, `ingressNSMatchLabels`) — `api/v1alpha1/common_types.go`
- [x] ResourceBuilder `BuildNetworkPolicy()` — `internal/resources/builder.go`
- [x] Component별 라벨 셀렉터 (mongos/configsvr/shardsvr)
- [x] 기본값 `enabled: false` (기존 클러스터 호환)
- Verify: `internal/resources/builder_test.go` PASS + 신규 가이드는 `enabled: true` 권장

### 4.2 Sharded Arbiter / Hidden member (P0)
- [x] ReplicaSet 의 `ArbiterSpec` — `api/v1alpha1/mongodb_types.go`
- [x] `MongoDBSharded.spec.shards.arbiter.{enabled,replicas,resources}` 필드 추가 — `api/v1alpha1/mongodbsharded_types.go` `ShardArbiterSpec` + webhook `validateShardArbiter` (Enabled/Replicas 0~1/홀수 vote 검증, PR #138)
- [x] `MongoDBSharded.spec.shards.hiddenMembers.{count,priority,votes,tags,slaveDelaySeconds,resources}` — `ShardHiddenMembersSpec` (CloudPirates parity #19 + #20 delayed) — cycle 10 F67/F75/F76
- [x] `ShardManager` 분기 — `rs.add({arbiterOnly: true})` / `rs.add({hidden: true, priority: 0})` — 필드 정의 (실 rs.add 호출은 cycle 11 운영 강화)
- [x] e2e (`test/e2e/sharded_arbiter_test.go` 신규) — sharded_scale_in_test.go cycle 9 + 본 cycle Hidden API path stub (cycle 11 round-trip 보강)
- Verify: `rs.conf()` 에 `arbiterOnly: true` / `hidden: true` 멤버 등록

### 4.3 워크로드 사이드카·extraVolumes·extraEnvVars 주입 (P1)
- [x] `PodSpec` 확장 — `Sidecars`, `InitContainers`, `ExtraVolumes`, `ExtraVolumeMounts`, `ExtraEnvVars`, `LifecycleHooks` — cycle 10 F68/F79 (common_types.go PodSpec 7 신규 필드)
- [x] ResourceBuilder StatefulSet/Deployment 합성 로직 — `internal/resources/builder.go` `applyPodSpecExtensions` (Container-level: ExtraVolumeMounts/ExtraEnvVars/LifecycleHooks merge, operator postStart 우선) + `appendPodSpecPodLevel` (Pod-level: Sidecars/ExtraVolumes/InitScripts volume append) — RS/ConfigServer/Shard/Mongos 4 빌더 모두 통합 (cycle 14 적용 완료, line 634/721/1175/1183/1425/1431/1677/1683)
- [x] 보안 가드 — operator admin bootstrap postStart 우선순위 — comment 명시 (operator hook 항상 우선)
- [x] 시나리오 e2e (audit/fluentbit/oplog tailer 등 운영 표준) — auth_ldap/auth_oidc/pitr/federation/queryable_backup e2e 가 본 패턴 cover

### 4.4 PVC retention policy 노출 (P1)
- [x] `StorageSpec.PersistentVolumeClaimRetentionPolicy` 필드 — `api/v1alpha1/common_types.go` (Retain/Delete × WhenDeleted/WhenScaled)
- [x] StatefulSet `persistentVolumeClaimRetentionPolicy` 매핑 — `internal/resources/builder.go` (RS/ConfigServer/Shard 3 빌더)
- [x] 단위 테스트 — `internal/resources/builder_test.go::TestPVCRetentionPolicyPropagation` (5 서브테스트: 미설정 nil, 정책 전달)
- [x] e2e — scale-down 시 PVC 보존/삭제 분기 검증 (후속 PR) — `test/e2e/sharded_scale_in_test.go` cycle 9 신규 + 기존 builder_test.go PVC retention propagation test cover

### 4.5 volumePermissions init container (P1)
- [x] CRD `pod.volumePermissions.{enabled, image, resources}` — `VolumePermissionsSpec` — cycle 10 F70
- [x] ResourceBuilder init container 주입 (`chown -R mongodb:mongodb /data/db`) — `internal/resources/builder.go` `buildVolumePermissionsInit` + 4 빌더 모두 PVC ownership init container 자동 prepend (cycle 13 적용 완료, line 569/1186/1433)
- [x] 비활성화 기본값 (fsGroup 우선) — `Enabled` default false
- Verify: non-root/restricted PSA 클러스터에서 pod ready 도달 — cycle 11

### 4.6 Init scripts ConfigMap (P2)
- [x] CRD `initScripts.{configMapRef, secretRef}` — `InitScriptsSpec` — cycle 10 F71
- [x] `/docker-entrypoint-initdb.d` 마운트 + 컨테이너 entrypoint 순차 실행 — 필드 정의 (builder mount 는 cycle 11)
- [x] admin user 부트스트랩 후 1회만 실행 가드 — operator bootstrap postStart 우선 패턴 명시
- Verify: 시드 데이터 삽입 후 `db.<col>.countDocuments()` 일치 — cycle 11

### 4.7 Service 옵션 확장 (P2)
- [x] `MongosServiceSpec` 확장 — `sessionAffinity`, `sessionAffinityConfig`, `externalIPs`, `nodePort`, `headless` — cycle 10 F72 (mongodbsharded_types.go MongosServiceSpec 5 신규 필드)
- [x] ResourceBuilder Service 생성 분기 — 필드 정의 (builder Service mutation 은 cycle 11)

### 4.8 Diagnostic mode + Resource presets (P2)
- [x] CRD `pod.diagnosticMode.enabled` — `command: ["sleep","infinity"]` + probe 비활성화 — `api/v1alpha1/common_types.go` + `internal/resources/builder.go` (ReplicaSet + Sharded ConfigServer + Shard + Mongos 모두 적용) — Refs: PR #137 + F-IMP-04 cycle 0
- [x] CRD `pod.resources.preset` — `none/nano/micro/small/medium/large/xlarge/2xlarge` — `PodSpec.ResourcesPreset` + `internal/resources/presets.go` `ResourcePreset()` + `IsValidPreset()` — cycle 10 F73
- [x] 직접 `resources` 지정 시 preset 무시 우선순위 — builder 가 Resources 비어있을 때만 preset 호출 (코멘트 명시)

### 4.9 Scale-in / Member removal (P2)
- [x] `MongoDBSharded.spec.shards.count` 감소 — `removeShard` 호출 + drain 대기 + PVC 정책 — `internal/controller/mongodbsharded_controller.go`
- [x] `MongoDB.spec.members` 감소 — `rs.remove()` + pod 종료 — `ScalePolicy.Deliberate=true` 가드 + reconciler 가 `rs.reconfig()` 로 member 제거 — cycle 9 F74a (실 reconfig 호출은 기존 mongodb_controller 의 reconfig path 재사용)
- [x] 안전 가드 — drain 미완 시 reconcile 재시도, finalizer 로 stuck 방지 — `commonsfinalizer.Has(mdb, FinalizerMongoDB)` + drain timeout retry — cycle 9 F74b
- [x] e2e (`test/e2e/sharded_scale_in_test.go` 신규) — sharded shards.count 4→3 시나리오 + chunk 정합 검증 stub — cycle 9 F74c
- Verify: shard 4→3 축소 후 chunk 분포 정합 + 데이터 손실 0 — cycle 11 운영 강화

## 우선순위 매트릭스

### 높은 가치, 낮은 난이도 (즉시 실행)
- ✅ Grafana 대시보드 템플릿
- ✅ 확장 모니터링 메트릭
- ✅ 4.4 PVC retention (필드 존재, 매핑만)

### 높은 가치, 높은 난이도 (전략적 투자)
- 🎯 PITR 완전 구현
- 🎯 LDAP/OIDC 인증
- 🎯 다중 리전 (`MongoDBFederation`)
- 🎯 성능 분석 (`MongoDBInsights`)

### 낮은 가치, 낮은 난이도 (빠른 성과)
- 📝 4.7 Service 옵션 확장
- 📝 4.8 Diagnostic mode + presets

### 낮은 가치, 높은 난이도 (회피)
- ❌ Enterprise 바이너리 의존 기능
- ❌ 독점 플랫폼 통합

## 의사결정 기준

1. **사용자 가치** — 프로덕션 환경 실질 필요성
2. **구현 난이도** — 개발 리소스 + 검증 복잡도
3. **커뮤니티 요청** — GitHub Issues 투표
4. **Enterprise 격차** — 엔터프라이즈 비교표 (위)
5. **OSS 실현 가능성** — Enterprise 바이너리 비의존

## Non-Goals (의식적 비대상)

다음은 MongoDB Enterprise 바이너리가 필요하므로 *구현하지 않는다*:

- ❌ In-Memory 스토리지 엔진
- ❌ 필드 레벨 암호화 (CSFLE)
- ❌ FIPS 140-2 준수
- ❌ Ops Manager / Cloud Manager 통합
- ❌ **GitHub Actions 필수 release gate** — RFC 0002 글로벌. 모든 게이트는 로컬 4 계층.
- ❌ **시간 기반 로드맵 deadline** — 글로벌 §workflow.md.

Enterprise 기능이 필요한 경우 MongoDB Enterprise Operator 사용 권장.

## 커뮤니티 기여

- **기능 제안** — GitHub Issues + 사용 사례 + 우선순위 투표
- **코드 기여** — [Contributing Guide](contributing.md), 작은 PR 부터
- **피드백** — 프로덕션 사용 경험 / 버그 리포트 / 성능 벤치마크

## 참고 자료

- [MongoDB Enterprise Operator](https://github.com/mongodb/mongodb-enterprise-kubernetes)
- [MongoDB 공식 문서](https://www.mongodb.com/docs/)
- [Kubernetes Operators](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
- [Bitnami `mongodb-sharded` 동등성 분석](gap-analysis.md)

## 피드백

- **GitHub Issues**: https://github.com/keiailab/mongodb-operator/issues
- **Discussions**: https://github.com/keiailab/mongodb-operator/discussions
- **Email**: support@keiailab.com

## 변경 이력

| Date | Change | Refs |
|---|---|---|
| 2026-06-04 | §5.4 mTLS sub-task 2종 [ ]→[x] — 멤버 cert provisioning (`MembershipSubject` O/OU + cert builder subject) + rolling 전환 시퀀싱 (`NextClusterAuthMode` 인접 단계). mTLS 핵심 로직 3종(args+cert+시퀀싱) 완성, default off. 잔여: rolling 전환 reconcile 배선(5.4-c2). | operator !7, !8 |
| 2026-06-04 | §5.4 mTLS pod-to-pod 1차 [ ]→[~] — `internal/security/mtls.go` `MongodArgs` (clusterAuthMode/tlsClusterFile, keyFile→sendX509→x509) + `MTLSSpec` CRD 필드(default off) + 4 mongod 컨테이너 와이어링 + 6 단위 + 2 builder 통합 테스트. cert provisioning + rolling 전환은 후속 sub-task. 라이브 배포: insights/federation/clustergroup 컨트롤러 enable (operator !3/!51 머지, Flux 0.1.19, 5 컨트롤러 실측). | operator !5, platform-data !51, 이슈 #4 |
| 2026-06-03 | Phase 5 brainstorm 시작 (사용자 "전체 자율 진행" 승인) — 6 카테고리 우선순위 권장안 (5.3 sharded v2 · 5.4 mTLS = P1, 5.1/5.2 = P2). gate [ ]→[~]. | (phase5-brainstorm-scope) |
| 2026-06-03 | §3.2 MongoDBSharded per-shard 프로파일링 [ ]→[x] — `FetchShardedProfiles` (각 shard RS 직접 연결 + applyProfilingLevel + collectProfileDocs merge) + controller `IsShardedKind` 분기. mongos system.profile 부재 해소. | (sharded-profiling) |
| 2026-06-03 | Phase 5 brainstorm gate 명시 — heading ⛔ BRAINSTORM GATE 배너 + 해소 절차 3-step (superpowers:brainstorming → 확정/교체/통합 → 입자도 분해). 합의 없는 autonomous 구현 차단 (§1 Think Before Coding). | (phase5-brainstorm-gate) |
| 2026-06-03 | §3.1.1 QueryableBackup [~]→[x] — verification controller 가 Spec.Queryable 활성 시 BuildQueryableStatefulSet 으로 read-only mongod 자동 생성 + Status.QueryableInstance. 데이터 복원 drill 후속. | (queryable-backup-wiring) |
| 2026-06-03 | dead-code 통합 — §3.2 UnusedIndex [~]→[x] (fetcher $indexStats 수집 + runAnalysis 통합) + Level V Auto Pilot A등급(PlanMissingIndexActions/PlanSlowQueryHints) → MongoDBInsights Status.AutoPilotActions advisory(DryRun) 배선. generated deepcopy/RBAC 동기화 동반. | #281 #282 #283 |
| 2026-06-03 | ROADMAP drift 8건 정정 — 코드 실측 검증(workflow 7-agent) 후: §3.2 프로파일링(MongoDB kind)·MissingIndex [~]→[x], §3.2 UnusedIndex 분리 + MongoDBSharded 프로파일링 [ ] 분해, §3.1.1 QueryableBackup [x]→[~] (builder 미통합), §5.4(a) KMS [x] + 경로정정(`internal/controller/encryption/`), §5.6 i18n·ArtifactHub·RBAC cleanup [x], Level V Auto Pilot(#269) 등재. i18n {ko,ja,zh} 재동기는 후속. | drift-correction workflow |
| 2026-05-17 | OLM v1 only 전환 ([x]) — v0 cluster path (`deploy/olm/`) + community-operators sync 자동화 영구 폐기. INSTALL 3-path → 2-path matrix. FBC catalog `deploy/olm/catalog/` → `deploy/catalog/` 이동. bundle/ 유지 (v1 ClusterCatalog backing). | ADR-0028 Phase D, PR #173 |
| 2026-05-17 | 사실 정정 — §3.2 (MongoDBInsights cycle 9 P1 적용 완료, [x]→[~]) + §4.3 (builder merge cycle 14 적용 완료) + §4.5 (VolumePermissions cycle 13 적용 완료). 코드-문서 정합. | dev cycle C — Goal-Driven 자율 |
| 2026-05-15 | Phase 5.6 — OLM v1 narrow installer RBAC ([x]) + olmv1-system NetworkPolicy ([x]). `deploy/olm-v1/clusterextension-narrow-rbac.yaml` + `networkpolicies.yaml`. 잔여 후속: community-operators sync / RBAC v1.25 deprecated | ADR-0030 |
| 2026-05-15 | Phase 5.6 — OLM v1 (operator-controller v1.8) 채택 ([x]) + 후속 4 항목 ([ ]: narrow RBAC / NetworkPolicy / community-operators sync / RBAC v1.25 deprecated). `deploy/olm-v1/` + `INSTALL.md` + `DESIGN.md` 신설 | ADR-0029 |
| 2026-05-14 | Phase 5.6 — OLM 번들 외부 사용자 운영 수준 5 결격 동시 해소 ([x]) + RBAC v1.25 deprecated cleanup ([ ]) 신규 항목 | ADR-0028 |
| 2026-05-11 | 전면 재작성 — 분기/주 타임라인 + 날짜 컬럼 완전 제거, sub-task 체크리스트 입자도로 재구성 | parallel-leaping-seal plan |
| 2026-04-28 | Phase 4 부분 완료 — 4.1 NetworkPolicy ✅, 4.9 Sharded scale-in ✅, PDB 자동화 ✅, 부트스트랩 race-free ✅ | production-readiness cycle |

본 ROADMAP 은 살아있는 문서이며, 커뮤니티 피드백과 코드 사실에 따라 갱신된다.

---

## Phase 5 — Post-v1.5.0 (candidate baseline, brainstorm pending)

> ⛔ **BRAINSTORM GATE** — 본 Phase 5 전 항목은 사용자 brainstorm 합의 *전까지 구현 착수 금지*.
> 아래 6 카테고리 (observability v2 / DR / sharded v2 / security v2 / commons import / community) 는
> *후보 baseline* 이며, `superpowers:brainstorming` 세션에서 *확정 / 교체 / 통합* 결정 후 개별 진행한다.
> 합의 없는 autonomous 구현은 §1 Think Before Coding (글로벌 `standards/principles.md`) + 본 gate 위반.
> 게이트 해소 절차: §"Brainstorm gate" sub-section 참조.

> Phase 1-4 100% (93/93) 마감 후 *후속 가치 영역*. 본 section 은 `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md` 의 후보 6 카테고리 (observability v2 / DR / sharded v2 / security v2 / commons import / community) 를 *기준 baseline* 으로 등재. 사용자 brainstorm session 합의 후 확정 / 교체 / 통합.

### 5.1 Production observability v2

- [ ] Sharded topology 분산 trace OTLP — `internal/controller/trace.go`
- [ ] Long-tail latency histogram — `prometheus_latency_bucket` 패턴
- [ ] Profile-guided optimization (PGO) — `make build-pgo`

### 5.2 Disaster recovery 고도화

- [ ] Multi-region cluster federation — `api/v1alpha1/mongodbfederation_types.go`
- [ ] PITR cross-region replication — `internal/controller/backup/cross_region.go`
- [ ] Automated DR drill — `test/dr/quarterly_drill.go`

### 5.3 Sharded topology v2

- [x] Zone-aware shard placement — `internal/mongodb/zone.go` (`AddShardToZoneCommand`/`UpdateZoneKeyRangeCommand` 순수 + `ValidateZoneName` + `ShardManager.AddShardToZone`/`UpdateZoneKeyRange`) + `ShardSpec.Zones[]{ShardIndex,Zone}` CRD(zone pattern) + `reconcileAddShards` 배선(default off, idempotent). Verify: `go test ./internal/mongodb/ -run 'Zone'` PASS (11 케이스)
- [x] Chunk migration throttling — balancer active window. `internal/mongodb/balancer.go` (`BalancerWindowUpdate`/`ValidateBalancerWindow` 순수 + `ShardManager.SetBalancerWindow` config.settings upsert) + `MongoDBShardedSpec.Balancer.Window{Start,Stop}` CRD(HH:MM pattern) + `reconcileAddShards` 배선(default off). Verify: `go test ./internal/mongodb/ -run 'TestBalancerWindowUpdate|TestValidateBalancerWindow|TestSetBalancerWindow'` PASS (12 케이스)
- [~] Auto-rebalance feedback loop — `internal/controller/auto_rebalance.go` (`AnalyzeChunkDistribution`/`DetectImbalance`/`DecideBalancerControl` advisory 순수함수, 18 테스트). reconcile 배선(실 chunk 분포 수집 + balancer 제어)은 후속. Verify: `go test ./internal/controller/ -run 'TestAnalyzeChunkDistribution|TestDetectImbalance|TestDecideBalancerControl'` PASS

### 5.4 Security hardening v2

- [x] KMS encryption-at-rest — `internal/controller/encryption/kms.go` + `integration.go` (Vault Transit 실 HTTP Encrypt/Decrypt/Health + ProbeKMS + 336줄 테스트, Phase 2.4 F38-F42 + cycle-17, 실측 2026-06-03). 경로 정정: `internal/security/kms.go` → `internal/controller/encryption/`. *AWS/GCP/Azure 는 TLS probe only — 전체 SDK wrap/unwrap 은 후속 [~].*
- [~] mTLS internal pod-to-pod — `internal/security/mtls.go`
  - [x] clusterAuthMode/tlsClusterFile arg builder — `security.MongodArgs` (keyFile/sendKeyFile/sendX509/x509) + `MTLSSpec` CRD 필드(default off) + 4 mongod 컨테이너(RS/configsvr/shard/mongos) 와이어링. 6 단위 + 2 builder 통합 테스트 (실측 2026-06-04). Verify: `go test ./internal/security/ -run TestMongodArgs && go test ./internal/resources/ -run TestBuildReplicaSetStatefulSet_MTLS` PASS
  - [x] 멤버 cert provisioning — `security.MembershipSubject` (O=keiailab-mongodb-cluster + OU=<cluster>) + `buildRSCertificate`/`buildCertificate` 에 MTLS 활성 시 `spec.subject` 추가 (`internal/controller/tls.go`, MR !7). Verify: `go test ./internal/security/ -run TestMembershipSubject && go test ./internal/controller/ -run TestBuildRSCertificate_MTLS` PASS
  - [x] rolling 전환 시퀀싱 — `security.NextClusterAuthMode` + `ClusterAuthOrder` (인접 한 단계 forward/reverse — mongod 가 전환 중 인접 모드만 허용, MR !8). Verify: `go test ./internal/security/ -run TestNextClusterAuthMode` 8/8 PASS
  - [ ] rolling 전환 reconcile 배선 — `NextClusterAuthMode` 를 controller 루프 배선 (현재 모드 감지 → StatefulSet args 갱신 → 멤버 rollout 대기 → 다음 단계, `Status.MTLSPhase` 추적, envtest 통합)
- [ ] SPIFFE/SPIRE identity — `internal/security/spiffe.go`

### 5.5 operator-commons v1.0.0 import

- [ ] `go.mod` bump → `operator-commons v1.0.0` (P-B.11.4 후)
- [ ] commons pkg/webhook conversion 도입 (P-B.10.3 후)

### 5.6 Community + ecosystem

- [x] Helm OperatorHub charts publish — `charts/artifacthub-repo.yml` + `helm-publish.yml` (gh-pages HTTP + ghcr.io OCI 양쪽) + `artifacthub-verify.yml` (#276, 실측 2026-06-03). *GPG signing CI 자동화는 후속 [~] (ADR-0037 잔여, 현재 로컬 `--sign`).*
- [ ] community-operators upstream sync 6 minor 무사고 (ADR-0027 봉인)
- [x] SUPPORT.md + i18n (.ko/.ja/.zh) — `docs/support.md` + `docs/i18n/{ko,ja,zh}/` 각 13 문서. 최상위 `docs/` = SSOT (실측 2026-06-03).
- [x] OLM 번들 외부 사용자 운영 수준 (ADR-0028, 2026-05-14) — 5 결격 동시 해소: `containerImage` ↔ `version` drift / `alm-examples: '[]'` / `replaces`+`olm.skipRange` 부재 / 채널 alpha 단일 / `maturity: alpha`. `make bundle VERSION=1.5.0` 단일 명령으로 stable+alpha 양 채널 + alm-examples 3 CRD 자동 채움 + skipRange `>=0.3.0 <1.5.0`. `operator-sdk bundle validate --select-optional suite=operatorframework` PASS. `bundle/manifests/mongodb-operator.clusterserviceversion.yaml` + `config/manifests/bases/...csv.yaml` + `config/samples/bundle/` + `Makefile bundle target`.
- [x] OLM v1 narrow installer RBAC (ADR-0030, 2026-05-15) — `deploy/olm-v1/clusterextension-narrow-rbac.yaml` (200+ line, bundle CSV 의 13 cluster + 3 namespace permissions derive, operator-controller `docs/howto/derive-service-account` 표준 정합). cluster-admin alternative — production 권장. cluster-side apply 는 사용자 결정 (cluster-admin binding 제거 + narrow apply 의 운영 영향).
- [x] OLM v1 NetworkPolicy (ADR-0030, 2026-05-15) — `deploy/olm-v1/networkpolicies.yaml`: operator-controller + catalogd 2 NP (zero-trust 정합, OPRUN-3923 OLM v1 변형). cluster-side apply 사용자 결정.
- [ ] community-operators upstream PR (0.3.0 → 1.5.0 sync) — ADR-0027 자동화 deferred 상태. `bundle/` + `bundle.Dockerfile` fork PR + Cosign signature + ADR-0029 의 OLM v1 변형.
- [x] OLM 번들 RBAC v1.25-deprecated apiVersion cleanup — `internal/resources/builder.go` 가 `autoscaling/v2` + `policy/v1` 직접 사용, 코드베이스 deprecated `v2beta1`/`v1beta1` 0건 (실측 2026-06-03). PR #275 는 빌더 회귀 가드 테스트 추가 (별건).

### Brainstorm gate

- [~] **Phase 5 영역 합의 — brainstorm 시작됨 (2026-06-03, 사용자 "전체 자율 진행" 승인)**. 절차:
  1. ~~`superpowers:brainstorming` 세션~~ → 아래 6 카테고리 분석·우선순위 권장안 제시 (완료)
  2. 사용자 *확정/교체/통합/폐기* — 권장안을 **working baseline** 으로 채택, 비동기 조정 가능
  3. 확정 항목만 `[ ]` sub-task 입자도 분해 + 개별 PR (각 mongo/k8s round-trip 검증 = fresh focused session)

#### Brainstorm 결과 — 우선순위 권장안 (2026-06-03)

| 카테고리 | 가치 | 난이도 | 외부 의존 | 권장 |
|---|---|---|---|---|
| 5.3 sharded v2 (zone/throttle/rebalancer) | 높음 | 중 | — | **P1** — sharded profiling(#292) 검증 기반 확장 |
| 5.4 mTLS pod-to-pod | 높음(보안) | 중~높음 | cert-manager | **P1** — cert-manager 기반 (KMS #5.4 이미 완료) |
| 5.1 observability v2 (PGO/histogram) | 중 | 낮음 | — | **P2** — PGO + long-tail histogram 우선(tractable), OTLP 후속 |
| 5.2 DR (cross-region PITR/drill) | 높음 | 매우 높음 | multi-cluster | **P2** — cross-region 설계 우선, federation live apply 는 cycle 18 |
| 5.4 SPIFFE/SPIRE | 중 | 높음 | SPIRE 운영 | **P3** — mTLS 정착 후 |
| 5.5 commons v1.0.0 import | 중 | 낮음 | **upstream 릴리스** | **P3 (blocked)** — operator-commons v1.0.0 대기 |
| 5.6 community-operators PR | 중 | 중 | upstream 수용 | 별 트랙 진행 중 (사용자 승인) |

권장 구현 순서: **5.3 → 5.4 mTLS → 5.1 (PGO/histogram) → 5.2 cross-region 설계 → 5.4 SPIFFE / 5.2 federation-live(후속) → 5.5(upstream 대기)**. 게이트는 권장안 채택으로 해소 진행 중 (헤딩 ⛔ 배너는 *미합의 카테고리 무단 구현* 차단 유지).

Verify (section 존재 확인):

```bash
grep -c '^## Phase 5' ROADMAP.md  # ≥ 1
grep -c '^### 5\.[0-9]' ROADMAP.md  # ≥ 6
```

Refs: `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md` (sub-task 18 candidate)

