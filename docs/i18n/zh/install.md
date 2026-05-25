<p align="center">
  <a href="(../install.md)">English</a> |
  <a href="INSTALL.ko.md">한국어</a> |
  <a href="INSTALL.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 安装指南

> mongodb-operator 面向 *external user* 的运维级安装指南。3 path matrix + 详细步骤 + Day-2 upgrade/rollback。

本文档为 *外部用户* (单独 cluster 运维者) 的安装步骤。KeiaiLab Cluster 内部应用的 *实时 evidence* 请参阅 [deploy/olm-v1/README.md](deploy/olm-v1/README.md) (OLM v1 ✓ 运行中)。

> **BREAKING (ADR-0028 Phase D, 2026-05-17)**: OLM v0 cluster install path + community-operators upstream sync 自动化已永久废弃。Path 3 (OLM v0) 章节已移除。外部用户应使用 *OLM v1 (Path 1)* 或 *Helm (Path 2)*。详情请参阅 [Changelog](../changelog.md) 的 BREAKING CHANGES 章节,以及 [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) Phase D。

## §1 2-Path Matrix

| Method | Modernity | GitOps 一致性 | Multi-tenancy | Upgrade | 推荐对象 |
|---|---|---|---|---|---|
| **OLM v1** | 🟢 next-generation (v1.8, 2026-02 GA) | 🟢 native (ArgoCD App source = ClusterExtension manifest) | 🟢 ClusterExtension per ns | 🟢 catalog.channels + version pin/range | external users, production GitOps |
| Helm chart | 🟡 stable | 🟢 native (ArgoCD App source = Helm chart) | 🟡 multi-release | 🟢 `helm upgrade` | local dev, single-cluster |

**选择标准**:
- *最新 K8s 环境 + GitOps + 对外部用户公开* → **OLM v1** (Path 1)
- *简单的 single-cluster dev / non-OLM cluster* → Helm (Path 2)
- *OpenShift 4.x / OperatorHub.io community-operators 用户*: 已停止支持。需要迁移到 helm 或 OLM v1。

## §2 Path 1 — OLM v1 (recommended)

### §2.1 Prerequisites

- Kubernetes v1.26+
- `cert-manager` v1.20+ (operator-controller dependency,[install guide](https://cert-manager.io/docs/installation/))
- cluster admin (1 次 bootstrap)
- private registry pull credentials (当前 release 的 catalog/bundle image 位于 `internal` GHCR — 需要 public 化或 imagePullSecret)

### §2.2 OLM v1 cluster bootstrap (1 次)

```bash
# 官方 install.sh — 集成安装 cert-manager + operator-controller + catalogd。
# 若 cert-manager 已运行,可跳过 install.sh 的 cert-manager 部分,直接 apply operator-controller.yaml。
curl -L -s https://github.com/operator-framework/operator-controller/releases/latest/download/install.sh | bash -s

# 或使用已有的 cert-manager:
kubectl create ns cert-manager --dry-run=client -o yaml | kubectl apply -f -
kubectl apply --server-side=true --force-conflicts \
  -f https://github.com/operator-framework/operator-controller/releases/download/v1.8.0/operator-controller.yaml
kubectl rollout status -n olmv1-system deployment/operator-controller-controller-manager --timeout=180s
kubectl rollout status -n olmv1-system deployment/catalogd-controller-manager --timeout=180s
```

### §2.3 ClusterCatalog (FBC catalog)

```yaml
# clustercatalog-keiailab.yaml
apiVersion: olm.operatorframework.io/v1
kind: ClusterCatalog
metadata:
  name: keiailab-operators
spec:
  priority: 0
  source:
    type: Image
    image:
      ref: ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0
      pollIntervalMinutes: 10
```

```bash
kubectl apply -f clustercatalog-keiailab.yaml
kubectl wait --for=condition=Serving=True clustercatalog/keiailab-operators --timeout=120s
```

### §2.4 ClusterExtension (operator install)

```yaml
# clusterextension-mongodb.yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: mongodb-system
  labels:
    pod-security.kubernetes.io/enforce: restricted
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mongodb-operator-installer
  namespace: mongodb-system
---
# *Note*: 生产环境推荐使用 narrow ClusterRole (bundle-derived)。
# 本示例使用 *cluster-admin* 进行简化 — installer 自身具备较广权限。
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: mongodb-operator-installer-cluster-admin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
  - kind: ServiceAccount
    name: mongodb-operator-installer
    namespace: mongodb-system
---
apiVersion: olm.operatorframework.io/v1
kind: ClusterExtension
metadata:
  name: mongodb-operator
spec:
  namespace: mongodb-system
  serviceAccount:
    name: mongodb-operator-installer
  source:
    sourceType: Catalog
    catalog:
      packageName: mongodb-operator
      version: 1.5.0  # 或 channel-based: channels: [stable]
```

```bash
kubectl apply -f clusterextension-mongodb.yaml
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s

# 验证 operator pod healthy
kubectl get pod -n mongodb-system -l app.kubernetes.io/name=mongodb-operator
```

### §2.5 Day-2 — Upgrade

```bash
# Version pin → 直接跳转到新 version
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"version":"1.6.0"}}}}'

# 或 channel-based — catalog 在 stable channel 发布新 version 时自动 upgrade
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"channels":["stable"],"version":""}}}}'
```

### §2.6 Rollback

```bash
# patch 到先前 version
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"version":"1.4.23"}}}}'

# 或完全移除 ClusterExtension + CRDs (CR 也会一并被删除 — 请注意)
kubectl delete clusterextension mongodb-operator
kubectl delete -f clusterextension-mongodb.yaml  # ns + SA + RBAC
```

### §2.7 GitOps (ArgoCD App-of-Apps)

```yaml
# argocd-app-mongodb-operator.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: mongodb-operator-olm-v1
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/keiailab/mongodb-operator
    targetRevision: v1.5.0
    path: deploy/olm-v1
  destination:
    server: https://kubernetes.default.svc
    namespace: mongodb-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - ServerSideApply=true
      - CreateNamespace=false  # namespace.yaml 中显式声明
```

## §3 Path 2 — Helm chart

### §3.1 Add repo + install

```bash
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace \
  --version 1.5.0
```

### §3.2 values.yaml customization

请参阅 `charts/mongodb-operator/values.yaml`。主要选项:

```yaml
replicaCount: 1
image:
  repository: ghcr.io/keiailab/mongodb-operator
  tag: v1.5.0
resources:
  requests: { cpu: 100m, memory: 128Mi }
  limits: { cpu: 500m, memory: 512Mi }
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
webhook:
  enabled: true
  failurePolicy: Fail
```

### §3.3 Day-2 — Upgrade / Rollback

```bash
helm upgrade mongodb-operator mongodb-operator/mongodb-operator --version 1.6.0
helm rollback mongodb-operator 1   # 回滚到先前 revision
helm uninstall mongodb-operator    # 完全卸载 (CRDs 另行处理)
```

## §4 Private Registry (所有 path 通用)

本 v1.5.0 release 的 GHCR images:
- `ghcr.io/keiailab/mongodb-operator:v1.5.0` (public)
- `ghcr.io/keiailab/mongodb-operator-bundle:v1.5.0` (private, internal — public 化进行中)
- `ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0` (private, internal — public 化进行中)

使用 private image 时:

```bash
# (1) 创建 Pull secret
kubectl create secret docker-registry ghcr-mongodb-operator-pull \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password=<github-pat-with-read:packages> \
  -n olmv1-system

# (2) OLM v1 — 在 SA imagePullSecrets 中添加 (pull-secret-controller 会自动 sync)
kubectl patch sa -n olmv1-system operator-controller-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-mongodb-operator-pull"}]}'
kubectl patch sa -n olmv1-system catalogd-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-mongodb-operator-pull"}]}'

# (3) Helm — 在 values.yaml 中添加 imagePullSecrets
helm install ... --set imagePullSecrets[0].name=ghcr-mongodb-operator-pull
```

## §5 Verification (所有 path)

```bash
# CRDs
kubectl get crd | grep mongodb.keiailab.com
# 期望: mongodbs, mongodbbackups, mongodbshardeds (3 个)

# operator pod
kubectl get pod -A -l app.kubernetes.io/name=mongodb-operator
# 期望: Running, image v1.5.0

# operator logs
kubectl logs -A -l app.kubernetes.io/name=mongodb-operator --tail=20
# 期望: "Starting Controller" + "Starting workers" + 0 ERROR
```

## §6 Sample CR — MongoDB ReplicaSet

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
  replicaSetName: rs0
  storage:
    storageClassName: ceph-block  # 或各环境对应的 storage class
    size: 50Gi
  resources:
    requests: { cpu: 500m, memory: 1Gi }
    limits: { cpu: 2, memory: 4Gi }
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin-credentials
```

详细 sample: `config/samples/` 或 `config/samples/bundle/`。

## §7 Reference

- [README.md](README.md) — project overview
- [Architecture](../architecture.md) — design + deployment models
- [deploy/olm-v1/README.md](deploy/olm-v1/README.md) — KeiaiLab live evidence (OLM v1)
- [charts/mongodb-operator/](charts/mongodb-operator/) — Helm chart source
- [Roadmap](../roadmap.md) — feature roadmap (包含 deployment models)
- [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) — 外部用户运维级决策
- [ADR-0029](docs/kb/adr/0029-olm-v1-migration-from-v0.md) — OLM v1 采用决策

