# deploy/ — GitOps 배포 디렉터리

본 디렉터리는 ArgoCD (또는 동등 GitOps tool) 가 git → cluster 단방향 동기를 수행하기 위한 매니페스트 진입점이다. **`config/` 와 별개 경로** — `make deploy` 등 단발성 푸시는 `config/default` 를 사용한다.

## 구조

```
deploy/
├── overlays/prod/                 # ArgoCD application path: operator 자체
│   ├── kustomization.yaml         # config/{crd,rbac,manager} → namespace=prod
│   └── delete-namespace.yaml      # 자동 생성 Namespace 제거 (prod ns 외부 사전생성)
└── mongodb-sharded.yaml           # ArgoCD application path: workload (CR 인스턴스, db ns)
```

운영 모델: **operator 와 workload 는 별개 ArgoCD application** 으로 분리한다. operator 라이프사이클 (CRD/RBAC/Deployment) 은 prod ns 에, 사용자 데이터 (MongoDBSharded CR) 는 db ns 에 둔다.

## 사전 조건 (cluster)

- [ ] `prod` namespace 사전 생성 (ArgoCD 가 만들지 않음 — `delete-namespace.yaml` patch).
- [ ] `db` namespace 사전 생성.
- [ ] StorageClass `ceph-block` 이용 가능.
- [ ] `mongodb-admin-creds` Secret (db ns) — ExternalSecret 또는 SealedSecret 으로 주입.
  - keys: `username`, `password` (mechanism=SCRAM-SHA-256).
- [ ] Prometheus Operator (monitoring.enabled=true 사용 시).

## 적용 (수동 검증)

```fish
# 1) 렌더 검증
kustomize build deploy/overlays/prod | head
kustomize build deploy/overlays/prod | grep -c "kind: Namespace"   # 0

# 2) operator 적용 (ArgoCD 없이 수동)
kustomize build deploy/overlays/prod | kubectl apply -f -

# 3) operator readiness
kubectl -n prod rollout status deploy/mongodb-operator-controller-manager

# 4) workload 적용
kubectl apply -f deploy/mongodb-sharded.yaml

# 5) workload readiness
kubectl -n db get mongodbsharded mongodb-sharded -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```

## 롤백

ArgoCD: 이전 commit 으로 application sync. 또는:

```fish
# 워크로드만 롤백
kubectl delete -f deploy/mongodb-sharded.yaml   # CR 만 제거; PV 는 reclaimPolicy 에 따름

# operator 자체 롤백
kustomize build deploy/overlays/prod | kubectl delete -f -
```

operator 제거 시 CRD 도 함께 제거되어 *모든* MongoDBSharded CR 의 finalizer 가 의존하므로 workload 를 *먼저* 비워야 한다.

## 변경 절차

본 디렉터리 변경은 ADR 작성 후 진행 (`docs/kb/adr/`). 매번 `kustomize build deploy/overlays/prod` 로 렌더 검증한다.
