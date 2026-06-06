<p align="center">
  <a href="ARCHITECTURE.md">English</a> |
  <a href="ARCHITECTURE.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="ARCHITECTURE.zh.md">中文</a>
</p>

# アーキテクチャ — mongodb-operator

> 単一ページのアーキテクチャ仕様書。CRD サーフェス / RBAC / reconcile パターンが変わるたびに更新されます。

## 概要

- **目的**: 宣言的な CRD を通じて、MongoDB ReplicaSet および Sharded Cluster のデプロイ、スケーリング、運用を自動化する Kubernetes Operator。
- **スコープ**: MongoDB 7.0+ デプロイメントに対し、`MongoDB`、`MongoDBSharded`、`MongoDBBackup`、federation、insights CRD を reconcile する K8s コントローラー。
- **安定性ティア**: v1.5.0 (GA scope = ReplicaSet、Sharded / Backup / HPA = beta feature gate)。
- **最新リリース**: v1.5.0 (2026-05-13)
- **ライセンス**: MIT
- **モジュールパス**: `github.com/keiailab/mongodb-operator`

## CRD サーフェス (8 CRD)

| CRD | apiVersion | Scope | Tier | 説明 |
|---|---|---|---|---|
| `MongoDB` | `mongodb.keiailab.com/v1alpha1` | Namespaced | **GA** | 3 メンバー以上の ReplicaSet + 自動 failover |
| `MongoDBSharded` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (feature gate `sharded.enabled`) | Sharded クラスター: config server + shard + mongos ルーター |
| `MongoDBBackup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta (`backup.enabled`) | S3 または PVC のバックアップスナップショット |
| `MongoDBBackupVerification` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Beta | リストアドリルの検証 |
| `MongoDBClusterGroup` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | マルチクラスターグルーピング (federation scaffold) |
| `MongoDBFederation` | `mongodb.keiailab.com/v1alpha1` | Cluster | Alpha | リージョン間 federation |
| `MongoDBInsights` | `mongodb.keiailab.com/v1alpha1` | Namespaced | Alpha | パフォーマンスインサイトのサーフェス |
| (共通型) | `mongodb.keiailab.com/v1alpha1` | — | — | Conditions / Finalizers ヘルパー |

## Reconcile フロー

```
┌──────────────────────────────────────────────────────────────┐
│                    MongoDB Operator                          │
├──────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌──────────────────────┐ │
│  │  MongoDB    │  │ MongoDBShar │  │   MongoDBBackup      │ │
│  │  Controller │  │ Controller  │  │   Controller         │ │
│  └─────────────┘  └─────────────┘  └──────────────────────┘ │
│           │              │                  │                │
│           ▼              ▼                  ▼                │
│  StatefulSet / Service / Secret / ConfigMap / PDB / NP /     │
│  ServiceMonitor / cert-manager Certificate                   │
└──────────────────────────────────────────────────────────────┘
```

- `MongoDB` reconciler: StatefulSet (3+ レプリカ) + headless Service + Secret (SCRAM keyfile) + cert-manager Certificate + PDB + ServiceMonitor
- `MongoDBSharded` reconciler: 3 つの StatefulSet (config / shard / mongos) を順序付きで初期化
- `MongoDBBackup` reconciler: S3 互換ストレージまたは PVC へスナップショットを取る Job

## RBAC スコープ

- ClusterRole: CRD watch + cert-manager Certificate + PrometheusOperator ServiceMonitor
- Role (ネームスペースごと): StatefulSet / Service / Secret / ConfigMap / PVC / PDB / NetworkPolicy
- ServiceAccount: `mongodb-operator` (default-deny NetworkPolicy 適用)

## operator-commons インポートサーフェス

`operator-commons/ARCHITECTURE.md` のマトリクスに基づく採用率: **6/8 (75%)**。

| パッケージ | ステータス | 用途 |
|---|---|---|
| `pkg/security` | ✅ | restricted PSA SecurityContext |
| `pkg/version` | ✅ | MongoDB バージョン allowlist |
| `pkg/labels` | ✅ | 推奨ラベル (`app.kubernetes.io/*`) |
| `pkg/monitoring` | ⏳ | ServiceMonitor reconciler のローカル実装 — commons 委譲は保留中 |
| `pkg/networkpolicy` | ✅ | Deny-by-default + functional option |
| `pkg/webhook` | ⏳ | Admission validation のローカル実装 — commons 委譲は保留中 |
| `pkg/finalizer` | ✅ | `Add` / `Remove` / `Has` |
| `pkg/status` | ✅ | Condition reason カタログ |

## テストレイヤー

| レイヤー | 場所 | カバレッジ |
|---|---|---|
| Unit | `internal/**/_test.go` | 80% 目標以上 |
| Integration (envtest) | `test/integration/` | コアの reconcile + finalizer 経路 |
| E2E (kind) | `test/e2e/` | release クリティカルなシナリオ (RS + sharded + backup) |
| Scorecard | `bundle/tests/scorecard/` | OLM v1alpha3、postgres ADR-0013 と 6-test parity |

## ビルド / デプロイ

### ビルド成果物 (release タグごと、例: v1.5.0)

| 成果物 | イメージ / パス | 用途 |
|---|---|---|
| Operator コンテナ | `ghcr.io/keiailab/mongodb-operator:v1.5.0` | manager pod ランタイム |
| Helm chart | `charts/mongodb-operator/` → `helm package` | Path 2 インストール (単一コマンド) |
| OLM bundle (CSV + CRD + scorecard) | `bundle/` → `ghcr.io/keiailab/mongodb-operator-bundle:v1.5.0` | OLM パッケージング単位 (FBC カタログから参照) |
| FBC カタログ | `deploy/catalog/` → `ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0` | OLM v1 ClusterCatalog ソース (ADR-0028 Phase D) |
| ArtifactHub repo | `artifacthub-repo.yml` | discovery + 署名検証 |
| SBOM | `make sbom` → SPDX-2.3 | SLSA / EU CRA |

### 3 つのデプロイモデル (外部ユーザー向け、ADR-0028 + ADR-0029)

| モデル | クラスターインストール | Operator インストール | モダン性 | Day-2 |
|---|---|---|---|---|
| **OLM v1** *(推奨)* | `operator-controller + catalogd` (olmv1-system ns) | `ClusterCatalog + ClusterExtension` のわずか 2 リソース | 🟢 next-generation (2026-02 GA) | catalog channel + version pin/range |
| Helm chart | (なし、direct deploy) | `helm install` | 🟡 stable | `helm upgrade/rollback` |
| OLM v0 *(legacy)* | `olm-operator + catalog-operator + packageserver` (olm ns) | `CatalogSource + OperatorGroup + Subscription + InstallPlan` | 🔴 maintenance mode | Subscription channel + approve |

詳細手順 + Day-2 upgrade/rollback: [Installation Guide](../install.md)。KeiaiLab Cluster ライブ evidence: [deploy/olm-v1/README.md](deploy/olm-v1/README.md) (OLM v0 path は ADR-0028 Phase D で廃止)。

### リリースパイプライン

- CI: ADR-0027 community-operators upstream sync (OLM v0 path) + 本 release タグ → GHCR push (operator + bundle + catalog) + Helm chart Pages publish。
- Cosign: コンテナイメージ + Helm chart + SBOM すべてを keyless OIDC で署名 (G-13、ADR-0023)。
- Renovate: digest pinning (ADR-0066 整合)。

## Feature gate (beta scope の opt-in)

`values.yaml` 基準:
- `features.sharded.enabled` (デフォルト false) — `MongoDBSharded` CRD watch + RBAC を gate
- `features.backup.enabled` (デフォルト false) — `MongoDBBackup` CRD watch + Job RBAC を gate
- `features.autoscaling.enabled` (デフォルト false) — HPA reconciler を gate

プロダクションクラスターのパターン: GA 専用。Beta CRD は明示的な opt-in が必要。

## ADR クロスリンク (29 ADR)

主なもの:
- ADR-0001: charter / プロジェクトの identity
- ADR-0013: scorecard OLM test parity 標準 (postgres co-author)
- ADR-0023: OperatorHub bundle scaffold
- ADR-0027: community-operators upstream sync 自動化
- ADR-0028: 外部ユーザー運用レベル (5 ギャップ解消、channel/maturity stable 昇格)
- **ADR-0029: OLM v1 採用 (next-generation、ClusterCatalog + ClusterExtension)**

完全な一覧: `docs/kb/adr/INDEX.md`。

## Roadmap ステータス

- Phase 1 Production hardening: **100%** (21/21)
- Phase 2 Enterprise auth + multi-region: **100%** (21/21)
- Phase 3 Advanced enterprise: **100%** (16/16)
- Phase 4 Bitnami parity: **100%** (26/26)
- Phase 5 (post-v1.5.0): *定義中* — `~/.claude/plans/2026-05-14-4-operators-100pct/P-E.md`

## Non-goal (意図的にスコープ外)

- ❌ MongoDB 7.0 未満のバージョン (`pkg/version` allowlist に従う)
- ❌ Operator バンドルに MongoDB Enterprise バイナリ同梱 (MIT ライセンスの境界)
- ❌ MongoDB Atlas / cloud-managed 統合 (out of scope)
- ❌ `bitnami/mongodb` chart の埋め込み (parity はネイティブ実装で達成)

## 参考資料

- `README.md` / `README.ko.md`
- `(../roadmap.md)` (Phase 1-4 100% complete)
- `(../changelog.md)`
- `ADOPTERS.md`
- `(../contributing.md)` / `CONTRIBUTING.ko.md`
- `(../governance.md)`
- `(../support.md)`
- `AGENTS.md` — AI アシスタントの runbook
- `docs/kb/adr/INDEX.md` — 28 ADR

