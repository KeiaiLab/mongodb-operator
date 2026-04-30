# Getting Started

## Prerequisites

Before deploying MongoDB Operator, ensure you have:

- **Kubernetes cluster**: v1.26 or higher (tested with Kind, Minikube, GKE, EKS)
- **Helm**: v3.8+ for operator installation
- **kubectl**: Configured with cluster access
- **StorageClass**: Default storage class configured for PVCs

## Installation Methods

### Using Helm (Recommended)

```bash
# Add the Helm repository
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

# Install the operator in a dedicated namespace
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

### Using kubectl Manifests

```bash
# Apply CRDs first
kubectl apply -f https://raw.githubusercontent.com/eightynine01/mongodb-operator/main/config/crd/bases/mongodb.keiailab.com_mongodbs.yaml
kubectl apply -f https://raw.githubusercontent.com/eightynine01/mongodb-operator/main/config/crd/bases/mongodb.keiailab.com_mongodbshardeds.yaml
kubectl apply -f https://raw.githubusercontent.com/eightynine01/mongodb-operator/main/config/crd/bases/mongodb.keiailab.com_mongodbbackups.yaml

# Deploy the operator
kubectl apply -f https://raw.githubusercontent.com/eightynine01/mongodb-operator/main/deploy/operator.yaml
```

## Quick Start Example

### 1. Create Namespace and Credentials

```bash
kubectl create namespace database
kubectl create secret generic mongodb-admin \
  --from-literal=username=admin \
  --from-literal=password=your-secure-password \
  -n database
```

### 2. Deploy a MongoDB ReplicaSet

Create `mongodb-replicaset.yaml`:

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  members: 3
  version:
    version: "8.2"
  storage:
    storageClassName: standard
    size: 10Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin
```

```bash
kubectl apply -f mongodb-replicaset.yaml
```

### 3. Verify Deployment

```bash
# Watch pods become ready
kubectl get pods -n database -w

# Check MongoDB status
kubectl get mongodb my-mongodb -n database -o yaml
```

## Next Steps

- **Configure TLS**: Enable encryption for cluster communication ([docs/advanced/tls.md](advanced/tls.md))
- **Set up Monitoring**: Configure Prometheus and Grafana ([docs/advanced/monitoring.md](advanced/monitoring.md))
- **Configure Backups**: Set up automated backups to S3 ([docs/advanced/backup.md](advanced/backup.md))
- **Scale Your Cluster**: Learn about scaling strategies ([docs/advanced/scaling.md](advanced/scaling.md))
- **Troubleshooting**: Resolve common issues ([docs/troubleshooting.md](troubleshooting.md))
