<p align="center">
  <a href="(../contributing.md)">English</a> |
  <a href="CONTRIBUTING.ko.md">한국어</a> |
  <a href="CONTRIBUTING.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 为 MongoDB Operator 做出贡献

感谢您有兴趣为 MongoDB Operator 做出贡献!本文档为贡献者提供指南和相关信息。

## 行为准则

本项目采用所有贡献者均须遵守的行为准则。请阅读 [Code of Conduct](../code-of-conduct.md),以了解我们的社区标准和期望。

## 如何贡献

### 报告问题

在创建 issue 之前,请:

1. 搜索现有 issue 以避免重复
2. 使用提供的 issue 模板
3. 尽可能提供详细信息:
   - Kubernetes 版本
   - Operator 版本
   - MongoDB 版本
   - 复现步骤
   - 期望行为与实际行为对比
   - 相关日志

### 功能请求

我们欢迎功能请求!请:

1. 先检查 roadmap 和现有 issue
2. 清晰描述使用场景
3. 说明该功能为何有益

### Pull Request

#### Sign-off (DCO 强制)

所有 commit 都必须符合 [Developer Certificate of Origin (DCO)](https://developercertificate.org/) — `Signed-off-by: Your Name <you@example.com>` trailer 为必填项。请使用 `git commit -s` 选项。commit-msg lefthook / pre-commit hook 会自动校验。

```bash
git commit -s -m "feat(controller): ..."
```

未签名的 commit 会被 PR 合并所阻止。

#### 入门

1. Fork 本仓库
2. 克隆您的 fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/mongodb-operator.git
   cd mongodb-operator
   ```

3. 添加 upstream 远程:
   ```bash
   git remote add upstream https://github.com/keiailab/mongodb-operator.git
   ```

4. 为您的变更创建一个分支:
   ```bash
   git checkout -b feature/your-feature-name
   ```

#### 开发环境搭建

1. 安装依赖:
   ```bash
   go mod download
   ```

2. 安装开发工具:
   ```bash
   make tools
   ```

3. 安装 pre-commit hook (推荐):
   ```bash
   curl https://pre-commit.com/install.sh | sh
   pre-commit install
   ```

4. 运行测试:
   ```bash
   make test
   ```

5. 运行 lint:
   ```bash
   make lint
   ```

### Pre-commit Hook

我们使用 [lefthook](https://github.com/evilmartians/lefthook) (Go 单一二进制文件) 在每次 commit / push 前自动检查代码质量。配置位于 `.lefthook.yml`。

#### Hook 列表

**pre-commit** (commit 阶段):
- **gofmt**: 自动格式化 `*.go`
- **govet**: `go vet ./...`
- **golangci-lint**: 仅阻断新增 issue (`--new-from-rev=HEAD~1`)
- **helm-lint**: 当 `charts/**/*.yaml` 发生变更时执行 `helm lint`

**pre-push** (push 阶段):
- **unit-test**: `go test -count=1 -timeout=120s ./...`
- **full-lint**: 完整 golangci-lint
- **helm-lint** + **helm-template**: chart 完整性检查
- **govulncheck**: Go module CVE (基于 call-graph)
- **gitleaks**: 阻断密钥泄露
- **platforms-amd64-guard**: 防止 RFC-0002 §2 multi-arch 再次出现
- **go-mod-tidy**: 阻断 go.mod / go.sum drift

**commit-msg**:
- **conventional**: 强制 Conventional Commits 模式 (`standards/commits.md §1`)
- **dco-signoff**: DCO `Signed-off-by:` trailer (DCO_STRICT=1 时 enforce,默认为 warn)

#### 安装

```bash
# 安装 lefthook
brew install lefthook   # 或者 go install github.com/evilmartians/lefthook@latest

# 启用 git hook
lefthook install        # 生成 .git/hooks/{pre-commit,pre-push,commit-msg}
```

#### 使用方法

Hook 在每次 commit / push 之前自动运行。手动执行:

```bash
# 对所有文件运行 pre-commit hook
lefthook run pre-commit --all-files

# 直接运行 pre-push hook
lefthook run pre-push

# 仅限自动化循环的绕过 (防事故: 常规 commit 禁止使用)
LEFTHOOK=0 git commit -m "..."   # 或者在 commit msg trailer 中加入 [skip-hooks]
```

如果 hook 失败:
1. 查看错误信息
2. 手动修复问题
3. 使用 `git add .` 将修复 stage
4. 再次执行 `git commit`

#### 本地开发流程

```bash
# 启用自动 hook 进行 stage 与 commit
git add .
git commit -m "feat: add new feature"

# pre-commit 会自动运行:
# 1. go fmt - 格式化 Go 代码
# 2. go vet - 检查问题
# 3. golangci-lint - 综合 lint
# 4. go test - 运行单元测试
```

如果任何 hook 失败,commit 将被阻止。请修复问题后重试。

#### 进行变更

1. 撰写清晰、简洁的 commit message
2. 为新功能编写测试
3. 根据需要更新文档
4. 确保所有测试通过
5. 遵循现有代码风格

#### Commit Message 格式

我们遵循 [Conventional Commits](https://www.conventionalcommits.org/) 规范:

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

Types:
- `feat`: 新功能
- `fix`: 缺陷修复
- `docs`: 文档变更
- `style`: 代码风格变更 (格式化等)
- `refactor`: 重构
- `test`: 新增或更新测试
- `chore`: 维护性任务

示例:
```
feat(controller): add support for arbiter nodes
fix(backup): handle S3 connection timeout
docs(readme): update installation instructions
```

#### 提交 Pull Request

1. 将变更推送到您的 fork:
   ```bash
   git push origin feature/your-feature-name
   ```

2. 从您的 fork 向主仓库创建 Pull Request

3. 完整填写 PR 模板

4. 等待 review 并处理任何反馈

## 开发指南

### 代码风格

- 遵循标准的 Go 约定
- 使用 `gofmt` 和 `golint`
- 编写富有描述性的变量名与函数名
- 为复杂逻辑添加注释

### 测试

- 为新功能编写单元测试
- 维持或提升代码覆盖率
- 测试边界情况与错误条件

### 文档

- 涉及用户的变更需更新 README.md
- 为导出函数添加 godoc 注释
- 在适用时更新 Helm chart 的文档

## 项目结构

```
mongodb-operator/
├── api/v1alpha1/          # CRD 类型定义
├── cmd/                   # 主入口
├── config/                # Kubernetes manifests
│   ├── crd/              # CRD 定义
│   ├── rbac/             # RBAC 资源
│   ├── manager/          # Operator 部署
│   └── samples/          # 示例 CR
├── charts/               # Helm chart
├── internal/
│   ├── controller/       # Reconciler 逻辑
│   └── resources/        # 资源构建器
└── docs/                 # 附加文档
```

## 发布流程

发布由维护者管理。流程包括:

1. 更新版本号
2. 更新 CHANGELOG
3. 创建 git tag
4. 构建并推送 Docker 镜像
5. 打包并发布 Helm chart

## 获取帮助

- 对于缺陷或问题,请打开 GitHub issue
- 对于一般性话题,请加入 discussions
- 必要时请联系维护者

## 许可证

通过为本项目做出贡献,即表示您同意您的贡献以 Apache License 2.0 进行授权。

