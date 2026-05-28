<p align="center">
  <a href="ADOPTERS.md">English</a> |
  <a href="ADOPTERS.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="ADOPTERS.zh.md">中文</a>
</p>

# mongodb-operator の採用組織

本ドキュメントは、`keiailab/mongodb-operator` を本番環境または評価環境で利用している組織・プロジェクトの *公開* リストです。自己登録を歓迎します — PR で行を追加してください。

> 非公開のユーザーは GitHub Discussions または SECURITY.md に記載のプライベートチャネル経由でご連絡いただけます。

## Production Users

本番環境において mongodb-operator を *production-grade SLA* で運用しているユーザー。

| 採用者 | コンポーネント | 利用パターン | 導入バージョン | 現行バージョン | 登録日 |
|---|---|---|---|---|---|
| **keiailab-platform-data** ([keiailab](https://github.com/keiailab)) | MongoDB 8.3 ReplicaSet + Sharded (Config Server + Shard + Mongos) | keiailab のメタデータストレージ。ArgoCD GitOps による自動同期。PodSecurity restricted、KEYFILE auth、ServiceMonitor active。 | v1.4.5 | v1.4.11 | 2026-05-07 |

## Evaluators

PoC / 評価 / non-production 環境で利用しているユーザー。

| 採用者 | フェーズ | 備考 |
|---|---|---|
| _自己登録を歓迎_ | — | PR で行を追加 |

## How to add yourself

PR を開いて上記のテーブルに 1 行追加してください:

```markdown
| **<組織 / プロジェクト>** ([profile](<URL>)) | <コンポーネント + トポロジー> | <利用パターン> | <導入バージョン> | <現行バージョン> | <登録日 YYYY-MM-DD> |
```

非公開もしくは匿名での登録をご希望の場合は SECURITY.md に記載されたセキュリティチャネル経由でご連絡ください。maintainer が *organization-anonymized* の行として登録します。

## CNCF Sandbox Reference

本 ADOPTERS リストは、CNCF graduation criteria の "≥1 public adopter" 要件を満たすための公開リファレンスとしても活用されます。

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://github.com/keiailab">keiailab.com</a>
</p>
