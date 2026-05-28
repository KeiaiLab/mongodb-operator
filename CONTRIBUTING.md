<p align="center">
  <b>English</b> |
  <a href="CONTRIBUTING.ko.md">한국어</a> |
  <a href="CONTRIBUTING.ja.md">日本語</a> |
  <a href="CONTRIBUTING.zh.md">中文</a>
</p>

# Contributing to MongoDB Operator

Thank you for your interest in contributing to MongoDB Operator! This document provides guidelines and information for contributors.

## Code of Conduct

This project adheres to a Code of Conduct that all contributors are expected to follow. Please read the [Code of Conduct](CODE_OF_CONDUCT.md) to understand our community standards and expectations.

## How to Contribute

### Reporting Issues

Before creating an issue, please:

1. Search existing issues to avoid duplicates
2. Use the issue templates provided
3. Include as much detail as possible:
   - Kubernetes version
   - Operator version
   - MongoDB version
   - Steps to reproduce
   - Expected vs actual behavior
   - Relevant logs

### Feature Requests

We welcome feature requests! Please:

1. Check the roadmap and existing issues first
2. Describe the use case clearly
3. Explain why this feature would be beneficial

### Pull Requests

#### Sign-off (DCO 의무)

모든 commit 은 [Developer Certificate of Origin (DCO)](https://developercertificate.org/) 정합 — `Signed-off-by: Your Name <you@example.com>` trailer 의무. `git commit -s` 옵션 사용. commit-msg lefthook / pre-commit hook 이 자동 검증.

```bash
git commit -s -m "feat(controller): ..."
```

미서명 commit 은 PR 머지 차단.

#### Getting Started

1. Fork the repository
2. Clone your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/mongodb-operator.git
   cd mongodb-operator
   ```

3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/keiailab/mongodb-operator.git
   ```

4. Create a branch for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

#### Development Setup

1. Install dependencies:
   ```bash
   go mod download
   ```

2. Install development tools:
   ```bash
   make tools
   ```

3. Install pre-commit hooks (recommended):
   ```bash
   curl https://pre-commit.com/install.sh | sh
   pre-commit install
   ```

4. Run tests:
   ```bash
   make test
   ```

5. Run linting:
   ```bash
   make lint
   ```

### Pre-commit Hooks

We use [lefthook](https://github.com/evilmartians/lefthook) (Go 단일 바이너리) to automatically check code quality before each commit / push. 설정은 `.lefthook.yml` 에 있음.

#### Hook 목록

**pre-commit** (commit 단계):
- **gofmt**: `*.go` 자동 포맷팅
- **govet**: `go vet ./...`
- **golangci-lint**: 신규 issue 만 차단 (`--new-from-rev=HEAD~1`)
- **helm-lint**: `charts/**/*.yaml` 변경 시 `helm lint`

**pre-push** (push 단계):
- **unit-test**: `go test -count=1 -timeout=120s ./...`
- **full-lint**: 전체 golangci-lint
- **helm-lint** + **helm-template**: chart sanity
- **govulncheck**: Go module CVE (call-graph 기반)
- **gitleaks**: 시크릿 누출 차단
- **platforms-amd64-guard**: RFC-0002 §2 multi-arch 재발 방지
- **go-mod-tidy**: go.mod/go.sum drift 차단

**commit-msg**:
- **conventional**: Conventional Commits 패턴 강제 (`standards/commits.md §1`)
- **dco-signoff**: DCO `Signed-off-by:` trailer (DCO_STRICT=1 시 enforce, 기본 warn)

#### Installation

```bash
# lefthook 설치
brew install lefthook   # 또는 go install github.com/evilmartians/lefthook@latest

# git hook 활성화
lefthook install        # .git/hooks/{pre-commit,pre-push,commit-msg} 생성
```

#### Usage

Hooks run automatically before each commit / push. 수동 실행:

```bash
# pre-commit hook 을 모든 파일에 실행
lefthook run pre-commit --all-files

# pre-push hook 을 직접 실행
lefthook run pre-push

# 자동화 루프 한정 우회 (사고 방지: 정상 commit 에 사용 금지)
LEFTHOOK=0 git commit -m "..."   # 또는 commit msg 트레일러 [skip-hooks]
```

If a hook fails:
1. Review the error messages
2. Fix the issues manually
3. Stage the fixes with `git add .`
4. Run `git commit` again

#### Local Development Workflow

```bash
# Stage and commit with auto-hooks
git add .
git commit -m "feat: add new feature"

# Pre-commit runs automatically:
# 1. go fmt - formats Go code
# 2. go vet - checks for issues
# 3. golangci-lint - comprehensive linting
# 4. go test - runs unit tests
```

If any hook fails, the commit is blocked. Fix the issues and try again.

#### Making Changes

1. Write clear, concise commit messages
2. Include tests for new functionality
3. Update documentation as needed
4. Ensure all tests pass
5. Follow the existing code style

#### Commit Message Format

We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, etc.)
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `chore`: Maintenance tasks

Examples:
```
feat(controller): add support for arbiter nodes
fix(backup): handle S3 connection timeout
docs(readme): update installation instructions
```

#### Submitting a Pull Request

1. Push your changes to your fork:
   ```bash
   git push origin feature/your-feature-name
   ```

2. Create a Pull Request from your fork to the main repository

3. Fill out the PR template completely

4. Wait for review and address any feedback

## Development Guidelines

### Code Style

- Follow standard Go conventions
- Use `gofmt` and `golint`
- Write descriptive variable and function names
- Add comments for complex logic

### Testing

- Write unit tests for new functionality
- Maintain or improve code coverage
- Test edge cases and error conditions

### Documentation

- Update README.md for user-facing changes
- Add godoc comments for exported functions
- Update Helm chart documentation when applicable

## Project Structure

```
mongodb-operator/
├── api/v1alpha1/          # CRD type definitions
├── cmd/                   # Main entry point
├── config/                # Kubernetes manifests
│   ├── crd/              # CRD definitions
│   ├── rbac/             # RBAC resources
│   ├── manager/          # Operator deployment
│   └── samples/          # Example CRs
├── charts/               # Helm chart
├── internal/
│   ├── controller/       # Reconciler logic
│   └── resources/        # Resource builders
└── docs/                 # Additional documentation
```

## Release Process

Releases are managed by maintainers. The process includes:

1. Update version numbers
2. Update CHANGELOG
3. Create a git tag
4. Build and push Docker images
5. Package and publish Helm chart

## Getting Help

- Open a GitHub issue for bugs or questions
- Join discussions for general topics
- Reach out to maintainers if needed

## License

By contributing to this project, you agree that your contributions will be licensed under the Apache License 2.0.

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
