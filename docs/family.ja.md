<p align="center">
  <img src="https://github.com/keiailab.png" alt="keiailab" width="120"/>
</p>

<p align="center">
  <a href="family.md">English</a> |
  <a href="family.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="family.zh.md">中文</a>
</p>

# keiailab operator family

> 共通基盤の上に構築された 4 つの姉妹 Kubernetes operator — `operator-commons` (Go ライブラリ) + Helm partial + Apache-2.0 スタック。

このページは **`mongodb-operator`** リポジトリから参照しています。本ドキュメントは family 全体の canonical な cross-link です。

## Family 概要

| プロジェクト | データベース | ステータス | リポジトリ |
|---|---|---|---|
| **`postgres-operator`** | PostgreSQL 18+ | active | https://github.com/keiailab/postgres-operator |
| **`mongodb-operator`** | MongoDB 7.0+ | active | https://github.com/keiailab/mongodb-operator |
| **`valkey-operator`** | Valkey 8.0+ (Redis fork, BSD-3) | active | https://github.com/keiailab/valkey-operator |
| **`operator-commons`** | 共通 Go ライブラリ | v0.7.0 | https://github.com/keiailab/operator-commons |

## 共有しているもの

4 つのプロジェクトはすべて同じ運用上の primitive に収束しています:

- **Apache-2.0** エンドツーエンド — SSPL なし、SaaS surface に copyleft なし
- **`operator-commons`** 共通 Go ライブラリ (v0.7.0+) — finalizer、label、status sugar、security context builder、NetworkPolicy / ServiceMonitor partial
- **Helm chart スケルトン** — RFC-0027 の `default` falsy-toggle 防止、RFC-0026 の component-keyed values、cycle 26 hardening の 6 マーカー (priorityClassName / lifecycle / SA / minReadySeconds / automount / revisionHistoryLimit)
- **OLM bundle parity** — scorecard v1alpha3 の 6-test matrix
- **i18n** — README + canonical な 11 ドキュメントを English / 한국어 / 日本語 / 中文 で提供 (cleanup supercycle 2026-05-21 の Wave 4)

## やらないこと

- ❌ **upstream operator の embed や wrap** (PGO、CloudNativePG、MongoDB Community Operator、Sentinel) — license-clean、copyleft 義務なし
- ❌ **release gate 用の GitHub Actions** — local 4 層 + GitLab CI L5 (RFC-0002、RFC-0043 を参照)
- ❌ **時間ベースのロードマップ締切** — feature チェックリスト + 完了パーセンテージ (`standards/roadmap.md §1.1` 参照)
- ❌ **Bitnami chart / image** — レジストリ deprecation リスク、Broadcom による買収 (ADR-0136 / ADR-0057 参照)

## 開始ポイント

| タスク | エントリーポイント |
|---|---|
| Kubernetes に `mongodb-operator` をデプロイ | [README.md](../README.md) の Quickstart セクション |
| アーキテクチャを読む | [ARCHITECTURE.md](../ARCHITECTURE.md) |
| issue や feature request の起票 | https://github.com/keiailab/mongodb-operator/issues |
| デザインやロードマップの議論 | https://github.com/keiailab/mongodb-operator/discussions |
| コードコントリビュート | [CONTRIBUTING.md](../CONTRIBUTING.md) |
| セキュリティ問題の報告 | [SECURITY.md](../SECURITY.md) |
| ブランド / ボイスを学ぶ | [BRANDING.md](../BRANDING.md) |
| 採用者 / ユーザーの追跡 | [ADOPTERS.md](../ADOPTERS.md) |
| メンテナを探す | [MAINTAINERS.md](../MAINTAINERS.md) |
| ガバナンスモデルの確認 | [GOVERNANCE.md](../GOVERNANCE.md) |
| 今後の作業をチェック | [ROADMAP.md](../ROADMAP.md) |

## Family 間互換性 (operator-commons)

3 つのデータベース operator はすべて同一バージョンの `github.com/keiailab/operator-commons` を import します (現在は `v0.7.0+`):

```go
import (
    "github.com/keiailab/operator-commons/pkg/version"
    "github.com/keiailab/operator-commons/pkg/security"
    "github.com/keiailab/operator-commons/pkg/labels"
    "github.com/keiailab/operator-commons/pkg/monitoring"
    "github.com/keiailab/operator-commons/pkg/finalizer"
    "github.com/keiailab/operator-commons/pkg/status"
)
```

`operator-commons` の breaking change は 3 つのデータベース operator すべてで同期した bump が必要です — supercycle Wave 5 の `make cross-validation` ターゲットで検証されます。

## i18n

canonical なプロジェクトドキュメント (README、CONTRIBUTING、SECURITY、GOVERNANCE、MAINTAINERS、ROADMAP、SUPPORT、BRANDING) は 4 言語で利用できます — 各ファイル上部の言語スイッチャーを参照してください。本 family 概要は English のみで、各リポジトリのローカライズされた README をネイティブ言語のエントリーポイントとしてご利用ください。

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="../LICENSE">Apache-2.0</a> · <a href="https://github.com/keiailab">github.com/keiailab</a>
</p>
