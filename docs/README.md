# MongoDB Operator Documentation

Welcome to the MongoDB Operator documentation. This guide helps you deploy, manage, and contribute to MongoDB Operator on Kubernetes.

## User Documentation

### Getting Started

- **[Getting Started](getting-started.md)** - Quick start guide for deploying MongoDB Operator
  - Prerequisites and installation methods
  - Quick start example with ReplicaSet
  - Links to advanced topics

### Advanced Topics

- **[Admission Webhook](advanced/webhook.md)** - Validating webhook for spec invariants
  - Quick start with cert-manager
  - 13 validation invariants matrix (MongoDB + MongoDBSharded)
  - failurePolicy=Fail trade-off (ADR-0015)
  - Troubleshooting + Type A' unreachable patterns

- **[TLS Configuration](advanced/tls.md)** - Enable TLS encryption with cert-manager
  - cert-manager setup and certificate issuer configuration
  - TLS enabling in MongoDB CRD
  - Certificate verification and common issues

- **[Monitoring](advanced/monitoring.md)** - Set up Prometheus monitoring and Grafana dashboards
  - Prometheus Operator setup
  - ServiceMonitor configuration
  - Grafana dashboard templates and alerting rules
  - Key metrics to monitor

- **[Backup and Restore](advanced/backup.md)** - Configure automated backups
  - MongoDBBackup CRD usage
  - S3 and PVC backup configuration
  - Restore procedures
  - Backup scheduling with CronJob

- **[Scaling Strategies](advanced/scaling.md)** - Horizontal and vertical scaling
  - Horizontal scale out (adding shards)
  - Vertical scaling (resource adjustment)
  - Mongos replica scaling and HPA
  - Best practices and limitations

### Troubleshooting

- **[Troubleshooting Guide](troubleshooting.md)** - Common issues and solutions
  - Common deployment issues
  - Connection problems
  - StatefulSet pod issues
  - Init script failures
  - Debugging tips and tools

## Operations Documentation

- **[Operations README](operations/README.md)** - argos data plane cluster ops 통합 entry
  - **[Cluster Audit](operations/cluster-audit.md)** - 현재 상태 (격차 11건 / clean 15건, ratio 57.7%)
  - **[Production-grade Sprint Plan](operations/production-grade-sprint.md)** - 7 phase step-by-step
  - **[DR Cluster Snapshots](operations/cluster-snapshots/README.md)** - 임시 보관소

## Developer Documentation

### Development Guide

- **[Development Guide](developers/development.md)** - Local development setup
  - Kind/Minikube setup for local testing
  - Running operator locally
  - Hot reload during development
  - Debugging operator
  - Code structure overview

- **[Testing Guide](developers/testing.md)** - Testing strategies
  - Unit test writing guide
  - Integration test setup with envtest
  - Running tests locally
  - Test coverage requirements
  - Continuous testing

- **[Architecture Overview](developers/architecture.md)** - Operator architecture
  - Controller design and reconciliation loop
  - Resource builders pattern
  - MongoDB package (internal/mongodb)
  - Finalizer patterns
  - Error handling and status management

## Additional Resources

- **[Project README](../README.md)** - Main project documentation
- **[GitHub Issues](https://github.com/keiailab/mongodb-operator/issues)** - Bug reports and feature requests
- **[GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)** - Community discussions

## Quick Links

| Topic | Document |
|-------|----------|
| Install Operator | [Getting Started](getting-started.md) |
| Deploy ReplicaSet | [Getting Started](getting-started.md#quick-start-example) |
| Enable TLS | [TLS Configuration](advanced/tls.md) |
| Set up Monitoring | [Monitoring](advanced/monitoring.md) |
| Configure Backups | [Backup and Restore](advanced/backup.md) |
| Scale Cluster | [Scaling Strategies](advanced/scaling.md) |
| Troubleshoot Issues | [Troubleshooting Guide](troubleshooting.md) |
| Contribute Code | [Development Guide](developers/development.md) |
| Write Tests | [Testing Guide](developers/testing.md) |
| Understand Architecture | [Architecture Overview](developers/architecture.md) |

## Document Index

```
docs/
├── README.md                           # This file
├── getting-started.md                   # User quick start
├── troubleshooting.md                   # Common issues
├── advanced/
│   ├── tls.md                          # TLS encryption
│   ├── monitoring.md                    # Prometheus/Grafana
│   ├── backup.md                        # Backup/restore
│   └── scaling.md                      # Scaling strategies
└── developers/
    ├── development.md                   # Local development
    ├── testing.md                       # Testing guide
    └── architecture.md                  # Architecture design
```
