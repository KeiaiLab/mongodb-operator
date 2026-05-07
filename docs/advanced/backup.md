# Backup and Restore

## Overview

MongoDB Operator provides automated backup capabilities through the `MongoDBBackup` CRD. Backups can be stored in S3-compatible storage or Persistent Volume Claims.

## MongoDBBackup CRD Usage

### Backup Specification Fields

| Field | Description | Default |
|-------|-------------|---------|
| `spec.clusterRef.name` | Target MongoDB cluster name | - |
| `spec.clusterRef.kind` | Cluster kind (`MongoDB` or `MongoDBSharded`) | `MongoDB` |
| `spec.type` | Backup type (`full` or `incremental`) | `full` |
| `spec.compression` | Enable backup compression | `true` |
| `spec.storage.type` | Storage type (`s3` or `pvc`) | `s3` |

## S3 Backup Configuration

### 1. Create S3 Credentials Secret

```bash
kubectl create secret generic s3-credentials \
  --from-literal=accessKeyId=YOUR_ACCESS_KEY \
  --from-literal=secretAccessKey=YOUR_SECRET_KEY \
  -n database
```

### 2. Configure S3 Backup

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: daily-backup
  namespace: database
spec:
  clusterRef:
    name: my-mongodb
    kind: MongoDB
  type: full
  compression: true
  storage:
    type: s3
    s3:
      bucket: mongodb-backups
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      prefix: my-cluster/
      credentialsRef:
        name: s3-credentials
```

### 3. S3 Compatibility

The operator supports S3-compatible storage:

**MinIO:**
```yaml
storage:
  type: s3
  s3:
    bucket: mongodb-backups
    endpoint: https://minio.example.com:9000
    region: us-east-1
    credentialsRef:
      name: s3-credentials
    # Use path style for MinIO
    pathStyle: true
```

**Wasabi:**
```yaml
storage:
  type: s3
  s3:
    bucket: mongodb-backups
    endpoint: https://s3.wasabisys.com
    region: us-east-1
    credentialsRef:
      name: s3-credentials
```

## PVC Backup Configuration

### 1. Create Backup PVC

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: mongodb-backup-pvc
  namespace: database
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: standard
  resources:
    requests:
      storage: 100Gi
```

### 2. Configure PVC Backup

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: local-backup
  namespace: database
spec:
  clusterRef:
    name: my-mongodb
    kind: MongoDB
  type: full
  compression: true
  storage:
    type: pvc
    pvc:
      claimName: mongodb-backup-pvc
      mountPath: /backup
```

## Backup Scheduling with CronJob

Since automatic scheduling is not yet implemented, use Kubernetes CronJob for periodic backups:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: mongodb-daily-backup
  namespace: database
spec:
  schedule: "0 2 * * *"  # 2 AM daily
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 1
  jobTemplate:
    spec:
      backoffLimit: 3
      template:
        spec:
          restartPolicy: OnFailure
          serviceAccountName: mongodb-backup-sa
          containers:
            - name: backup
              image: bitnami/kubectl:latest
              command:
                - /bin/sh
                - -c
                - |
                  BACKUP_NAME="backup-$(date +%Y%m%d-%H%M%S)"
                  cat <<EOF | kubectl apply -f -
                  apiVersion: mongodb.keiailab.com/v1alpha1
                  kind: MongoDBBackup
                  metadata:
                    name: ${BACKUP_NAME}
                    namespace: database
                  spec:
                    clusterRef:
                      name: my-mongodb
                      kind: MongoDB
                    type: full
                    storage:
                      type: s3
                      s3:
                        bucket: mongodb-backups
                        endpoint: https://s3.amazonaws.com
                        region: us-east-1
                        credentialsRef:
                          name: s3-credentials
                  EOF
```

### Create Service Account for CronJob

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mongodb-backup-sa
  namespace: database
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: mongodb-backup-role
  namespace: database
rules:
  - apiGroups: ["mongodb.keiailab.com"]
    resources: ["mongodbbackups"]
    verbs: ["get", "list", "create", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: mongodb-backup-binding
  namespace: database
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: mongodb-backup-role
subjects:
  - kind: ServiceAccount
    name: mongodb-backup-sa
```

## Restore Procedures

### Restore from S3 Backup

The operator doesn't provide automatic restore yet. Use `mongorestore` manually:

```bash
# 1. Download backup from S3
aws s3 sync s3://mongodb-backups/my-cluster/backup-20240101-020000 ./backup

# 2. Get MongoDB admin password
kubectl get secret mongodb-admin -n database -o jsonpath='{.data.password}' | base64 --decode

# 3. Restore to MongoDB
kubectl exec -it my-mongodb-0 -n database -c mongod -- mongorestore \
  --uri="mongodb://admin:PASSWORD@localhost:27017" \
  --dir=/backup/ \
  --drop
```

### Restore from PVC Backup

```bash
# 1. Mount backup PVC to temporary pod
kubectl run restore-pod --rm -it --image=mongo:8.3.1 \
  --overrides='
{
  "spec": {
    "containers": [{
      "name": "restore",
      "image": "mongo:8.3.1",
      "command": ["sleep", "3600"],
      "volumeMounts": [{
        "name": "backup-pvc",
        "mountPath": "/backup"
      }]
    }],
    "volumes": [{
      "name": "backup-pvc",
      "persistentVolumeClaim": {
        "claimName": "mongodb-backup-pvc"
      }
    }]
  }
}' \
  -n database

# 2. From within the pod, restore the backup
kubectl exec -it restore-pod -n database -- mongorestore \
  --uri="mongodb://admin:PASSWORD@my-mongodb-0.my-mongodb.database.svc.cluster.local:27017" \
  --dir=/backup/backup-20240101-020000/ \
  --drop
```

### Point-in-Time Recovery (PITR)

For point-in-time recovery, enable MongoDB's oplog:

```yaml
# In MongoDB CRD, add oplog size
spec:
  mongod:
    additionalMongodConfig:
      storage:
        oplogSizeMB: 1024
```

Then use `mongorestore` with `--oplogReplay`:
```bash
mongorestore --uri="mongodb://admin:PASSWORD@localhost:27017" \
  --dir=/backup/ \
  --oplogReplay \
  --oplogLimit="1717200000:1"
```

## Backup Verification

### Check Backup Status

```bash
# List all backups
kubectl get mongodbbackup -n database

# Describe backup details
kubectl describe mongodbbackup daily-backup -n database

# Check backup job
kubectl get jobs -n database -l mongodbbackup=daily-backup
```

### Verify Backup Integrity

```bash
# List backup files in S3
aws s3 ls s3://mongodb-backups/my-cluster/backup-20240101-020000/

# Check backup metadata
kubectl get mongodbbackup backup-20240101-020000 -n database -o yaml
```

## Backup Best Practices

1. **Test Restores Regularly**: Verify backups by performing test restores
2. **Multiple Locations**: Store backups in multiple regions or providers
3. **Compression**: Enable compression to reduce storage costs
4. **Retention Policy**: Implement lifecycle policies for old backups
5. **Monitoring**: Set up alerts for backup failures
6. **Incremental Backups**: Use incremental backups for large databases
7. **Backup Encryption**: Enable S3 bucket encryption for sensitive data

### S3 Lifecycle Policy Example

```json
{
  "Rules": [
    {
      "Id": "BackupRetentionPolicy",
      "Status": "Enabled",
      "Transitions": [
        {
          "Days": 30,
          "StorageClass": "STANDARD_IA"
        },
        {
          "Days": 90,
          "StorageClass": "GLACIER"
        }
      ],
      "Expiration": {
        "Days": 365
      }
    }
  ]
}
```

## Troubleshooting

### Backup Job Fails

```bash
# Check job logs
kubectl logs -n database job/backup-20240101-020000

# Check backup status
kubectl get mongodbbackup backup-20240101-020000 -n database -o yaml

# Verify S3 credentials
kubectl describe secret s3-credentials -n database
```

### Slow Backup Performance

- Use incremental backups for large databases
- Increase CPU/memory limits for backup jobs
- Consider network bandwidth between cluster and S3

### Backup Not Appearing in S3

- Verify S3 bucket exists and is accessible
- Check credentials have proper permissions
- Verify endpoint URL is correct for your S3 provider
- Check backup job logs for errors
