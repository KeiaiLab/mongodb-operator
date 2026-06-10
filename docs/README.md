# MongoDB Operator Documentation

Complete documentation for deploying, managing, and contributing to MongoDB Operator on Kubernetes.

<p>
  <b>English</b> |
  <a href="i18n/ko/readme.md">한국어</a> |
  <a href="i18n/ja/readme.md">日本語</a> |
  <a href="i18n/zh/readme.md">中文</a>
</p>

## Getting Started

- **[Getting Started](getting-started.md)** — Quick start guide for deploying MongoDB Operator
- **[Installation Guide](install.md)** — Detailed installation methods (OLM v1, Helm, manual)
- **[Troubleshooting](troubleshooting.md)** — Common issues and solutions
- **[Upgrading](UPGRADING.md)** — Version upgrade guide

## Architecture & Design

- **[Architecture](architecture.md)** — Operator architecture and component overview
- **[Design](design.md)** — Design decisions and patterns
- **[Gap Analysis](gap-analysis.md)** — Feature comparison with Bitnami mongodb-sharded

## Advanced Topics

- **[TLS Configuration](advanced/tls.md)** — TLS encryption with cert-manager
- **[Monitoring](advanced/monitoring.md)** — Prometheus metrics and Grafana dashboards
- **[Backup and Restore](advanced/backup.md)** — Automated backups to S3/PVC
- **[Secret Management](advanced/secret-management.md)** — External Secrets Operator and Infisical credential sourcing
- **[Scaling Strategies](advanced/scaling.md)** — Horizontal and vertical scaling
- **[Admission Webhook](advanced/webhook.md)** — Validating webhook configuration

## Developer Guide

- **[Development Guide](developers/development.md)** — Local development setup
- **[Architecture Overview](developers/architecture.md)** — Controller and reconciler internals
- **[Testing Guide](developers/testing.md)** — Unit, integration, and e2e tests
- **[Testing Strategy](developers/testing-strategy.md)** — Test philosophy and coverage goals

## Project

- **[Contributing](contributing.md)** — How to contribute
- **[Code of Conduct](code-of-conduct.md)** — Community standards
- **[Governance](governance.md)** — Project governance model
- **[Security](security.md)** — Security policy and reporting
- **[Branding](branding.md)** — Logo and branding guidelines
- **[Maintainers](maintainers.md)** — Project maintainers
- **[Adopters](adopters.md)** — Organizations using MongoDB Operator
- **[Support](support.md)** — Getting help
- **[Roadmap](roadmap.md)** — Development roadmap
- **[Changelog](changelog.md)** — Release history

## Knowledge Base

- **[ADR Index](kb/adr/INDEX.md)** — Architecture Decision Records
- **[Incident Index](kb/incident/INDEX.md)** — Postmortem records
- **[RFC-0001](kb/rfc/0001-auto-rs-reconfig.md)** — Auto RS reconfig on host change
- **[Dependencies](kb/deps/2026-05.md)** — Dependency change log

## Release Guides

- **[Artifact Hub](releases/artifact-hub-verification.md)** — Artifact Hub verification
- **[GHCR Setup](releases/ghcr-setup.md)** — GitHub Container Registry setup
- **[Docker Hub Setup](releases/docker-hub-setup.md)** — Docker Hub publication

## Translations

| Language | Directory |
|----------|-----------|
| 한국어 (Korean) | [docs/i18n/ko/](i18n/ko/) |
| 日本語 (Japanese) | [docs/i18n/ja/](i18n/ja/) |
| 中文 (Chinese) | [docs/i18n/zh/](i18n/zh/) |

## Document Tree

```
docs/
├── README.md                    # This index
├── getting-started.md           # Quick start
├── install.md                   # Installation guide
├── troubleshooting.md           # Common issues
├── UPGRADING.md                 # Version upgrades
├── architecture.md              # Architecture overview
├── design.md                    # Design patterns
├── gap-analysis.md              # Bitnami comparison
├── changelog.md                 # Release history
├── roadmap.md                   # Development roadmap
├── contributing.md              # Contribution guide
├── code-of-conduct.md           # Community standards
├── governance.md                # Governance model
├── security.md                  # Security policy
├── branding.md                  # Branding guidelines
├── maintainers.md               # Project maintainers
├── adopters.md                  # Adopter list
├── support.md                   # Getting help
├── advanced/
│   ├── tls.md                   # TLS encryption
│   ├── monitoring.md            # Prometheus/Grafana
│   ├── backup.md                # Backup/restore
│   ├── secret-management.md     # ExternalSecret/Infisical credential sourcing
│   ├── scaling.md               # Scaling strategies
│   └── webhook.md               # Admission webhook
├── developers/
│   ├── development.md           # Local development
│   ├── architecture.md          # Controller internals
│   ├── testing.md               # Testing guide
│   └── testing-strategy.md      # Test philosophy
├── kb/
│   ├── adr/                     # Architecture Decision Records
│   ├── rfc/                     # RFCs
│   └── deps/                    # Dependency logs
├── releases/                    # Release guides
└── i18n/
    ├── ko/                      # Korean translations
    ├── ja/                      # Japanese translations
    └── zh/                      # Chinese translations
```
