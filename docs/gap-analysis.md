# Gap Analysis: mongodb-operator vs Bitnami mongodb-sharded

Feature comparison between this project (`mongodb-operator` v1.5.0) and [Bitnami `mongodb-sharded` Helm chart 9.4.12](https://artifacthub.io/packages/helm/bitnami/mongodb-sharded).

## Overview

| Aspect | Bitnami `mongodb-sharded` | `mongodb-operator` |
|---|---|---|
| Abstraction | Helm chart (static manifests) | Kubernetes Operator (CRD + reconciliation loop) |
| Deployment unit | Helm release | `MongoDB`, `MongoDBSharded`, `MongoDBBackup` CR |
| Lifecycle automation | `helm upgrade` trigger | Spec change triggers automatic reconciliation |
| Container base | Bitnami Secure Images (Photon) | Official `mongo:8.x` + `ghcr.io/keiailab/mongodb-operator` |

## Feature Matrix

Legend: ✅ Equivalent or better · ⚠️ Partial · ❌ Not supported · ⚪ Neither supports

| # | Feature | Bitnami 9.4.12 | mongodb-operator v1.5.0 | Status | Evidence |
|---|---|---|---|---|---|
| 1 | Sharded topology | configsvr + shards + mongos + arbiter + hidden | `MongoDBSharded` CRD: configServer + shards + mongos + **arbiter (ShardArbiterSpec)** | ✅ | `api/v1alpha1/mongodbsharded_types.go:182` |
| 2 | ReplicaSet standalone | Separate chart (`bitnami/mongodb`) | `MongoDB` CRD — first-class ReplicaSet with arbiter | ✅ Better | `api/v1alpha1/mongodb_types.go:109` |
| 3 | SCRAM authentication | `auth.rootUser/rootPassword` | `AuthSpec.Mechanism: SCRAM-SHA-256` + Secret ref | ✅ | `api/v1alpha1/common_types.go:307` |
| 4 | X.509 authentication | Partial | `AuthSpec.Mechanism: X509` + cert-manager | ✅ | `api/v1alpha1/common_types.go` X509 enum |
| 5 | LDAP authentication | Not supported | `AuthSpec.LDAP` — servers, bind credentials, TLS, DN mapping | ✅ Better | `api/v1alpha1/common_types.go:332` |
| 6 | TLS / cert-manager | `tls.enabled`, auto-gen | `TLSSpec` with cert-manager Issuer integration | ✅ | `internal/resources/builder.go` TLS args |
| 7 | Persistence (PVC) | storageClass, size, accessModes, subPath, selector | `StorageSpec.{size, storageClassName}` | ⚠️ | Missing accessModes/subPath/selector |
| 8 | PVC retention policy | `persistentVolumeClaimRetentionPolicy` | `StorageSpec.PersistentVolumeClaimRetentionPolicy` | ✅ | `common_types.go:89`, 3 builders + test |
| 9 | Prometheus exporter | Sidecar mongodb_exporter | Sidecar Percona exporter via `MonitoringSpec` | ✅ | `internal/resources/builder.go` |
| 10 | ServiceMonitor | PodMonitor only | ServiceMonitor + **PrometheusRule** (alerts) | ✅ Better | `charts/mongodb-operator/templates/` |
| 11 | Grafana dashboards | Not included | 4 built-in dashboards (overview, replicaset, sharded, operational) | ✅ Better | `charts/mongodb-operator/dashboards/` |
| 12 | Built-in backup | Not supported (Velero recommended) | `MongoDBBackup` CRD — mongodump, S3/PVC, full/incremental | ✅ Better | `api/v1alpha1/` + `internal/controller/` |
| 13 | NetworkPolicy | `networkPolicy.enabled` (default on) | `NetworkPolicySpec` with Enabled + AdditionalIngressFrom | ✅ | `api/v1alpha1/common_types.go:1068` |
| 14 | PodDisruptionBudget | `pdb.create` per component | `PodDisruptionBudgetSpec` for MongoDB + Sharded | ✅ | `internal/resources/builder.go` BuildPDB |
| 15 | Affinity / anti-affinity | Preset (soft/hard) + custom | Default pod anti-affinity + raw `corev1.Affinity` | ✅ | `common_types.go:955` |
| 16 | Topology spread | All components | `TopologySpreadConstraints` via PodSpec | ✅ | `common_types.go:975` |
| 17 | PriorityClassName | Per component | `PriorityClassName` via PodSpec | ✅ | `common_types.go:967` |
| 18 | Sidecars / InitContainers | Per component | `Sidecars` + `InitContainers` fields | ✅ | `common_types.go:986-996` |
| 19 | Custom MongoDB config | `*.config` inline or ConfigMap | `ConfigMapRef` for external ConfigMap | ⚠️ | No inline config support |
| 20 | Diagnostic mode | `diagnosticMode.enabled` (sleep infinity) | `DiagnosticModeSpec` — replaces mongod with sleep | ✅ | `common_types.go:1061-1066` |
| 21 | Horizontal scale-out | Manual `helm upgrade` | Automatic via spec change + `sh.addShard()` | ✅ Better | Reconciler auto-registers shards |
| 22 | Scale-in (shard removal) | Manual | `removeShard()` with ShardDraining condition | ⚠️ | Beta — long-polling 30s fixed |
| 23 | Mongos StatefulSet option | `mongos.useStatefulSet` toggle | `MongosSpec.UseStatefulSet` + `BuildMongosStatefulSet()` | ✅ | `mongodbsharded_types.go:267`, `builder.go:1530` |
| 24 | Mongos service-per-replica | `mongos.servicePerReplica.enabled` | Not supported | ❌ | |
| 25 | External config servers | `configsvr.external.host` | Not supported (always in-cluster) | ❌ | |
| 26 | Volume permissions init | `volumePermissions.enabled` | `VolumePermissionsSpec` — PSA restricted PVC ownership | ✅ | `common_types.go:1016`, `builder.go:594` |
| 27 | Resource presets | 8-level presets (nano~2xlarge) | Direct requests/limits only | ❌ | Convenience gap |
| 28 | SessionAffinity | Per component | `MongosServiceSpec.SessionAffinity` | ✅ | `mongodbsharded_types.go:311` |
| 29 | RS member removal | User responsibility | `RemoveMember()` via `replSetReconfig` | ✅ | `replicaset.go:264` + test |
| 30 | Password rotation | Manual | Not automated | ⚪ | Both manual |
| 31 | OpenShift compatibility | `adaptSecurityContext` | Not explicitly tested | ⚠️ | |
| 32 | License | Apache-2.0 | Apache-2.0 | ✅ | |

## Summary

| Category | Count |
|---|---|
| ✅ Equivalent or better | 26 |
| ⚠️ Partial | 3 |
| ❌ Not supported | 3 |

**Operator advantages** (features Bitnami lacks):
1. Built-in `MongoDBBackup` CRD with S3/PVC support
2. Declarative horizontal scaling with automatic `sh.addShard()`
3. PrometheusRule + 4 Grafana dashboards bundled
4. LDAP authentication support
5. mongo-go-driver v2 based (zero `pods/exec` permissions)
6. First-class `MongoDB` ReplicaSet CRD
7. RS member removal via `replSetReconfig` (driver-level)
8. Volume permissions init container for PSA restricted clusters

**Remaining gaps** (3 items):
1. Mongos service-per-replica (#24)
2. External config server support (#25)
3. Resource presets convenience (#27)

## Migration Guide

Key field mappings from Bitnami `values.yaml` to operator CRDs:

| Bitnami values.yaml | Operator CRD field |
|---|---|
| `shards: 2` | `MongoDBSharded.spec.shards.count` |
| `shardsvr.dataNode.replicaCount: 3` | `MongoDBSharded.spec.shards.membersPerShard` |
| `configsvr.replicaCount: 3` | `MongoDBSharded.spec.configServer.members` |
| `mongos.replicaCount: 2` | `MongoDBSharded.spec.mongos.replicas` |
| `auth.rootUser` / `auth.rootPassword` | `auth.adminCredentialsSecretRef` (K8s Secret) |
| `tls.enabled: true` | `tls.enabled: true` + `tls.certManager.issuerRef` |
| `metrics.enabled: true` | `monitoring.enabled: true` |
| `persistence.size: 8Gi` | `storage.size: 8Gi` |
| `networkPolicy.enabled: true` | `networkPolicy.enabled: true` |
| `shardsvr.arbiter.replicaCount: 1` | `shards.arbiter.enabled: true` |
| `diagnosticMode.enabled` | `diagnosticMode.enabled: true` |

## Verification

```bash
# Verify CRD fields exist
grep -n "NetworkPolicy\|PodDisruptionBudget\|Arbiter\|DiagnosticMode\|LDAP\|Sidecar" api/v1alpha1/*.go

# Verify builder functions
grep -n "BuildMongoDBNetworkPolicy\|BuildMongoDBPDB\|BuildShardedPDBs" internal/resources/builder.go

# Verify Bitnami latest
helm show values bitnami/mongodb-sharded --version 9.4.12 | head -50
```

## References

- Bitnami chart source: https://github.com/bitnami/charts/tree/main/bitnami/mongodb-sharded
- ArtifactHub: https://artifacthub.io/packages/helm/bitnami/mongodb-sharded
- CRD definitions: `api/v1alpha1/{mongodb,mongodbsharded,mongodbbackup,common}_types.go`
- [Roadmap](roadmap.md)
