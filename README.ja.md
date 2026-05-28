<p align="center">
  <img src="https://github.com/keiailab.png" alt="keiailab" width="120"/>
</p>

# mongodb-operator

> **Kubernetes 向け Apache-2.0 MongoDB Operator — ReplicaSet + Sharded Cluster + バックアップ、vanilla MongoDB 7.0+**

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License"/></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go Version"/></a>
  <a href="https://www.mongodb.com/"><img src="https://img.shields.io/badge/MongoDB-7.0%2B-47A248?logo=mongodb" alt="MongoDB"/></a>
  <a href="https://kubernetes.io/"><img src="https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes" alt="Kubernetes"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator"><img src="https://img.shields.io/badge/ghcr.io-keiailab%2Fmongodb--operator-blue?logo=github" alt="Container Image"/></a>
  <a href="https://github.com/keiailab/mongodb-operator"><img src="https://img.shields.io/badge/dynamic/yaml?url=https://raw.githubusercontent.com/keiailab/mongodb-operator/main/charts/mongodb-operator/Chart.yaml&label=helm%20v" alt="Helm Chart"/></a>
  <a href="https://artifacthub.io/packages/search?repo=mongodb-operator"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/mongodb-operator" alt="Artifact Hub"/></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/keiailab/mongodb-operator"><img src="https://api.scorecard.dev/projects/github.com/keiailab/mongodb-operator/badge" alt="OpenSSF Scorecard"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/discussions"><img src="https://img.shields.io/github/discussions/keiailab/mongodb-operator?label=discussions&logo=github" alt="GitHub Discussions"/></a>
  <a href="https://github.com/keiailab/operator-commons"><img src="https://img.shields.io/badge/keiailab-v3.x--stable-success?style=flat-square" alt="keiailab v3.x-stable"/></a>
  <a href="https://github.com/keiailab/operator-commons"><img src="https://img.shields.io/badge/audit-100%25-success?style=flat-square" alt="audit"/></a>
</p>

<p align="center">
  <a href="README.md">English</a> |
  <a href="README.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="README.zh.md">中文</a>
</p>

---

Kubernetes 上で MongoDB ReplicaSet および Sharded Cluster をデプロイ・運用するための Kubernetes Operator です。

> ## ⚠️ ベータリリース — v1.3.2-beta.x (carve-out)
>
> 現在の最新リリースは **prerelease ベータ** です — 正式な 1.4.0 GA リリースまでは *非プロダクションデータ* に限定した利用を推奨します。
>
> **ベータ scope (デフォルトで有効)**: MongoDB ReplicaSet
>
> **ベータ scope 外 (デフォルトで無効、RBAC + reconciler の feature gate で遮断)**:
> - `MongoDBSharded` — ConfigServer init / HPA ordering 未解決 (`features.sharded.enabled=true` で有効化)
> - `MongoDBBackup` — 自動テスト 0 件、connectionString 平文露出のリスク (`features.backup.enabled=true` で有効化)
> - HorizontalPodAutoscaler — RS/cfg drift mutex が不在 (`features.autoscaling.enabled=true` で有効化)
>
> 詳細な残存リスクは [CHANGELOG.md](CHANGELOG.md) の Known Issues セクションを参照してください。

## 概要

MongoDB Operator は、Kubernetes 上での MongoDB クラスターのデプロイ、スケーリング、運用を自動化します。カスタムリソース定義 (CRD) を用いて MongoDB インフラストラクチャを宣言的に管理する手段を提供します。

### 主な機能

- **MongoDB ReplicaSet**: 自動フェイルオーバーを備えた 3 メンバー以上の高可用性レプリカセットをデプロイ
- **Sharded Cluster** *(ベータでは無効)*: config server、shard、mongos router を含む分散クラスターをデプロイ
- **TLS 暗号化**: cert-manager 統合による TLS 証明書の自動管理
- **認証**: クラスター内部通信向けに keyfile をサポートした SCRAM-SHA-256 認証
- **モニタリング**: ServiceMonitor をサポートする Prometheus メトリクスエクスポート
- **バックアップ / リストア** *(ベータでは無効)*: S3 互換ストレージまたは PVC への自動バックアップ
- **オートスケーリング**: Mongos router 向け Horizontal Pod Autoscaler サポート

## アーキテクチャ

```
┌─────────────────────────────────────────────────────────────────┐
│                    MongoDB Operator                              │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │  MongoDB    │  │ MongoDBShar │  │    MongoDBBackup        │  │
│  │  Controller │  │ Controller  │  │    Controller           │  │
│  └──────┬──────┘  └──────┬──────┘  └───────────┬─────────────┘  │
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  Resource Builder                           ││
│  │  (StatefulSets, Deployments, Services, Secrets, Jobs)       ││
│  └─────────────────────────────────────────────────────────────┘│
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  MongoDB Package                            ││
│  │  (Executor, ReplicaSet, Auth, Sharding)                     ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
├─────────────────────────────────────────────────────────────────┤
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │  StatefulSet  │  │  StatefulSet  │  │  Deployment   │        │
│  │  (ReplicaSet) │  │  (Shards)     │  │  (Mongos)     │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
│                                                                  │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │   Services    │  │    Secrets    │  │  ConfigMaps   │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
└─────────────────────────────────────────────────────────────────┘
```

### 自動初期化

Operator は MongoDB クラスターの初期化を自動的に処理します。

**ReplicaSet の初期化:**
```
1. Keyfile Secret を作成 (内部認証用)
2. ConfigMap を作成 (mongod.conf)
3. Service を作成 (headless + client)
4. StatefulSet を作成
5. すべての Pod が ready になるまで待機
6. primary 候補で rs.initiate() を実行
7. primary 選出を待機
8. localhost 例外を利用して admin ユーザーを作成
```

**Sharded Cluster の初期化:**
```
1. 共有 Keyfile Secret を作成
2. Config Server StatefulSet をデプロイ (ポート 27019)
3. Shard StatefulSet をデプロイ (ポート 27018)
4. Mongos Deployment をデプロイ (ポート 27017)
5. Config Server ReplicaSet を初期化
6. 各 Shard ReplicaSet を初期化
7. Mongos 上に admin ユーザーを作成
8. 各 shard に対して sh.addShard() を実行
```

### ポート構成

| コンポーネント | ポート | フラグ |
|-----------|------|------|
| Mongos | 27017 | - |
| Shard | 27018 | `--shardsvr` |
| Config Server | 27019 | `--configsvr` |

## クイックスタート

### 前提条件

- Kubernetes クラスター v1.26+
- クラスターへのアクセスが構成された kubectl
- *インストール方式* ごとの追加要件:
  - **OLM v1** (推奨、モダン): cert-manager の稼働 + cluster admin (初回ブートストラップ時のみ)
  - **Helm**: Helm v3.8+
  - **OLM v0** (レガシー): Helm の簡潔さと OLM v1 の洗練度の中間 — *非推奨*

### インストール — 3 つの方式 (マトリクス)

| 方式 | 対象ユーザー | モダン度 | 手順数 |
|---|---|---|---|
| **OLM v1** *(推奨)* | 外部ユーザー、GitOps プラットフォーム (ArgoCD App-of-Apps)、Day-0 プロダクション | **次世代** (v1.8.0、2026-02 GA) | manifest 2 つ (ClusterCatalog + ClusterExtension) |
| Helm チャート | ローカル開発、シングルクラスターのシンプルなデプロイ | stable | 1 コマンド (`helm install`) |
| OLM v0 | OpenShift レガシー、OperatorHub.io コミュニティ | メンテナンスモード (v0.42、2026-04) | manifest 4 つ + InstallPlan の approve |

**詳細な手順**: [INSTALL.md](INSTALL.md)。本節は *クイックスタート* です。

#### Path 1 — OLM v1 (モダン標準、推奨)

```bash
# (1) OLM v1 クラスターインストール — 初回ブートストラップ
curl -L -s https://github.com/operator-framework/operator-controller/releases/latest/download/install.sh | bash -s

# (2) ClusterCatalog + ClusterExtension を適用
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clustercatalog.yaml
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clusterextension.yaml

# (3) インストールを検証
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s
```

#### Path 2 — Helm チャート

```bash
# Helm リポジトリの追加
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

# Operator のインストール
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

<!-- Path 3 (OLM v0 legacy) は削除されました — ADR-0028 Phase D、v1 のみ。helm または OLM v1 を使用してください。 -->


### MongoDB ReplicaSet のデプロイ

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  members: 3
  version:
    version: "8.3.1"
  storage:
    storageClassName: standard
    size: 10Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin
  monitoring:
    enabled: true
```

```bash
# Namespace と認証情報の作成
kubectl create namespace database
kubectl create secret generic mongodb-admin \
  --from-literal=username=admin \
  --from-literal=password=your-secure-password \
  -n database

# MongoDB をデプロイ
kubectl apply -f mongodb-replicaset.yaml
```

### Sharded Cluster のデプロイ

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: my-sharded
  namespace: database
spec:
  version:
    version: "8.3.1"
  configServer:
    members: 3
    storage:
      size: 5Gi
  shards:
    count: 3
    membersPerShard: 3
    storage:
      size: 50Gi
  mongos:
    replicas: 2
    service:
      type: LoadBalancer
```

## カスタムリソース定義

### MongoDB (ReplicaSet)

| フィールド | 説明 | デフォルト |
|-------|-------------|---------|
| `spec.members` | レプリカセットのメンバー数 | `3` |
| `spec.version.version` | MongoDB バージョン | `8.3.1` |
| `spec.storage.storageClassName` | StorageClass 名 | - |
| `spec.storage.size` | メンバーあたりの PVC サイズ | `10Gi` |
| `spec.auth.mechanism` | 認証メカニズム | `SCRAM-SHA-256` |
| `spec.tls.enabled` | TLS を有効化 | `false` |
| `spec.monitoring.enabled` | Prometheus メトリクスを有効化 | `false` |
| `spec.arbiter.enabled` | アービターノードを有効化 | `false` |

### MongoDBSharded

| フィールド | 説明 | デフォルト |
|-------|-------------|---------|
| `spec.configServer.members` | Config server のレプリカ数 | `3` |
| `spec.shards.count` | シャード数 | `2` |
| `spec.shards.membersPerShard` | 1 シャードあたりのメンバー数 | `3` |
| `spec.mongos.replicas` | Mongos router のレプリカ数 | `2` |
| `spec.mongos.autoScaling.enabled` | Mongos の HPA を有効化 | `false` |

## スケーリング

### 水平スケールアウト (シャード追加)

Operator は動的なシャードスケーリングをサポートします。`spec.shards.count` を増やすと、Operator は次の処理を自動的に実行します:

1. 新規 Shard StatefulSet と headless Service を作成
2. すべての Pod が ready になるまで待機
3. 新規シャードの ReplicaSet を初期化 (`rs.initiate()`)
4. 新規シャードを mongos に登録 (`sh.addShard()`)
5. MongoDB のバランサーが新規シャードへ自動的にチャンクを移行

**例: 3 シャードから 5 シャードへのスケールアウト**

```bash
# 現在のシャード数を確認
kubectl get mongodbsharded my-cluster -o jsonpath='{.spec.shards.count}'
# 出力: 3

# 5 シャードへスケールアウト
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"shards":{"count":5}}}'

# 新規シャード Pod を監視
kubectl get pods -l app.kubernetes.io/component=shard

# シャード登録を確認
kubectl exec -it my-cluster-mongos-xxx -c mongos -- \
  mongosh -u admin -p $PASSWORD --eval 'sh.status()'
```

**ステータス追跡:**
```yaml
status:
  shardsInitialized: [true, true, true, true, true]
  shardsAdded: [true, true, true, true, true]
  shards:
    - name: my-cluster-shard-0
      phase: Running
    - name: my-cluster-shard-1
      phase: Running
    - name: my-cluster-shard-2
      phase: Running
    - name: my-cluster-shard-3
      phase: Running
    - name: my-cluster-shard-4
      phase: Running
```

### 垂直スケーリング (リソース調整)

リソースの request / limit を更新します (ローリング再起動をトリガー):

```bash
kubectl patch mongodbsharded my-cluster --type='merge' -p '{
  "spec": {
    "shards": {
      "resources": {
        "requests": {"memory": "2Gi", "cpu": "1"},
        "limits": {"memory": "4Gi", "cpu": "2"}
      }
    }
  }
}'
```

### Mongos レプリカのスケーリング

Mongos router のスケールアップ / スケールダウン:

```bash
# スケールアップ
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":3}}}'

# スケールダウン
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":1}}}'
```

## リソース推奨値

### 最小要件

| コンポーネント | メモリ | CPU | 備考 |
|-----------|--------|-----|-------|
| Config Server | 256Mi | 100m | 3 メンバー必須 |
| Shard メンバー | 512Mi | 250m | レプリカごと |
| Mongos | 512Mi | 250m | 256Mi では OOM が発生 |

### プロダクション推奨値

| コンポーネント | メモリ | CPU | ストレージ |
|-----------|--------|-----|---------|
| Config Server | 1Gi | 500m | 10Gi SSD |
| Shard メンバー | 4Gi | 2 | 100Gi+ SSD |
| Mongos | 1Gi | 500m | - |

## テスト済み機能

ステータス表記の基準:
- **✅ Stable**: envtest 回帰 + 単体テスト + 実 mongod ワークロード (testcontainers / kind / 実クラスター) における負荷 / 耐久検証完了。エビデンス (ストレステスト結果 / インシデント事後処理) を保存しています。
- **✅ Implemented**: コード + envtest 回帰 + 単体テストによって *機能的* な正しさを確認済み。*負荷検証は運用者の責任* です。
- **⚠️ Beta**: コードは動作するものの、単体テストの一部のみで、実環境での検証はありません — 本番環境に適用する前に追加の検証が必要です。

| 機能 | ステータス | 備考 |
|---------|--------|-------|
| ReplicaSet 自動初期化 | ✅ Implemented | `rs.initiate()` を自動実行。envtest + driver 単体テスト。 |
| Sharded cluster 初期化 | ✅ Implemented | Config server、shard、mongos。envtest で検証。 |
| Admin ユーザー作成 | ✅ Implemented | K8s Lease ロック + ブートストラップ後 usersInfo 検証付きのドライバベースブートストラップ。 |
| Shard scale out (2→5) | ⚠️ Beta | `sh.addShard()` を自動実行 — ドライバ呼び出しは検証済みだが、*実クラスター負荷* は未検証。 |
| Shard scale in (5→2) | ⚠️ Beta | `removeShard()` 自動実行 + ShardDraining condition + リソースクリーンアップ (PVC は保持)。チャンク移行のロングランニングポーリングは 30s 固定 (バックオフ未適用)。 |
| Mongos レプリカスケーリング | ✅ Implemented | Deployment replicas 変更 → ローリング。 |
| リソース更新 | ✅ Implemented | STS UpdateStrategy 経由のローリング再起動。 |
| スケーリング中のデータ整合性 | ⚠️ Beta | コードフロー上はデータ損失をブロック (PVC retain、removeShard drain wait) — *実データ負荷検証* は未実施。 |
| スケーリング中の同時書き込み | ⚠️ Beta | ストレステストのエビデンスなし。今後 testcontainers-go ベースの負荷試験を予定。 |
| PodDisruptionBudget 自動化 | ✅ Implemented | `spec.podDisruptionBudget` で opt-in (MongoDB + Sharded)。ビルダー単体テストで 4 コンポーネントの生成を検証。 |
| NetworkPolicy 自動化 | ✅ Implemented | `spec.networkPolicy` で opt-in (deny-by-default + 追加 peer)。単体テストで cfg=27019 / shard=27018 / mongos=27017 のポートを検証。*実通信遮断検証は未実施*。 |
| Admin ブートストラップの競合フリー | ✅ Implemented | K8s Lease 分散ロック (30s TTL) + ブートストラップ後 `usersInfo` 検証。fake-client 単体テスト (busy / takeover / release)。holder Pod がクラッシュした場合、30s までは他の reconcile はバックオフ。 |

## 制限事項

### 未サポート

| 機能 | ステータス | 回避策 |
|---------|--------|------------|
| ReplicaSet メンバー削除 | ❌ 未実装 | 手動で `rs.remove()` を実行 |
| バックアップ自動スケジュール | ❌ Planned | 外部 CronJob を利用 |
| クロスクラスターレプリケーション | ❌ Planned | - |
| Sharded での Arbiter / Hidden トポロジー | ⚠️ ReplicaSet のみ | Arbiter は MongoDB CR でサポート、Sharded への拡張はロードマップ |

### 既知の問題

1. **Mongos のメモリ**: 最低 512Mi を推奨。256Mi では負荷時に OOM が発生します。
2. **ReplicaSet メンバーのグレースフル削除が未対応**: ReplicaSet メンバーをスケールダウンしても `rs.remove()` は呼び出されません — StatefulSet の replicas が減るだけです。
3. **Scale-in 時の PVC 保持**: `removeShard` の完了後、drain された shard の PVC は意図的に保持されます (意図しないデータ損失防止)。運用者は検証後に手動削除することが想定されています。

### MongoDBBackup

| フィールド | 説明 | デフォルト |
|-------|-------------|---------|
| `spec.clusterRef.name` | 対象クラスター名 | - |
| `spec.clusterRef.kind` | 対象クラスターの kind | `MongoDB` |
| `spec.type` | バックアップタイプ (full / incremental) | `full` |
| `spec.compression` | 圧縮を有効化 | `true` |
| `spec.storage.type` | ストレージタイプ (s3 / pvc) | `s3` |

## 設定

### cert-manager を用いた TLS

```yaml
spec:
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
```

### Prometheus モニタリング

```yaml
spec:
  monitoring:
    enabled: true
    prometheusRule:
      enabled: true
    serviceMonitor:
      interval: 30s
```

### S3 へのバックアップ

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: daily-backup
spec:
  clusterRef:
    name: my-mongodb
    kind: MongoDB
  storage:
    type: s3
    s3:
      bucket: mongodb-backups
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      credentialsRef:
        name: s3-credentials
```

## 開発

### 前提条件

- Go 1.21+
- Docker
- kubectl
- Kind または Minikube (ローカルテスト用)

### ビルド

```bash
# Operator のビルド
make build

# テスト実行
make test

# Docker イメージのビルド
make docker-build IMG=your-registry/mongodb-operator:tag

# Docker イメージのプッシュ
make docker-push IMG=your-registry/mongodb-operator:tag
```

### ローカル開発

```bash
# CRD をインストール
make install

# Operator をローカル実行
make run

# サンプル MongoDB を作成
kubectl apply -f config/samples/mongodb_replicaset.yaml
```

## ライセンス

本プロジェクトは Apache License 2.0 のもとでライセンスされています — 詳細は [LICENSE](LICENSE) ファイルを参照してください。

### サードパーティライセンス

本 Operator は MongoDB データベースを管理しますが、MongoDB ソフトウェアそのものを同梱・配布するものではありません。MongoDB Community Server は [Server Side Public License (SSPL)](https://www.mongodb.com/licensing/server-side-public-license) のもとでライセンスされています。

**重要なライセンス上の注意:**
- 本 Operator (Apache 2.0) は MongoDB のデプロイをオーケストレーションする独立したソフトウェアです
- MongoDB コンテナイメージは公式 MongoDB リポジトリから取得されます
- MongoDB のライセンス条項への準拠はユーザーの責任です
- 本 Operator は MongoDB バイナリの改変・再配布を行いません

## コントリビュート

コントリビュートを歓迎しています。コードオブコンダクトおよびプルリクエスト提出のプロセスについては [Contributing Guide](CONTRIBUTING.md) を参照してください。

## サポート

- **Issues**: [GitHub Issues](https://github.com/keiailab/mongodb-operator/issues)
- **Discussions**: [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)

## ロードマップ

- [x] ReplicaSet 自動初期化
- [x] Sharded Cluster 自動初期化
- [x] 水平シャードスケーリング (scale out)
- [x] Admin ユーザー自動作成
- [ ] Point-in-Time Recovery (PITR)
- [ ] バージョン自動アップグレード
- [ ] クロスクラスターレプリケーション
- [ ] Grafana ダッシュボードテンプレート
- [ ] CronJob によるバックアップスケジューリング
- [ ] データ移行付き scale down

## 謝辞

- [Kubernetes](https://kubernetes.io/)
- [Operator SDK](https://sdk.operatorframework.io/)
- [MongoDB](https://www.mongodb.com/)
- インスピレーション源としての [Bitnami MongoDB Charts](https://github.com/bitnami/charts)

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a> ·
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/forgewise">forgewise</a>
</p>

<p align="center">© 2026 keiailab · Apache-2.0 · <a href="https://github.com/keiailab">keiailab.com</a></p>
