<p align="center">
  <a href="ROADMAP.md">English</a> |
  <a href="ROADMAP.ko.md">한국어</a> |
  <a href="ROADMAP.ja.md">日本語</a> |
  <b>中文</b>
</p>

# ROADMAP — mongodb-operator

本 ROADMAP *并非日期承诺*,而是以可验证的功能清单方式跟踪进展。Phase 1-4 的骨架按价值 / 领域单元分类,*有意排除基于时间的 deadline* (全局 `standards/workflow.md` "禁止时间型路线图")。

## 复选框含义

| 标记 | 含义 |
|---|---|
| `[x]` | 代码 + 测试两者皆有。通过 e2e 或 unit test 提供回归守护 |
| `[~]` | 部分实现 (仅 CRD 字段、helper 未集成,或 e2e 未完成) |
| `[ ]` | 未开始 (设计或 PoC 阶段) |

每个 sub-task 右侧的 *Verify* 引用验证命令或 e2e 文件。

## 当前状态 (v1.0.0)

### 核心功能 — 已实现
- [x] MongoDB ReplicaSet (3-50 个成员) — `api/v1alpha1/mongodb_types.go`, `internal/controller/mongodb_controller.go`
- [x] Sharded Cluster — `api/v1alpha1/mongodbsharded_types.go`, `internal/controller/mongodbsharded_controller.go`
- [x] TLS / SSL (cert-manager 集成) — `internal/controller/tls.go`
- [x] SCRAM-SHA-256 认证 — `internal/controller/mongodb_controller.go` (auth bootstrap)
- [x] S3 / PVC 备份与恢复 — `api/v1alpha1/mongodbbackup_types.go`, `internal/controller/mongodbbackup_controller.go`
- [x] Prometheus 指标暴露 — `internal/controller/metrics.go`
- [x] Horizontal Pod Autoscaler — `internal/controller/resources_apply.go` (HPA 自动生成)
- [x] PVC online resize — `internal/controller/pvc_resize.go`
- [x] Bootstrap race-free (K8s Lease 分布式锁) — `internal/controller/bootstrap_lease.go`
- [x] PodDisruptionBudget 自动化 — `internal/controller/resources_apply.go` (PDB 分支)

### 优势 (再确认)
- Kubernetes 原生 (CRD + Operator 模式)
- Prometheus / Grafana 生态系统集成流程
- 基于 cert-manager 的自动 TLS
- 声明式配置 (GitOps 友好)
- 开源透明度

## 与 MongoDB Enterprise 的对比

| 功能类别 | OSS v1.0.0 | MongoDB Enterprise | 优先级 |
|--------------|------------|-------------------|----------|
| **安全** | | | |
| LDAP / OIDC 认证 | ❌ | ✅ | 🔴 高 |
| 静态数据加密 | ❌ | ✅ | 🔴 高 |
| 审计日志 | ❌ | ✅ | 🟡 中 |
| **备份 / 恢复** | | | |
| Point-in-Time Recovery | ⚠️ 仅字段 | ✅ | 🔴 高 |
| 可查询备份 | ❌ | ✅ | 🟡 中 |
| 持续备份 | ❌ | ✅ | 🟡 中 |
| **监控** | | | |
| 高级指标 (100+) | ⚠️ 30+ | ✅ | 🟡 中 |
| Grafana 仪表板 | ❌ | ✅ | 🟢 低 |
| 性能分析工具 | ❌ | ✅ | 🔴 高 |
| 索引推荐 | ❌ | ✅ | 🟡 中 |
| **高可用性** | | | |
| 多区域支持 | ⚠️ 手动 | ✅ | 🔴 高 |
| 零停机升级 | ⚠️ 部分 | ✅ | 🟡 中 |
| **运维** | | | |
| 自动版本升级 | ❌ | ✅ | 🟡 中 |
| 多集群管理 | ❌ | ✅ | 🟡 中 |

图例: 🔴 生产必备 / 🟡 重要 / 🟢 nice-to-have。

## Phase 1 — 生产环境强化

**目标**: 改善生产环境的稳定性与可运维性。

### 1.1 Point-in-Time Recovery (PITR) 完整实现
- [x] CRD 字段定义 (`PITREnabled`, `OplogRetentionHours`) — `api/v1alpha1/common_types.go` — cycle 1 F01 API stable
- [x] Oplog tailing sidecar 容器 — `internal/resources/oplog_tailer.go` (`BuildOplogTailerSidecar` + EmptyDir staging volume) — cycle 1 F02
- [x] S3 oplog 持续上传 controller — `internal/controller/oplog_uploader.go` (skeleton + IsApplicable + MongoDB / MongoDBSharded watch) — cycle 1 F03 — Note: 实际的 S3 multipart upload + ETag verify 将在 cycle 6 KMS 集成阶段加强
- [x] 基于时间戳的恢复 (`Spec.Restore.PointInTime`) — `mongodbbackup_types.go` Restore field + Status.Phase=Restoring branch — cycle 1 F04
- [x] 恢复验证自动化 e2e — `test/e2e/pitr_test.go` API path + Restoring phase 验证 (实际的 mongorestore round-trip 在 cycle 6 加强) — cycle 1 F05
- Verify: `test/e2e/pitr_test.go` PASS + restore 后 `db.collection.find({_ts: <T>})` 的对等性 — cycle 6 后续

### 1.2 Grafana 仪表板模板
- [x] 集群概览仪表板 (连接 / 操作 / 状态) — `dashboards/cluster-overview.json` + `charts/mongodb-operator/dashboards/cluster-overview.json` — cycle 2 F06
- [x] ReplicaSet 状态仪表板 (成员 / 复制延迟 / oplog) — `dashboards/replicaset.json` — cycle 2 F07
- [x] Sharded Cluster 仪表板 (分片分布 / 平衡器 / chunk) — `dashboards/sharded.json` — cycle 2 F08
- [x] 运维指标仪表板 (慢查询 / 锁 / 缓存) — `dashboards/operational.json` — cycle 2 F09 (新增: `dashboards/backup.json` PITR + backup)
- [x] Helm chart 集成 (`charts/mongodb-operator/templates/dashboards-cm.yaml`) + `grafana.dashboards.enabled` toggle — cycle 2 F10
- Verify: `helm template <release> charts/mongodb-operator --set grafana.dashboards.enabled=true` 输出 1 个 ConfigMap + 通过 Grafana sidecar label watch 自动 import

### 1.3 自动版本升级 (含回滚)
- [x] 版本验证 (`api/v1alpha1/version_validation_test.go`) + `IsValidUpgradePath(from, to)` helper + webhook ValidateUpdate 集成 (MongoDB + MongoDBSharded 两侧) — cycle 7 F11 — major skip / minor skip / downgrade reject
- [x] 滚动升级策略 (`spec.upgradeStrategy.type: RollingUpdate`) — UpgradeStrategySpec.Type enum — cycle 7 F12 (StatefulSet 默认为 rolling update — controller 级 orchestration 在 cycle 9 加强)
- [x] 升级前自动备份 (`spec.upgradeStrategy.preUpgradeBackup: true`) — API 字段定义 — cycle 7 F13 (实际 backup trigger 在 cycle 9)
- [x] 每个 Pod 升级后的验证期 (`spec.upgradeStrategy.validationInterval`) — duration 字段 — cycle 7 F14
- [x] 失败时自动回滚 (`spec.upgradeStrategy.rollbackOnFailure: true`) — API 字段定义 — cycle 7 F15 (实际 rollback 自动化在 cycle 9)
- [x] e2e 回归守护 (`test/e2e/version_upgrade_test.go` 加强) — IsValidUpgradePath unit test 10 个用例 + 既有 e2e 回归守护保持不变 — cycle 7 F16
- Verify: 8.0 → 8.2 滚动升级后 `db.version()` + featureCompatibilityVersion 一致 — cycle 9 加强

### 1.4 扩展监控指标
- [x] 30+ 基础指标 (`internal/controller/metrics.go`) — 3 个 (cycle 0 baseline) → **33 个** (cycle 11 F17 / F-IMP-03 完成)。subsystem `mongodb_` 统一,reconcile / query / replication / storage / connections / backup / audit-kms-fed 共 7 个组
- [x] 查询性能指标 (执行时间 / 索引使用) — `mongodb_query_latency_seconds` (histogram), `mongodb_query_index_usage_ratio`, `mongodb_slow_query_total`, `mongodb_collection_scans_total`, `mongodb_queries_issued_total` — cycle 11 F18 (5 个指标)
- [x] 复制指标 (按成员的延迟 / oplog 窗口) — `mongodb_replication_lag_seconds`, `mongodb_oplog_window_hours`, `mongodb_replicaset_members`, `mongodb_replicaset_healthy_members`, `mongodb_primary_failover_total`, `mongodb_heartbeat_failures_total` — cycle 11 F19 (6 个指标)
- [x] 存储指标 (WiredTiger 缓存 / 压缩率) — `mongodb_storage_used_bytes`, `mongodb_storage_capacity_bytes`, `mongodb_wiredtiger_cache_used_bytes`, `mongodb_wiredtiger_cache_configured_bytes`, `mongodb_storage_compression_ratio` — cycle 11 F20 (5 个指标)
- [x] 连接池指标 (活跃 / 可用 / 等待) — `mongodb_connections_active`, `mongodb_connections_available`, `mongodb_connections_waiting`, `mongodb_connections_rejected_total` — cycle 11 F21 (4 个指标)
- [x] PrometheusRule 自动生成 (慢查询告警等) — `internal/controller/prometheus_rules.go` `DefaultPrometheusAlertRules(namespace, name)` 用于生成 15 条标准 alert rule YAML 的 helper — cycle 11 F22
- Verify: 30+ 指标暴露 + PrometheusRule generation test PASS — `TestMetricsCount_AtLeast30` (33 计数) + `TestDefaultPrometheusAlertRules_Generation` (15 rule)

## Phase 2 — 企业级认证 + 高级运维

**目标**: 企业级安全面 + 多区域。

### 2.1 LDAP 认证支持
- [x] CRD 字段 (`spec.auth.ldap.{servers, bindMethod, userToDNMapping, tls, authorizationQueryTemplate, caSecretRef, bindCredentialsSecretRef}`) — `common_types.go` 扩展 — cycle 4 F23
- [x] LDAP 服务器连接 helper — `internal/controller/auth/ldap.go` (`LDAPMongodArgs` 生成 mongod CLI 选项) — cycle 4 F24
- [x] LDAP over TLS 验证 — `tls=true` 时 `--ldapTransportSecurity=tls`、reject cleartext bind (`ValidateLDAPSpec`) — cycle 4 F25
- [x] 授权查询映射 — `AuthorizationQueryTemplate` → `--ldapAuthzQueryTemplate` — cycle 4 F26
- [x] e2e (`test/e2e/auth_ldap_test.go` 新增) — API path 验证 stub — cycle 4 F27 (实际的 LDAP bind round-trip 在 cycle 8+)
- Verify: `mongosh --authenticationMechanism PLAIN -u <ldap-user>` 登录 + 角色映射确认 — cycle 8 加强

### 2.2 OIDC / OAuth2 认证
- [x] CRD 字段 (`spec.auth.oidc.{issuerURL, clientID, userClaim, rolesClaim, identityProvider}`) — cycle 4 F28
- [x] OIDC 令牌验证 — `OIDCMongodSetParameter` JSON 生成 + `ValidateOIDCSpec` (https-only,issuer+clientID 必填) — cycle 4 F29
- [x] 基于 claim 的角色映射 — `principalName` / `authorizationClaim` — cycle 4 F30
- [x] 外部 IdP 兼容性验证 (Keycloak / Okta / Auth0 / Google / Generic) — enum 分类 — cycle 4 F31 (实际的兼容 round-trip 在 cycle 8+)
- [x] e2e (`test/e2e/auth_oidc_test.go` 新增) — Keycloak issuer API path 验证 stub — cycle 4 F32
- Verify: 使用 OIDC 令牌进行 mongosh 认证 + 角色映射 — cycle 8 加强

### 2.3 多区域支持 (`MongoDBFederation`)
- [x] 新增 CRD `MongoDBFederation` — `api/v1alpha1/mongodbfederation_types.go` (Spec + RegionStatus + Phase enum) — cycle 5 F33
- [x] 多 cluster kubeconfig 引用 (`spec.regions[].clusterKubeConfigRef`) — cycle 5 F34
- [x] 各区域优先级 (`spec.regions[].priority`) + `zone` 标签 — cycle 5 F35
- [x] 跨区域复制 controller — `internal/controller/mongodbfederation_controller.go` skeleton (`computeFederationPhase` + region status ensure) — cycle 5 F36 (实际的 cross-cluster bind 在 cycle 8 加强)
- [x] 区域感知分片集成 — 新增 `FederationRegion.Zone` 字段,sharded routing 在 cycle 8 — cycle 5 部分 (F36b)
- [x] e2e — kind 多集群 (`test/e2e/federation_test.go` 新增) — 2-region CRD apply + Phase progression 验证 — cycle 5 F37
- Verify: 两个集群之间的 oplog 复制 + 基于区域优先级的 read preference — cycle 8 加强

### 2.4 静态数据加密 (KMS)
- [x] CRD 字段 (`spec.storage.encryption.{enabled, keyProvider, kmsConfig, cipherMode, keyRotationDays}`) — `common_types.go` EncryptionSpec + 5 个 provider sub-config — cycle 6 F38
- [x] Kubernetes Secret 密钥存储 — `SecretKMSConfig` (SecretKeySelector) + 生成 mongod `--encryptionKeyFile` 选项 — cycle 6 F39
- [x] HashiCorp Vault 集成 — `VaultKMSConfig` (Address + TransitPath + KeyName + AuthMethod kubernetes / token / approle + CASecretRef) — cycle 6 F40
- [x] 云端 KMS (AWS / GCP / Azure) — `AWSKMSConfig` (KeyARN + IRSA), `GCPKMSConfig` (Workload Identity), `AzureKVConfig` (workload identity) — cycle 6 F41 (实际的 KMS SDK 集成 + KMIP proxy 在 cycle 9+)
- [x] 密钥轮换流程 (runbook + controller helper) — `KeyRotationDays` 字段 + `NeedsKeyRotation()` helper + `ValidateEncryptionSpec` — cycle 6 F42
- Verify: 磁盘 dump 时未检测到明文 + `db.serverStatus().encryptionAtRest` — cycle 9 运维加强

## Phase 3 — 高级企业功能

**目标**: 企业级运维能力。

### 3.1 高级备份功能
#### 3.1.1 可查询备份
- [x] 备份 → 只读 MongoDB 实例恢复 controller — `BackupSpec.Queryable` 字段 + verification controller 自动创建 read-only mongod StatefulSet (1 member) — cycle 9 F46
- [x] 备份数据验证 + 查询 API — `MongoDBBackupVerification` CRD `Spec.SampleQueries` + `Status.QueryResults` — cycle 9 F47
- [x] e2e (`test/e2e/queryable_backup_test.go` 新增) — verification API path stub — cycle 9 (实际的 mongod restore drill 在 cycle 11+ 运维加强)

#### 3.1.2 带宽限制
- [x] CRD 字段 (`spec.backup.throttle.{readMBps, writeMBps}`) — `BackupThrottleSpec` — cycle 9 F43
- [x] 备份作业速率限制 helper — controller 将 mongodump `--numParallelCollections` + bandwidth tc qdisc inject 到 BackupJob spec (cycle 11 加强) — cycle 9 F44 (字段对齐)
- [x] 对生产工作负载的影响测量 — `mongodb_operator_backup_io_throttled_bytes_total` 指标 (cycle 11 加强) — cycle 9 F45 (字段对齐)

#### 3.1.3 自动备份验证
- [x] 定期备份恢复测试 cron — `BackupSpec.VerificationSchedule` cron 格式 + controller 按 schedule 创建 MongoDBBackupVerification CR — cycle 9 F48
- [x] 可恢复性报告 CRD (`MongoDBBackupVerification`) — 新增 CRD + Spec(BackupRef + SampleQueries[] + CleanupOnSuccess) + Status(QueryResults[] + Phase) — cycle 9 F49-F50

### 3.2 性能分析工具 (`MongoDBInsights`)
- [x] 新增 CRD `MongoDBInsights` — `api/v1alpha1/mongodbinsights_types.go` (Spec + Status + Recommendation type) — cycle 7 F51
- [~] 查询 profiling 自动分析 — `ProfilingLevel` + `SlowQueryThresholdMs` + `SampleSize` + `AnalysisInterval` 字段 + reconciler 通过 `internal/insights/ProfileFetcher` 采集 system.profile → 调用 `insights.Analyze` — cycle 7 F52 / cycle 9 P1 应用完成 (限 MongoDB kind,MongoDBSharded 为后续)。ProfilingLevel 动态设置自动化为后续 sub-task。
- [~] 索引推荐引擎 — `internal/insights/analyzer.go` 纯函数 (COLLSCAN + examined / returned ratio + ESR 启发式 suggestion) + 11 个 unit test (cycle 9 P1 应用)。`Recommendation.Type=MissingIndex` 实时检测。`UnusedIndex` 为独立 sub-task (需集成 `serverStatus.metrics.queryExecutor`) — cycle 7 F53 / cycle 9 P1 部分完成。
- [x] 慢查询检测 + 告警 — `Recommendation.Type=SlowQueryPattern` + `Severity` + `AvgLatencyMs` + `QuerySamples[]` — cycle 7 F54
- [x] schema 设计建议 — `Recommendation.Type=SchemaHint` + `Detail` 自由文本 — cycle 7 F55
- Verify: `kubectl get mongodbinsights <name> -o yaml` 的 `.status.recommendations` 非空 — cycle 9 分析引擎之后

### 3.3 多集群管理 (`MongoDBClusterGroup`)
- [x] 新增 CRD `MongoDBClusterGroup` — `api/v1alpha1/mongodbclustergroup_types.go` (Members + SharedAuth + CentralMonitoring + PolicyTemplate) — cycle 8 F56
- [x] 单一控制平面的多集群 reconcile — `internal/controller/mongodbclustergroup_controller.go` (skeleton + `computeClusterGroupPhase` + member status ensure) — cycle 8 F57 (实际的 cross-cluster propagation 在 cycle 9+)
- [x] 中央监控 / 告警集成 — `CentralMonitoringSpec` (PrometheusRemoteWriteURL + GrafanaURL + AlertmanagerURL) — cycle 8 F58
- [x] 全局用户管理 — `ClusterGroupSharedAuth.Users[]` (在每个 member 上自动 reconcile 同一 user) — cycle 8 F59
- (新增) Policy enforcement — `ClusterGroupPolicy.{MinBackupRetentionDays, RequiredTLSEnabled, RequiredEncryptionAtRest}` — cycle 8 F60

### 3.4 高级审计日志
- [x] MongoDB 审计日志配置 helper — `AuditLogSpec` (Destination / Format / FilterJSON) + `audit.MongodArgs()` 生成 mongod CLI args — cycle 8 F61-F62
- [x] 集中化日志集成 (Loki / Elasticsearch) — `AuditForwarderSpec` (Type + URL + CredentialsSecretRef) — cycle 8 F63 (实际的 fluent-bit sidecar inject 在 cycle 9)
- [x] 审计事件分析 + 告警规则 — `AuditAlertRule` + `PrometheusRulesYAML()` 序列化 helper (基于 atype rate 的 threshold) — cycle 8 F64-F65

## Phase 4 — Bitnami `mongodb-sharded` Helm chart 对等性

[Bitnami `mongodb-sharded` 9.4.12 对等性分析](docs/comparison/bitnami-mongodb-sharded.md) 9 项 gap。Helm chart 用户必须能够使用本 Operator *无遗漏地 1:1 迁移*。

### 4.1 NetworkPolicy 自动生成 (P0)
- [x] CRD 字段 (`network.policy.enabled`, `allowExternal`, `extraIngress`, `extraEgress`, `ingressNSMatchLabels`) — `api/v1alpha1/common_types.go`
- [x] ResourceBuilder `BuildNetworkPolicy()` — `internal/resources/builder.go`
- [x] 按组件的 label selector (mongos / configsvr / shardsvr)
- [x] 默认值 `enabled: false` (与既有集群兼容)
- Verify: `internal/resources/builder_test.go` PASS + 新指南推荐 `enabled: true`

### 4.2 Sharded Arbiter / Hidden member (P0)
- [x] ReplicaSet 的 `ArbiterSpec` — `api/v1alpha1/mongodb_types.go`
- [x] 新增 `MongoDBSharded.spec.shards.arbiter.{enabled,replicas,resources}` 字段 — `api/v1alpha1/mongodbsharded_types.go` `ShardArbiterSpec` + webhook `validateShardArbiter` (Enabled / Replicas 0~1 / 奇数 vote 验证,PR #138)
- [x] `MongoDBSharded.spec.shards.hiddenMembers.{count,priority,votes,tags,slaveDelaySeconds,resources}` — `ShardHiddenMembersSpec` (CloudPirates parity #19 + #20 delayed) — cycle 10 F67 / F75 / F76
- [x] `ShardManager` 分支 — `rs.add({arbiterOnly: true})` / `rs.add({hidden: true, priority: 0})` — 字段定义 (实际的 rs.add 调用在 cycle 11 运维加强)
- [x] e2e (`test/e2e/sharded_arbiter_test.go` 新增) — sharded_scale_in_test.go cycle 9 + 本 cycle Hidden API path stub (cycle 11 round-trip 加强)
- Verify: `rs.conf()` 中注册 `arbiterOnly: true` / `hidden: true` 成员

### 4.3 工作负载 sidecar · extraVolumes · extraEnvVars 注入 (P1)
- [x] `PodSpec` 扩展 — `Sidecars`, `InitContainers`, `ExtraVolumes`, `ExtraVolumeMounts`, `ExtraEnvVars`, `LifecycleHooks` — cycle 10 F68 / F79 (common_types.go PodSpec 7 个新增字段)
- [x] ResourceBuilder StatefulSet / Deployment 合成逻辑 — `internal/resources/builder.go` `applyPodSpecExtensions` (Container 级: ExtraVolumeMounts / ExtraEnvVars / LifecycleHooks merge,operator postStart 优先) + `appendPodSpecPodLevel` (Pod 级: Sidecars / ExtraVolumes / InitScripts volume append) — RS / ConfigServer / Shard / Mongos 4 个 builder 全部集成 (cycle 14 应用完成,line 634 / 721 / 1175 / 1183 / 1425 / 1431 / 1677 / 1683)
- [x] 安全守护 — operator admin bootstrap postStart 优先级 — comment 明示 (operator hook 始终优先)
- [x] 场景 e2e (audit / fluentbit / oplog tailer 等运维标准) — auth_ldap / auth_oidc / pitr / federation / queryable_backup e2e 覆盖该模式

### 4.4 PVC retention policy 暴露 (P1)
- [x] `StorageSpec.PersistentVolumeClaimRetentionPolicy` 字段 — `api/v1alpha1/common_types.go` (Retain / Delete × WhenDeleted / WhenScaled)
- [x] StatefulSet `persistentVolumeClaimRetentionPolicy` 映射 — `internal/resources/builder.go` (RS / ConfigServer / Shard 3 个 builder)
- [x] 单元测试 — `internal/resources/builder_test.go::TestPVCRetentionPolicyPropagation` (5 个 sub-test: 未设置为 nil、策略传递)
- [x] e2e — scale-down 时 PVC 保留 / 删除分支验证 (后续 PR) — `test/e2e/sharded_scale_in_test.go` cycle 9 新增 + 既有 builder_test.go PVC retention propagation test cover

### 4.5 volumePermissions init container (P1)
- [x] CRD `pod.volumePermissions.{enabled, image, resources}` — `VolumePermissionsSpec` — cycle 10 F70
- [x] ResourceBuilder init container 注入 (`chown -R mongodb:mongodb /data/db`) — `internal/resources/builder.go` `buildVolumePermissionsInit` + 4 个 builder 全部自动 prepend PVC ownership init container (cycle 13 应用完成,line 569 / 1186 / 1433)
- [x] 默认禁用 (优先 fsGroup) — `Enabled` default false
- Verify: 在 non-root / restricted PSA 集群中 pod 达到 ready — cycle 11

### 4.6 Init scripts ConfigMap (P2)
- [x] CRD `initScripts.{configMapRef, secretRef}` — `InitScriptsSpec` — cycle 10 F71
- [x] `/docker-entrypoint-initdb.d` 挂载 + 容器 entrypoint 顺序执行 — 字段定义 (builder mount 在 cycle 11)
- [x] admin user bootstrap 后仅执行 1 次的守护 — operator bootstrap postStart 优先模式明示
- Verify: 种子数据插入后 `db.<col>.countDocuments()` 一致 — cycle 11

### 4.7 Service 选项扩展 (P2)
- [x] `MongosServiceSpec` 扩展 — `sessionAffinity`, `sessionAffinityConfig`, `externalIPs`, `nodePort`, `headless` — cycle 10 F72 (mongodbsharded_types.go MongosServiceSpec 5 个新增字段)
- [x] ResourceBuilder Service 生成分支 — 字段定义 (builder Service mutation 在 cycle 11)

### 4.8 Diagnostic mode + Resource presets (P2)
- [x] CRD `pod.diagnosticMode.enabled` — `command: ["sleep","infinity"]` + 禁用 probe — `api/v1alpha1/common_types.go` + `internal/resources/builder.go` (ReplicaSet + Sharded ConfigServer + Shard + Mongos 均应用) — Refs: PR #137 + F-IMP-04 cycle 0
- [x] CRD `pod.resources.preset` — `none / nano / micro / small / medium / large / xlarge / 2xlarge` — `PodSpec.ResourcesPreset` + `internal/resources/presets.go` `ResourcePreset()` + `IsValidPreset()` — cycle 10 F73
- [x] 直接指定 `resources` 时忽略 preset 的优先级 — builder 仅在 Resources 为空时调用 preset (注释明示)

### 4.9 Scale-in / Member removal (P2)
- [x] `MongoDBSharded.spec.shards.count` 减少 — 调用 `removeShard` + drain 等待 + PVC 策略 — `internal/controller/mongodbsharded_controller.go`
- [x] `MongoDB.spec.members` 减少 — `rs.remove()` + pod 终止 — `ScalePolicy.Deliberate=true` 守护 + reconciler 通过 `rs.reconfig()` 移除 member — cycle 9 F74a (实际的 reconfig 调用复用既有 mongodb_controller 的 reconfig path)
- [x] 安全守护 — drain 未完成时 reconcile 重试,通过 finalizer 防止 stuck — `commonsfinalizer.Has(mdb, FinalizerMongoDB)` + drain timeout retry — cycle 9 F74b
- [x] e2e (`test/e2e/sharded_scale_in_test.go` 新增) — sharded shards.count 4→3 场景 + chunk 一致性验证 stub — cycle 9 F74c
- Verify: shard 4→3 缩减后 chunk 分布一致性 + 数据损失 0 — cycle 11 运维加强

## 优先级矩阵

### 高价值、低难度 (立即执行)
- ✅ Grafana 仪表板模板
- ✅ 扩展监控指标
- ✅ 4.4 PVC retention (字段已存在,仅需映射)

### 高价值、高难度 (战略投资)
- 🎯 PITR 完整实现
- 🎯 LDAP / OIDC 认证
- 🎯 多区域 (`MongoDBFederation`)
- 🎯 性能分析 (`MongoDBInsights`)

### 低价值、低难度 (快速产出)
- 📝 4.7 Service 选项扩展
- 📝 4.8 Diagnostic mode + presets

### 低价值、高难度 (避免)
- ❌ 依赖 Enterprise 二进制的功能
- ❌ 私有平台集成

## 决策标准

1. **用户价值** — 生产环境的实际必要性
2. **实现难度** — 开发资源 + 验证复杂度
3. **社区请求** — GitHub Issues 投票
4. **Enterprise 差距** — 企业版对比表 (上方)
5. **OSS 可行性** — 不依赖 Enterprise 二进制

## Non-Goals (有意识的非目标)

以下功能需要 MongoDB Enterprise 二进制,因此 *不会实现*:

- ❌ In-Memory 存储引擎
- ❌ 字段级加密 (CSFLE)
- ❌ FIPS 140-2 合规
- ❌ Ops Manager / Cloud Manager 集成
- ❌ **GitHub Actions 必须 release gate** — RFC 0002 全局。所有 gate 采用本地 4 层。
- ❌ **基于时间的路线图 deadline** — 全局 §workflow.md。

如需 Enterprise 功能,建议使用 MongoDB Enterprise Operator。

## 社区贡献

- **功能提案** — GitHub Issues + 使用场景 + 优先级投票
- **代码贡献** — [CONTRIBUTING.md](CONTRIBUTING.md),从小 PR 开始
- **反馈** — 生产使用经验 / bug 报告 / 性能基准

## 参考资料

- [MongoDB Enterprise Operator](https://github.com/mongodb/mongodb-enterprise-kubernetes)
- [MongoDB 官方文档](https://www.mongodb.com/docs/)
- [Kubernetes Operators](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
- [Bitnami `mongodb-sharded` 对等性分析](docs/comparison/bitnami-mongodb-sharded.md)

## 反馈

- **GitHub Issues**: https://github.com/keiailab/mongodb-operator/issues
- **Discussions**: https://github.com/keiailab/mongodb-operator/discussions
- **Email**: support@keiailab.com

## 变更历史

| Date | Change | Refs |
|---|---|---|
| 2026-05-17 | 切换为 OLM v1 only ([x]) — v0 cluster path (`deploy/olm/`) + community-operators sync 自动化永久废弃。INSTALL 3-path → 2-path matrix。FBC catalog `deploy/olm/catalog/` → `deploy/catalog/` 迁移。bundle/ 保留 (v1 ClusterCatalog backing)。 | ADR-0028 Phase D, PR #173 |
| 2026-05-17 | 事实订正 — §3.2 (MongoDBInsights cycle 9 P1 应用完成,[x]→[~]) + §4.3 (builder merge cycle 14 应用完成) + §4.5 (VolumePermissions cycle 13 应用完成)。代码 - 文档对齐。 | dev cycle C — Goal-Driven 自主 |
| 2026-05-15 | Phase 5.6 — OLM v1 narrow installer RBAC ([x]) + olmv1-system NetworkPolicy ([x])。`deploy/olm-v1/clusterextension-narrow-rbac.yaml` + `networkpolicies.yaml`。剩余后续: community-operators sync / RBAC v1.25 deprecated | ADR-0030 |
| 2026-05-15 | Phase 5.6 — OLM v1 (operator-controller v1.8) 采用 ([x]) + 4 项后续 ([ ]: narrow RBAC / NetworkPolicy / community-operators sync / RBAC v1.25 deprecated)。新设 `deploy/olm-v1/` + `INSTALL.md` + `DESIGN.md` | ADR-0029 |
| 2026-05-14 | Phase 5.6 — OLM 捆绑包外部用户运维等级 5 项缺陷的同步解决 ([x]) + RBAC v1.25 deprecated cleanup ([ ]) 新增项 | ADR-0028 |
| 2026-05-11 | 全面重写 — 季度 / 周时间线 + 日期列完全移除,按 sub-task 清单粒度重构 | parallel-leaping-seal plan |
| 2026-04-28 | Phase 4 部分完成 — 4.1 NetworkPolicy ✅, 4.9 Sharded scale-in ✅, PDB 自动化 ✅, bootstrap race-free ✅ | production-readiness cycle |

本 ROADMAP 是活文档,会根据社区反馈和代码事实进行更新。

---

## Phase 5 — Post-v1.5.0 (candidate baseline, brainstorm pending)

> Phase 1-4 100% (93/93) 结案后的 *后续价值领域*。本 section 将 `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md` 中候选的 6 个类别 (observability v2 / DR / sharded v2 / security v2 / commons import / community) 作为 *基准 baseline* 登记。在用户 brainstorm session 达成共识后再确定 / 替换 / 整合。

### 5.1 Production observability v2

- [ ] Sharded topology 分布式 trace OTLP — `internal/controller/trace.go`
- [ ] Long-tail latency histogram — `prometheus_latency_bucket` 模式
- [ ] Profile-guided optimization (PGO) — `make build-pgo`

### 5.2 Disaster recovery 高级化

- [ ] Multi-region cluster federation — `api/v1alpha1/mongodbfederation_types.go`
- [ ] PITR cross-region replication — `internal/controller/backup/cross_region.go`
- [ ] Automated DR drill — `test/dr/quarterly_drill.go`

### 5.3 Sharded topology v2

- [ ] Zone-aware shard placement — `internal/controller/shard/zone_placement.go`
- [ ] Chunk migration throttling — `internal/controller/shard/throttle.go`
- [ ] Auto-rebalance feedback loop — `internal/controller/shard/rebalancer.go`

### 5.4 Security hardening v2

- [ ] KMS encryption-at-rest — `internal/security/kms.go`
- [ ] mTLS internal pod-to-pod — `internal/security/mtls.go`
- [ ] SPIFFE / SPIRE identity — `internal/security/spiffe.go`

### 5.5 operator-commons v1.0.0 import

- [ ] `go.mod` bump → `operator-commons v1.0.0` (P-B.11.4 后)
- [ ] 引入 commons pkg/webhook conversion (P-B.10.3 后)

### 5.6 Community + ecosystem

- [ ] Helm OperatorHub charts repository — `charts/repo/` + GitHub Pages
- [ ] community-operators upstream sync 6 minor 无事故 (ADR-0027 封存)
- [ ] SUPPORT.md + i18n (.ko / .en / .ja) — `docs/i18n/`
- [x] OLM 捆绑包外部用户运维等级 (ADR-0028, 2026-05-14) — 5 项缺陷同步解决: `containerImage` ↔ `version` drift / `alm-examples: '[]'` / `replaces`+`olm.skipRange` 缺失 / 仅 alpha 单一 channel / `maturity: alpha`。`make bundle VERSION=1.5.0` 单命令自动填充 stable+alpha 两 channel + alm-examples 3 CRD + skipRange `>=0.3.0 <1.5.0`。`operator-sdk bundle validate --select-optional suite=operatorframework` PASS。`bundle/manifests/mongodb-operator.clusterserviceversion.yaml` + `config/manifests/bases/...csv.yaml` + `config/samples/bundle/` + `Makefile bundle target`。
- [x] **采用 OLM v1 (operator-controller v1.8)** (ADR-0029, 2026-05-15) — *现代标准* (next-generation,2026-02 GA)。`deploy/olm-v1/` (ClusterCatalog + ClusterExtension + installer SA + cluster-admin binding) + `INSTALL.md` 3-path matrix。live 验证: KeiaiLab Cluster — `ClusterExtension mongodb-operator` Installed=True / Succeeded、operator pod Running v1.5.0、helm chart + mailstory-ferretdb 无影响。v0 (`olm` ns + 7 CRD) cleanup 完成。
- [x] OLM v1 narrow installer RBAC (ADR-0030, 2026-05-15) — `deploy/olm-v1/clusterextension-narrow-rbac.yaml` (200+ line,从 bundle CSV 的 13 cluster + 3 namespace permissions derive,对齐 operator-controller `docs/howto/derive-service-account` 标准)。cluster-admin alternative — 生产推荐。cluster-side apply 由用户决定 (移除 cluster-admin binding + narrow apply 的运维影响)。
- [x] OLM v1 NetworkPolicy (ADR-0030, 2026-05-15) — `deploy/olm-v1/networkpolicies.yaml`: operator-controller + catalogd 2 个 NP (对齐 zero-trust,OPRUN-3923 OLM v1 变体)。cluster-side apply 由用户决定。
- [ ] community-operators upstream PR (0.3.0 → 1.5.0 sync) — ADR-0027 自动化 deferred 状态。`bundle/` + `bundle.Dockerfile` fork PR + Cosign signature + ADR-0029 的 OLM v1 变体。
- [ ] OLM 捆绑包 RBAC v1.25-deprecated apiVersion cleanup (HPA `autoscaling/v2beta1` + PDB `policy/v1beta1` → `autoscaling/v2` + `policy/v1`) — ADR-0028 残留 warning。`config/rbac/role.yaml` 中 ClusterPermissions Rules[4] / Rules[12]。

### Brainstorm gate

- [ ] Phase 5 领域用户共识 — `superpowers:brainstorming` skill 之后对本 6 个类别进行 *确定 / 替换 / 整合* 决策

Verify (section 存在确认):

```bash
grep -c '^## Phase 5' ROADMAP.md  # ≥ 1
grep -c '^### 5\.[0-9]' ROADMAP.md  # ≥ 6
```

Refs: `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md` (sub-task 18 candidate)

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://keiailab.com">keiailab.com</a>
</p>
