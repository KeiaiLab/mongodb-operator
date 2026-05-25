<p align="center">
  <img src="https://github.com/keiailab.png" alt="keiailab" width="120"/>
</p>

# mongodb-operator

> **Kubernetes 的 Apache-2.0 MongoDB Operator — ReplicaSet + Sharded Cluster + 备份,原生 MongoDB 7.0+**

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License"/></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go Version"/></a>
  <a href="https://www.mongodb.com/"><img src="https://img.shields.io/badge/MongoDB-7.0%2B-47A248?logo=mongodb" alt="MongoDB"/></a>
  <a href="https://kubernetes.io/"><img src="https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes" alt="Kubernetes"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator"><img src="https://img.shields.io/badge/ghcr.io-keiailab%2Fmongodb--operator-blue?logo=github" alt="Container Image"/></a>
  <a href="https://github.com/keiailab/mongodb-operator"><img src="https://img.shields.io/badge/dynamic/yaml?url=https://raw.githubusercontent.com/keiailab/mongodb-operator/main/charts/mongodb-operator/Chart.yaml&label=helm%20v" alt="Helm Chart"/></a>
  <a href="https://artifacthub.io/packages/search?repo=mongodb-operator"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/mongodb-operator" alt="Artifact Hub"/></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/keiailab/mongodb-operator"><img src="https://api.scorecard.dev/projects/github.com/keiailab/mongodb-operator/badge" alt="OpenSSF Scorecard"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/discussions"><img src="https://img.shields.io/github/discussions/keiailab/mongodb-operator?label=discussions&logo=github" alt="GitHub Discussions"/></a>
  <a href="https://github.com/keiailab/operator-commons/blob/main/docs/quality/audit-history.md"><img src="https://img.shields.io/badge/keiailab-v3.x--stable-success?style=flat-square" alt="keiailab v3.x-stable"/></a>
  <a href="https://github.com/keiailab/operator-commons/blob/main/scripts/audit-production-grade.sh"><img src="https://img.shields.io/badge/audit-100%25-success?style=flat-square" alt="audit"/></a>
</p>

<p align="center">
  <a href="README.md">English</a> |
  <a href="README.ko.md">한국어</a> |
  <a href="README.ja.md">日本語</a> |
  <b>中文</b>
</p>

---

一个在 Kubernetes 上部署和管理 MongoDB ReplicaSet 与 Sharded Cluster 的 Kubernetes Operator。

> ## ⚠️ Beta 版本 — v1.3.2-beta.x (carve-out)
>
> 当前最新 release 为 **prerelease beta** — 在正式 1.4.0 GA 发布之前,建议仅在 *非生产数据* 场景下使用。
>
> **Beta scope (默认启用)**: MongoDB ReplicaSet
>
> **Beta scope 之外 (默认禁用,通过 RBAC + reconciler 的 feature gate 阻断)**:
> - `MongoDBSharded` — ConfigServer init / HPA ordering 未解决 (通过 `features.sharded.enabled=true` 启用)
> - `MongoDBBackup` — 自动化测试 0 件,connectionString 明文暴露风险 (通过 `features.backup.enabled=true` 启用)
> - HorizontalPodAutoscaler — RS/cfg drift mutex 缺失 (通过 `features.autoscaling.enabled=true` 启用)
>
> 详细的剩余风险请参阅 [CHANGELOG.md](CHANGELOG.md) 的 Known Issues 章节。

## 概述

MongoDB Operator 在 Kubernetes 上自动化 MongoDB 集群的部署、伸缩和运维。通过自定义资源定义 (CRD, Custom Resource Definitions) 提供以声明式方式管理 MongoDB 基础设施的方法。

### 功能

- **MongoDB ReplicaSet**: 部署具备自动故障转移 (automatic failover) 的 3 个以上成员的高可用 replica set
- **Sharded Cluster** *(Beta 中默认禁用)*: 部署包含 config server、shard 和 mongos router 的分布式集群
- **TLS 加密**: 通过 cert-manager 集成自动管理 TLS 证书
- **认证 (Authentication)**: 支持 keyfile 的 SCRAM-SHA-256 认证,用于集群内部通信
- **监控 (Monitoring)**: 支持 ServiceMonitor 的 Prometheus 指标导出
- **备份 / 恢复 (Backup / Restore)** *(Beta 中默认禁用)*: 自动备份到 S3 兼容存储或 PVC
- **自动伸缩 (Auto-scaling)**: 为 Mongos router 提供 Horizontal Pod Autoscaler 支持

## 架构

```
┌─────────────────────────────────────────────────────────────────┐
│                    MongoDB Operator                              │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │  MongoDB    │  │ MongoDBShar │  │    MongoDBBackup        │  │
│  │  Controller │  │ Controller  │  │    Controller           │  │
│  └──────┬──────┘  └──────┬──────┘  └───────────┬─────────────┘  │
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  Resource Builder                           ││
│  │  (StatefulSets, Deployments, Services, Secrets, Jobs)       ││
│  └─────────────────────────────────────────────────────────────┘│
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  MongoDB Package                            ││
│  │  (Executor, ReplicaSet, Auth, Sharding)                     ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
├─────────────────────────────────────────────────────────────────┤
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │  StatefulSet  │  │  StatefulSet  │  │  Deployment   │        │
│  │  (ReplicaSet) │  │  (Shards)     │  │  (Mongos)     │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
│                                                                  │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │   Services    │  │    Secrets    │  │  ConfigMaps   │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
└─────────────────────────────────────────────────────────────────┘
```

### 自动初始化

Operator 自动处理 MongoDB 集群的初始化。

**ReplicaSet 初始化:**
```
1. 创建 Keyfile Secret (用于内部认证)
2. 创建 ConfigMap (mongod.conf)
3. 创建 Service (headless + client)
4. 创建 StatefulSet
5. 等待所有 pod 就绪
6. 在 primary 候选上执行 rs.initiate()
7. 等待 primary 选举完成
8. 创建 admin 用户 (通过 localhost 例外)
```

**Sharded Cluster 初始化:**
```
1. 创建共享的 Keyfile Secret
2. 部署 Config Server StatefulSet (端口 27019)
3. 部署 Shard StatefulSet (端口 27018)
4. 部署 Mongos Deployment (端口 27017)
5. 初始化 Config Server ReplicaSet
6. 初始化每个 Shard 的 ReplicaSet
7. 在 Mongos 上创建 admin 用户
8. 为每个 shard 执行 sh.addShard()
```

### 端口配置

| 组件 | 端口 | Flag |
|-----------|------|------|
| Mongos | 27017 | - |
| Shard | 27018 | `--shardsvr` |
| Config Server | 27019 | `--configsvr` |

## 快速开始

### 前提条件

- Kubernetes 集群 v1.26+
- 已配置访问集群权限的 kubectl
- 按 *安装方式* 的额外条件:
  - **OLM v1** (推荐,现代方式): cert-manager 在线 + cluster admin (一次性 bootstrap)
  - **Helm**: Helm v3.8+
  - **OLM v0** (legacy): 介于 Helm 简洁性和 OLM v1 简洁性之间的折中方案 — *不推荐*

### 安装 — 3 种方式 (matrix)

| 方式 | 目标用户 | 现代性 | 步骤 |
|---|---|---|---|
| **OLM v1** *(推荐)* | 外部用户、GitOps 平台 (ArgoCD App-of-Apps)、Day-0 生产 | **下一代** (v1.8.0,2026-02 GA) | 2 个 manifest (ClusterCatalog + ClusterExtension) |
| Helm chart | 本地开发,单集群简单部署 | stable | 1 条命令 (`helm install`) |
| OLM v0 | OpenShift legacy,OperatorHub.io community | 维护模式 (v0.42,2026-04) | 4 个 manifest + InstallPlan approve |

**详细步骤**: 请参阅 [INSTALL.md](INSTALL.md)。本节为 *快速开始*。

#### 方式 1 — OLM v1 (现代标准,推荐)

```bash
# (1) OLM v1 集群安装 — 一次性 bootstrap
curl -L -s https://github.com/operator-framework/operator-controller/releases/latest/download/install.sh | bash -s

# (2) 应用 ClusterCatalog + ClusterExtension
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clustercatalog.yaml
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clusterextension.yaml

# (3) 验证安装
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s
```

#### 方式 2 — Helm chart

```bash
# 添加 Helm repository
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

# 安装 operator
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

<!-- 方式 3 (OLM v0 legacy) 已移除 — ADR-0028 Phase D,v1 only。请使用 helm 或 OLM v1。 -->


### 部署 MongoDB ReplicaSet

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
  monitoring:
    enabled: true
```

```bash
# 创建 namespace 和凭据
kubectl create namespace database
kubectl create secret generic mongodb-admin \
  --from-literal=username=admin \
  --from-literal=password=your-secure-password \
  -n database

# 部署 MongoDB
kubectl apply -f mongodb-replicaset.yaml
```

### 部署 Sharded Cluster

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
    service:
      type: LoadBalancer
```

## 自定义资源定义 (CRD)

### MongoDB (ReplicaSet)

| 字段 | 说明 | 默认值 |
|-------|-------------|---------|
| `spec.members` | replica set 成员数 | `3` |
| `spec.version.version` | MongoDB 版本 | `8.3.1` |
| `spec.storage.storageClassName` | 存储类 (storage class) 名称 | - |
| `spec.storage.size` | 每个成员的 PVC 大小 | `10Gi` |
| `spec.auth.mechanism` | 认证机制 | `SCRAM-SHA-256` |
| `spec.tls.enabled` | 启用 TLS | `false` |
| `spec.monitoring.enabled` | 启用 Prometheus 指标 | `false` |
| `spec.arbiter.enabled` | 启用 arbiter 节点 | `false` |

### MongoDBSharded

| 字段 | 说明 | 默认值 |
|-------|-------------|---------|
| `spec.configServer.members` | config server replica 数 | `3` |
| `spec.shards.count` | shard 数 | `2` |
| `spec.shards.membersPerShard` | 每个 shard 的成员数 | `3` |
| `spec.mongos.replicas` | Mongos router replica 数 | `2` |
| `spec.mongos.autoScaling.enabled` | 为 mongos 启用 HPA | `false` |

## 伸缩 (Scaling)

### 水平扩展 (新增 Shard)

Operator 支持动态 shard 伸缩。当增加 `spec.shards.count` 时,operator 会自动:

1. 创建新的 Shard StatefulSet 和 headless Service
2. 等待所有 pod 就绪
3. 初始化新 shard 的 ReplicaSet (`rs.initiate()`)
4. 通过 mongos 注册新 shard (`sh.addShard()`)
5. MongoDB balancer 自动将 chunk 迁移到新 shard

**示例: 从 3 个 shard 扩展到 5 个**

```bash
# 查看当前 shard 数
kubectl get mongodbsharded my-cluster -o jsonpath='{.spec.shards.count}'
# 输出: 3

# 扩展到 5 个 shard
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"shards":{"count":5}}}'

# 监控新 shard 的 pod
kubectl get pods -l app.kubernetes.io/component=shard

# 验证 shard 已注册
kubectl exec -it my-cluster-mongos-xxx -c mongos -- \
  mongosh -u admin -p $PASSWORD --eval 'sh.status()'
```

**状态追踪:**
```yaml
status:
  shardsInitialized: [true, true, true, true, true]
  shardsAdded: [true, true, true, true, true]
  shards:
    - name: my-cluster-shard-0
      phase: Running
    - name: my-cluster-shard-1
      phase: Running
    - name: my-cluster-shard-2
      phase: Running
    - name: my-cluster-shard-3
      phase: Running
    - name: my-cluster-shard-4
      phase: Running
```

### 垂直伸缩 (资源调整)

更新资源 requests/limits (触发 rolling restart):

```bash
kubectl patch mongodbsharded my-cluster --type='merge' -p '{
  "spec": {
    "shards": {
      "resources": {
        "requests": {"memory": "2Gi", "cpu": "1"},
        "limits": {"memory": "4Gi", "cpu": "2"}
      }
    }
  }
}'
```

### Mongos Replica 伸缩

扩展或缩减 mongos router:

```bash
# 扩展
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":3}}}'

# 缩减
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":1}}}'
```

## 资源建议

### 最低要求

| 组件 | 内存 | CPU | 备注 |
|-----------|--------|-----|-------|
| Config Server | 256Mi | 100m | 需要 3 个成员 |
| Shard 成员 | 512Mi | 250m | 每个 replica |
| Mongos | 512Mi | 250m | 256Mi 会导致 OOM |

### 生产推荐

| 组件 | 内存 | CPU | 存储 |
|-----------|--------|-----|---------|
| Config Server | 1Gi | 500m | 10Gi SSD |
| Shard 成员 | 4Gi | 2 | 100Gi+ SSD |
| Mongos | 1Gi | 500m | - |

## 已测试功能

状态标记说明:
- **✅ Stable**: envtest 回归 + 单元测试 + 实际 mongod workload (testcontainers / kind / 真实集群) 上完成负载 / 持久性验证。证据 (压力测试结果 / incident 复盘) 已保存。
- **✅ Implemented**: 代码 + envtest 回归 + 单元测试验证了 *功能性* 正确性。*负载验证由运维方负责*。
- **⚠️ Beta**: 代码可运行,但仅有部分单元测试,无真实环境验证 — 应用到生产环境之前需要额外验证。

| 功能 | 状态 | 备注 |
|---------|--------|-------|
| ReplicaSet 自动初始化 | ✅ Implemented | `rs.initiate()` 自动执行。envtest + driver 单元测试。 |
| Sharded cluster 初始化 | ✅ Implemented | Config server、shard、mongos。envtest 验证。 |
| Admin 用户创建 | ✅ Implemented | Driver-based bootstrap,采用 K8s Lease lock + bootstrap 后 usersInfo verify。 |
| Shard scale out (2→5) | ⚠️ Beta | 自动 `sh.addShard()` — driver 调用已验证,*真实集群负载* 未验证。 |
| Shard scale in (5→2) | ⚠️ Beta | 自动 `removeShard()` + ShardDraining condition + 资源清理 (PVC 保留)。chunk 迁移的 long-running polling 固定为 30s (未应用 backoff)。 |
| Mongos replica 伸缩 | ✅ Implemented | Deployment replica 变更 → rolling。 |
| 资源更新 | ✅ Implemented | 通过 STS UpdateStrategy 进行 rolling restart。 |
| 伸缩中的数据完整性 | ⚠️ Beta | 代码流程上阻断数据丢失 (PVC retain,removeShard drain wait) — *真实数据负载验证* 未执行。 |
| 伸缩中并发写入 | ⚠️ Beta | 无压力测试证据。计划基于 testcontainers-go 进行负载测试。 |
| PodDisruptionBudget 自动化 | ✅ Implemented | 通过 `spec.podDisruptionBudget` (MongoDB + Sharded) opt-in。builder 单元测试已验证 4 个组件的创建。 |
| NetworkPolicy 自动化 | ✅ Implemented | 通过 `spec.networkPolicy` (deny-by-default + 追加 peers) opt-in。单元测试已验证 cfg=27019 / shard=27018 / mongos=27017 端口。*实际通信阻断验证未执行*。 |
| Admin bootstrap race-free | ✅ Implemented | K8s Lease 分布式锁 (30s TTL) + bootstrap 后 `usersInfo` verify。fake-client 单元测试 (busy / takeover / release)。持有 pod 崩溃时,其他 reconcile 在 30s 内 backoff。 |

## 限制

### 暂未支持

| 功能 | 状态 | 应对方法 |
|---------|--------|------------|
| ReplicaSet 成员移除 | ❌ 未实现 | 需要手动执行 `rs.remove()` |
| 自动备份调度 | ❌ 计划中 | 使用外部 CronJob |
| 跨集群复制 | ❌ 计划中 | - |
| Sharded Arbiter / Hidden 拓扑 | ⚠️ 仅 ReplicaSet | MongoDB CR 支持 Arbiter;Sharded 扩展在 roadmap 上 |

### 已知问题

1. **Mongos 内存**: 建议最低 512Mi。256Mi 在负载下会导致 OOM。
2. **ReplicaSet 成员缩容不优雅**: 缩减 ReplicaSet 成员时未调用 `rs.remove()` — 仅减少 StatefulSet 的 replica 数。
3. **Scale-in 时 PVC 保留**: `removeShard` 完成后,已 drain 的 shard 的 PVC 会有意保留,以防止意外数据丢失。运维方需要在验证后手动删除。

### MongoDBBackup

| 字段 | 说明 | 默认值 |
|-------|-------------|---------|
| `spec.clusterRef.name` | 目标集群名称 | - |
| `spec.clusterRef.kind` | 目标集群类型 | `MongoDB` |
| `spec.type` | 备份类型 (full / incremental) | `full` |
| `spec.compression` | 启用压缩 | `true` |
| `spec.storage.type` | 存储类型 (s3 / pvc) | `s3` |

## 配置

### 使用 cert-manager 的 TLS

```yaml
spec:
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
```

### Prometheus 监控

```yaml
spec:
  monitoring:
    enabled: true
    prometheusRule:
      enabled: true
    serviceMonitor:
      interval: 30s
```

### 备份到 S3

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: daily-backup
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
      credentialsRef:
        name: s3-credentials
```

## 开发

### 前提条件

- Go 1.21+
- Docker
- kubectl
- Kind 或 Minikube (用于本地测试)

### 构建

```bash
# 构建 operator
make build

# 运行测试
make test

# 构建 Docker 镜像
make docker-build IMG=your-registry/mongodb-operator:tag

# 推送 Docker 镜像
make docker-push IMG=your-registry/mongodb-operator:tag
```

### 本地开发

```bash
# 安装 CRD
make install

# 本地运行 operator
make run

# 创建示例 MongoDB
kubectl apply -f config/samples/mongodb_replicaset.yaml
```

## 许可证

本项目采用 Apache License 2.0 许可证 — 详情请参阅 [LICENSE](LICENSE) 文件。

### 第三方许可证

本 operator 管理 MongoDB 数据库,但不包含或分发 MongoDB 软件。MongoDB Community Server 采用 [Server Side Public License (SSPL)](https://www.mongodb.com/licensing/server-side-public-license) 许可证。

**重要许可证说明:**
- 本 operator (Apache 2.0) 是编排 MongoDB 部署的独立软件
- MongoDB 容器镜像从 MongoDB 官方仓库拉取
- 用户负责遵守 MongoDB 的许可证条款
- 本 operator 不修改或重新分发 MongoDB 二进制文件

## 贡献

欢迎贡献!提交 pull request 之前请阅读我们的 [Contributing Guide](CONTRIBUTING.md),了解我们的行为准则以及提交流程。

## 支持

- **Issues**: [GitHub Issues](https://github.com/keiailab/mongodb-operator/issues)
- **Discussions**: [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)

## Roadmap

- [x] ReplicaSet 自动初始化
- [x] Sharded Cluster 自动初始化
- [x] 水平 shard 伸缩 (scale out)
- [x] Admin 用户自动创建
- [ ] Point-in-Time Recovery (PITR)
- [ ] 自动版本升级
- [ ] 跨集群复制
- [ ] Grafana dashboard 模板
- [ ] 使用 CronJob 进行备份调度
- [ ] 包含数据迁移的 scale down

## 致谢

- [Kubernetes](https://kubernetes.io/)
- [Operator SDK](https://sdk.operatorframework.io/)
- [MongoDB](https://www.mongodb.com/)
- [Bitnami MongoDB Charts](https://github.com/bitnami/charts) — 提供灵感来源

## 参考

- [English README](README.md) — canonical SSOT (正本)
- [한국어 README](README.ko.md) — 韩文版
- [日本語 README](README.ja.md) — 日文版
- [INSTALL.md](INSTALL.md) — 安装详情
- [DESIGN.md](DESIGN.md) — 设计文档
- [CHANGELOG.md](CHANGELOG.md) — 变更历史

