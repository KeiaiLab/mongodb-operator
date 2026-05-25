<p align="center">
  <b>English</b> |
  <a href="INSTALL.ko.md">한국어</a> |
  <a href="INSTALL.ja.md">日本語</a> |
  <a href="INSTALL.zh.md">中文</a>
</p>

# Installation Guide

> mongodb-operator 의 *external user* 운영 수준 설치 가이드. 3 path matrix + 상세 절차 + Day-2 upgrade/rollback.

본 문서는 *외부 사용자* (개별 cluster 운영자) 의 설치 절차. KeiaiLab Cluster 내부 적용의 *라이브 evidence* 는 [deploy/olm-v1/README.md](deploy/olm-v1/README.md) (OLM v1 ✓ 라이브) 참조.

> **BREAKING (ADR-0028 Phase D, 2026-05-17)**: OLM v0 cluster install path + community-operators upstream sync 자동화 영구 폐기. Path 3 (OLM v0) 챕터 제거. 외부 사용자는 *OLM v1 (Path 1)* 또는 *Helm (Path 2)* 사용. 자세한 내용은 [Changelog](changelog.md) BREAKING CHANGES 섹션 + [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) Phase D 참조.

## §1 2-Path Matrix

| Method | Modernity | GitOps 정합 | Multi-tenancy | Upgrade | 추천 대상 |
|---|---|---|---|---|---|
| **OLM v1** | 🟢 next-generation (v1.8, 2026-02 GA) | 🟢 native (ArgoCD App source = ClusterExtension manifest) | 🟢 ClusterExtension per ns | 🟢 catalog.channels + version pin/range | external users, production GitOps |
| Helm chart | 🟡 stable | 🟢 native (ArgoCD App source = Helm chart) | 🟡 multi-release | 🟢 `helm upgrade` | local dev, single-cluster |

**선택 기준**:
- *최신 K8s 환경 + GitOps + 외부 사용자 노출* → **OLM v1** (Path 1)
- *단순 single-cluster dev / non-OLM cluster* → Helm (Path 2)
- *OpenShift 4.x / OperatorHub.io community-operators 사용자*: 지원 종료. helm 또는 OLM v1 로 마이그레이션 필요.

## §2 Path 1 — OLM v1 (recommended)

### §2.1 Prerequisites

- Kubernetes v1.26+
- `cert-manager` v1.20+ (operator-controller dependency, [install guide](https://cert-manager.io/docs/installation/))
- cluster admin (1회 bootstrap)
- private registry pull credentials (현재 release 의 catalog/bundle image 가 `internal` GHCR — public 화 또는 imagePullSecret)

### §2.2 OLM v1 cluster bootstrap (1회)

```bash
# 공식 install.sh — cert-manager + operator-controller + catalogd 통합 설치.
# cert-manager 가 이미 라이브이면 install.sh 의 cert-manager 부분 skip + operator-controller.yaml 직접 apply.
curl -L -s https://github.com/operator-framework/operator-controller/releases/latest/download/install.sh | bash -s

# 또는 cert-manager 기존 사용:
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
# *Note*: production 은 narrow ClusterRole 권장 (bundle-derived).
# 본 예제 는 *cluster-admin* 단순화 — installer 자체 권한이 광범위.
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
      version: 1.5.0  # 또는 channel-based: channels: [stable]
```

```bash
kubectl apply -f clusterextension-mongodb.yaml
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s

# operator pod healthy 검증
kubectl get pod -n mongodb-system -l app.kubernetes.io/name=mongodb-operator
```

### §2.5 Day-2 — Upgrade

```bash
# Version pin → 새 version 으로 직접 점프
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"version":"1.6.0"}}}}'

# 또는 channel-based — catalog 가 stable 채널의 새 version publish 시 자동 upgrade
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"channels":["stable"],"version":""}}}}'
```

### §2.6 Rollback

```bash
# 이전 version 으로 패치
kubectl patch clusterextension mongodb-operator --type=merge \
  -p '{"spec":{"source":{"catalog":{"version":"1.4.23"}}}}'

# 또는 ClusterExtension + CRDs 완전 제거 (CR 도 함께 삭제됨 — 주의)
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
      - CreateNamespace=false  # namespace.yaml 가 명시
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

`charts/mongodb-operator/values.yaml` 참조. 주요 옵션:

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
helm rollback mongodb-operator 1   # 이전 revision 으로
helm uninstall mongodb-operator    # 완전 제거 (CRDs 는 별도)
```

## §4 Private Registry (모든 path 공통)

본 v1.5.0 release 의 GHCR images:
- `ghcr.io/keiailab/mongodb-operator:v1.5.0` (public)
- `ghcr.io/keiailab/mongodb-operator-bundle:v1.5.0` (private, internal — public 화 진행 중)
- `ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0` (private, internal — public 화 진행 중)

private image 사용 시:

```bash
# (1) Pull secret 생성
kubectl create secret docker-registry ghcr-mongodb-operator-pull \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password=<github-pat-with-read:packages> \
  -n olmv1-system

# (2) OLM v1 — SA imagePullSecrets 에 추가 (pull-secret-controller 가 자동 sync)
kubectl patch sa -n olmv1-system operator-controller-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-mongodb-operator-pull"}]}'
kubectl patch sa -n olmv1-system catalogd-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-mongodb-operator-pull"}]}'

# (3) Helm — values.yaml 에 imagePullSecrets 추가
helm install ... --set imagePullSecrets[0].name=ghcr-mongodb-operator-pull
```

## §5 Verification (모든 path)

```bash
# CRDs
kubectl get crd | grep mongodb.keiailab.com
# 기대: mongodbs, mongodbbackups, mongodbshardeds (3개)

# operator pod
kubectl get pod -A -l app.kubernetes.io/name=mongodb-operator
# 기대: Running, image v1.5.0

# operator logs
kubectl logs -A -l app.kubernetes.io/name=mongodb-operator --tail=20
# 기대: "Starting Controller" + "Starting workers" + 0 ERROR
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
    storageClassName: ceph-block  # 또는 환경 별
    size: 50Gi
  resources:
    requests: { cpu: 500m, memory: 1Gi }
    limits: { cpu: 2, memory: 4Gi }
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin-credentials
```

상세 sample: `config/samples/` 또는 `config/samples/bundle/`.

## §7 Reference

- [README.md](README.md) — project overview
- [Architecture](architecture.md) — design + deployment models
- [deploy/olm-v1/README.md](deploy/olm-v1/README.md) — KeiaiLab live evidence (OLM v1)
- [charts/mongodb-operator/](charts/mongodb-operator/) — Helm chart source
- [Roadmap](roadmap.md) — feature roadmap (deployment models 포함)
- [ADR-0028](docs/kb/adr/0028-olm-external-user-production-readiness.md) — 외부 사용자 운영 수준 결정
- [ADR-0029](docs/kb/adr/0029-olm-v1-migration-from-v0.md) — OLM v1 채택 결정

