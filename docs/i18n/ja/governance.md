<p align="center">
  <a href="GOVERNANCE.md">English</a> |
  <a href="GOVERNANCE.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="GOVERNANCE.zh.md">中文</a>
</p>

# ガバナンス

本ドキュメントは keiailab/mongodb-operator プロジェクトの意思決定プロセスを定義します。

## 原則

1. **オープンネス**: すべての意思決定は公開チャネル (GitHub issue / PR / RFC) で行われます。
2. **Lazy Consensus (最小限のコンセンサス)**: 日常的な変更は反対がなければ進められます。
3. **Explicit Consensus (明示的なコンセンサス)**: アーキテクチャ変更、CRD 変更、セキュリティモデル変更、ライセンス変更は RFC / ADR を経て Maintainer の **2/3 supermajority** で承認します。通常の RFC (単一コンポーネント / ツール採用 / ポリシー補強) は **simple majority (>50%)**。GOVERNANCE 自体の変更 (§「本ドキュメントの変更」) は常に 2/3 supermajority。
4. **共同責任**: Maintainer はコード品質、ユーザーの安全性、コミュニティの健全性について共同で責任を負います。

## 意思決定の分類

### 日常的な変更 (Lazy Consensus)
- バグ修正、ドキュメント改善、テスト追加、依存関係の minor / patch アップグレード、リファクタリング (公開 API は不変)
- プロセス: PR → 1 名以上の Maintainer による LGTM → マージ
- 期限: コメントウィンドウは別途設けません (ローカルゲート通過時、即時マージ可能 — RFC-0002 に従い GitHub Actions は不使用、pre-commit / pre-push hook + Makefile で検証)

### 中程度の変更 (Explicit Consensus)
- 新規 CRD フィールドの追加、新規 reconciler、依存関係の major アップグレード、公開 API の変更
- プロセス: issue で提案 → 7 日間のコメントウィンドウ → Maintainer の多数派による LGTM → マージ
- 反対が 1 件でもある場合は Maintainer 会議にて議論

### アーキテクチャ変更 (RFC / ADR 必須)
- 新規コンポーネントの導入、セキュリティモデルの変更、ライセンスの変更、互換性を破る変更
- プロセス:
  1. `docs/kb/adr/NNNN-title.md` に ADR または RFC を提出
  2. 14 日間のコメントウィンドウ
  3. Maintainer の 2/3 以上の賛成
  4. ADR / RFC Status: `Draft → Accepted` を経てから実装 PR に進む

### デプロイメントモデルの変更 (ADR + ユーザー明示的な cluster apply、2026-05-15 追加)

OLM v0 ↔ OLM v1 ↔ Helm chart の *デフォルト推奨変更* または *cluster apply* は本領域に該当します。

| 決定 | ツール | ゲート |
|---|---|---|
| bundle / catalog manifest の変更 | PR + ADR | Conventional Commits + bundle validate PASS |
| OLM v0 → v1 migration のような *モデル切替え* | ADR (cluster-side) | ADR + Maintainer 2/3 + ユーザー明示的な cluster apply |
| installer RBAC 変更 (cluster-admin ↔ narrow) | PR + ADR | bundle CSV derive 検証 + cluster apply のユーザー明示的指示 |
| NetworkPolicy の新設 / 変更 | PR + ADR | OPRUN-3923 reference + cluster apply のユーザー明示的指示 |
| 外部ユーザー向け *recommended install path* の変更 | RFC | INSTALL.md §1 matrix 更新 + 14 日間のコメント |

本領域の ADR chain: ADR-0028 (外部ユーザー運用水準) → ADR-0029 (OLM v1 採用) → ADR-0030 (narrow RBAC + NP)。後続 ADR の *cluster apply* はユーザー明示的指示の領域 (グローバル §2.0 自律憲章 ② の条件 — 取り返しのつかない運用作業)。

## セキュリティに関する決定

CVE 報告、シークレット / 認証モデルの変更は [SECURITY.md](SECURITY.md) のプロセスに従い、非公開チャネルで優先的に処理した後、パッチリリース後に公開でのコンセンサスを取ります。

### Installer RBAC scope (ADR-0030、2026-05-15)

- **production cluster**: `clusterextension-narrow-rbac.yaml` (bundle CSV derive) を推奨。
- **PoC / dev cluster**: `clusterextension.yaml` (cluster-admin) を許容 — シンプルさを優先。
- **cluster-admin binding の恒久運用は禁止** — bundle 操作時に cluster 全体に影響します。PR review で cluster-admin を使用する場合は *production 外* であることを明示的に確認してください。

### Network surface (ADR-0030、2026-05-15)

- **default-deny cluster**: olmv1-system + mongodb-system に対する NetworkPolicy の *明示的適用* が必須。`deploy/olm-v1/networkpolicies.yaml` を参照 (OLM v0 path は ADR-0028 Phase D で恒久廃止)。
- **default-allow cluster**: NP は optional だが、適用を推奨 (security baseline)。

## リリースに関する決定

リリース分岐 / バージョン bump は Maintainer 1 名による Lazy Consensus で進められます。ただし LTS ラインの新設 / EOL 宣言 / Sharded GA 卒業など *主要マイルストーン* は Explicit Consensus が必須。

### Release artifact の到達可能性 (ADR-0028 の後続)

各 release 時に *3 deployment models* すべてに到達可能でなければなりません:
- **operator container**: ghcr.io public — すべての path に共通
- **Helm chart**: gh-pages → artifacthub.io へ自動 publish
- **OLM bundle + catalog**: `make bundle-push + catalog-push` 後、ArtifactHub + community-operators の upstream PR (ADR-0027 の自動化は deferred — 手動 fallback)

上記 3 つの artifact のうち *1 つでも missing* の場合は release tag を禁止。

## 変更履歴

| Date | Change | Refs |
|---|---|---|
| 2026-05-15 | Deployment モデル変更分類 + Installer RBAC scope + Network surface + Release artifact 到達可能性を新設 | ADR-0028, ADR-0029, ADR-0030 |
| 2026-05-07 | 本ドキュメント新設 — 3-repo (mongodb / postgresql / valkey) ガバナンス資産の整合 | INC-2026-05-07 |

