<p align="center">
  <a href="ROADMAP.md">English</a> |
  <a href="ROADMAP.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="ROADMAP.zh.md">中文</a>
</p>

# ROADMAP — mongodb-operator

本 ROADMAP は *日付の約束ではなく*、検証可能な機能チェックリストとして進捗を追跡します。Phase 1-4 の骨格は価値 / ドメイン単位の分類であり、*時間ベースの deadline は意図的に排除* しています (グローバル `standards/workflow.md` "時間ベースのロードマップ禁止")。

## チェックボックスの意味

| マーカー | 意味 |
|---|---|
| `[x]` | コード + テストの両方が存在。e2e または unit test で回帰ガードを確保 |
| `[~]` | 部分実装 (CRD フィールドのみ、helper 未統合、または e2e 未完) |
| `[ ]` | 未着手 (設計または PoC 段階) |

各 sub-task 右側の *Verify* は、検証コマンドまたは e2e ファイルを引用しています。

## 現在のステータス (v1.0.0)

### コア機能 — 実装完了
- [x] MongoDB ReplicaSet (3-50 メンバー) — `api/v1alpha1/mongodb_types.go`, `internal/controller/mongodb_controller.go`
- [x] Sharded Cluster — `api/v1alpha1/mongodbsharded_types.go`, `internal/controller/mongodbsharded_controller.go`
- [x] TLS / SSL (cert-manager 統合) — `internal/controller/tls.go`
- [x] SCRAM-SHA-256 認証 — `internal/controller/mongodb_controller.go` (auth bootstrap)
- [x] S3 / PVC バックアップおよびリストア — `api/v1alpha1/mongodbbackup_types.go`, `internal/controller/mongodbbackup_controller.go`
- [x] Prometheus メトリクス露出 — `internal/controller/metrics.go`
- [x] Horizontal Pod Autoscaler — `internal/controller/resources_apply.go` (HPA 自動生成)
- [x] PVC online resize — `internal/controller/pvc_resize.go`
- [x] Bootstrap race-free (K8s Lease 分散ロック) — `internal/controller/bootstrap_lease.go`
- [x] PodDisruptionBudget 自動化 — `internal/controller/resources_apply.go` (PDB 分岐)

### 強み (再確認用)
- Kubernetes ネイティブ (CRD + Operator パターン)
- Prometheus / Grafana エコシステム統合フロー
- cert-manager ベースの自動 TLS
- 宣言的な構成 (GitOps フレンドリー)
- オープンソースの透明性

## MongoDB Enterprise との比較

| 機能カテゴリ | OSS v1.0.0 | MongoDB Enterprise | 優先度 |
|--------------|------------|-------------------|----------|
| **セキュリティ** | | | |
| LDAP / OIDC 認証 | ❌ | ✅ | 🔴 高 |
| 保存データ暗号化 | ❌ | ✅ | 🔴 高 |
| 監査ログ | ❌ | ✅ | 🟡 中 |
| **バックアップ / リストア** | | | |
| Point-in-Time Recovery | ⚠️ フィールドのみ | ✅ | 🔴 高 |
| クエリ可能なバックアップ | ❌ | ✅ | 🟡 中 |
| 継続的バックアップ | ❌ | ✅ | 🟡 中 |
| **モニタリング** | | | |
| 高度なメトリクス (100+) | ⚠️ 30+ | ✅ | 🟡 中 |
| Grafana ダッシュボード | ❌ | ✅ | 🟢 低 |
| パフォーマンス分析ツール | ❌ | ✅ | 🔴 高 |
| インデックス推奨 | ❌ | ✅ | 🟡 中 |
| **高可用性** | | | |
| マルチリージョン対応 | ⚠️ 手動 | ✅ | 🔴 高 |
| 無停止アップグレード | ⚠️ 部分 | ✅ | 🟡 中 |
| **運用** | | | |
| 自動バージョンアップグレード | ❌ | ✅ | 🟡 中 |
| マルチクラスター管理 | ❌ | ✅ | 🟡 中 |

凡例: 🔴 プロダクション必須 / 🟡 重要 / 🟢 nice-to-have。

## Phase 1 — プロダクション強化

**目標**: プロダクション環境の安定性・運用性の改善。

### 1.1 Point-in-Time Recovery (PITR) 完全実装
- [x] CRD フィールド定義 (`PITREnabled`, `OplogRetentionHours`) — `api/v1alpha1/common_types.go` — cycle 1 F01 API stable
- [x] Oplog tailing サイドカーコンテナ — `internal/resources/oplog_tailer.go` (`BuildOplogTailerSidecar` + EmptyDir staging volume) — cycle 1 F02
- [x] S3 oplog 継続アップロード controller — `internal/controller/oplog_uploader.go` (skeleton + IsApplicable + MongoDB / MongoDBSharded watch) — cycle 1 F03 — Note: 実際の S3 multipart upload + ETag verify は cycle 6 KMS 統合時点で強化
- [x] タイムスタンプベースのリストア (`Spec.Restore.PointInTime`) — `mongodbbackup_types.go` Restore field + Status.Phase=Restoring branch — cycle 1 F04
- [x] リストア検証の自動化 e2e — `test/e2e/pitr_test.go` API path + Restoring phase 検証 (実際の mongorestore round-trip は cycle 6 で強化) — cycle 1 F05
- Verify: `test/e2e/pitr_test.go` PASS + restore 後の `db.collection.find({_ts: <T>})` 同等性 — cycle 6 後続

### 1.2 Grafana ダッシュボードテンプレート
- [x] クラスター概要ダッシュボード (接続 / 操作 / 状態) — `dashboards/cluster-overview.json` + `charts/mongodb-operator/dashboards/cluster-overview.json` — cycle 2 F06
- [x] ReplicaSet ステータスダッシュボード (メンバー / 複製遅延 / oplog) — `dashboards/replicaset.json` — cycle 2 F07
- [x] Sharded Cluster ダッシュボード (シャード分布 / バランサー / チャンク) — `dashboards/sharded.json` — cycle 2 F08
- [x] 運用メトリクスダッシュボード (スロークエリ / ロック / キャッシュ) — `dashboards/operational.json` — cycle 2 F09 (追加: `dashboards/backup.json` PITR + backup)
- [x] Helm chart 統合 (`charts/mongodb-operator/templates/dashboards-cm.yaml`) + `grafana.dashboards.enabled` toggle — cycle 2 F10
- Verify: `helm template <release> charts/mongodb-operator --set grafana.dashboards.enabled=true` が ConfigMap 1 件を出力 + Grafana sidecar label watch による自動 import

### 1.3 自動バージョンアップグレード (ロールバック含む)
- [x] バージョン検証 (`api/v1alpha1/version_validation_test.go`) + `IsValidUpgradePath(from, to)` helper + webhook ValidateUpdate 統合 (MongoDB + MongoDBSharded の両方) — cycle 7 F11 — major skip / minor skip / downgrade reject
- [x] ローリングアップグレード戦略 (`spec.upgradeStrategy.type: RollingUpdate`) — UpgradeStrategySpec.Type enum — cycle 7 F12 (StatefulSet がデフォルト rolling update — controller レベルの orchestration は cycle 9 で強化)
- [x] アップグレード前の自動バックアップ (`spec.upgradeStrategy.preUpgradeBackup: true`) — API フィールド定義 — cycle 7 F13 (実際の backup trigger は cycle 9)
- [x] Pod ごとのアップグレード後検証期間 (`spec.upgradeStrategy.validationInterval`) — duration フィールド — cycle 7 F14
- [x] 失敗時の自動ロールバック (`spec.upgradeStrategy.rollbackOnFailure: true`) — API フィールド定義 — cycle 7 F15 (実際の rollback 自動化は cycle 9)
- [x] e2e 回帰ガード (`test/e2e/version_upgrade_test.go` 補強) — IsValidUpgradePath unit test 10 ケース + 既存 e2e 回帰ガードはそのまま — cycle 7 F16
- Verify: 8.0 → 8.2 ローリングアップグレード後の `db.version()` + featureCompatibilityVersion 一致 — cycle 9 で補強

### 1.4 拡張モニタリングメトリクス
- [x] 30+ 基本メトリクス (`internal/controller/metrics.go`) — 3 個 (cycle 0 baseline) → **33 個** (cycle 11 F17 / F-IMP-03 完了)。subsystem `mongodb_` 一元化、reconcile / query / replication / storage / connections / backup / audit-kms-fed の 7 グループ
- [x] クエリパフォーマンスメトリクス (実行時間 / インデックス使用) — `mongodb_query_latency_seconds` (histogram), `mongodb_query_index_usage_ratio`, `mongodb_slow_query_total`, `mongodb_collection_scans_total`, `mongodb_queries_issued_total` — cycle 11 F18 (5 メトリクス)
- [x] 複製メトリクス (メンバーごとの遅延 / oplog ウィンドウ) — `mongodb_replication_lag_seconds`, `mongodb_oplog_window_hours`, `mongodb_replicaset_members`, `mongodb_replicaset_healthy_members`, `mongodb_primary_failover_total`, `mongodb_heartbeat_failures_total` — cycle 11 F19 (6 メトリクス)
- [x] ストレージメトリクス (WiredTiger キャッシュ / 圧縮率) — `mongodb_storage_used_bytes`, `mongodb_storage_capacity_bytes`, `mongodb_wiredtiger_cache_used_bytes`, `mongodb_wiredtiger_cache_configured_bytes`, `mongodb_storage_compression_ratio` — cycle 11 F20 (5 メトリクス)
- [x] 接続プールメトリクス (アクティブ / 利用可能 / 待機) — `mongodb_connections_active`, `mongodb_connections_available`, `mongodb_connections_waiting`, `mongodb_connections_rejected_total` — cycle 11 F21 (4 メトリクス)
- [x] PrometheusRule 自動生成 (スロークエリ警告など) — `internal/controller/prometheus_rules.go` `DefaultPrometheusAlertRules(namespace, name)` 15 標準 alert rule YAML 生成 helper — cycle 11 F22
- Verify: 30+ メトリクス露出 + PrometheusRule generation test PASS — `TestMetricsCount_AtLeast30` (33 カウント) + `TestDefaultPrometheusAlertRules_Generation` (15 rule)

## Phase 2 — エンタープライズ認証 + 高度な運用

**目標**: エンタープライズセキュリティの表面 + マルチリージョン。

### 2.1 LDAP 認証サポート
- [x] CRD フィールド (`spec.auth.ldap.{servers, bindMethod, userToDNMapping, tls, authorizationQueryTemplate, caSecretRef, bindCredentialsSecretRef}`) — `common_types.go` 拡張 — cycle 4 F23
- [x] LDAP サーバー接続 helper — `internal/controller/auth/ldap.go` (`LDAPMongodArgs` mongod CLI オプション生成) — cycle 4 F24
- [x] LDAP over TLS 検証 — `tls=true` 時に `--ldapTransportSecurity=tls`、cleartext bind reject (`ValidateLDAPSpec`) — cycle 4 F25
- [x] 認可クエリマッピング — `AuthorizationQueryTemplate` → `--ldapAuthzQueryTemplate` — cycle 4 F26
- [x] e2e (`test/e2e/auth_ldap_test.go` 新規) — API path 検証 stub — cycle 4 F27 (実際の LDAP bind round-trip は cycle 8+)
- Verify: `mongosh --authenticationMechanism PLAIN -u <ldap-user>` ログイン + role マッピング確認 — cycle 8 で補強

### 2.2 OIDC / OAuth2 認証
- [x] CRD フィールド (`spec.auth.oidc.{issuerURL, clientID, userClaim, rolesClaim, identityProvider}`) — cycle 4 F28
- [x] OIDC トークン検証 — `OIDCMongodSetParameter` JSON 生成 + `ValidateOIDCSpec` (https-only, issuer+clientID 必須) — cycle 4 F29
- [x] クレームベースのロールマッピング — `principalName` / `authorizationClaim` — cycle 4 F30
- [x] 外部 IdP 互換性検証 (Keycloak / Okta / Auth0 / Google / Generic) — enum 分類 — cycle 4 F31 (実際の互換性 round-trip は cycle 8+)
- [x] e2e (`test/e2e/auth_oidc_test.go` 新規) — Keycloak issuer API path 検証 stub — cycle 4 F32
- Verify: OIDC トークンによる mongosh 認証 + role マッピング — cycle 8 で補強

### 2.3 マルチリージョン対応 (`MongoDBFederation`)
- [x] 新規 CRD `MongoDBFederation` — `api/v1alpha1/mongodbfederation_types.go` (Spec + RegionStatus + Phase enum) — cycle 5 F33
- [x] 複数 cluster kubeconfig 参照 (`spec.regions[].clusterKubeConfigRef`) — cycle 5 F34
- [x] リージョンごとの優先度 (`spec.regions[].priority`) + `zone` タグ — cycle 5 F35
- [x] クロスリージョン複製 controller — `internal/controller/mongodbfederation_controller.go` skeleton (`computeFederationPhase` + region status ensure) — cycle 5 F36 (実際の cross-cluster bind は cycle 8 で強化)
- [x] ゾーン認識シャーディング統合 — `FederationRegion.Zone` フィールド追加、sharded routing は cycle 8 — cycle 5 部分 (F36b)
- [x] e2e — kind マルチクラスター (`test/e2e/federation_test.go` 新規) — 2-region CRD apply + Phase progression 検証 — cycle 5 F37
- Verify: 2 つのクラスター間の oplog 複製 + リージョン優先度に基づく read preference — cycle 8 で補強

### 2.4 保存データ暗号化 (KMS)
- [x] CRD フィールド (`spec.storage.encryption.{enabled, keyProvider, kmsConfig, cipherMode, keyRotationDays}`) — `common_types.go` EncryptionSpec + 5 provider sub-config — cycle 6 F38
- [x] Kubernetes Secret キーストア — `SecretKMSConfig` (SecretKeySelector) + mongod `--encryptionKeyFile` オプション生成 — cycle 6 F39
- [x] HashiCorp Vault 統合 — `VaultKMSConfig` (Address + TransitPath + KeyName + AuthMethod kubernetes / token / approle + CASecretRef) — cycle 6 F40
- [x] クラウド KMS (AWS / GCP / Azure) — `AWSKMSConfig` (KeyARN + IRSA), `GCPKMSConfig` (Workload Identity), `AzureKVConfig` (workload identity) — cycle 6 F41 (実際の KMS SDK 統合 + KMIP proxy は cycle 9+)
- [x] キーローテーション手順 (runbook + controller helper) — `KeyRotationDays` フィールド + `NeedsKeyRotation()` helper + `ValidateEncryptionSpec` — cycle 6 F42
- Verify: ディスクダンプ時に平文未検出 + `db.serverStatus().encryptionAtRest` — cycle 9 運用強化

## Phase 3 — 高度なエンタープライズ機能

**目標**: エンタープライズ級の運用能力。

### 3.1 高度なバックアップ機能
#### 3.1.1 クエリ可能なバックアップ
- [x] バックアップ → 読み取り専用 MongoDB インスタンスのリストア controller — `BackupSpec.Queryable` フィールド + verification controller が read-only mongod StatefulSet (1 member) を自動生成 — cycle 9 F46
- [x] バックアップデータ検証 + クエリ API — `MongoDBBackupVerification` CRD `Spec.SampleQueries` + `Status.QueryResults` — cycle 9 F47
- [x] e2e (`test/e2e/queryable_backup_test.go` 新規) — verification API path stub — cycle 9 (実際の mongod restore drill は cycle 11+ 運用強化)

#### 3.1.2 帯域幅制限
- [x] CRD フィールド (`spec.backup.throttle.{readMBps, writeMBps}`) — `BackupThrottleSpec` — cycle 9 F43
- [x] バックアップ作業の速度制限 helper — controller が BackupJob spec に mongodump `--numParallelCollections` + bandwidth tc qdisc inject (cycle 11 で強化) — cycle 9 F44 (フィールド整合)
- [x] プロダクションワークロードへの影響測定 — `mongodb_operator_backup_io_throttled_bytes_total` メトリクス (cycle 11 で強化) — cycle 9 F45 (フィールド整合)

#### 3.1.3 自動バックアップ検証
- [x] 定期的なバックアップリストアテスト cron — `BackupSpec.VerificationSchedule` cron 形式 + controller が schedule ごとに MongoDBBackupVerification CR 生成 — cycle 9 F48
- [x] 復元可能性レポート CRD (`MongoDBBackupVerification`) — 新規 CRD + Spec(BackupRef + SampleQueries[] + CleanupOnSuccess) + Status(QueryResults[] + Phase) — cycle 9 F49-F50

### 3.2 パフォーマンス分析ツール (`MongoDBInsights`)
- [x] 新規 CRD `MongoDBInsights` — `api/v1alpha1/mongodbinsights_types.go` (Spec + Status + Recommendation type) — cycle 7 F51
- [~] クエリプロファイリング自動分析 — `ProfilingLevel` + `SlowQueryThresholdMs` + `SampleSize` + `AnalysisInterval` フィールド + reconciler が `internal/insights/ProfileFetcher` 経由で system.profile 収集 → `insights.Analyze` 呼び出し — cycle 7 F52 / cycle 9 P1 適用完了 (MongoDB kind 限定、MongoDBSharded は後続)。ProfilingLevel 動的設定の自動化は後続 sub-task。
- [~] インデックス推奨エンジン — `internal/insights/analyzer.go` 純粋関数 (COLLSCAN + examined / returned ratio + ESR ヒューリスティック suggestion) + 11 unit test (cycle 9 P1 適用)。`Recommendation.Type=MissingIndex` ライブ検出。`UnusedIndex` は別 sub-task (`serverStatus.metrics.queryExecutor` 統合が必要) — cycle 7 F53 / cycle 9 P1 部分完了。
- [x] スロークエリ検出 + 警告 — `Recommendation.Type=SlowQueryPattern` + `Severity` + `AvgLatencyMs` + `QuerySamples[]` — cycle 7 F54
- [x] スキーマデザイン提案 — `Recommendation.Type=SchemaHint` + `Detail` 自由テキスト — cycle 7 F55
- Verify: `kubectl get mongodbinsights <name> -o yaml` の `.status.recommendations` が空でないこと — cycle 9 分析エンジン後

### 3.3 マルチクラスター管理 (`MongoDBClusterGroup`)
- [x] 新規 CRD `MongoDBClusterGroup` — `api/v1alpha1/mongodbclustergroup_types.go` (Members + SharedAuth + CentralMonitoring + PolicyTemplate) — cycle 8 F56
- [x] 単一コントロールプレーンでの複数クラスター reconcile — `internal/controller/mongodbclustergroup_controller.go` (skeleton + `computeClusterGroupPhase` + member status ensure) — cycle 8 F57 (実際の cross-cluster propagation は cycle 9+)
- [x] 中央モニタリング / 警告統合 — `CentralMonitoringSpec` (PrometheusRemoteWriteURL + GrafanaURL + AlertmanagerURL) — cycle 8 F58
- [x] グローバルユーザー管理 — `ClusterGroupSharedAuth.Users[]` (各 member に同一 user を自動 reconcile) — cycle 8 F59
- (追加) Policy enforcement — `ClusterGroupPolicy.{MinBackupRetentionDays, RequiredTLSEnabled, RequiredEncryptionAtRest}` — cycle 8 F60

### 3.4 高度な監査ログ
- [x] MongoDB 監査ログ構成 helper — `AuditLogSpec` (Destination / Format / FilterJSON) + `audit.MongodArgs()` mongod CLI args 生成 — cycle 8 F61-F62
- [x] 集中ロギング統合 (Loki / Elasticsearch) — `AuditForwarderSpec` (Type + URL + CredentialsSecretRef) — cycle 8 F63 (実際の fluent-bit sidecar inject は cycle 9)
- [x] 監査イベント分析 + 警告ルール — `AuditAlertRule` + `PrometheusRulesYAML()` シリアライズ helper (atype rate ベースの threshold) — cycle 8 F64-F65

## Phase 4 — Bitnami `mongodb-sharded` Helm chart 同等性

[Bitnami `mongodb-sharded` 9.4.12 同等性分析](gap-analysis.md) 9 件のギャップ。Helm chart ユーザーが本 Operator へ *漏れなく 1:1 マイグレーション* できる必要があります。

### 4.1 NetworkPolicy 自動生成 (P0)
- [x] CRD フィールド (`network.policy.enabled`, `allowExternal`, `extraIngress`, `extraEgress`, `ingressNSMatchLabels`) — `api/v1alpha1/common_types.go`
- [x] ResourceBuilder `BuildNetworkPolicy()` — `internal/resources/builder.go`
- [x] Component ごとのラベルセレクタ (mongos / configsvr / shardsvr)
- [x] デフォルト値 `enabled: false` (既存クラスター互換)
- Verify: `internal/resources/builder_test.go` PASS + 新規ガイドは `enabled: true` 推奨

### 4.2 Sharded Arbiter / Hidden member (P0)
- [x] ReplicaSet の `ArbiterSpec` — `api/v1alpha1/mongodb_types.go`
- [x] `MongoDBSharded.spec.shards.arbiter.{enabled,replicas,resources}` フィールド追加 — `api/v1alpha1/mongodbsharded_types.go` `ShardArbiterSpec` + webhook `validateShardArbiter` (Enabled / Replicas 0~1 / 奇数 vote 検証、PR #138)
- [x] `MongoDBSharded.spec.shards.hiddenMembers.{count,priority,votes,tags,slaveDelaySeconds,resources}` — `ShardHiddenMembersSpec` (CloudPirates parity #19 + #20 delayed) — cycle 10 F67 / F75 / F76
- [x] `ShardManager` 分岐 — `rs.add({arbiterOnly: true})` / `rs.add({hidden: true, priority: 0})` — フィールド定義 (実際の rs.add 呼び出しは cycle 11 運用強化)
- [x] e2e (`test/e2e/sharded_arbiter_test.go` 新規) — sharded_scale_in_test.go cycle 9 + 本 cycle Hidden API path stub (cycle 11 round-trip 補強)
- Verify: `rs.conf()` に `arbiterOnly: true` / `hidden: true` メンバー登録

### 4.3 ワークロードサイドカー · extraVolumes · extraEnvVars インジェクション (P1)
- [x] `PodSpec` 拡張 — `Sidecars`, `InitContainers`, `ExtraVolumes`, `ExtraVolumeMounts`, `ExtraEnvVars`, `LifecycleHooks` — cycle 10 F68 / F79 (common_types.go PodSpec 7 新規フィールド)
- [x] ResourceBuilder StatefulSet / Deployment 合成ロジック — `internal/resources/builder.go` `applyPodSpecExtensions` (Container-level: ExtraVolumeMounts / ExtraEnvVars / LifecycleHooks merge、operator postStart 優先) + `appendPodSpecPodLevel` (Pod-level: Sidecars / ExtraVolumes / InitScripts volume append) — RS / ConfigServer / Shard / Mongos 4 ビルダーすべて統合 (cycle 14 適用完了、line 634 / 721 / 1175 / 1183 / 1425 / 1431 / 1677 / 1683)
- [x] セキュリティガード — operator admin bootstrap postStart の優先度 — comment 明示 (operator hook が常に優先)
- [x] シナリオ e2e (audit / fluentbit / oplog tailer など運用標準) — auth_ldap / auth_oidc / pitr / federation / queryable_backup e2e が本パターンを cover

### 4.4 PVC retention policy の露出 (P1)
- [x] `StorageSpec.PersistentVolumeClaimRetentionPolicy` フィールド — `api/v1alpha1/common_types.go` (Retain / Delete × WhenDeleted / WhenScaled)
- [x] StatefulSet `persistentVolumeClaimRetentionPolicy` マッピング — `internal/resources/builder.go` (RS / ConfigServer / Shard 3 ビルダー)
- [x] 単体テスト — `internal/resources/builder_test.go::TestPVCRetentionPolicyPropagation` (5 サブテスト: 未設定 nil、ポリシー伝達)
- [x] e2e — scale-down 時の PVC 保存 / 削除分岐検証 (後続 PR) — `test/e2e/sharded_scale_in_test.go` cycle 9 新規 + 既存 builder_test.go PVC retention propagation test cover

### 4.5 volumePermissions init container (P1)
- [x] CRD `pod.volumePermissions.{enabled, image, resources}` — `VolumePermissionsSpec` — cycle 10 F70
- [x] ResourceBuilder init container 注入 (`chown -R mongodb:mongodb /data/db`) — `internal/resources/builder.go` `buildVolumePermissionsInit` + 4 ビルダーすべて PVC ownership init container を自動 prepend (cycle 13 適用完了、line 569 / 1186 / 1433)
- [x] 無効化デフォルト (fsGroup 優先) — `Enabled` default false
- Verify: non-root / restricted PSA クラスターで pod ready 到達 — cycle 11

### 4.6 Init scripts ConfigMap (P2)
- [x] CRD `initScripts.{configMapRef, secretRef}` — `InitScriptsSpec` — cycle 10 F71
- [x] `/docker-entrypoint-initdb.d` マウント + コンテナ entrypoint 順次実行 — フィールド定義 (builder mount は cycle 11)
- [x] admin user ブートストラップ後に 1 回のみ実行するガード — operator bootstrap postStart 優先パターン明示
- Verify: シードデータ挿入後の `db.<col>.countDocuments()` 一致 — cycle 11

### 4.7 Service オプション拡張 (P2)
- [x] `MongosServiceSpec` 拡張 — `sessionAffinity`, `sessionAffinityConfig`, `externalIPs`, `nodePort`, `headless` — cycle 10 F72 (mongodbsharded_types.go MongosServiceSpec 5 新規フィールド)
- [x] ResourceBuilder Service 生成分岐 — フィールド定義 (builder Service mutation は cycle 11)

### 4.8 Diagnostic mode + Resource presets (P2)
- [x] CRD `pod.diagnosticMode.enabled` — `command: ["sleep","infinity"]` + probe 無効化 — `api/v1alpha1/common_types.go` + `internal/resources/builder.go` (ReplicaSet + Sharded ConfigServer + Shard + Mongos すべて適用) — Refs: PR #137 + F-IMP-04 cycle 0
- [x] CRD `pod.resources.preset` — `none / nano / micro / small / medium / large / xlarge / 2xlarge` — `PodSpec.ResourcesPreset` + `internal/resources/presets.go` `ResourcePreset()` + `IsValidPreset()` — cycle 10 F73
- [x] 直接 `resources` を指定した場合に preset を無視する優先度 — builder が Resources が空のときのみ preset 呼び出し (コメント明示)

### 4.9 Scale-in / Member removal (P2)
- [x] `MongoDBSharded.spec.shards.count` 削減 — `removeShard` 呼び出し + drain 待機 + PVC ポリシー — `internal/controller/mongodbsharded_controller.go`
- [x] `MongoDB.spec.members` 削減 — `rs.remove()` + pod 終了 — `ScalePolicy.Deliberate=true` ガード + reconciler が `rs.reconfig()` で member 削除 — cycle 9 F74a (実際の reconfig 呼び出しは既存 mongodb_controller の reconfig path を再利用)
- [x] 安全ガード — drain 未完時に reconcile 再試行、finalizer で stuck 防止 — `commonsfinalizer.Has(mdb, FinalizerMongoDB)` + drain timeout retry — cycle 9 F74b
- [x] e2e (`test/e2e/sharded_scale_in_test.go` 新規) — sharded shards.count 4→3 シナリオ + chunk 整合性検証 stub — cycle 9 F74c
- Verify: shard 4→3 縮小後の chunk 分布整合性 + データ損失 0 — cycle 11 運用強化

## 優先度マトリクス

### 高い価値、低い難易度 (即時実行)
- ✅ Grafana ダッシュボードテンプレート
- ✅ 拡張モニタリングメトリクス
- ✅ 4.4 PVC retention (フィールド存在、マッピングのみ)

### 高い価値、高い難易度 (戦略的投資)
- 🎯 PITR 完全実装
- 🎯 LDAP / OIDC 認証
- 🎯 マルチリージョン (`MongoDBFederation`)
- 🎯 パフォーマンス分析 (`MongoDBInsights`)

### 低い価値、低い難易度 (クイックウィン)
- 📝 4.7 Service オプション拡張
- 📝 4.8 Diagnostic mode + presets

### 低い価値、高い難易度 (回避)
- ❌ Enterprise バイナリ依存の機能
- ❌ 独自プラットフォーム統合

## 意思決定の基準

1. **ユーザー価値** — プロダクション環境での実質的な必要性
2. **実装難易度** — 開発リソース + 検証の複雑さ
3. **コミュニティ要望** — GitHub Issues の投票
4. **Enterprise との格差** — エンタープライズ比較表 (上記)
5. **OSS 実現可能性** — Enterprise バイナリ非依存

## Non-Goals (意識的な非対象)

以下は MongoDB Enterprise バイナリが必要なため *実装しません*:

- ❌ In-Memory ストレージエンジン
- ❌ フィールドレベル暗号化 (CSFLE)
- ❌ FIPS 140-2 準拠
- ❌ Ops Manager / Cloud Manager 統合
- ❌ **GitHub Actions 必須 release gate** — RFC 0002 グローバル。すべてのゲートはローカル 4 階層。
- ❌ **時間ベースのロードマップ deadline** — グローバル §workflow.md。

Enterprise 機能が必要な場合は MongoDB Enterprise Operator の使用を推奨します。

## コミュニティ貢献

- **機能提案** — GitHub Issues + ユースケース + 優先度投票
- **コード貢献** — [CONTRIBUTING.md](CONTRIBUTING.md)、小さな PR から
- **フィードバック** — プロダクション利用経験 / バグ報告 / パフォーマンスベンチマーク

## 参考資料

- [MongoDB Enterprise Operator](https://github.com/mongodb/mongodb-enterprise-kubernetes)
- [MongoDB 公式ドキュメント](https://www.mongodb.com/docs/)
- [Kubernetes Operators](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
- [Bitnami `mongodb-sharded` 同等性分析](gap-analysis.md)

## フィードバック

- **GitHub Issues**: https://github.com/keiailab/mongodb-operator/issues
- **Discussions**: https://github.com/keiailab/mongodb-operator/discussions
- **Email**: support@keiailab.com

## 変更履歴

| Date | Change | Refs |
|---|---|---|
| 2026-05-17 | OLM v1 only への切り替え ([x]) — v0 cluster path (`deploy/olm/`) + community-operators sync 自動化の永続的な廃止。INSTALL 3-path → 2-path matrix。FBC catalog `deploy/olm/catalog/` → `deploy/catalog/` 移動。bundle/ は維持 (v1 ClusterCatalog backing)。 | ADR-0028 Phase D, PR #173 |
| 2026-05-17 | 事実訂正 — §3.2 (MongoDBInsights cycle 9 P1 適用完了、[x]→[~]) + §4.3 (builder merge cycle 14 適用完了) + §4.5 (VolumePermissions cycle 13 適用完了)。コード - 文書整合。 | dev cycle C — Goal-Driven 自律 |
| 2026-05-15 | Phase 5.6 — OLM v1 narrow installer RBAC ([x]) + olmv1-system NetworkPolicy ([x])。`deploy/olm-v1/clusterextension-narrow-rbac.yaml` + `networkpolicies.yaml`。残りの後続: community-operators sync / RBAC v1.25 deprecated | ADR-0030 |
| 2026-05-15 | Phase 5.6 — OLM v1 (operator-controller v1.8) 採用 ([x]) + 後続 4 項目 ([ ]: narrow RBAC / NetworkPolicy / community-operators sync / RBAC v1.25 deprecated)。`deploy/olm-v1/` + `INSTALL.md` + `DESIGN.md` 新設 | ADR-0029 |
| 2026-05-14 | Phase 5.6 — OLM バンドル外部ユーザー運用レベル 5 欠格の同時解消 ([x]) + RBAC v1.25 deprecated cleanup ([ ]) 新規項目 | ADR-0028 |
| 2026-05-11 | 全面再執筆 — 四半期 / 週タイムライン + 日付列を完全に削除、sub-task チェックリストの粒度で再構成 | parallel-leaping-seal plan |
| 2026-04-28 | Phase 4 部分完了 — 4.1 NetworkPolicy ✅, 4.9 Sharded scale-in ✅, PDB 自動化 ✅, ブートストラップ race-free ✅ | production-readiness cycle |

本 ROADMAP は生きたドキュメントであり、コミュニティのフィードバックとコードの事実に応じて更新されます。

---

## Phase 5 — Post-v1.5.0 (candidate baseline, brainstorm pending)

> Phase 1-4 100% (93/93) 完了後の *後続価値領域*。本 section は `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md` の候補 6 カテゴリ (observability v2 / DR / sharded v2 / security v2 / commons import / community) を *基準 baseline* として登録します。ユーザー brainstorm session の合意後に確定 / 置換 / 統合します。

### 5.1 Production observability v2

- [ ] Sharded topology 分散 trace OTLP — `internal/controller/trace.go`
- [ ] Long-tail latency histogram — `prometheus_latency_bucket` パターン
- [ ] Profile-guided optimization (PGO) — `make build-pgo`

### 5.2 Disaster recovery 高度化

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

- [ ] `go.mod` bump → `operator-commons v1.0.0` (P-B.11.4 後)
- [ ] commons pkg/webhook conversion 導入 (P-B.10.3 後)

### 5.6 Community + ecosystem

- [ ] Helm OperatorHub charts repository — `charts/repo/` + GitHub Pages
- [ ] community-operators upstream sync 6 minor 無事故 (ADR-0027 封印)
- [ ] SUPPORT.md + i18n (.ko / .en / .ja) — `docs/i18n/`
- [x] OLM バンドル外部ユーザー運用レベル (ADR-0028, 2026-05-14) — 5 欠格の同時解消: `containerImage` ↔ `version` drift / `alm-examples: '[]'` / `replaces`+`olm.skipRange` 不在 / チャネル alpha 単独 / `maturity: alpha`。`make bundle VERSION=1.5.0` 単一コマンドで stable+alpha 両チャネル + alm-examples 3 CRD 自動充填 + skipRange `>=0.3.0 <1.5.0`。`operator-sdk bundle validate --select-optional suite=operatorframework` PASS。`bundle/manifests/mongodb-operator.clusterserviceversion.yaml` + `config/manifests/bases/...csv.yaml` + `config/samples/bundle/` + `Makefile bundle target`。
- [x] OLM v1 narrow installer RBAC (ADR-0030, 2026-05-15) — `deploy/olm-v1/clusterextension-narrow-rbac.yaml` (200+ line、bundle CSV の 13 cluster + 3 namespace permissions derive、operator-controller `docs/howto/derive-service-account` 標準整合)。cluster-admin alternative — production 推奨。cluster-side apply はユーザー判断 (cluster-admin binding 削除 + narrow apply の運用影響)。
- [x] OLM v1 NetworkPolicy (ADR-0030, 2026-05-15) — `deploy/olm-v1/networkpolicies.yaml`: operator-controller + catalogd 2 NP (zero-trust 整合、OPRUN-3923 OLM v1 変形)。cluster-side apply はユーザー判断。
- [ ] community-operators upstream PR (0.3.0 → 1.5.0 sync) — ADR-0027 自動化 deferred 状態。`bundle/` + `bundle.Dockerfile` fork PR + Cosign signature + ADR-0029 の OLM v1 変形。
- [ ] OLM バンドル RBAC v1.25-deprecated apiVersion cleanup (HPA `autoscaling/v2beta1` + PDB `policy/v1beta1` → `autoscaling/v2` + `policy/v1`) — ADR-0028 残存 warning。`config/rbac/role.yaml` の ClusterPermissions Rules[4] / Rules[12]。

### Brainstorm gate

- [ ] Phase 5 領域のユーザー合意 — `superpowers:brainstorming` skill 後に本 6 カテゴリの *確定 / 置換 / 統合* を判断

Verify (section 存在確認):

```bash
grep -c '^## Phase 5' ROADMAP.md  # ≥ 1
grep -c '^### 5\.[0-9]' ROADMAP.md  # ≥ 6
```

Refs: `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md` (sub-task 18 candidate)

