# MongoDB Operator Examples

This directory contains production-ready deployment examples for MongoDB Operator. Each category provides tailored manifests for different use cases.

## Categories

### [minimal/](./minimal/)
Simple, production-ready deployments with minimal resource requirements. Ideal for development, testing, or production workloads with modest needs.

- **mongodb-replicaset.yaml**: 3-member ReplicaSet with 10Gi storage, SCRAM-SHA-256 authentication
- **mongodb-sharded.yaml**: 2-shard cluster (3 members each) with 50Gi storage

### [production/](./production/)
Enterprise-grade deployments with resource limits, monitoring, TLS, and high availability features.

- **mongodb-replicaset-prod.yaml**: 5-member ReplicaSet with resource limits, pod disruption budget, network policies
- **mongodb-sharded-prod.yaml**: 3-shard cluster (3 members each) with HA mongos, anti-affinity rules

### [backups/](./backups/)
Backup and restore configurations for automated data protection.

- **s3-backup.yaml**: MongoDBBackup CRD with S3-compatible storage, compression, and scheduling

### [monitoring/](./monitoring/)
Prometheus and Grafana integration for comprehensive observability.

- **prometheus-stack.yaml**: Prometheus, Grafana, and ServiceMonitor for MongoDB metrics

## Quick Start

1. **Deploy the operator** (if not already installed):
   ```bash
   helm install mongodb-operator mongodb-operator/mongodb-operator \
     --namespace mongodb-operator-system --create-namespace
   ```

2. **Choose an example** and customize it for your environment:
   - Update `storageClassName` for your cluster
   - Adjust resource requests/limits as needed
   - Configure secrets with your credentials

3. **Deploy**:
   ```bash
   kubectl apply -f examples/minimal/mongodb-replicaset.yaml
   ```

## Documentation

For complete documentation, see:
- [Main README](../README.md) - Overview and architecture
- [Project Documentation](https://github.com/keiailab/mongodb-operator) - GitHub repository
- [CRD Reference](../README.md#custom-resource-definitions) - Available configuration options

## Prerequisites

- Kubernetes 1.26+
- MongoDB Operator installed
- Appropriate StorageClass configured
- Secret resources for credentials (create before deploying)

## Customization

All examples include comments explaining key configuration options. Modify values based on your requirements:
- **Storage**: Adjust `spec.storage.size` and `storageClassName`
- **Resources**: Update `spec.resources` for CPU/memory limits
- **Replicas**: Change `spec.members` or `spec.shards.count`
- **Monitoring**: Enable/disable `spec.monitoring.enabled`
- **TLS**: Configure `spec.tls.enabled` and cert-manager settings

## Security Notes

- Never commit credentials to version control
- Use Kubernetes Secrets for sensitive data
- Enable TLS for production deployments
- Use strong passwords with SCRAM-SHA-256 authentication
- Configure network policies to restrict access
