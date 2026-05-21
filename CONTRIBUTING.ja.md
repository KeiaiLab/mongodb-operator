<p align="center">
  <a href="CONTRIBUTING.md">English</a> |
  <a href="CONTRIBUTING.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="CONTRIBUTING.zh.md">中文</a>
</p>

# MongoDB Operator へのコントリビュート

MongoDB Operator へのコントリビュートに関心をお寄せいただきありがとうございます。本ドキュメントはコントリビューター向けのガイドラインと情報を提供します。

## 行動規範

本プロジェクトはすべてのコントリビューターが遵守すべき行動規範を採用しています。コミュニティの基準と期待を理解するため、[Code of Conduct](CODE_OF_CONDUCT.md) を必ずお読みください。

## コントリビュート方法

### Issue の報告

Issue を作成する前に、以下を行ってください:

1. 重複を避けるため既存の issue を検索する
2. 用意された issue テンプレートを使用する
3. できるだけ詳細な情報を含める:
   - Kubernetes バージョン
   - Operator バージョン
   - MongoDB バージョン
   - 再現手順
   - 期待される動作と実際の動作の比較
   - 関連するログ

### 機能リクエスト

機能リクエストを歓迎します。以下を行ってください:

1. まずロードマップと既存の issue を確認する
2. ユースケースを明確に記述する
3. その機能がなぜ有益かを説明する

### Pull Request

#### Sign-off (DCO 義務)

すべての commit は [Developer Certificate of Origin (DCO)](https://developercertificate.org/) に準拠していなければなりません — `Signed-off-by: Your Name <you@example.com>` trailer が必須です。`git commit -s` オプションを使用してください。commit-msg lefthook / pre-commit hook が自動で検証します。

```bash
git commit -s -m "feat(controller): ..."
```

未署名の commit は PR のマージがブロックされます。

#### はじめに

1. リポジトリを fork する
2. fork をクローンする:
   ```bash
   git clone https://github.com/YOUR_USERNAME/mongodb-operator.git
   cd mongodb-operator
   ```

3. upstream リモートを追加する:
   ```bash
   git remote add upstream https://github.com/eightynine01/mongodb-operator.git
   ```

4. 変更用のブランチを作成する:
   ```bash
   git checkout -b feature/your-feature-name
   ```

#### 開発環境のセットアップ

1. 依存関係をインストールする:
   ```bash
   go mod download
   ```

2. 開発ツールをインストールする:
   ```bash
   make tools
   ```

3. pre-commit hook をインストールする (推奨):
   ```bash
   curl https://pre-commit.com/install.sh | sh
   pre-commit install
   ```

4. テストを実行する:
   ```bash
   make test
   ```

5. lint を実行する:
   ```bash
   make lint
   ```

### Pre-commit Hook

各 commit / push の前にコード品質を自動チェックするため [lefthook](https://github.com/evilmartians/lefthook) (Go 単一バイナリ) を利用しています。設定は `.lefthook.yml` にあります。

#### Hook 一覧

**pre-commit** (commit 段階):
- **gofmt**: `*.go` の自動フォーマット
- **govet**: `go vet ./...`
- **golangci-lint**: 新規 issue のみブロック (`--new-from-rev=HEAD~1`)
- **helm-lint**: `charts/**/*.yaml` 変更時に `helm lint`

**pre-push** (push 段階):
- **unit-test**: `go test -count=1 -timeout=120s ./...`
- **full-lint**: 全体 golangci-lint
- **helm-lint** + **helm-template**: chart の sanity 検査
- **govulncheck**: Go module の CVE (call-graph ベース)
- **gitleaks**: シークレット漏洩のブロック
- **platforms-amd64-guard**: RFC-0002 §2 multi-arch 再発防止
- **go-mod-tidy**: go.mod / go.sum の drift をブロック

**commit-msg**:
- **conventional**: Conventional Commits パターンを強制 (`standards/commits.md §1`)
- **dco-signoff**: DCO `Signed-off-by:` trailer (DCO_STRICT=1 で enforce、デフォルトは warn)

#### インストール

```bash
# lefthook をインストール
brew install lefthook   # または go install github.com/evilmartians/lefthook@latest

# git hook を有効化
lefthook install        # .git/hooks/{pre-commit,pre-push,commit-msg} を生成
```

#### 使い方

Hook は各 commit / push 前に自動実行されます。手動実行は以下のとおりです:

```bash
# pre-commit hook をすべてのファイルに実行
lefthook run pre-commit --all-files

# pre-push hook を直接実行
lefthook run pre-push

# 自動化ループ限定の迂回 (事故防止: 通常 commit には使用禁止)
LEFTHOOK=0 git commit -m "..."   # または commit msg trailer に [skip-hooks]
```

Hook が失敗した場合:
1. エラーメッセージを確認する
2. 手動で問題を修正する
3. `git add .` で修正を stage する
4. もう一度 `git commit` を実行する

#### ローカル開発ワークフロー

```bash
# 自動 hook 付きで stage して commit
git add .
git commit -m "feat: add new feature"

# pre-commit が自動的に実行されます:
# 1. go fmt - Go コードをフォーマット
# 2. go vet - 問題を検査
# 3. golangci-lint - 総合 lint
# 4. go test - 単体テスト実行
```

いずれかの hook が失敗すると commit はブロックされます。問題を修正してから再度実行してください。

#### 変更を加える

1. 明快で簡潔な commit メッセージを書く
2. 新規機能にはテストを含める
3. 必要に応じてドキュメントを更新する
4. すべてのテストを通過させる
5. 既存のコードスタイルに従う

#### Commit メッセージ形式

[Conventional Commits](https://www.conventionalcommits.org/) 仕様に従います:

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

Types:
- `feat`: 新機能
- `fix`: バグ修正
- `docs`: ドキュメントの変更
- `style`: コードスタイルの変更 (フォーマットなど)
- `refactor`: リファクタリング
- `test`: テストの追加または更新
- `chore`: メンテナンス作業

例:
```
feat(controller): add support for arbiter nodes
fix(backup): handle S3 connection timeout
docs(readme): update installation instructions
```

#### Pull Request の提出

1. 変更を fork にプッシュする:
   ```bash
   git push origin feature/your-feature-name
   ```

2. fork からメインリポジトリへ Pull Request を作成する

3. PR テンプレートを最後まで記入する

4. レビューを待ち、フィードバックに対応する

## 開発ガイドライン

### コードスタイル

- 標準的な Go の慣習に従う
- `gofmt` と `golint` を使用する
- わかりやすい変数名・関数名を書く
- 複雑なロジックにはコメントを追加する

### テスト

- 新規機能には単体テストを書く
- コードカバレッジを維持または向上させる
- 境界ケースとエラー条件をテストする

### ドキュメント

- ユーザー向けの変更があれば README.md を更新する
- エクスポートされた関数には godoc コメントを追加する
- 該当する場合は Helm chart のドキュメントを更新する

## プロジェクト構成

```
mongodb-operator/
├── api/v1alpha1/          # CRD 型定義
├── cmd/                   # メインエントリーポイント
├── config/                # Kubernetes manifests
│   ├── crd/              # CRD 定義
│   ├── rbac/             # RBAC リソース
│   ├── manager/          # Operator デプロイ
│   └── samples/          # サンプル CR
├── charts/               # Helm chart
├── internal/
│   ├── controller/       # Reconciler ロジック
│   └── resources/        # リソースビルダー
└── docs/                 # 追加ドキュメント
```

## リリースプロセス

リリースはメンテナーが管理します。プロセスは以下を含みます:

1. バージョン番号を更新する
2. CHANGELOG を更新する
3. git タグを作成する
4. Docker イメージをビルドしてプッシュする
5. Helm chart をパッケージして公開する

## ヘルプ

- バグや質問は GitHub issue を開いてください
- 一般的なトピックは discussions に参加してください
- 必要に応じてメンテナーに連絡してください

## ライセンス

本プロジェクトにコントリビュートすることで、あなたのコントリビュートが Apache License 2.0 のもとでライセンスされることに同意するものとします。

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://keiailab.com">keiailab.com</a>
</p>
