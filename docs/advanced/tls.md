# TLS Configuration

## Overview

MongoDB Operator integrates with cert-manager for automatic TLS certificate provisioning and rotation. This guide covers setting up TLS for MongoDB clusters.

## cert-manager Setup

### 1. Install cert-manager

```bash
# Install cert-manager (v1.13+)
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.2/cert-manager.yaml

# Verify installation
kubectl get pods -n cert-manager
```

### 2. Create a Certificate Issuer

Choose between **ClusterIssuer** (cluster-wide) or **Issuer** (namespace-scoped).

#### Using Let's Encrypt (Production)

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: your-email@example.com
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
      - http01:
          ingress:
            class: nginx
```

#### Using Self-Signed Certificates (Development)

```yaml
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned-issuer
  namespace: database
spec:
  selfSigned: {}
```

#### Using CA Issuer

```yaml
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: ca-issuer
  namespace: database
spec:
  ca:
    secretName: ca-cert
```

```bash
# Create CA secret
kubectl create secret tls ca-cert \
  --cert=ca.crt \
  --key=ca.key \
  -n database
```

## Enabling TLS in MongoDB CRD

### MongoDB ReplicaSet

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  members: 3
  version:
    version: "8.3.1"
  storage:
    storageClassName: standard
    size: 10Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
      # Optional: Specify common name
      commonName: "my-mongodb.database.svc.cluster.local"
```

### MongoDBSharded Cluster

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: my-sharded
  namespace: database
spec:
  version:
    version: "8.3.1"
  configServer:
    members: 3
    storage:
      size: 5Gi
  shards:
    count: 3
    membersPerShard: 3
    storage:
      size: 50Gi
  mongos:
    replicas: 2
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: ca-issuer
        kind: Issuer
```

## Certificate Verification

After applying the TLS configuration:

```bash
# Check certificate status
kubectl get certificates -n database

# View certificate details
kubectl describe certificate my-mongodb-tls -n database

# Verify MongoDB pod has TLS certificates mounted
kubectl describe pod my-mongodb-0 -n database | grep -A 5 "tls-"
```

## Connecting with TLS

### Using mongosh

```bash
# Connect with TLS verification enabled
mongosh "mongodb://admin:password@my-mongodb-0.my-mongodb.database.svc.cluster.local:27017/admin?tls=true&tlsCAFile=/path/to/ca.pem"

# Connect with TLS verification disabled (not recommended)
mongosh "mongodb://admin:password@my-mongodb-0.my-mongodb.database.svc.cluster.local:27017/admin?tls=true&tlsInsecure=true"
```

### From Applications

Update your connection string to include TLS parameters:

```
mongodb://user:password@host:port/database?tls=true&replicaSet=my-mongodb&authSource=admin
```

## Common TLS Issues & Solutions

### Issue: Certificate Pending

**Symptom**: Certificate stuck in `Issuing` status.

**Solution**:
```bash
# Check certificate status
kubectl get certificate -n database

# Check cert-manager logs
kubectl logs -n cert-manager -l app=cert-manager

# Verify issuer is ready
kubectl get clusterissuer/letsencrypt-prod
kubectl describe clusterissuer/letsencrypt-prod
```

### Issue: DNS-01 Challenge Fails

**Symptom**: Certificate fails with DNS-01 challenge error.

**Solution**:
- Ensure your domain is configured correctly
- Check DNS provider credentials
- Verify the solver matches your ingress controller

### Issue: Certificate Expired

**Symptom**: MongoDB pods fail to start with TLS error.

**Solution**:
```bash
# Force certificate renewal
kubectl delete certificate my-mongodb-tls -n database
# The operator will recreate the certificate
```

### Issue: Connection Refused with TLS

**Symptom**: Client cannot connect despite TLS enabled.

**Solution**:
1. Verify the CA certificate is correct
2. Check that the service uses the correct hostname
3. Ensure TLS is enabled on all nodes in the replica set
4. Check MongoDB logs for TLS errors:

```bash
kubectl logs my-mongodb-0 -n database -c mongod
```

## Best Practices

- Use **ClusterIssuer** for production deployments
- Set appropriate **commonName** matching your service DNS
- Monitor certificate expiration dates
- Test certificate renewal before production
- Keep CA certificates secure
- Use **internal CA** for clusters not exposed to the internet
