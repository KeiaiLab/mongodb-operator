<p align="center">
  <a href="INSTALL.md">English</a> |
  <a href="INSTALL.ko.md">한국어</a> |
  <b>日本語</b> |
  <a href="INSTALL.zh.md">中文</a>
</p>

# インストールガイド

> mongodb-operator の *external user* 向け運用レベルのインストールガイド。3 path matrix + 詳細手順 + Day-2 upgrade / rollback。

本ドキュメントは *外部ユーザー* (個別 cluster 運用者) 向けのインストール手順です。KeiaiLab Cluster 内部適用の *ライブ evidence* は [deploy/olm-v1/README.md](deploy/olm-v1/README.md) (OLM v1 ✓ ライブ) を参照してください。

> **BREAKING (ADR-0028 Phase D, 2026-05-17)**: OLM v0 cluster install path + community-operators upstream sync 自動化は永久に廃止されました。Path 3 (OLM v0) の章は削除されています。外部ユーザーは *OLM v1 (Path 1)* または *Helm (Path 2)* を使用してください。詳細は [CHANGELOG.md](CHANGELOG.md) の BREAKING CHANGES セクション、および [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) Phase D を参照してください。

## §1 2-Path Matrix

| Method | Modernity | GitOps 整合性 | Multi-tenancy | Upgrade | 推奨対象 |
|---|---|---|---|---|---|
| **OLM v1** | 🟢 next-generation (v1.8, 2026-02 GA) | 🟢 native (ArgoCD App source = ClusterExtension manifest) | 🟢 ClusterExtension per ns | 🟢 catalog.channels + version pin/range | external users, production GitOps |
| Helm chart | 🟡 stable | 🟢 native (ArgoCD App source = Helm chart) | 🟡 multi-release | 🟢 `helm upgrade` | local dev, single-cluster |

**選択基準**:
- *最新 K8s 環境 + GitOps + 外部ユーザーへの公開* → **OLM v1** (Path 1)
- *シンプルな single-cluster dev / non-OLM cluster* → Helm (Path 2)
- *OpenShift 4.x / OperatorHub.io community-operators ユーザー*: サポート終了。helm または OLM v1 へのマイグレーションが必要です。

## §2 Path 1 — OLM v1 (recommended)

### §2.1 Prerequisites

- Kubernetes v1.26+
- `cert-manager` v1.20+ (operator-controller dependency、[install guide](https://cert-manager.io/docs/installation/))
- cluster admin (1 回 bootstrap)
- private registry pull credentials (現在の release の catalog/bundle image が `internal` GHCR — public 化または imagePullSecret が必要)

### §2.2 OLM v1 cluster bootstrap (1 回)

```bash
# 公式 install.sh — cert-manager + operator-controller + catalogd を一括インストール。
# cert-manager が既にライブであれば install.sh の cert-manager 部分は skip + operator-controller.yaml を直接 apply。
curl -L -s https://github.com/operator-framework/operator-controller/releases/latest/download/install.sh | bash -s

# あるいは cert-manager を既存利用:
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
# *Note*: production では narrow ClusterRole の使用を推奨します (bundle-derived)。
# 本サンプルは *cluster-admin* で簡略化 — installer 自体の権限が広範になります。
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
      version: 1.5.0  # または channel-based: channels: [stable]
```

```bash
kubectl apply -f clusterextension-mongodb.yaml
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s

# operator pod の healthy 検証
kubectl get pod -n mongodb-system -l app.kubernetes.io/name=mongodb-operator
```

### §2.5 Day-2 — Upgrade

```bash
# Version pin → 新しい version へ直接ジャンプ
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"version":"1.6.0"}}}}'

# あるいは channel-based — catalog が stable channel の新しい version を publish した時点で自動 upgrade
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"channels":["stable"],"version":""}}}}'
```

### §2.6 Rollback

```bash
# 以前の version へパッチ
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"version":"1.4.23"}}}}'

# あるいは ClusterExtension + CRDs を完全削除 (CR も同時に削除されます — 注意)
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
      - CreateNamespace=false  # namespace.yaml に明示
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

`charts/mongodb-operator/values.yaml` を参照してください。主なオプション:

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
helm rollback mongodb-operator 1   # 以前の revision へ
helm uninstall mongodb-operator    # 完全削除 (CRDs は別途)
```

## §4 Private Registry (全 path 共通)

本 v1.5.0 release の GHCR images:
- `ghcr.io/keiailab/mongodb-operator:v1.5.0` (public)
- `ghcr.io/keiailab/mongodb-operator-bundle:v1.5.0` (private, internal — public 化進行中)
- `ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0` (private, internal — public 化進行中)

private image 使用時:

```bash
# (1) Pull secret 作成
kubectl create secret docker-registry ghcr-mongodb-operator-pull \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password=<github-pat-with-read:packages> \
  -n olmv1-system

# (2) OLM v1 — SA imagePullSecrets に追加 (pull-secret-controller が自動 sync)
kubectl patch sa -n olmv1-system operator-controller-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-mongodb-operator-pull"}]}'
kubectl patch sa -n olmv1-system catalogd-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-mongodb-operator-pull"}]}'

# (3) Helm — values.yaml に imagePullSecrets を追加
helm install ... --set imagePullSecrets[0].name=ghcr-mongodb-operator-pull
```

## §5 Verification (全 path)

```bash
# CRDs
kubectl get crd | grep mongodb.keiailab.com
# 期待: mongodbs, mongodbbackups, mongodbshardeds (3 件)

# operator pod
kubectl get pod -A -l app.kubernetes.io/name=mongodb-operator
# 期待: Running, image v1.5.0

# operator logs
kubectl logs -A -l app.kubernetes.io/name=mongodb-operator --tail=20
# 期待: "Starting Controller" + "Starting workers" + ERROR 0 件
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
    storageClassName: ceph-block  # または環境ごとの storage class
    size: 50Gi
  resources:
    requests: { cpu: 500m, memory: 1Gi }
    limits: { cpu: 2, memory: 4Gi }
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin-credentials
```

詳細な sample: `config/samples/` または `config/samples/bundle/`。

## §7 Reference

- [README.md](README.md) — project overview
- [ARCHITECTURE.md](ARCHITECTURE.md) — design + deployment models
- [deploy/olm-v1/README.md](deploy/olm-v1/README.md) — KeiaiLab live evidence (OLM v1)
- [charts/mongodb-operator/](charts/mongodb-operator/) — Helm chart source
- [ROADMAP.md](ROADMAP.md) — feature roadmap (deployment models を含む)
- [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) — 外部ユーザー運用レベルの決定
- [ADR-0029](docs/kb/adr/0029-olm-v1-migration-from-v0.md) — OLM v1 採用の決定

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://github.com/keiailab">keiailab.com</a>
</p>
