<p align="center">
  <a href="SECURITY.md">English</a> |
  <a href="SECURITY.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="SECURITY.zh.md">中文</a>
</p>

# セキュリティポリシー

## サポート対象バージョン

MongoDB Operator チームでは、以下のバージョンに対してセキュリティアップデートを提供しています:

| バージョン | サポート状況 |
|---------|----------------|
| 現行シリーズ | ✅ アクティブなセキュリティサポート |
| MongoDB 8.2 | ✅ テスト済み・サポート対象 |
| Kubernetes 1.26+ | ✅ テスト済み・サポート対象 |

セキュリティアップデートは、サポート対象バージョン向けのパッチとしてリリースされます。最新のセキュリティ修正の恩恵を受けるため、operator および MongoDB クラスターを最新状態に保つことを強く推奨します。

## 脆弱性の報告

弊チームではセキュリティ報告を真摯に受け止めています。セキュリティ脆弱性を発見された場合は、公開する前に非公開で弊チームへ報告してください。

### 報告方法

**推奨方法**: GitHub の private vulnerability reporting を利用してください
1. https://github.com/keiailab/mongodb-operator/security/advisories にアクセスします
2. 「Report a vulnerability」をクリックします
3. 表示される指示に従って報告を送信します

**代替方法**: security@keiailab.com まで直接メールでご連絡ください

### 報告に含めるべき内容

可能な限り詳細な情報を含めてください:
- 脆弱性の説明
- 問題を再現する手順
- 脆弱性の潜在的な影響
- proof-of-concept または exploit コード(利用可能な場合)

### プライバシー

すべての脆弱性報告は厳格な機密性を保って取り扱われます。報告内容は、当該問題への対応を担当する maintainer のみに共有されます。許可なく報告者の身元を公開することはありません。

## ユーザー向けセキュリティのベストプラクティス

MongoDB デプロイメントを安全に運用するために:

1. **TLS の有効化**: cert-manager 統合を利用し、転送中のデータに対して TLS 暗号化を常に有効化してください
2. **強力な認証**: Kubernetes Secrets に保存された強力で一意なパスワードとともに SCRAM-SHA-256 を使用してください
3. **RBAC**: operator の権限を最小権限の原則に従って制限するため、適切な Kubernetes RBAC を設定してください
4. **NetworkPolicy**: pod 間通信を制限するため、network policy を実装してください
5. **定期的なアップデート**: operator および基盤となる MongoDB のバージョンを最新に保ってください
6. **バックアップのセキュリティ**: バックアップストレージの認証情報を安全に管理し、バックアップに対する暗号化を有効化してください
7. **モニタリング**: 異常なアクティビティパターンを検知するため、Prometheus モニタリングを有効化してください
8. **リソース制限**: DoS 攻撃を防ぐため、適切なリソース制限を設定してください

## Operator のセキュリティ機能

MongoDB Operator には複数のセキュリティ機能が含まれています:

- **TLS 暗号化**: cert-manager 統合による証明書の自動管理
- **認証**: 安全なユーザーアクセスのための SCRAM-SHA-256 認証
- **内部認証**: クラスター内通信向けの keyfile ベース認証
- **RBAC 統合**: アクセス制御として Kubernetes RBAC を尊重
- **Secret 管理**: Kubernetes Secrets に認証情報を安全に保存
- **Prometheus モニタリング**: セキュリティモニタリングおよびアラート用のメトリクス export
- **セキュアなベースイメージ**: 攻撃対象領域を最小化するため distroless Docker イメージを使用

## 開示ポリシー

弊チームの開示プロセスは以下のガイドラインに従います:

1. **初回応答**: 脆弱性報告については 48 時間以内の受信確認を目指します
2. **評価**: 脆弱性の重大度と影響を評価します
3. **修正対応**: 脆弱性に対する修正を開発・テストします
4. **協調的開示**: 報告者と協力し、開示のタイムラインを決定します
5. **公開リリース**: セキュリティアドバイザリを公開し、修正をリリースします
6. **クレジット表示**: 発見者として報告者にクレジットを与えます(許可がある場合)

## Apache 2.0 セキュリティ免責事項

本プロジェクトは Apache License 2.0 の下でライセンスされています。同ライセンスの第 7 条に従い、本プロジェクトは「現状のまま (AS IS)」提供されており、明示または黙示を問わず、TITLE、NON-INFRINGEMENT、MERCHANTABILITY、FITNESS FOR A PARTICULAR PURPOSE を含むがそれに限定されない、いかなる種類の保証または条件も付されません。

弊チームは高いセキュリティ基準の維持に努めますが、本プロジェクトの使用または再配布の妥当性を判断する責任は完全に利用者にあり、ライセンスに基づく権限の行使に伴うあらゆるリスクも利用者が負うものとします。

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
