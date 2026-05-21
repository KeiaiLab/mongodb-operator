# GitHub Repository Settings Configuration Guide

This document provides a comprehensive guide for configuring the MongoDB Operator GitHub repository settings for professional open source project management.

## Table of Contents

1. [General Settings](#1-general-settings)
2. [Branch Protection](#2-branch-protection-main-branch)
3. [Issue Templates](#3-issue-templates-githubissue_template)
4. [Pull Request Template](#4-pull-request-template-githubpull_request_templatemd)
5. [Security Settings](#5-security-settings)
6. [Required Checks](#6-required-checks-workflows)
7. [Actions & Permissions](#7-actions--permissions)
8. [Settings Checklist](#8-settings-checklist)

---

## 1. General Settings

### Repository Visibility

- **Setting**: Public
- **Reason**: Open source project requires public visibility for community contributions

### Repository Topics

Add the following topics to improve discoverability:
- `database`
- `mongodb`
- `kubernetes`
- `kubernetes-operator`
- `operator`
- `k8s`
- `mongodb-operator`

### Default Branch

- **Branch**: `main`
- **Reason**: Modern convention, gender-neutral, follows GitHub best practices

### Repository Description

Update the repository description to:
```
A Kubernetes Operator for deploying and managing MongoDB ReplicaSets and Sharded Clusters
```

### Social Preview Settings

- **Description**: MongoDB Operator for Kubernetes - Deploy and manage MongoDB clusters with ease
- **Website**: `https://keiailab.github.io/mongodb-operator/` (when docs are published)
- **Topics**: As listed above

---

## 2. Branch Protection (main branch)

Navigate to: **Settings → Branches → Add branch protection rule**

### Rule Settings

```
Branch name pattern: main
```

#### Required Checks

**Status Checks**
- ✅ **Require status checks to pass before merging**
- ✅ **Require branches to be up to date before merging**

**Required Checks** (select all):
- `Lint` (from CI workflow)
- `Unit Tests` (from CI workflow)
- `Integration Tests` (from CI workflow)
- `Build` (from CI workflow)
- `Dependency Vulnerability Scan` (from Security Scanning workflow)
- `Container Vulnerability Scan` (from Security Scanning workflow)
- `License Compliance Check` (from Security Scanning workflow)
- `Helm Chart Lint` (from Helm Chart Lint & Test workflow)
- `Helm Chart Test` (from Helm Chart Lint & Test workflow)
- `Docker Build` (from Docker Build & Push workflow - on PRs)

**Additional Settings**:
- Require only these checks to pass (not all)
- Require admins to use status checks

#### Protection Rules

✅ **Require pull request reviews before merging**
- Dismiss stale PR approvals when new commits are pushed
- Require review from Code Owners (when CODEOWNERS is configured)
- Require approval from: 1 reviewer
- Dismiss approving reviews when commits are pushed (recommended)

✅ **Require branches to be up to date before merging**

❌ **Do not enable** (or configure):
- Require conversation resolution before merging
- Require signed commits
- Require linear history (optional, recommended for cleaner history)
- Block force pushes (recommended)
- Allow force pushes (if disabled above)
- Restrict who can push to matching branches (optional)
- Restrict who can dismiss stale PR approvals (optional)

#### Stale PR Handling

Configure automated stale PR closing via a separate workflow or manually:
- Auto-close stale PRs after 30 days of inactivity
- Add `stale` label to inactive PRs after 14 days
- Comment before closing with template

---

## 3. Issue Templates (.github/ISSUE_TEMPLATE/)

### Templates Location

The following templates are already configured in `.github/ISSUE_TEMPLATE/`:

1. **Bug Report** - `bug_report.yml`
2. **Feature Request** - `feature_request.yml`
3. **Question** - `question.yml`

### Template Details

#### Bug Report (`bug_report.yml`)

**Purpose**: Report software bugs and issues

**Required Fields**:
- Prerequisites (search existing issues, reproduce in clean environment, not a security vulnerability)
- Kubernetes Version (1.26, 1.27, 1.28, 1.29, 1.30, Other)
- MongoDB Version (8.2, 8.0, 7.0, 6.0, Other)
- MongoDB Operator Version (v0.0.5, v0.0.3, Other)
- Installation Method (Helm, Manual, OLM, Other)
- Description of the Bug
- Steps to Reproduce
- Expected Behavior
- Actual Behavior

**Optional Fields**:
- Cluster Type
- Logs, Metrics, or Screenshots
- Relevant YAML Configuration
- Additional Context

**Labels Applied**: `bug`, `triage`

---

#### Feature Request (`feature_request.yml`)

**Purpose**: Propose new features or enhancements

**Required Fields**:
- Prerequisites (searched existing issues, not in roadmap)
- Feature Description
- Use Case / Motivation
- Proposed Solution
- Alternative Solutions
- Willingness to Contribute (Yes, implement/test/feedback/use only)
- Priority (Critical, High, Medium, Low)

**Optional Fields**:
- Additional Context

**Labels Applied**: `enhancement`, `triage`

---

#### Question (`question.yml`)

**Purpose**: Ask questions about MongoDB Operator usage

**Required Fields**:
- Prerequisites (searched existing issues, read README, read Contributing Guide)
- Question Context
- What Have You Tried?
- Your Question

**Optional Fields**:
- Kubernetes Version
- MongoDB Version
- MongoDB Operator Version
- Configuration Details
- Error Messages or Unexpected Behavior
- Relevant Logs
- Additional Context

**Labels Applied**: `question`

---

### Issue Labels Configuration

Ensure the following labels exist in the repository:

**Triage Labels**:
- `bug` - Bug reports
- `enhancement` - Feature requests
- `question` - Questions
- `documentation` - Documentation issues
- `duplicate` - Duplicate issues
- `wontfix` - Won't be fixed
- `wont implement` - Won't be implemented
- `help wanted` - Good for contributors
- `good first issue` - Beginner-friendly

**Status Labels**:
- `needs-triage` - Needs review
- `needs-info` - Needs more information
- `in-progress` - Being worked on
- `blocked` - Blocked by something
- `review-required` - Needs review
- `ready-to-merge` - Ready for merge

**Priority Labels**:
- `critical` - Critical priority
- `high` - High priority
- `medium` - Medium priority
- `low` - Low priority

**Component Labels**:
- `replicaset` - ReplicaSet-related
- `sharded-cluster` - Sharded cluster-related
- `backup` - Backup-related
- `monitoring` - Monitoring-related
- `tls` - TLS-related
- `helm` - Helm chart-related

---

## 4. Pull Request Template (.github/PULL_REQUEST_TEMPLATE.md)

### Template Location

The PR template is already configured at `.github/PULL_REQUEST_TEMPLATE.md`

### Template Structure

#### Required Sections:

1. **PR Title Convention**
   - Follow Conventional Commits: `type(scope): description`
   - Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

2. **Description**
   - Why is this change needed?
   - What does this change do?
   - How was it tested?

3. **Type of Change** (checkboxes)
   - Bug fix (non-breaking)
   - New feature (non-breaking)
   - Breaking change
   - Documentation update
   - Code refactoring
   - Performance improvement
   - Tests

4. **Related Issues**
   - Link to related issues with `Closes #issue-number`

5. **Checklist** (checkboxes)
   - Tests added/updated
   - Documentation updated
   - All tests pass locally: `make test`
   - Code follows Go style: `make lint`
   - Commit messages follow Conventional Commits
   - Self-review completed
   - PR title follows convention

6. **Additional Notes**
   - Context, screenshots, links

---

### PR Labels Configuration

Ensure the following labels exist for PRs:

**Type Labels**:
- `bug` - Bug fix
- `feature` - New feature
- `refactor` - Refactoring
- `docs` - Documentation
- `test` - Tests
- `chore` - Maintenance

**Status Labels**:
- `needs-review` - Needs review
- `approved` - Approved for merge
- `changes-requested` - Changes requested
- `work-in-progress` - Work in progress
- `ready-to-merge` - Ready for merge

**Size Labels**:
- `size/XS` - < 20 lines
- `size/S` - 20-99 lines
- `size/M` - 100-499 lines
- `size/L` - 500-999 lines
- `size/XL` - 1000+ lines

**Breaking Change**:
- `breaking-change` - Breaking changes

---

## 5. Security Settings

Navigate to: **Settings → Security**

### Dependabot Security Updates

**Enable**: ✅ Yes

**Configuration** (`.github/dependabot.yml` if not present):
```yaml
version: 2
updates:
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 10

  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 10
```

---

### Code Scanning (GitHub Advanced Security)

**Enable**: ✅ Yes (if GitHub Advanced Security is available)

**Configuration**: Already configured in `.github/workflows/security-scan.yml`

**Actions**:
- Dependency vulnerability scanning with `govulncheck`
- Container vulnerability scanning with `Trivy`
- License compliance checking with `go-licenses`
- SBOM generation (CycloneDX and SPDX)

**Severity Levels**:
- CRITICAL: Fail workflow
- HIGH: Fail workflow
- MEDIUM: Warning only
- LOW: Warning only

---

### Secret Scanning

**Enable**: ✅ Yes

**Secrets to Scan For**:
- AWS credentials
- GitHub tokens
- Google Cloud credentials
- Docker Hub credentials
- Generic API keys
- Private keys

**Custom Patterns** (add if needed):
- MongoDB connection strings
- API tokens

---

### Two-Factor Authentication

**Recommended**: ✅ Enable for maintainers

**Configuration**:
- Navigate to **Settings → Security → Two-factor authentication**
- Enable 2FA for all organization members
- Enforce 2FA for maintainers and admins

---

### Security Advisories

**Visibility**: Private by default

**Settings**:
- Private vulnerability reporting
- Security advisories only visible to maintainers
- Allow CVE assignment for published vulnerabilities

---

## 6. Required Checks (workflows)

### Workflow Files

The following workflows must pass before merging to `main`:

#### CI Workflow (`.github/workflows/ci.yml`)

**Required Checks**:
1. **Lint** ✅
   - `go fmt ./...`
   - `go vet ./...`
   - Quick sanity test

2. **Unit Tests** ✅
   - `make test-unit`
   - Coverage threshold: 60% minimum
   - Upload to Codecov

3. **Integration Tests** ✅
   - `make test-integration`
   - Tests on Go 1.21, 1.22, 1.25
   - Coverage threshold: 60% minimum

4. **Build** ✅
   - `make build`
   - Verify binary exists
   - Verify `bin/manager` executable

**Trigger**: Push to `main/**`, Pull Request to `main/**`

---

#### Security Scanning Workflow (`.github/workflows/security-scan.yml`)

**Required Checks**:
1. **Dependency Vulnerability Scan** ✅
   - `govulncheck` on all Go code
   - Fail on CRITICAL and HIGH vulnerabilities
   - Generate SBOM (CycloneDX and SPDX)

2. **Container Vulnerability Scan** ✅
   - `Trivy` scan on Docker image
   - Fail on CRITICAL vulnerabilities
   - Report HIGH and MEDIUM vulnerabilities
   - Upload SARIF to GitHub Security tab

3. **License Compliance Check** ✅
   - Check for Apache 2.0 compatible licenses
   - Warn on GPL/AGPL/LGPL licenses
   - Generate license report

4. **Security Summary** ✅
   - Aggregate all security check results
   - Fail if any security job fails

**Trigger**: Push to `main/**`, Pull Request to `main/**`, Manual dispatch

**Coverage Threshold**: 60% minimum (enforced in unit and integration tests)

---

#### Docker Build Workflow (`.github/workflows/docker-build.yml`)

**Required Checks**:
1. **Build Docker Image** ✅
   - Single-platform builds (linux/amd64, RFC-0002 §2)
   - Buildx with QEMU
   - Cache from/to GitHub Actions cache

2. **Test Docker Image** ✅ (on push to main and tags)
   - Verify manager binary exists
   - Verify binary is executable
   - Verify user is non-root (UID 65532)
   - Smoke test binary version check

3. **Push Multi-Arch Manifest** ✅ (on push to main and tags)
   - Create amd64 manifest (RFC-0002 §2)
   - Push to Docker Hub
   - Verify manifest

**Trigger**: Push to `main`, Tags `v*`, Pull Request to `main`, Manual dispatch

---

#### Helm Lint & Test Workflow (`.github/workflows/helm-lint-test.yml`)

**Required Checks**:
1. **Lint Helm Chart** ✅
   - `helm lint charts/mongodb-operator/`
   - Check for required fields in Chart.yaml
   - Validate values.yaml syntax

2. **Test Helm Chart Templates** ✅
   - Template with default values
   - Template with minimal values
   - Template with production values
   - Validate rendered Kubernetes manifests
   - Check for deprecated APIs

3. **Package Helm Chart** ✅
   - `helm package charts/mongodb-operator/`
   - Verify package created
   - Verify package integrity
   - Extract and verify package contents

**Trigger**: Push to `main`, Pull Request to `main`

---

#### Release Workflow (`.github/workflows/release.yml`)

**Note**: This workflow only runs on releases and is not required for PR merges.

**Trigger**: Push to tags matching `v*`

**Actions**:
- Create GitHub release
- Generate release notes
- Publish Helm chart to GitHub Pages

---

### Coverage Threshold

**Minimum Coverage**: 60%

**Enforcement**:
- Unit tests: Fails if coverage < 60%
- Integration tests: Fails if coverage < 60%

**Reporting**:
- Coverage reports uploaded as artifacts
- Reports uploaded to Codecov
- HTML reports available for review

---

## 7. Actions & Permissions

### Permissions Configuration

Navigate to: **Settings → Actions → General**

#### Workflow Permissions

**Setting**: ✅ Read and write permissions

**Rationale**:
- Release workflow needs write permissions to create releases
- Helm publish workflow needs write permissions to push to gh-pages branch
- Security scan workflow needs write permissions to upload SARIF results
- Status checks need write permissions

**Specific Permissions**:
- **Contents**: Read and write
- **Metadata**: Read
- **Pull requests**: Read and write
- **Security events**: Read and write
- **Actions**: Read
- **Checks**: Read and write
- **Dependabot secrets**: Read and write
- **Packages**: Read

---

#### Fork Pull Request Workflows

**Setting**: ✅ Enable workflow permissions for fork pull requests

**Configuration**:
- Read and write permissions
- Provide access to secrets (for testing workflows)
- Allow custom action creation (optional, not recommended)

**Note**: Be careful with secret access for fork PRs. Restrict to only necessary secrets.

---

### Actions Features

#### Actions Settings

✅ **Enabled**: Allow all actions and reusable workflows

✅ **Allow GitHub Actions to create and approve pull requests**: Disabled (security)

✅ **Allow GitHub Actions to run approved pull requests**: Enabled

❌ **Allow reuse and disable from all actions by public actors**: Disabled (security)

---

#### Workflow Run Retention

**Default**: 90 days

**Recommended**: Keep default for compliance and debugging

---

### Branch Permissions

Navigate to: **Settings → Branches**

#### Branch Creation

**Setting**: Allow all collaborators to create branches

**Rationale**: Encourage community contributions

**Alternative**: Restrict to maintainers (if stricter governance needed)

---

#### Tag Creation

**Setting**: Restrict to maintainers only

**Rationale**:
- Tags trigger releases and publishing
- Prevent accidental tags from non-maintainers
- Control versioning

**Configuration**:
- Only maintainers can create tags
- Require tag format: `v*` (semantic versioning)

---

## 8. Settings Checklist

Use this checklist to verify all settings are configured correctly.

### General Settings

- [ ] Repository visibility set to Public
- [ ] Repository topics added:
  - [ ] `database`
  - [ ] `mongodb`
  - [ ] `kubernetes`
  - [ ] `kubernetes-operator`
  - [ ] `operator`
  - [ ] `k8s`
  - [ ] `mongodb-operator`
- [ ] Default branch set to `main`
- [ ] Repository description updated
- [ ] Social preview settings configured

### Branch Protection

- [ ] Branch protection rule created for `main`
- [ ] Require pull request reviews: Enabled
  - [ ] Dismiss stale PR approvals: Enabled
  - [ ] Require review from Code Owners: Optional
  - [ ] Require approval from: 1 reviewer
- [ ] Require status checks to pass: Enabled
  - [ ] `Lint` check required
  - [ ] `Unit Tests` check required
  - [ ] `Integration Tests` check required
  - [ ] `Build` check required
  - [ ] `Dependency Vulnerability Scan` check required
  - [ ] `Container Vulnerability Scan` check required
  - [ ] `License Compliance Check` check required
  - [ ] `Helm Chart Lint` check required
  - [ ] `Helm Chart Test` check required
  - [ ] `Docker Build` check required
  - [ ] Require branches to be up to date: Enabled
  - [ ] Require only these checks: Enabled
  - [ ] Require admins to use status checks: Enabled
- [ ] Require linear history: Optional (recommended)
- [ ] Block force pushes: Enabled (recommended)
- [ ] Stale PR handling configured (optional)

### Issue Templates

- [ ] Issue templates directory exists: `.github/ISSUE_TEMPLATE/`
- [ ] Bug report template configured: `bug_report.yml`
- [ ] Feature request template configured: `feature_request.yml`
- [ ] Question template configured: `question.yml`
- [ ] Issue labels created:
  - [ ] `bug`, `enhancement`, `question`, `documentation`
  - [ ] `needs-triage`, `needs-info`, `in-progress`, `blocked`, `review-required`, `ready-to-merge`
  - [ ] `critical`, `high`, `medium`, `low`
  - [ ] `replicaset`, `sharded-cluster`, `backup`, `monitoring`, `tls`, `helm`
- [ ] Issue templates referenced in CONTRIBUTING.md

### Pull Request Template

- [ ] PR template exists: `.github/PULL_REQUEST_TEMPLATE.md`
- [ ] PR labels created:
  - [ ] `bug`, `feature`, `refactor`, `docs`, `test`, `chore`
  - [ ] `needs-review`, `approved`, `changes-requested`, `work-in-progress`, `ready-to-merge`
  - [ ] `size/XS`, `size/S`, `size/M`, `size/L`, `size/XL`
  - [ ] `breaking-change`
- [ ] PR template referenced in CONTRIBUTING.md

### Security Settings

- [ ] Dependabot security updates enabled
- [ ] Code scanning enabled (if GitHub Advanced Security available)
- [ ] Secret scanning enabled
- [ ] Two-factor authentication enabled for maintainers (recommended)
- [ ] Security advisories visibility set to private

### Required Checks

- [ ] CI workflow (`.github/workflows/ci.yml`):
  - [ ] `Lint` check passes
  - [ ] `Unit Tests` check passes
  - [ ] `Integration Tests` check passes
  - [ ] `Build` check passes
  - [ ] Coverage threshold (60%) enforced
- [ ] Security scan workflow (`.github/workflows/security-scan.yml`):
  - [ ] `Dependency Vulnerability Scan` check passes
  - [ ] `Container Vulnerability Scan` check passes
  - [ ] `License Compliance Check` check passes
  - [ ] No CRITICAL or HIGH vulnerabilities allowed
- [ ] Docker build workflow (`.github/workflows/docker-build.yml`):
  - [ ] `Build Docker Image` check passes
  - [ ] `Test Docker Image` check passes
  - [ ] linux/amd64 builds working (RFC-0002 §2)
- [ ] Helm lint workflow (`.github/workflows/helm-lint-test.yml`):
  - [ ] `Lint Helm Chart` check passes
  - [ ] `Test Helm Chart Templates` check passes
  - [ ] `Package Helm Chart` check passes
- [ ] All required checks added to branch protection rules

### Actions & Permissions

- [ ] Workflow permissions: Read and write
- [ ] Contents: Read and write
- [ ] Metadata: Read
- [ ] Pull requests: Read and write
- [ ] Security events: Read and write
- [ ] Checks: Read and write
- [ ] Fork pull request workflows: Enabled (with caution)
- [ ] Actions retention: 90 days (default)
- [ ] Branch creation: Allow all collaborators
- [ ] Tag creation: Restrict to maintainers only

---

## Additional Configuration Files

### CODEOWNERS (Optional)

Create `.github/CODEOWNERS` to specify code ownership:

```
# Global code owners
* @maintainer1 @maintainer2

# Specific directories
/api/ @api-team
/charts/mongodb-operator/ @helm-team
/docs/ @docs-team

# File patterns
*.go @go-team
*.md @docs-team
.github/ @devops-team
```

---

### DEPENDABOT Configuration

Ensure `.github/dependabot.yml` exists:

```yaml
version: 2
updates:
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 10

  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 10
```

---

## Maintenance

### Regular Tasks

**Weekly**:
- Review Dependabot alerts
- Review and merge Dependabot PRs

**Monthly**:
- Review and close stale issues and PRs
- Update version numbers in templates if needed

**Quarterly**:
- Review branch protection rules
- Review required checks and thresholds
- Update security scanning tools

**On Release**:
- Update version numbers in templates
- Update roadmap in README
- Review and update documentation

---

## Troubleshooting

### Common Issues

#### Required Checks Failing

**Problem**: PR cannot merge due to failed checks

**Solutions**:
1. Check workflow logs for specific failure
2. Run workflow locally using `act` (GitHub Actions local runner)
3. Check for environment-specific issues (secrets, permissions)

#### Workflow Permissions Denied

**Problem**: Workflow fails with permission denied error

**Solutions**:
1. Check Actions permissions in repository settings
2. Verify workflow permissions are set to "Read and write"
3. Ensure required secrets are configured

#### Branch Protection Too Strict

**Problem**: Cannot merge PR due to branch protection

**Solutions**:
1. Add admin bypass if needed (use with caution)
2. Temporarily relax restrictions for urgent fixes
3. Review and adjust required checks

---

## References

- [GitHub Branch Protection](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
- [GitHub Issue Forms — Syntax](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-issue-forms)
- [GitHub Dependabot](https://docs.github.com/en/code-security/dependabot)
- [GitHub Actions Permissions](https://docs.github.com/en/actions/security-guides/automatic-token-authentication)
- [Conventional Commits](https://www.conventionalcommits.org/)

---

## Support

For questions or issues with repository settings:

- Open an issue using the [Question template](../.github/ISSUE_TEMPLATE/question.yml)
- Contact maintainers via [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)
- Refer to [CONTRIBUTING.md](../CONTRIBUTING.md) for contribution guidelines
