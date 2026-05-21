<p align="center">
  <img src="https://keiailab.com/assets/logo.svg" alt="keiailab" width="120"/>
</p>

# mongodb-operator (日本語)

> [English](README.md) | [한국어](README.ko.md) | **日本語** (placeholder) | [中文](README.zh.md) (placeholder)

> **Apache-2.0 Kubernetes 用 MongoDB Operator — ReplicaSet + Sharded Cluster + Backup、vanilla MongoDB 7.0+**

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="ライセンス"/></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go バージョン"/></a>
  <a href="https://www.mongodb.com/"><img src="https://img.shields.io/badge/MongoDB-7.0%2B-47A248?logo=mongodb" alt="MongoDB"/></a>
  <a href="https://kubernetes.io/"><img src="https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes" alt="Kubernetes"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator"><img src="https://img.shields.io/badge/ghcr.io-keiailab%2Fmongodb--operator-blue?logo=github" alt="コンテナイメージ"/></a>
</p>

---

> **状態**: `[~]` 部分実装 (placeholder) — RFC-0025 §1.2 チェックボックス意味.
> native reviewer による品質検証後、`[x]` 完了状態へ昇格 candidate.

## 概要

mongodb-operator は Kubernetes 上で MongoDB クラスター (ReplicaSet および
Sharded Cluster) のデプロイ・管理を自動化する Operator です。CRD (Custom
Resource Definitions) を使って MongoDB インフラを宣言的に管理します。

詳細は [English README](README.md) の "Overview" セクションを参照してください。

## 機能

- **MongoDB ReplicaSet**: 自動フェイルオーバー付き 3 メンバー以上の高可用性
  replica set デプロイ
- **Sharded Cluster** *(ベータ無効)*: config server / shard / mongos router を
  含む分散クラスターデプロイ
- **TLS 暗号化**: cert-manager 連携による TLS 証明書自動管理
- **認証**: クラスター内部通信用 keyfile 対応 SCRAM-SHA-256 認証
- **モニタリング**: ServiceMonitor 対応 Prometheus メトリクスエクスポート
- **バックアップ/リストア** *(ベータ無効)*: S3 互換ストレージまたは PVC への
  自動バックアップ
- **自動スケーリング**: Mongos router 向け Horizontal Pod Autoscaler サポート

機能表面の詳細は [English README](README.md) を参照してください。

## ⚠️ ベータリリース — v1.3.2-beta.x (carve-out)

現在の最新 release は **prerelease ベータ** です — 正式 1.4.0 GA リリース前まで
*非プロダクションデータ* 限定使用を推奨します。

**ベータ scope (デフォルト有効)**: MongoDB ReplicaSet

**ベータ scope 外 (デフォルト無効、RBAC + reconciler feature gate で遮断)**:
- `MongoDBSharded` — ConfigServer init/HPA ordering 未解決 (`features.sharded.enabled=true` で有効化)
- `MongoDBBackup` — 自動テスト 0 件、connectionString 平文露出リスク (`features.backup.enabled=true` で有効化)
- HorizontalPodAutoscaler — RS/cfg drift mutex 不在 (`features.autoscaling.enabled=true` で有効化)

詳細な残存リスクは [CHANGELOG.md](CHANGELOG.md) の Known Issues セクションを参照。

## インストール

3 つの install path (OLM v1 / Helm / OLM v0 legacy) があります。詳細は
[INSTALL.md](INSTALL.md) および [English README](README.md) の "Installation" セクションを参照してください。

クイック例 (Helm chart):

```bash
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

## クイックスタート

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

Sharded Cluster の例、CRD フィールド一覧、スケーリング手順、TLS 設定、
モニタリング設定、開発ワークフローなどの詳細は
[English README](README.md) を参照してください。Native reviewer 翻訳完了後に
本 placeholder は完全版へ拡張されます。

## 参照

- [English README](README.md) — canonical SSOT
- [한국어 README](README.ko.md) — 韓国語版
- [中文 README](README.zh.md) — 中国語版 (placeholder)
- [INSTALL.md](INSTALL.md) — インストール詳細
- [DESIGN.md](DESIGN.md) — 設計ドキュメント
- [CHANGELOG.md](CHANGELOG.md) — 変更履歴
- [Glossary (日本語)](../operator-commons/docs/i18n/glossary-ja.md) — 標準用語集
  (operator-commons リポジトリ、placeholder 状態)

## ライセンス

Apache-2.0 — [LICENSE](LICENSE) を参照。

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a> ·
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/forgewise">forgewise</a>
</p>

<p align="center">© 2026 keiailab · Apache-2.0 · <a href="https://keiailab.com">keiailab.com</a></p>
