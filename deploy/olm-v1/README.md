# OLM v1 (operator-controller) — mongodb-operator 배포

> *진정한 현대 표준* — [OLM v1 (operator-controller v1.8.0)](https://github.com/operator-framework/operator-controller) 으로 mongodb-operator install. v0 (CSV/Subscription/OperatorGroup/InstallPlan) 의 4 자원 모델 → v1 의 ClusterCatalog + ClusterExtension 단 2 자원.

## §1 OLM v0 → v1 비교

| 영역 | OLM v0 | OLM v1 |
|---|---|---|
| Controller | olm-operator + catalog-operator + packageserver (3) | operator-controller + catalogd (2) |
| install CRDs | CSV + Subscription + OperatorGroup + InstallPlan + CatalogSource + OperatorCondition + Operator (7) | ClusterCatalog + ClusterExtension (2) |
| 사용자 자원 (operator install) | CatalogSource + OperatorGroup + Subscription + Approve InstallPlan (4 step) | ClusterCatalog + ClusterExtension (2 step) |
| Helm/GitOps 정합 | 부분 | 1급 지원 (Helm-based deployment model) |
| security-by-default | 별 NetworkPolicy 필수 (OPRUN-3923) | architecture 차원 (single installer SA + RBAC) |
| maintenance | OLM v0 = maintenance mode | OLM v1 = 활성 development |

## §2 라이브 적용 (2026-05-15, KeiaiLab Cluster)

```
$ kubectl get deployment -n olmv1-system
NAME                                     READY   UP-TO-DATE   AVAILABLE
catalogd-controller-manager              1/1     1            1
operator-controller-controller-manager   1/1     1            1

$ kubectl get clustercatalog
NAME                 LASTUNPACKED   SERVING   AGE
keiailab-operators   6m28s          True      6m56s

$ kubectl get clusterextension
NAME               INSTALLED BUNDLE          VERSION   INSTALLED   PROGRESSING
mongodb-operator   mongodb-operator.v1.5.0   1.5.0     True        True

$ kubectl get pod -n mongodb-system
NAME                                                   READY   STATUS    RESTARTS
mongodb-operator-controller-manager-6b65567bd8-wq8n5   1/1     Running   0
```

`<!-- live-verified: 2026-05-15 -->`

## §3 적용 절차

### 사전 조건

- KeiaiLab Cluster API 도달 (외부 endpoint `api.keiailab.com:6443`)
- cert-manager 라이브 (platform-system ns, ArgoCD App `platform-base-cert-manager-policy`)
- ghcr-keiailab-pull secret (data ns 등에 라이브, GHCR PAT 보유)

### 3.1 OLM v1 install (cluster admin)

```fish
# operator-controller.yaml v1.8.0 (catalogd + operator-controller 통합 manifest).
# install.sh 는 cert-manager 재설치 시도하므로 *직접 apply* 권장.
kubectl create ns cert-manager --dry-run=client -o yaml | kubectl apply -f -
kubectl apply --server-side=true --force-conflicts \
  -f https://github.com/operator-framework/operator-controller/releases/download/v1.8.0/operator-controller.yaml

# cert-manager 의 cluster-resource-namespace=$(POD_NAMESPACE)=platform-system 정합 위해
# olmv1-ca secret 을 platform-system 에 복제 + nodeSelector 제거 (control-plane label value mismatch fix).
kubectl get secret -n cert-manager olmv1-ca -o yaml \
  | sed '/namespace:/d;/resourceVersion:/d;/uid:/d;/creationTimestamp:/d;/ownerReferences:/,/^[a-z]/d' \
  | kubectl apply -n platform-system -f -
kubectl annotate clusterissuer olmv1-ca cert-manager.io/issue-temporary-certificate=$(date +%s) --overwrite
for d in operator-controller-controller-manager catalogd-controller-manager; do
  kubectl patch deployment -n olmv1-system $d --type=json \
    -p '[{"op":"remove","path":"/spec/template/spec/nodeSelector/node-role.kubernetes.io~1control-plane"}]'
done

kubectl rollout status -n olmv1-system deployment/operator-controller-controller-manager --timeout=180s
kubectl rollout status -n olmv1-system deployment/catalogd-controller-manager --timeout=180s
```

### 3.2 GHCR pull secret (olmv1-system)

```fish
# ghcr-keiailab-pull 을 olmv1-system 에 복제.
kubectl get secret -n data ghcr-keiailab-pull -o yaml \
  | sed '/namespace:/d;/resourceVersion:/d;/uid:/d;/creationTimestamp:/d' \
  | kubectl apply -n olmv1-system -f -

# OLM v1 의 pull-secret-controller 가 SA imagePullSecrets 를 자동 sync (global pull secret pattern).
kubectl patch sa -n olmv1-system operator-controller-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-keiailab-pull"}]}'
kubectl patch sa -n olmv1-system catalogd-controller-manager \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-keiailab-pull"}]}'
```

### 3.3 ClusterCatalog + ClusterExtension apply

```fish
kubectl apply -k deploy/olm-v1/
```

### 3.4 CRD adopt (기존 helm CRD takeover, 일회성)

```fish
# 본 cluster 에 기존 helm chart mongodb-operator v1.4.20 의 CRD 가 라이브 → OLM v1 가 takeover.
# helm v3 의 adopt mechanism (meta.helm.sh annotation + managed-by=Helm label) 으로 ownership transfer.
# OLM v1 의 ClusterExtension `mongodb-operator` 의 internal helm release name 정합.
for crd in mongodbs.mongodb.keiailab.com mongodbbackups.mongodb.keiailab.com mongodbshardeds.mongodb.keiailab.com; do
  kubectl annotate crd $crd \
    meta.helm.sh/release-name=mongodb-operator \
    meta.helm.sh/release-namespace=mongodb-system \
    --overwrite
  kubectl label crd $crd \
    app.kubernetes.io/managed-by=Helm \
    olm.operatorframework.io/owner-kind=ClusterExtension \
    olm.operatorframework.io/owner-name=mongodb-operator \
    --overwrite
done

# ClusterExtension reconcile 강제 trigger.
kubectl annotate clusterextension mongodb-operator olm.operatorframework.io/retry=$(date +%s) --overwrite
```

### 3.5 정상구동 검증

```fish
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s
kubectl get clusterextension mongodb-operator
kubectl get pod -n mongodb-system -l app.kubernetes.io/name=mongodb-operator
```

## §4 helm chart 영구 cutover (사용자 git PR)

본 cluster 에 helm chart mongodb-operator v1.4.20 (data ns) 와 OLM v1 mongodb-operator v1.5.0 (mongodb-system ns) 가 *공존* — CR 0건이라 race 없음. *영구 cutover* 는 git PR 으로만:

### PR 1: `keiailab/platform/data` (helm chart 제거)

```diff
# platform-data 의 mongodb-operator 서브 chart 제거.
# 또는 ArgoCD App platform-data-mongodb 의 source 자체 제거.
```

### PR 2: `keiailab/platform/<적절한 location>` (OLM v1 manifests 추가)

```diff
# deploy/olm-v1/{namespace,clustercatalog,clusterextension}.yaml 의 ArgoCD App 등록.
```

### 머지 후 cluster-side

```fish
# helm chart 자동 prune (ArgoCD App syncPolicy.prune=true 이므로 PR 1 머지 시 자동).
kubectl get deployment -n data platform-data-mongodb-mongodb-operator
# 기대: NotFound

# helm.sh annotation 정리 (helm release 없으므로 더 이상 필요 없음).
for crd in mongodbs.mongodb.keiailab.com mongodbbackups.mongodb.keiailab.com mongodbshardeds.mongodb.keiailab.com; do
  kubectl annotate crd $crd meta.helm.sh/release-name- meta.helm.sh/release-namespace-
  kubectl label crd $crd app.kubernetes.io/managed-by-
done

# OLM v1 가 CRD 완전 소유 (olm.operatorframework.io/owner-* label 만 유지).
```

## §5 후속 강화

### §5.1 narrow installer RBAC (ADR-0030, 완료 — manifest)

`clusterextension-narrow-rbac.yaml` — cluster-admin alternative. *production 권장*. cluster-side apply:

```fish
# cluster-admin binding 제거 + narrow apply
kubectl delete clusterrolebinding mongodb-operator-installer-cluster-admin
kubectl apply -f deploy/olm-v1/clusterextension-narrow-rbac.yaml

# 검증: 제한된 권한
kubectl auth can-i --as=system:serviceaccount:mongodb-system:mongodb-operator-installer delete namespaces
# 기대: no (cluster-admin 회수)
```

### §5.2 olmv1-system NetworkPolicy (ADR-0030, 완료 — manifest)

`networkpolicies.yaml` — operator-controller + catalogd 2 NP (zero-trust 정합):

```fish
kubectl apply -f deploy/olm-v1/networkpolicies.yaml
kubectl get networkpolicy -n olmv1-system
```

### §5.3 잔여 (별 ADR / 별 plan)

- **upgrade 정책** — channel-based upgrade (ClusterExtension 의 catalog.channels) 또는 version range
- **PoC CR** — `database` ns 에 test-mongodb CR 1건 reconcile 검증 (helm chart 영구 제거 후 race 회피)
- **community-operators upstream PR** — 0.3.0 → 1.5.0 sync (ADR-0027 자동화 deferred)

## §6 참조

- [OLM v1 announcement (Red Hat blog, 2025-11)](https://www.redhat.com/en/blog/announcing-olm-v1-next-generation-operator-lifecycle-management)
- [operator-controller v1.8.0 release](https://github.com/operator-framework/operator-controller/releases/tag/v1.8.0)
- [Getting started — olmv1](https://operator-framework.github.io/operator-controller/getting-started/olmv1_getting_started/)
- [Sample ClusterExtension YAML](https://raw.githubusercontent.com/operator-framework/operator-controller/main/config/samples/olm_v1_clusterextension.yaml)
- [About OLM v1 (OKD docs)](https://docs.okd.io/latest/operators/olm_v1/index.html)
