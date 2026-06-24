# MongoDB Search (`$search` / `$vectorSearch`)

> **Public Preview.** The Search feature ships behind a feature gate and uses the
> Community `mongot` engine as a sidecar. APIs are stable but the engine is still
> evolving — validate in staging before production rollout.

## Overview

MongoDB Operator brings declarative full-text (`$search`) and vector (`$vectorSearch`)
search to self-hosted clusters via two CRDs:

- **`MongoDBSearch`** — enables the `mongot` search engine for a source cluster
  (`MongoDB` ReplicaSet or `MongoDBSharded`). The operator runs `mongot` as a
  **sidecar** in each source `mongod` pod (the Community `mongot` connects to
  `localhost`, so a separate StatefulSet is not compatible).
- **`MongoDBSearchIndex`** — declares a single `$search` or `$vectorSearch` index
  on a collection. The operator reconciles `createSearchIndex` / `updateSearchIndex`
  / `dropSearchIndex` against the source cluster, which forwards to `mongot`.

```
MongoDBSearchIndex ──▶ MongoDBSearch ──▶ source MongoDB / MongoDBSharded
   (index lifecycle)     (mongot sidecar)    (mongod + mongot sidecar)
```

The `mongot` index store lives on the **`mongod` data PVC** under the `search-index`
subPath (not an `emptyDir`/node disk) so indexes survive reschedules and are not
affected by node disk pressure.

## Enabling the Search controllers

Search is **off by default**. Enable it with the operator feature gate:

```
--enable-search-controller=true
```

This single flag activates both the `MongoDBSearch` and `MongoDBSearchIndex`
controllers. The `MongoDBSearchIndex` validating webhook (which checks
`definitionJSON` and rejects mutation of immutable fields) is gated **separately**
by `--enable-webhooks=true` and requires cert-manager for its serving certificate.
Set the flag(s) on the operator Deployment (or the corresponding Helm value) and
restart the controller manager.

## `MongoDBSearch`

Enables `mongot` for a source cluster.

| Field | Description | Default |
|-------|-------------|---------|
| `spec.source.kind` | `MongoDB` (ReplicaSet) or `MongoDBSharded` | `MongoDB` |
| `spec.source.mongodbResourceRef.name` | Name of the source cluster CR | - |
| `spec.version.version` | `mongot` image version override | operator default |
| `spec.replicas` | `mongot` count (RS: total; Sharded: per-shard). `>1` needs an L7 gRPC LB (future — keep `1`) | `1` |
| `spec.resources` | `mongot` container resource requests/limits | - |
| `spec.tls` | `mongod`↔`mongot` TLS; follows the source cluster TLS when unset | source |
| `spec.syncUserSecretRef.name` | `searchCoordinator` credentials Secret. **Omit to let the operator auto-create and manage it** | auto-create |

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBSearch
metadata:
  name: my-search
  namespace: database
spec:
  source:
    kind: MongoDB
    mongodbResourceRef:
      name: my-mongodb
  # syncUserSecretRef omitted → operator auto-creates the searchCoordinator user
```

The `MongoDBSearch` status reports readiness based on the **actual `mongot`
sidecar readiness** (not just cluster phase):

- `Provisioning` — sidecars not injected yet (rolling).
- `Syncing` — some sidecars ready.
- `Ready` — all sidecars ready. **Only then are indexes built.**
- `Degraded` — sidecars present but none ready.

### Sync user (auto-create vs. user-managed)

`mongot` authenticates to `mongod` as a `searchCoordinator` user.

- **Auto-create (default):** when `spec.syncUserSecretRef` is omitted, the operator
  creates a `<name>-search-sync` Secret and a `search-sync` user with **dual SCRAM
  (SHA-1 + SHA-256)** mechanisms (the `mongot` engine requires SCRAM-SHA-1). The
  user name is fixed to `search-sync` and is not taken from the Secret, to prevent
  privilege escalation via a pre-staged Secret.
- **User-managed:** provide `spec.syncUserSecretRef` pointing to a Secret with
  `username`/`password` keys. The operator will not manage the `mongod` user.

## `MongoDBSearchIndex`

Declares one search index on a collection.

| Field | Description | Default |
|-------|-------------|---------|
| `spec.searchRef.name` | The `MongoDBSearch` that hosts this index | - |
| `spec.database` | Target database | - |
| `spec.collection` | Target collection | - |
| `spec.indexName` | Index name (the `index` argument of `$search`/`$vectorSearch`) | - |
| `spec.type` | `search` (full-text) or `vectorSearch` | `search` |
| `spec.definitionJSON` | Index definition as JSON | - |

The status `phase` follows the `mongot` index state: `Pending` → `Building` →
`Ready` (queryable) / `Failed`. Deleting the CR drops the index (finalizer).

### Full-text (`$search`) example

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBSearchIndex
metadata:
  name: movies-text
  namespace: database
spec:
  searchRef:
    name: my-search
  database: sample
  collection: movies
  indexName: default
  type: search
  definitionJSON: |
    {"mappings":{"dynamic":true}}
```

```javascript
db.movies.aggregate([
  { $search: { index: "default", text: { query: "godfather", path: "title" } } }
])
```

### Vector (`$vectorSearch`) example

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBSearchIndex
metadata:
  name: items-vector
  namespace: database
spec:
  searchRef:
    name: my-search
  database: testdb
  collection: items
  indexName: vector_index
  type: vectorSearch
  definitionJSON: |
    {"fields":[{"type":"vector","path":"embedding","numDimensions":1536,"similarity":"cosine"}]}
```

```javascript
db.items.aggregate([
  { $vectorSearch: {
      index: "vector_index",
      path: "embedding",
      queryVector: [/* 1536 floats */],
      numCandidates: 100,
      limit: 10
  } }
])
```

## Sharded clusters

For a `MongoDBSharded` source, the operator injects a `mongot` sidecar into **each
shard** replicaSet. Each shard's `mongot` indexes only that shard's data, and
`mongos` fans out `$search`/`$vectorSearch` across shards.

```yaml
apiVersion: mongodb.keiailab.com/v1beta1
kind: MongoDBSearch
metadata:
  name: sharded-search
  namespace: database
spec:
  source:
    kind: MongoDBSharded
    mongodbResourceRef:
      name: my-sharded
```

Notes:

- Shard `mongod` listens on **port 27018** (not 27017); the operator wires each
  shard's `mongot` syncSource accordingly.
- The config server runs **no** `mongot` (metadata only).
- Index commands are issued **through `mongos`** (`<name>-mongos:27017`), which
  propagates them to every shard's `mongot` — never to individual shards directly.
- Enabling search on a running sharded cluster rolls each shard sequentially to add
  the sidecar; a maintenance window is recommended for large shard counts.

## Troubleshooting

| Symptom | Likely cause | Action |
|---------|--------------|--------|
| `MongoDBSearch` stuck in `Provisioning`/`Degraded` | sidecars not injected/ready | check the source `mongod` pods for a `mongot` container; inspect `mongot` container logs |
| `MongoDBSearchIndex` stuck in `Building` | `mongot` still building / replicating | wait; verify the source cluster is `Running` and `MongoDBSearch` is `Ready` |
| `$vectorSearch` returns no results | index not `queryable` yet, or data inserted before the index synced | wait for `MongoDBSearchIndex` `Ready`; re-query |
| `mongot` replication paused | node disk pressure | ensure node disk headroom; the index store uses the `mongod` data PVC `search-index` subPath, so size `spec.storage.size` on the source for index growth |
| Index command fails on auth | `searchCoordinator` user missing | with auto-create, ensure the source is `Running` (the user is created best-effort once reachable); otherwise verify your `syncUserSecretRef` |
