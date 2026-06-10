# Secret Management

MongoDB Operator keeps CRD compatibility by referencing ordinary Kubernetes
Secrets. In production, create those Secrets through External Secrets Operator
from the KeiaiLab Infisical `ClusterSecretStore` instead of committing raw
Secret manifests.

## Helm Values

```yaml
externalSecrets:
  enabled: true
  secretStoreKind: ClusterSecretStore
  secretStoreName: infisical
  admin:
    enabled: true
    targetName: mongodb-admin-credentials
    usernameRemoteKey: /data/mongodb/admin/username
    passwordRemoteKey: /data/mongodb/admin/password
  app:
    enabled: true
    targetName: mongodb-app-credentials
    usernameRemoteKey: /data/mongodb/app/username
    passwordRemoteKey: /data/mongodb/app/password
  backupS3:
    enabled: true
    targetName: ceph-objectstore-credentials
    accessKeyRemoteKey: /data/mongodb/backup/access-key
    secretKeyRemoteKey: /data/mongodb/backup/secret-key
```

The generated Secret names are consumed by the existing CRD fields:

| Use | CRD field | Required Secret keys |
|---|---|---|
| Admin user bootstrap | `spec.auth.adminCredentialsSecretRef.name` | `username`, `password` |
| App user password | `spec.users[].passwordSecretRef.name` | `username`, `password` |
| S3 backup credentials | `spec.backup.storage.s3.credentialsRef.name` / `MongoDBBackup.spec.storage.s3.credentialsRef.name` | `access-key`, `secret-key` |

## Verification

```bash
kubectl get externalsecret -n database
kubectl describe externalsecret mongodb-admin-credentials -n database
kubectl get secret mongodb-admin-credentials -n database
```

If a MongoDB CR reports an authentication or backup credential error, check the
`ExternalSecret` readiness condition before inspecting Secret data.
