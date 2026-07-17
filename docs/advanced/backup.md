# Backup and Restore

## Overview

MongoDB Operator provides automated backup capabilities through the `MongoDBBackup` CRD.
A `MongoDBBackup` CR does one of two things:

- **capture** a backup of `spec.clusterRef` (default), or
- **restore** into `spec.clusterRef` — when `spec.restore` is set (see
  [Restore Procedures](#restore-procedures)).

Backups can be stored in S3-compatible storage or a PVC.

> **Storage support is not symmetric.** S3 is the supported path and the only one
> that supports Point-in-Time Recovery. PVC storage is minimally wired (the
> operator does not provision or reference an existing claim from
> `spec.storage.pvc` — see [PVC Backup Configuration](#pvc-backup-configuration)).
> Prefer S3 for anything you intend to restore from.

## MongoDBBackup CRD Usage

### Backup Specification Fields

| Field | Description | Default |
|-------|-------------|---------|
| `spec.clusterRef.name` | Target MongoDB cluster name | - |
| `spec.clusterRef.kind` | Cluster kind (`MongoDB` or `MongoDBSharded`) | `MongoDB` |
| `spec.type` | Backup type (`full` or `incremental`) | `full` |
| `spec.compression` | Enable backup compression | `true` |
| `spec.compressionType` | Compression algorithm (`gzip`, `zstd`, `snappy`) | `zstd` |
| `spec.storage.type` | Storage type (`s3` or `pvc`) | - |
| `spec.restore` | Set to make this CR a **restore** instead of a capture | unset |

### Restore Specification Fields

Set only when this CR should restore. See [Restore Procedures](#restore-procedures).

| Field | Description | Default |
|-------|-------------|---------|
| `spec.restore.sourceBackupName` | Source `MongoDBBackup` (must be `Completed`, same namespace) | - |
| `spec.restore.pointInTime` | PITR target, RFC3339 **second** precision | unset (base snapshot only) |
| `spec.restore.pointInTimeTimestamp` | PITR target as raw BSON timestamp `"<sec>:<ordinal>"`. Takes precedence over `pointInTime` | unset |

## S3 Backup Configuration

### 1. Create S3 Credentials Secret

The operator reads the keys **`access-key`** and **`secret-key`** from this Secret.
Other key names are not recognized — the backup Job would fail to start with
`CreateContainerConfigError`.

```bash
kubectl create secret generic s3-credentials \
  --from-literal=access-key=YOUR_ACCESS_KEY \
  --from-literal=secret-key=YOUR_SECRET_KEY \
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

The operator supports S3-compatible storage. The full set of `spec.storage.s3`
fields is `bucket` (required), `credentialsRef` (required), `endpoint`, `region`,
`prefix`, and `insecureSkipTLS` — there is **no path-style toggle**; the backup
Job shells out to `aws s3` with `--endpoint-url`, which handles S3-compatible
endpoints.

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
    # Self-signed MinIO endpoint only — skips TLS verification.
    # insecureSkipTLS: true
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

> **Limited support.** `spec.storage.pvc` accepts only `size` and
> `storageClassName` — there is no `claimName` (you cannot point a backup at an
> existing PVC) and no `mountPath`. PVC storage does **not** support PITR. Use S3
> unless you have a specific reason not to.

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
      size: 100Gi
      storageClassName: standard
```

## Backup Scheduling

### Built-in scheduler (`spec.backup.schedule`)

Setting `spec.backup.schedule` on the **cluster** CR (`MongoDB` /
`MongoDBSharded`) makes the operator reconcile a `<cluster>-backup-schedule`
CronJob that periodically creates `MongoDBBackup` CRs:

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  # ...
  backup:
    enabled: true
    schedule: "0 2 * * *"   # 2 AM daily
    storage:
      type: s3
      s3:
        bucket: mongodb-backups
        endpoint: https://s3.amazonaws.com
        region: us-east-1
        credentialsRef:
          name: s3-credentials
```

> **Two gaps you must know before relying on this.**
>
> 1. **RBAC is not created for you.** The generated CronJob runs as
>    ServiceAccount `<cluster>-backup-scheduler`, which the operator does not
>    create. Without it the CronJob's pods never start. Create the SA + Role +
>    RoleBinding yourself (see [below](#create-service-account-for-cronjob), using
>    that name).
> 2. **Storage details are not propagated.** The `MongoDBBackup` CRs it emits
>    carry only `storage.type` — `bucket` / `endpoint` / `credentialsRef` from
>    `spec.backup.storage` are **not** copied onto them, and the backup Job reads
>    storage config only from the `MongoDBBackup` CR (there is no fallback to the
>    cluster's `spec.backup.storage`). A scheduled `type: s3` backup therefore has
>    no destination and fails.
>
> Until both are addressed, schedule backups with your own CronJob that emits a
> complete `MongoDBBackup` CR, as shown next.

### Self-managed CronJob (recommended today)

This emits a fully specified `MongoDBBackup` CR, so it does not hit gap 2 above:

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
              image: registry.k8s.io/kubectl:v1.31.0
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

For the self-managed CronJob above, the name is yours to pick (`mongodb-backup-sa`
here — it must match `serviceAccountName` in the CronJob). If you are instead
using the built-in scheduler, the name is fixed: rename this ServiceAccount to
`<cluster>-backup-scheduler` (e.g. `my-mongodb-backup-scheduler`).

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

### Restore with `spec.restore` (operator-managed)

A `MongoDBBackup` CR with `spec.restore` set restores **into**
`spec.clusterRef` instead of capturing a new backup. The operator fetches the
source backup from its storage and runs `mongorestore` in a Job:

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: restore-to-my-mongodb
  namespace: database
spec:
  clusterRef:            # restore TARGET
    name: my-mongodb
    kind: MongoDB
  storage:               # must match the source backup's storage
    type: s3
    s3:
      bucket: mongodb-backups
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      prefix: my-cluster/
      credentialsRef:
        name: s3-credentials
  restore:
    sourceBackupName: daily-backup   # must be Phase=Completed
    # no pointInTime → restores the base snapshot only
```

Track it with `status.phase`: `Pending` → `Restoring` → `Completed` / `Failed`
(`status.error` carries the reason on failure).

```bash
kubectl get mdbbackup restore-to-my-mongodb -n database -w
```

To restore to a *specific time* rather than the base snapshot, add
`spec.restore.pointInTime` — see [Point-in-Time Recovery](#point-in-time-recovery-pitr).

### Restore from S3 manually

If you need to restore outside the operator (e.g. into a cluster it does not
manage):

```bash
# 1. Download backup from S3
aws s3 cp s3://mongodb-backups/my-cluster/my-mongodb-20240101-020000.archive.gz - | \
  gunzip > ./dump.archive

# 2. Get MongoDB admin password
kubectl get secret mongodb-admin -n database -o jsonpath='{.data.password}' | base64 --decode

# 3. Restore to MongoDB (base snapshot uses --archive, not --dir)
kubectl exec -i my-mongodb-0 -n database -c mongod -- mongorestore \
  --uri="mongodb://admin:PASSWORD@localhost:27017" \
  --archive \
  --drop < ./dump.archive
```

### Restore from PVC Backup

Substitute `claimName` below with the PVC actually holding the backup, and
`/backup/<dir>` with the dump directory inside it (PVC backups are written with
`mongodump --out`, i.e. a directory — hence `--dir`, not `--archive`).

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

## Point-in-Time Recovery (PITR)

PITR restores a cluster to an arbitrary moment *between* base backups, by
replaying archived oplog on top of a base snapshot.

> **Constraints — read first.**
>
> - **ReplicaSet only.** `clusterRef.kind: MongoDB` is the only topology with a
>   consistency guarantee. Each shard of a `MongoDBSharded` cluster has its own
>   independent oplog timestamp, so a single point in time cannot define a
>   cluster-wide consistent snapshot. Sharded PITR is *not rejected* — it restores
>   per-shard on a best-effort basis and the admission webhook returns a warning.
>   Cluster-wide sharded PITR is unsupported (backlog).
> - **S3 only.** Oplog archiving requires `storage.type: s3`. PVC storage has no
>   PITR.
> - **`pointInTime` is second-precision.** For sub-second boundaries use
>   `pointInTimeTimestamp`.

### 1. Enable it

PITR is enabled on the **cluster** CR, not on individual backups:

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  # ...
  backup:
    enabled: true              # required
    pitrEnabled: true          # required
    oplogRetentionHours: 24    # must be > 0 (default 24)
    storage:
      type: s3                 # required — PITR is S3-only
      s3:
        bucket: mongodb-backups
        endpoint: https://s3.amazonaws.com
        region: us-east-1
        prefix: my-cluster/
        credentialsRef:
          name: s3-credentials
```

All three of `enabled`, `pitrEnabled`, and `oplogRetentionHours > 0` must hold.
If any is missing the operator **silently skips** oplog archiving — explicitly
setting `oplogRetentionHours: 0` disables PITR even with `pitrEnabled: true`.

The operator then injects an oplog tailer sidecar into each `mongod` pod, which
incrementally tails the oplog and streams it straight to S3 as gzipped segments:

```
<prefix><cluster>/oplog/<startTs>_<endTs>.bson.gz
```

Each `Ts` is `<seconds>-<ordinal>` (zero-padded), so the segments sort
chronologically. Verify archiving is live:

```bash
kubectl get pod my-mongodb-0 -n database \
  -o jsonpath='{.spec.containers[*].name}'          # expect an oplog tailer sidecar
aws s3 ls s3://mongodb-backups/my-cluster/oplog/    # expect segments accumulating
```

> **Operator image requirement — `OPLOG_TAILER_IMAGE` (operational prerequisite).**
> The tailer streams the oplog with `mongodump | gzip | aws s3 cp -`, so its
> container needs **both** the MongoDB database tools **and** the AWS CLI. The
> stock `mongo` image has no `aws`, and the sidecar runs non-root (999) next to
> `mongod`, so it cannot `apt-get install` at runtime. Point the operator
> Deployment's `OPLOG_TAILER_IMAGE` env at a combined image built from
> `oplog-tailer.Dockerfile` (repo root):
>
> ```bash
> make oplog-tailer-image-push \
>   OPLOG_TAILER_IMG=harbor.keiailab.dev/keiailab/platform/mongodb-operator-oplog-tailer:v1.16.6
> # then set the operator Deployment env:
> #   OPLOG_TAILER_IMAGE=harbor.keiailab.dev/keiailab/platform/mongodb-operator-oplog-tailer:v1.16.6
> ```
>
> **Fail-open when unset.** If `OPLOG_TAILER_IMAGE` is not set the operator
> **does not inject the sidecar** — falling back to the plain `mongo` image
> would crash the sidecar (no `aws`), drop the whole pod out of `Ready`, and
> take `mongod` down with it. Injection is skipped instead and the reason is
> surfaced on the cluster status (not silent). PITR incremental archiving stays
> off until the env points at a combined image.

### 2. Check the restorable window

A PITR target is only valid inside a base backup's restorable window. The window
is the gap-free oplog segment chain anchored at that backup's base snapshot:

```bash
kubectl get mdbbackup -n database -o wide     # WINDOW column
kubectl get mdbbackup daily-backup -n database \
  -o jsonpath='{.status.earliestRestore}/{.status.latestRestore}{"\n"}'
```

| Status field | Meaning |
|---|---|
| `status.oplogStart` | The base snapshot's oplog-consistent point — the replay floor. Empty means the backup was taken without `--oplog` and **cannot** anchor a PITR restore. |
| `status.earliestRestore` | Window lower bound. |
| `status.latestRestore` | Window upper bound — end of the last gap-free segment. |
| `status.restorableWindow` | Both bounds as one human-readable line (display only). |

If oplog retention has already pruned the segments right after the base snapshot,
the chain is broken and the window collapses to the base snapshot instant
(`earliestRestore == latestRestore == oplogStart`) — only a base restore is
possible. Keep `oplogRetentionHours` comfortably longer than your backup interval.

### 3. Restore to a point in time

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: restore-before-incident
  namespace: database
spec:
  clusterRef:
    name: my-mongodb
    kind: MongoDB
  storage:
    type: s3
    s3:
      bucket: mongodb-backups
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      prefix: my-cluster/
      credentialsRef:
        name: s3-credentials
  restore:
    sourceBackupName: daily-backup
    pointInTime: "2026-07-17T09:30:00Z"
```

The operator restores the base snapshot, then replays archived oplog segments up
to the target.

To cut inside a single second — e.g. immediately before one bad write — use the
raw BSON timestamp instead. It takes precedence over `pointInTime`:

```yaml
  restore:
    sourceBackupName: daily-backup
    pointInTimeTimestamp: "1752745800:7"   # "<seconds>:<ordinal>"
```

### How a bad target is caught

Validation happens at two points, and they are not equivalent:

1. **Admission webhook** — rejects a target outside
   `[earliestRestore, latestRestore]`. If the window is not recorded yet it
   **fails open** and admits the request: the window is derived from observed S3
   segments, so it is an advisory gate, not ground truth. Requires
   `webhook.enabled=true` ([Admission Webhook](webhook.md)).
2. **Restore Job** — **authoritative**. It attempts the actual replay and, if no
   segment chain reaches the target, ends as `Phase=Failed` with `status.error`.

So a restore admitted by the webhook can still fail at the Job — always confirm
`Phase=Completed`, never treat admission as proof of success.

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
4. **Retention Policy**: Use an S3 lifecycle policy (below) to expire old
   backups. **`spec.backup.retention` (`days` / `count`) is currently inert** —
   the field exists but nothing reads it, so base backups are never pruned by the
   operator and will grow without bound. `spec.backup.oplogRetentionHours` is
   separate and *is* enforced (it prunes archived oplog segments only).
5. **Monitoring**: Set up alerts for backup failures
6. **Incremental Backups**: Use incremental backups for large databases
7. **Backup Encryption**: Enable S3 bucket encryption for sensitive data

### S3 Lifecycle Policy Example

> **If PITR is on, scope this rule.** A bucket-wide rule also applies to the
> `<cluster>/oplog/` segments, and transitioning them to GLACIER (or expiring them
> early) silently truncates your restorable window. Restrict the rule with a
> `Filter.Prefix` that excludes `oplog/`, and let `oplogRetentionHours` manage
> segment lifetime.

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
- Confirm the Secret uses the keys `access-key` / `secret-key` (other names are
  not read — the Job pod fails with `CreateContainerConfigError`)

### PITR Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| No oplog sidecar in the `mongod` pods | One of `backup.enabled` / `backup.pitrEnabled` / `oplogRetentionHours > 0` is unset — the operator skips injection silently | Check all three on the **cluster** CR; `oplogRetentionHours: 0` disables PITR |
| No oplog sidecar, but all three flags are set | `OPLOG_TAILER_IMAGE` is unset on the operator Deployment — the sidecar is skipped **fail-open** (a plain `mongo` image has no `aws` and would crash the pod) | Build/publish `oplog-tailer.Dockerfile` (`make oplog-tailer-image-push`) and set `OPLOG_TAILER_IMAGE` on the operator Deployment; the skip reason is on the cluster status |
| No segments under `<prefix><cluster>/oplog/` | Sidecar cannot reach S3 | Check the sidecar container logs and the S3 credentials Secret |
| `status.restorableWindow` empty | Window not computed yet, or the base backup has no `status.oplogStart` | A backup taken without `--oplog` cannot anchor PITR — take a fresh base backup |
| Window collapsed to a single instant | Oplog retention pruned the segments following the base snapshot, breaking the chain | Raise `oplogRetentionHours` above your backup interval; back up more often |
| Restore admitted but `Phase=Failed` | The webhook fails open on an unknown window; the Job is authoritative | Read `status.error`; verify a gap-free segment chain covers the target |
| Sharded restore returns a warning | PITR is ReplicaSet-only; shards have independent oplog timestamps | Expect per-shard best-effort, not a cluster-wide consistent point |
