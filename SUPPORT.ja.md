<p align="center">
  <a href="SUPPORT.md">English</a> |
  <a href="SUPPORT.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="SUPPORT.zh.md">中文</a>
</p>

# サポート

> 韓国語ユーザー向け注記: 本ドキュメントのチャネルは英語と韓国語のいずれも歓迎します。

`mongodb-operator` をご利用いただきありがとうございます。本ページはヘルプを得られる場所について説明します。

## 必要なものを見極める

| 状況 | 連絡先 |
|---|---|
| **セキュリティ脆弱性を発見したと思われる場合。** | **公開 issue を開かないでください。** [SECURITY.md](SECURITY.md) を使用 — GitHub Security Advisory または `security@keiailab.com` (PGP 署名)。 |
| 「これは X のように動作するのが正しいのですか?」または「Y はどのように設定するのですか?」といった質問。 | [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)。検索可能で、将来のオペレーターのためにインデックス化されます。 |
| バグを発見した — ドキュメントとは異なる動作をする。 | **Bug report** テンプレートを使用して [issue を開いてください](https://github.com/keiailab/mongodb-operator/issues/new/choose)。 |
| 機能の追加または動作の変更を希望する。 | **Feature request** テンプレートを使用して [issue を開いてください](https://github.com/keiailab/mongodb-operator/issues/new/choose)。すでに計画されているかどうか、まず [ROADMAP.md](ROADMAP.md) を確認してください。 |
| 「これは FAQ に載せるべき」という質問。 | **Question** テンプレートを使用して [issue を開いてください](https://github.com/keiailab/mongodb-operator/issues/new/choose)。 |
| Prometheus アラートが発火し、MTTR 手順が必要。 | [`docs/operations/runbook.md`](docs/operations/runbook.md) §9 (各アラートの `runbook_url` アノテーションがここを指します)。 |
| アラートはないが、奇妙な動作が見られる。 | [`docs/operations/troubleshooting.md`](docs/operations/troubleshooting.md) — 症状 → 原因 → 診断 → 是正のフローチャート。 |
| コードまたはドキュメントに貢献したい。 | [CONTRIBUTING.md](CONTRIBUTING.md)。 |

## Issue を開く前に

1. [既存の issues](https://github.com/keiailab/mongodb-operator/issues?q=is%3Aissue) および [Discussions](https://github.com/keiailab/mongodb-operator/discussions) を検索してください — すでに回答済みかもしれません。
2. [troubleshooting フローチャート](docs/operations/troubleshooting.md) を試してください。
3. 報告には以下を準備してください:
   - `mongodb-operator` バージョン (`kubectl get deploy -n mongodb-operator-system -o jsonpath='{.items[0].spec.template.spec.containers[0].image}'`)
   - Kubernetes バージョン (`kubectl version`)
   - Helm chart バージョン (`helm list -A | grep mongodb-operator`)
   - 作成可能な最小の再現ケース
   - `kubectl describe <Valkey|ValkeyCluster> <name>` の出力

## レスポンスへの期待値

本プロジェクトは best-effort の時間で維持されているオープンソースプロジェクトです。
[GOVERNANCE.md](GOVERNANCE.md) が意思決定および
レビュープロセスを説明しています。通常、issue には数営業日以内に
応答します; セキュリティ報告は
[SECURITY.md](SECURITY.md) の SLA に従って処理されます (initial ack は 72 h 以内、severity triage は
7 日以内)。

有償サポート契約や hard SLA が必要な場合は、
`security@keiailab.com` までご連絡いただければ、オプションについて協議できます。

## 商用ベンダー

`mongodb-operator` は現時点では有償サポートベンダーを推奨していません。
これが変更された場合、ベンダーの条件と
サポートする upstream 機能と共にエントリーが追加されます。

## Code of Conduct (行動規範)

上記のすべてのチャネルは
[Code of Conduct](CODE_OF_CONDUCT.md) に従って運営されます。参加前に
必ずお読みください。

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
