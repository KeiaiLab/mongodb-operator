---
title: "We Built an Open-Source MongoDB Operator Because Neither Percona Nor the Community Operator Was Enough"
published: false
description: "An Apache-2.0 Kubernetes operator for MongoDB that handles ReplicaSets, Sharded Clusters, Backup, TLS, LDAP, and monitoring — all declaratively. Here's why we built it and how it compares."
tags: kubernetes, mongodb, opensource, devops
cover_image: https://github.com/keiailab/mongodb-operator/raw/main/docs/branding/cover.png
canonical_url: https://github.com/keiailab/mongodb-operator
---

## The Problem

You want to run MongoDB on Kubernetes. You have three options:

1. **MongoDB Community Operator** — No backup automation. No sharding. SSPL-licensed images.
2. **Percona Operator** — Feature-rich, but tied to Percona Server for MongoDB (not vanilla MongoDB). Some features require commercial support.
3. **Bitnami Helm Chart** — Static manifests. No reconciliation loop. Scale out requires manual `helm upgrade`.

None of them gave us what we needed: **a fully declarative, Apache-2.0 operator for vanilla MongoDB 8.x with built-in backup, sharding, and monitoring**.

So we built one.

## What Is mongodb-operator?

[**mongodb-operator**](https://github.com/keiailab/mongodb-operator) is a Kubernetes operator that manages MongoDB clusters through Custom Resource Definitions:

- **`MongoDB`** — ReplicaSet (3+ members, automatic failover, arbiter support)
- **`MongoDBSharded`** — Config servers + Shards + Mongos routers
- **`MongoDBBackup`** — Automated backups to S3 or PVC

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDB
metadata:
  name: my-mongodb
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
  monitoring:
    enabled: true
```

Apply this YAML. The operator handles everything: StatefulSet creation, `rs.initiate()`, admin user bootstrap (via Lease-based distributed lock), TLS certificate management, and Prometheus metrics export.

## How It Compares

We ran a detailed [gap analysis](https://github.com/keiailab/mongodb-operator/blob/main/docs/gap-analysis.md) against Bitnami's mongodb-sharded chart (32 features). Results:

| Category | Score |
|---|---|
| Features at parity or better | **29/32** |
| Partial support | 3 |
| Not supported | 0 |

Here's where we're **ahead** of every alternative:

### 1. Built-in Backup CRD

MongoDB Community Operator: no backup. Percona: requires PBM. Bitnami: "use Velero."

We have a first-class `MongoDBBackup` CRD:

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBBackup
metadata:
  name: daily-backup
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
      credentialsRef:
        name: s3-credentials
```

PITR (Point-in-Time Recovery) is supported via oplog tailer sidecar + `--oplogReplay` restore.

### 2. LDAP Authentication

Neither Percona nor Bitnami expose LDAP configuration for open-source MongoDB. We do:

```yaml
spec:
  auth:
    mechanism: PLAIN
    ldap:
      servers: "ldap.example.com:636"
      tls: true
      bindCredentialsSecretRef:
        name: ldap-bind
```

### 3. Automatic Shard Registration

Change `spec.shards.count` from 3 to 5. The operator:
1. Creates new StatefulSets
2. Waits for pods to be ready
3. Calls `rs.initiate()` on each new shard
4. Calls `sh.addShard()` to register with mongos

No manual intervention. No `helm upgrade`. Just change one number.

### 4. Full Observability Stack

- Prometheus exporter sidecar (Percona's mongodb_exporter)
- `ServiceMonitor` for Prometheus Operator
- `PrometheusRule` with production alert rules
- **4 Grafana dashboards** (cluster overview, replicaset, sharded, operational)

Bitnami only has a `PodMonitor`. Percona pushes you toward PMM (their commercial monitoring platform).

### 5. Security Hardened

- NetworkPolicy automation (deny-by-default + configurable peers)
- PodDisruptionBudget per component
- PodSecurity restricted profile
- Volume permissions init container for non-root clusters
- Priority classes, topology spread, diagnostic mode

## Production Evidence

This isn't a weekend project. We run a **21-pod production sharded cluster** on our infrastructure:

```
MongoDBSharded: argos-mongo
├── Config Servers: 3 (StatefulSet, ceph-rbd)
├── Shards: 5 × 3 members = 15 (StatefulSet, ceph-rbd, 1TB each)
├── Mongos: 3 (Deployment, TLS + SCRAM-SHA-256)
└── Status: Running (18+ days uptime, zero data loss)
```

MongoDB version: 8.3.2. TLS enabled. PDB active. Priority class: `system-cluster-critical`.

## Architecture

The operator uses **mongo-go-driver v2** for all MongoDB operations — no `kubectl exec`, no shell scripts piped into pods. This eliminates an entire class of injection vulnerabilities.

```
┌─────────────────────────────────────────┐
│         MongoDB Operator                │
├─────────────────────────────────────────┤
│  MongoDB Controller                     │
│  MongoDBSharded Controller              │
│  MongoDBBackup Controller               │
├─────────────────────────────────────────┤
│  Resource Builder (STS, Deploy, Svc)    │
│  MongoDB Package (driver-based ops)     │
└────────────────┬────────────────────────┘
                 │ reconcile loop
                 ▼
┌─────────────────────────────────────────┐
│  StatefulSets + Deployments + Services  │
│  Secrets + ConfigMaps + Jobs            │
└─────────────────────────────────────────┘
```

## Quick Start

### Option 1: Helm (simplest)

```bash
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

### Option 2: OLM v1 (recommended for GitOps)

```bash
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.9.0/deploy/olm-v1/clustercatalog.yaml
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.9.0/deploy/olm-v1/clusterextension.yaml
```

## Tech Stack

| Component | Version |
|---|---|
| Go | 1.26 |
| controller-runtime | v0.24.1 |
| client-go | v0.36.1 |
| mongo-go-driver | v2.6.0 |
| Kubernetes | 1.26+ |
| MongoDB | 8.x (vanilla) |

CRD API: `v1beta1` (graduated from v1alpha1).

## What's Next

- [ ] Automated version upgrades (rolling, canary)
- [ ] Cross-cluster replication (MongoDBFederation CRD)
- [ ] OperatorHub.io listing
- [ ] SLSA provenance + Sigstore image signing

## Get Involved

- **GitHub**: [keiailab/mongodb-operator](https://github.com/keiailab/mongodb-operator)
- **Discussions**: [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)
- **Docs**: [Documentation](https://github.com/keiailab/mongodb-operator/tree/main/docs)
- **Artifact Hub**: [mongodb-operator](https://artifacthub.io/packages/search?repo=mongodb-operator)

Contributions welcome. Star the repo if you find it useful.

---

*mongodb-operator is Apache-2.0 licensed. It manages vanilla MongoDB — no vendor lock-in, no SSPL constraints, no commercial upsell.*
