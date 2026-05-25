# examples/gitops/ — ArgoCD/GitOps 예시

> **Status: experimental / 예시 (operator 운영자가 직접 적용하기 위한 *예비* 경로)**
>
> 실 운영(keiailab 내부)은 `your-platform` umbrella helm chart 를 사용하며, 이 디렉터리는 그 chart 와 동일 cluster state 를 산출하는지 *parity 미검증* 상태다. 동일 환경에 helm release 와 이 매니페스트를 동시에 적용하면 충돌한다.

본 디렉터리는 ArgoCD (또는 동등 GitOps tool) 가 git → cluster 단방향 동기를 수행하기 위한 **예시 진입점**이다. kubebuilder 표준 매니페스트 source 는 `config/` 이며, `make deploy` 단발성 푸시 또한 `config/default` 를 사용한다.

## 구조

```
examples/gitops/
├── overlays/prod/                 # ArgoCD application path: operator 자체 (envName=prod, ns=data)
│   ├── kustomization.yaml         # config/{crd,rbac,manager} → namespace=data
│   └── delete-namespace.yaml      # 자동 생성 Namespace 제거 (data ns 외부 사전생성)
└── mongodb-sharded.yaml           # ArgoCD application path: workload (CR 인스턴스, data ns)
```

운영 모델: **operator 와 workload 는 별개 ArgoCD application** 으로 분리한다 — 단, 클러스터 ns 통합 정책 (your-cluster 2026-05-06 cycle: 5 차트 모두 `data` ns 단일) 에 따라 operator 와 CR 이 *동일 ns* 를 공유한다. envName 분리 (`overlays/prod`) 는 환경 식별자로만 유지하고 namespace 는 `data`.

## 현 운영 상태 (2026-05-06)

실 클러스터 ArgoCD source 는 `your-platform/mongodb` umbrella chart (revision=stable). umbrella 가 본 repo 의 helm chart (mongodb-operator 1.4.x) 를 dependency 로 흡수하여 operator + MongoDBSharded CR 을 단일 helm release 로 묶는다 (`platform/data/application.yaml` ApplicationSet path).

본 디렉터리는 *대안/예비 진입점* — your-platform 의 ApplicationSet generator path 가 본 path 를 직접 가리키도록 마이그레이션 가능하나, 현 단계에서는 helm umbrella 와 *동일 cluster state 산출* 의 parity 가 검증되지 않았다. 직접 적용은 *기존 helm release 와 충돌* 위험. 적용 전 ArgoCD application 또는 helm release 중 하나를 비활성화해야 한다.

## 사전 조건 (cluster)

- [x] `data` namespace 사전 생성 (your-cluster 2026-05-06 cycle 으로 이미 Active).
- [ ] StorageClass `ceph-block` 이용 가능 (현 클러스터: `ceph-rbd` 사용 중 — your-platform/mongodb values.yaml 인용. 필요 시 storageClassName 동기 조정).
- [ ] `mongodb-admin-creds` Secret (data ns) — your-platform/mongodb 의 manual K8s Secret 패턴 (ESO Phase 3 차단으로 임시 manual; ESO ≥ v0.18 이후 ExternalSecret 자동화).
- [ ] Prometheus Operator (monitoring.enabled=true 사용 시 — VictoriaMetrics operator 합류 후).

## 적용 (수동 검증)

```fish
# 1) 렌더 검증
kustomize build examples/gitops/overlays/prod | head
kustomize build examples/gitops/overlays/prod | grep -c "kind: Namespace"   # 0

# 2) operator 적용 (ArgoCD 없이 수동)
kustomize build examples/gitops/overlays/prod | kubectl apply -f -

# 3) operator readiness
kubectl -n data rollout status deploy/mongodb-operator-controller-manager

# 4) workload 적용
kubectl apply -f examples/gitops/mongodb-sharded.yaml

# 5) workload readiness
kubectl -n data get mongodbsharded mongodb-sharded -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```

## 롤백

ArgoCD: 이전 commit 으로 application sync. 또는:

```fish
# 워크로드만 롤백
kubectl delete -f examples/gitops/mongodb-sharded.yaml   # CR 만 제거; PV 는 reclaimPolicy 에 따름

# operator 자체 롤백
kustomize build examples/gitops/overlays/prod | kubectl delete -f -
```

operator 제거 시 CRD 도 함께 제거되어 *모든* MongoDBSharded CR 의 finalizer 가 의존하므로 workload 를 *먼저* 비워야 한다.

## 변경 절차

본 디렉터리 변경은 ADR 작성 후 진행 (`docs/kb/adr/`). 매번 `kustomize build examples/gitops/overlays/prod` 로 렌더 검증한다.
