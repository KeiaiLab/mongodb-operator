# ADR-0030: OLM v1 narrow installer RBAC + olmv1-system NetworkPolicy

- Date: 2026-05-15
- Status: Accepted
- Authors: @keiailab
- Refs: ADR-0029 (OLM v1 채택, Phase C), ADR-0028 (외부 사용자 운영 수준)

## Context

ADR-0029 Phase C 의 OLM v1 라이브 적용에서 *Installer RBAC 단순화* 로 `cluster-admin` binding 사용. 본 결정의 정당화:
- 기존 helm chart v1.4.20 도 cluster-admin scope deploy
- narrow RBAC 작성은 *시간 들지만* production security 표준

ADR-0029 의 후속 명시 작업 — *narrow installer RBAC* + *olmv1-system NetworkPolicy* 의 별 ADR. 본 문서로 처리.

### 보안 표면 분석

**현재 (ADR-0029)**:
- `mongodb-operator-installer` SA → `cluster-admin` binding
- 권한 surface: *cluster 의 모든 자원 + 모든 verb*
- 영향: ClusterExtension 의 *helm release 처리 logic* 이 *operator 본래 권한 외* 의 자원에도 접근 가능 (예: 다른 ns 의 Secret, 다른 operator 의 CRD)
- 위험: bundle 자체가 *조작 또는 손상* 시 cluster 전체 영향

**Narrow RBAC**:
- Installer ClusterRole — bundle CSV 의 `spec.install.spec.{clusterPermissions, permissions}` 의 정확한 mirror + RBAC management + CRD management + ClusterExtension finalizer
- 권한 surface: *operator 가 자체 declare 한 권한만*
- 영향: bundle 조작 시에도 *declared scope* 외 자원 영향 불가능

또한 OLM v1 의 *cluster default-deny* 적용 cluster (KeiaiLab) 의 olmv1-system NetworkPolicy 부재 시 *catalogd / operator-controller* 의 normal 작동 중 *unexpected NetworkPolicy 추가* 가 OLM 차단 가능 — 명시적 표준 NP 작성으로 *예측 가능성 + 보안 표면 가시화*.

## Decision

OLM v1 의 *production deployment* 의 *기본 권장* 을 *narrow installer RBAC + olmv1-system NetworkPolicy* 로 채택. cluster-admin binding 은 *Phase C 단순화 path* 로 *전환 가능* 하지만 production 권장 안 함.

### §1 narrow installer RBAC manifest

`deploy/olm-v1/clusterextension-narrow-rbac.yaml` 신설:

**구조** (operator-controller `docs/howto/derive-service-account` 정합):
- `ServiceAccount` mongodb-operator-installer (mongodb-system ns)
- `ClusterRole` mongodb-operator-installer (16 rule sections):
  - §2A: RBAC Management (clusterroles + clusterrolebindings, broad list/watch + narrow get/update/patch/delete with resourceNames)
  - §2B: CustomResourceDefinition (broad list/watch + narrow with `mongodbs/mongodbbackups/mongodbshardeds.mongodb.keiailab.com` resourceNames)
  - §2C: ClusterExtension finalizer (resourceNames=mongodb-operator)
  - §2D: CSV `clusterPermissions` 13 rules — `configmaps/secrets/services/events/pods/deployments/statefulsets/hpa/jobs/certificates/leases/mongodb CRDs/finalizers/status/networkpolicies/poddisruptionbudgets`
  - §2E: Webhook (validating/mutating webhook configurations)
- `Role` mongodb-operator-installer (namespace-scoped, mongodb-system):
  - §3A: Deployment Management (resourceNames=mongodb-operator-controller-manager)
  - §3B: ServiceAccount Management
  - §3C: RoleBinding Management (resourceNames=mongodb-operator-leader-election)
  - §3D: Bundled Services + ConfigMaps (resourceNames=mongodb-operator-controller-manager-metrics-service + mongodb-operator-webhook-service)
  - §3E: CSV `permissions` 3 rules — `configmaps/leases/events`
- `ClusterRoleBinding` + `RoleBinding`
- `ClusterExtension` (동일 spec, installer SA reference)

본 manifest 는 *cluster-admin alternative* — clusterextension.yaml (cluster-admin) 와 *공존*, 운영자가 production 시 본 manifest 적용.

### §2 olmv1-system NetworkPolicy

`deploy/olm-v1/networkpolicies.yaml` 신설:

**2 NP**:
- `operator-controller` — ingress 8443 metrics + 8081 healthz, egress kube-apiserver + DNS + catalogd grpc + bundle image pull (80/443)
- `catalogd` — ingress from operator-controller + metrics 7443, egress kube-apiserver + DNS + catalog image pull

OLM v0 의 5 NP (OPRUN-3923 PR #1008) 의 *간략화* — OLM v1 architecture (CatalogSource + OperatorGroup + Subscription/InstallPlan 부재) 정합.

### Future Refinements

**generated resourceNames pinning** (Phase D, 선택):

First install 후 라이브 검증으로 *정확한 generated name* 확인:

```bash
kubectl get clusterrole,clusterrolebinding | grep mongodb-operator
# 출력: mongodb-operator.<hash> — 본 hash 정확 name pinning
```

본 hash 정확 name 으로 §2A 의 resourceNames placeholder 정정. 이는 *post-install pinning* — first install 은 broad list/watch 통과, 이후 narrow.

또는 OLM v1 의 *auto-generated RBAC* 가능성 (operator-framework 의 향후 feature) — 본 ADR 의 manual narrow 가 transitional pattern.

## Consequences

### 긍정

- **Principle of least privilege** — installer 가 declared scope 외 권한 없음
- **Bundle 조작 시 cluster impact 제한** — supply chain attack 의 blast radius 감소
- **Predictable network surface** — olmv1-system NP 으로 *허용된 traffic* 의 명시
- **production 표준 정합** — operator-controller docs 의 derive-service-account 표준 정확 정합

### 부정 / 트레이드오프

- **Manifest 복잡성 ↑** — cluster-admin (5 line) → narrow (200+ line). 사용자 PR 검토 부담.
- **resourceNames placeholder** — first install 후 *pinning* 작업 필요 (post-install audit step, 자동화 가능)
- **Future feature 추가 시 manifest 갱신** — operator 가 새로운 자원 type 의 verb 추가 시 CSV + 본 manifest 양쪽 갱신

### 후속 작업

- [ ] **첫 install 후 resourceNames pinning** — `kubectl get cr,crb,role,rolebinding | grep mongodb-operator` 출력으로 §2A 의 placeholder 정정 + 별 commit
- [ ] **CI 검증** — Makefile `narrow-rbac-check` target (CSV 와 manifest 의 rules 일치 verify, drift 차단)
- [x] **deploy/olm/networkpolicies.yaml 와의 정합 정리** — *해소* (2026-05-17 ADR-0028 Phase D 로 `deploy/olm/` 디렉토리 전체 영구 폐기 → v1 NP single canonical 화). deploy/olm-v1/networkpolicies.yaml 가 cluster NP SSOT.
- [ ] **clusterextension.yaml (cluster-admin) 의 README warning** — production 시 narrow 사용 권장 명시

## Alternatives Considered

**A. cluster-admin 유지** — 본 ADR 의 거부 결정. 안전성 위배.

**B. broader narrow (cluster-wide list/watch + cluster-wide get/update/patch/delete, no resourceNames)** — 본 ADR 의 narrow 보다 *덜 narrow*. resourceNames pinning 어려움 시 fallback path 로 valid. Reject (production 표준 아님).

**C. operator-controller 의 향후 *automatic RBAC generation* 대기** — operator-framework roadmap 에 *RBAC auto-derive* feature 있을 수 있음. 단 *현재 v1.8.0 미지원*. Reject (대기 불가).

**D. cluster-admin + admission webhook 으로 *자원 scope 강제*** — admission webhook 추가 = complexity ↑ + 새 의존. Reject (단순함 위배).

## Verification

`deploy/olm-v1/clusterextension-narrow-rbac.yaml` 의 라이브 적용 (사용자 결정 시):

```fish
export KUBECONFIG=~/.kube/config-via-api-keiailab.com  # 또는 cluster admin context

# 기존 cluster-admin binding 제거
kubectl delete clusterrolebinding mongodb-operator-installer-cluster-admin

# narrow RBAC apply
kubectl apply -f deploy/olm-v1/clusterextension-narrow-rbac.yaml

# 검증
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s
kubectl auth can-i --as=system:serviceaccount:mongodb-system:mongodb-operator-installer create deployments -n mongodb-system
# 기대: yes (deployment create allowed)
kubectl auth can-i --as=system:serviceaccount:mongodb-system:mongodb-operator-installer delete namespaces
# 기대: no (cluster-admin 권한 회수 확인)

# NetworkPolicy apply
kubectl apply -f deploy/olm-v1/networkpolicies.yaml
kubectl get networkpolicy -n olmv1-system
```

`<!-- live-verified: pending — 사용자 cluster apply 결정 후 본 § 갱신 -->`

## References

- [Operator Controller: Derive ServiceAccount Permissions](https://operator-framework.github.io/operator-controller/howto/derive-service-account/)
- [OpenShift OLM PR #1008: NetworkPolicy support to OLMv0](https://github.com/openshift/operator-framework-olm/pull/1008)
- ADR-0028: 외부 사용자 운영 수준
- ADR-0029: OLM v1 채택
- `bundle/manifests/mongodb-operator.clusterserviceversion.yaml` — CSV (13 clusterPermissions + 3 permissions, 본 ADR 의 derive source)
