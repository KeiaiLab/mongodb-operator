# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **BREAKING (security)**: mongosh `kubectl exec` 의존을 mongo-go-driver v2 네트워크
  client로 완전 대체. `pods/exec` ClusterRole 권한이 RBAC에서 삭제됨.
  - operator Pod이 침해되어도 cluster의 임의 Pod에서 임의 명령 실행 불가능
  - 첫 admin user 생성은 mongodb pod / mongos pod의 `lifecycle.postStart`
    bootstrap 스크립트가 담당 (localhost exception을 operator가 아닌 pod
    자체에서 사용)
  - 기존 cluster 마이그레이션: admin user가 이미 있으면 자동 호환. 없거나
    password가 다르면 reconcile 영구 실패 → 운영 매뉴얼 별도 필요.
- 부수 효과: connection pooling, context 기반 timeout, BSON 타입 안전성으로
  JS 인젝션 가능성 0건.

### Removed
- `pods/exec` ClusterRole 규칙 (config/rbac/role.yaml,
  charts/mongodb-operator/templates/clusterrole.yaml)
- `internal/mongodb/{executor,escape}.go` 및 관련 테스트
- `docs/security/rbac-known-limitations.md` (한계 해소)

### Verification (사용자 권장)
fresh install 시:
```bash
kind create cluster --name mongo-op-e2e
make docker-build IMG=ghcr.io/eightynine01/mongodb-operator:test-v2
kind load docker-image ghcr.io/eightynine01/mongodb-operator:test-v2
make deploy IMG=ghcr.io/eightynine01/mongodb-operator:test-v2
kubectl apply -f examples/minimal/
# 검증:
# - replica set 3 멤버 init 성공
# - operator pod의 audit log에 pods/exec 호출 0건
# - sharded 예제에서 addShard 성공
```

후속 phase 권장:
- envtest binaries 자동 다운로드 + in-process mongod(memongo)로 driver
  호출 단위 검증
- kind 골든 패스를 GitHub Actions에 자동화

### Planned
- Point-in-Time Recovery (PITR)
- Automated version upgrades
- Cross-cluster replication
- Grafana dashboard templates
- Shard scale-in support

## [1.0.1] - 2026-01-22

### Changed
- **BREAKING**: Migrated container registry from Docker Hub to GitHub Container Registry (GHCR)
  - New image path: `ghcr.io/eightynine01/mongodb-operator` (previously `eightynine01/mongodb-operator`)
  - No Docker Hub Secrets required - uses GITHUB_TOKEN automatically
  - Public images with no rate limits for authenticated users
  - Better integration with GitHub Security scanning

### Migration Guide
Existing users need to update image references:
```yaml
# Old (Docker Hub)
image: eightynine01/mongodb-operator:1.0.0

# New (GHCR)
image: ghcr.io/eightynine01/mongodb-operator:1.0.1
```

For Helm users, the repository is automatically updated. Just upgrade:
```bash
helm repo update
helm upgrade mongodb-operator mongodb-operator/mongodb-operator --version 1.0.1
```

### Added
- Documentation: GHCR setup and migration guide (`docs/releases/ghcr-setup.md`)
- E2E testing framework with comprehensive test scripts (`test/e2e/`)
- Extended roadmap with MongoDB Enterprise feature comparison (`ROADMAP.md`)

### Fixed
- GitHub Actions Docker build failures due to Docker Hub authentication
- CI/CD pipeline now fully automated without external secrets

## [1.0.0] - 2026-01-21

### Summary
Initial stable (GA) release with comprehensive CI/CD, documentation, and examples. This release marks production-readiness of the MongoDB Operator with full enterprise-grade infrastructure for open source maintenance.

### Breaking Changes
None. This is a major release representing stabilization of the project with all features from previous pre-releases. No deprecations or breaking changes introduced.

### New Features

#### GitHub Repository Templates
- Issue template with bug report, feature request, and documentation categories
- Pull request template with checklist and contribution guidelines

#### GitHub Actions CI/CD (5 Workflows)
- `ci.yml`: Continuous integration with Go tests, linting, and Docker build verification
- `docker-build.yml`: Automated Docker image building and pushing to Docker Hub
- `release.yml`: Complete release automation for GitHub releases
- `helm-publish.yml`: Helm chart packaging and publishing to gh-pages branch
- `security.yml`: Comprehensive security scanning (dependencies, containers, licenses)

#### Comprehensive Documentation (9 Documents)
- `docs/ci-cd/overview.md`: CI/CD pipeline architecture and workflow descriptions
- `docs/ci-cd/workflows.md`: Detailed workflow configuration and troubleshooting
- `docs/ci-cd/release-process.md`: Automated release process documentation
- `docs/ci-cd/testing-strategy.md`: Test coverage strategy and guidelines
- `docs/ci-cd/quality-assurance.md`: Code quality standards and tooling
- `docs/ci-cd/artifact-hub-integration.md`: Artifact Hub package registry setup
- `docs/repository/github-settings.md`: GitHub repository configuration guide
- `docs/repository/issue-management.md`: Issue tracking and triage guidelines
- `docs/repository/pull-request-process.md`: Pull request workflow and review process

#### Comprehensive Examples (7 Examples)
- `examples/basic/mongodb-replicaset.yaml`: Simple 3-member ReplicaSet deployment
- `examples/basic/mongodb-sharded.yaml`: Basic sharded cluster with 2 shards
- `examples/production/mongodb-replicaset-resources.yaml`: ReplicaSet with production resource limits
- `examples/production/mongodb-sharded-resources.yaml`: Sharded cluster with production resource limits
- `examples/monitoring/mongodb-prometheus.yaml`: ReplicaSet with Prometheus monitoring enabled
- `examples/monitoring/mongodb-sharded-prometheus.yaml`: Sharded cluster with Prometheus monitoring
- `examples/backup/mongodb-backup-s3.yaml`: Backup configuration with S3 storage

#### Artifact Hub Integration
- Publisher configuration with repository ID (386b6255-6da7-4a73-8fc0-a8e79e3c7b90)
- Artifact Hub annotations in Helm chart
- Automatic metadata synchronization on releases

#### Dependency Automation
- Dependabot configuration for Go modules
- Automatic dependency update PRs
- Security vulnerability monitoring

#### Pre-commit Hooks
- Go code formatting with `gofmt`
- Linting with `golangci-lint`
- Shell script validation with `shellcheck`
- Markdown linting with `markdownlint`
- YAML formatting with `yamlfmt`
- JSON validation with `jsonlint`
- Trailing whitespace detection

#### Code Coverage with Codecov
- Automatic coverage upload on PRs
- Coverage threshold enforcement
- Badge integration in README

#### Helm Repository Publishing
- Automated chart packaging
- gh-pages branch management
- Helm repository index generation

#### Security Scanning
- Trivy vulnerability scanning for container images
- Dependabot for dependency security
- License compliance checking
- SBOM and Provenance attestations

#### Test Suite Strategy
- Unit test coverage requirements
- Integration test guidelines
- E2E test examples
- Coverage thresholds and goals

#### GitHub Repository Settings
- Branch protection rules (main branch)
- Security policies and alerts
- Team and collaborator access guidelines
- Issue and PR template setup

### Changed
- Marked all features as production-ready and stable
- Moved from pre-release (0.0.x) to stable (1.0.0) versioning
- Added comprehensive release documentation and maintainers guide

### Security
- All container images use immutable SHA256 digests
- SBOM and Provenance attestations enabled
- Regular security scanning automated
- CVE tracking and dependency updates automated

## [0.0.7] - 2026-01-05

### Security
- Upgraded Go version to 1.25.0 to address multiple CVEs (CVE-2025-22871, CVE-2025-61723, etc.)
- Upgraded `golang.org/x/oauth2` to v0.34.0 to fix CVE-2025-22868
- Updated base images to use immutable SHA256 digests for `golang:1.25` and `distroless/static:nonroot`
- Enabled SBOM and Provenance attestations in container image builds
- Updated all dependencies to latest secure versions

### Fixed
- Added `pods/exec` permission to ClusterRole to fix replica set initialization failures

## [0.0.6] - 2026-01-05

### Changed
- Updated image repository to `eightynine01/mongodb-operator`

## [0.0.5] - 2025-12-31

### Fixed
- Backup authentication: include credentials from auth secret in connection string
- Backup all databases: removed `/admin` path from URI to enable full cluster backup
  - Previously only backed up admin database
  - Now correctly backs up all databases using `?authSource=admin` only

## [0.0.4] - 2025-12-31

### Changed
- Helm chart version bump for Artifact Hub update
- Documentation updates for scaling and resource recommendations

## [0.0.3] - 2024-12-31

### Added
- Automatic ReplicaSet initialization with `rs.initiate()`
- Automatic Sharded Cluster initialization
  - Config server ReplicaSet initialization
  - Shard ReplicaSet initialization
  - Automatic `sh.addShard()` execution
- Admin user auto-creation via MongoDB localhost exception
- Horizontal shard scaling (scale out) support
- Resource recommendations documentation
- Tested features and limitations documentation
- Mongos replica scaling examples

### Fixed
- Preserve shard status arrays during scale out (prevent re-initialization)
- Port configuration: configsvr (27019), shardsvr (27018), mongos (27017)
- Keyfile Secret regeneration causing authentication failures
- Mongos readiness probe timeout (increased to 5s)
- Container-aware command execution for mongos pods

### Changed
- Marked as stable release (prerelease: false)
- Minimum mongos memory recommendation: 512Mi

## [0.0.2] - 2024-12-24

### Added
- Verified Publisher configuration for ArtifactHub

### Changed
- Updated repository metadata with repository ID (386b6255-6da7-4a73-8fc0-a8e79e3c7b90)

## [0.0.1] - 2024-12-23

### Added
- Initial pre-release for testing
- MongoDB ReplicaSet CRD and controller
  - Support for 3+ member replica sets
  - Automatic keyfile generation for internal authentication
  - SCRAM-SHA-256 authentication support
  - Arbiter node support
- MongoDB Sharded Cluster CRD and controller
  - Config server replica set management
  - Multiple shard support with configurable members per shard
  - Mongos router deployment with auto-scaling (HPA)
- MongoDBBackup CRD and controller
  - S3-compatible storage support
  - PVC-based backup storage
  - Full and incremental backup types
  - Compression support (gzip, zstd, snappy)
- TLS encryption support
  - cert-manager integration for automatic certificate management
  - Self-signed certificate option
- Prometheus monitoring integration
  - MongoDB exporter sidecar
  - ServiceMonitor resource creation
  - PrometheusRule for alerts
- Helm chart for easy deployment
  - Configurable values for all operator settings
  - CRD installation via Helm
  - RBAC resources included

### Security
- Non-root container execution
- Read-only root filesystem
- Dropped capabilities
- SeccompProfile enforcement

---

[Unreleased]: https://github.com/eightynine01/mongodb-operator/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.7...v1.0.0
[0.0.7]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.6...v0.0.7
[0.0.6]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/eightynine01/mongodb-operator/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/eightynine01/mongodb-operator/releases/tag/v0.0.1
