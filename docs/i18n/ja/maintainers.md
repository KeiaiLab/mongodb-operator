<p align="center">
  <a href="(../maintainers.md)">English</a> |
  <a href="MAINTAINERS.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="MAINTAINERS.zh.md">中文</a>
</p>

# Maintainers

本ドキュメントは keiailab/mongodb-operator の意思決定権限を持つメンテナーの名簿を管理します。

## 現在のメンテナー

| 名前/チーム | GitHub | 役割 | 担当領域 |
|---|---|---|---|
| keiailab maintainers | [@keiailab/maintainers](https://github.com/orgs/keiailab/teams/maintainers) | Lead | 全領域 |

GitHub team `@keiailab/maintainers` が本プロジェクトの全領域に対するマージ/承認権限を保有します。個別メンテナーの追加は以下の手順に従って行われます。

## メンテナー資格

以下の条件を 6 か月以上満たした contributor をメンテナーに推薦できます:

- マージされた PR ≥ 20 件 (意味のあるコード/ドキュメント貢献)
- レビューした PR ≥ 30 件 (建設的なフィードバックを伴う)
- 本プロジェクトの [Governance](../governance.md) と [Code of Conduct](../code-of-conduct.md) の遵守
- 一つ以上のコア領域 (controller, resource builder, sharded reconcile, bootstrap-admin script, observability など) に対する深い理解

## 追加手順

1. 既存メンテナー、または candidate 本人が issue または ADR で提案
2. `@keiailab/maintainers` チームの lazy consensus (7 日間のコメントウィンドウ)
3. 反対がなければ GitHub team に追加し、(../maintainers.md) 更新の PR

## 非アクティブなメンテナー

連続 6 か月活動がないメンテナーは emeritus へ移行します (権限を回収し、名誉名簿として維持)。復帰は新規追加手順と同じです。

## 領域別担当 (CODEOWNERS と同期)

`.github/CODEOWNERS` (存在する場合) を参照してください。ディレクトリごとに自動レビュアーが割り当てられます。

## Emeritus

(まだいません)

