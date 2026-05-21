<p align="center">
  <img src="https://keiailab.com/assets/logo.svg" alt="keiailab" width="120"/>
</p>

# mongodb-operator (中文)

> [English](README.md) | [한국어](README.ko.md) | [日本語](README.ja.md) (placeholder) | **中文** (placeholder)

> **Apache-2.0 用于 Kubernetes 的 MongoDB Operator — ReplicaSet + Sharded Cluster + Backup,vanilla MongoDB 7.0+**

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="许可证"/></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go 版本"/></a>
  <a href="https://www.mongodb.com/"><img src="https://img.shields.io/badge/MongoDB-7.0%2B-47A248?logo=mongodb" alt="MongoDB"/></a>
  <a href="https://kubernetes.io/"><img src="https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes" alt="Kubernetes"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator"><img src="https://img.shields.io/badge/ghcr.io-keiailab%2Fmongodb--operator-blue?logo=github" alt="容器镜像"/></a>
</p>

---

> **状态**: `[~]` 部分实现 (placeholder) — RFC-0025 §1.2 复选框含义.
> native reviewer 质量验证后升级到 `[x]` 完成状态 candidate.

## 概述

mongodb-operator 是一个在 Kubernetes 上自动化部署和管理 MongoDB 集群
(ReplicaSet 和 Sharded Cluster) 的 Operator。通过 CRD (Custom Resource
Definitions) 以声明式方式管理 MongoDB 基础设施。

详细信息请参阅 [English README](README.md) 的 "Overview" 章节。

## 功能

- **MongoDB ReplicaSet**: 部署具备自动故障转移的 3 个以上成员高可用 replica
  set
- **Sharded Cluster** *(beta 默认关闭)*: 部署包含 config server / shard /
  mongos router 的分布式集群
- **TLS 加密**: 通过 cert-manager 集成自动管理 TLS 证书
- **认证**: 集群内部通信支持 keyfile 的 SCRAM-SHA-256 认证
- **监控**: 支持 ServiceMonitor 的 Prometheus 指标导出
- **备份/恢复** *(beta 默认关闭)*: 自动备份到 S3 兼容存储或 PVC
- **自动伸缩**: Mongos router 的 Horizontal Pod Autoscaler 支持

功能表面详细请参阅 [English README](README.md)。

## ⚠️ Beta 版本 — v1.3.2-beta.x (carve-out)

当前最新 release 为 **prerelease beta** — 在正式 1.4.0 GA 发布前,建议仅在
*非生产数据* 场景下使用。

**Beta scope (默认启用)**: MongoDB ReplicaSet

**Beta scope 外 (默认禁用,通过 RBAC + reconciler feature gate 阻断)**:
- `MongoDBSharded` — ConfigServer init/HPA ordering 未解决 (`features.sharded.enabled=true` 启用)
- `MongoDBBackup` — 自动测试 0 项,connectionString 明文暴露风险 (`features.backup.enabled=true` 启用)
- HorizontalPodAutoscaler — RS/cfg drift mutex 缺失 (`features.autoscaling.enabled=true` 启用)

详细的剩余风险请参阅 [CHANGELOG.md](CHANGELOG.md) 的 Known Issues 章节。

## 安装

提供 3 种 install path (OLM v1 / Helm / OLM v0 legacy)。详细信息请参阅
[INSTALL.md](INSTALL.md) 和 [English README](README.md) 的 "Installation" 章节。

快速示例 (Helm chart):

```bash
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

## 快速开始

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

Sharded Cluster 示例、CRD 字段列表、伸缩步骤、TLS 配置、监控设置和
开发工作流等详细信息请参阅 [English README](README.md)。Native reviewer
翻译完成后本 placeholder 将扩展为完整版本。

## 参考

- [English README](README.md) — canonical SSOT
- [한국어 README](README.ko.md) — 韩文版
- [日本語 README](README.ja.md) — 日文版 (placeholder)
- [INSTALL.md](INSTALL.md) — 安装详情
- [DESIGN.md](DESIGN.md) — 设计文档
- [CHANGELOG.md](CHANGELOG.md) — 变更历史
- [Glossary (中文)](../operator-commons/docs/i18n/glossary-zh.md) — 标准术语表
  (operator-commons 仓库,placeholder 状态)

## 许可证

Apache-2.0 — 请参阅 [LICENSE](LICENSE)。

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a> ·
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/forgewise">forgewise</a>
</p>

<p align="center">© 2026 keiailab · Apache-2.0 · <a href="https://keiailab.com">keiailab.com</a></p>
