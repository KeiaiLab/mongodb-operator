# ADR-0029: OLM v1 (operator-controller v1.8) 채택 — v0 에서 next-generation 으로 migration

- Date: 2026-05-15 (Phase D cutover sealed: 2026-05-17)
- Status: **Accepted (sealed via ADR-0028 Phase D, 2026-05-17)**
- Authors: @keiailab
- Refs: ADR-0028 Phase D (v0 only path 폐기), ADR-0023 (bundle scaffold), ADR-0027 (community-operators sync, deprecated)
- Supersedes: ADR-0028 §Decision (OLM v0.30 채택 부분), ADR-0027 (community-operators upstream sync 자동화)

> **2026-05-17 cutover note**: 본 ADR 의 결정이 ADR-0028 Phase D 로 *완전 sealed*. KeiaiLab Cluster 의 OLM cluster install path = OLM v1 single canonical (operator-controller v1.8.0 + ClusterCatalog + ClusterExtension). v0 path (`deploy/olm/`) + community-operators upstream sync 자동화 영구 폐기. 외부 사용자는 helm chart 또는 OLM v1 직접 사용.

## Context

ADR-0028 Phase B 에서 KeiaiLab Cluster 에 **OLM v0.30.0** 으로 설치. 그러나 사용자 검토 (2026-05-15) 에서 *최신 표준* 확인 결과:

| 라인 | 최신 release | 비고 |
|---|---|---|
| OLM v0 (`operator-lifecycle-manager`) | v0.42.0 (2026-04-09) | maintenance mode — 신규 feature 없음. 본 적용 v0.30.0 은 12 minor + 18개월 stale |
| **OLM v1** (`operator-controller`) | **v1.8.0** (2026-02-19) — GA | next-generation architecture, 2025-11 Red Hat announcement |
| `catalogd` (OLM v1 component) | v1.1.0 (2025-01-07) — GA | FBC server, ClusterCatalog 처리 |

**OLM v0 의 한계**:
- maintenance mode = 보안 패치만, 신규 architecture/feature 없음
- 4 자원 모델 (CatalogSource + OperatorGroup + Subscription + InstallPlan) — install flow 복잡
- ArgoCD GitOps 정합 부분 (Subscription 의 mid-state 관리 별 ignoreDifferences 필요)
- bundle unpack Job 의 NetworkPolicy 표준 (OPRUN-3923 PR #1008) 도 *별도 작성* 필요

**OLM v1 의 advances** ([Red Hat announcement](https://www.redhat.com/en/blog/announcing-olm-v1-next-generation-operator-lifecycle-management)):
- **ClusterCatalog + ClusterExtension** 단 2 자원 — 단순 mental model
- **Helm-based deployment model** — operator install 이 helm release 와 *동일 mechanism*. helm chart adopt 가능.
- **GitOps 1급 지원** — ArgoCD App source 로 직접 사용
- **Operator 외 일반 K8s app 지원** — ClusterExtension 의 scope 확장
- **security-by-default** — installer SA + RBAC 명시 강제, NetworkPolicy 표면 단순

본 사용자 결정 (2026-05-15): *옵션 C 선택* (vs A=v0.30 유지, B=v0.42 upgrade, D=OLM 단념). **OLM v1.8.0 으로 migration**.

## Decision

KeiaiLab Cluster + mongodb-operator 외부 사용자 노출의 *기본 배포 모델* 을 **OLM v1 (operator-controller v1.8.0 + catalogd)** 으로 채택한다.

### 6 행동 항목

1. **OLM v0 cleanup** — `olm` + `operators` ns + 8 v0 CRDs 모두 제거. v0/v1 *공존 차단* (CRD group 차이 없으나 architecture 충돌).
2. **OLM v1 install** — `operator-controller.yaml v1.8.0` server-side apply, `olmv1-system` ns. `install.sh` 우회 (cert-manager 재설치 차단). KeiaiLab 의 라이브 cert-manager (`platform-system` ns) 재사용 — `olmv1-ca` secret 을 `platform-system` 에 복제 (cert-manager 의 `cluster-resource-namespace=$(POD_NAMESPACE)` 정합).
3. **nodeSelector 제거** — operator-controller 와 catalogd deployment 의 `node-role.kubernetes.io/control-plane: ""` selector 가 KeiaiLab 의 `"true"` 값과 mismatch. nodeSelector 제거 (worker 도 scheduling 허용).
4. **Pull authentication** — `olmv1-system` ns 에 `ghcr-keiailab-pull` secret 복제. operator-controller-controller-manager + catalogd-controller-manager SA 의 `imagePullSecrets` 에 추가 — OLM v1 의 *pull-secret-controller* 가 *global pull secret pattern* 으로 자동 sync.
5. **ClusterCatalog + ClusterExtension** — `deploy/olm-v1/` 의 4 자원 (Namespace + ClusterCatalog + ServiceAccount + ClusterRoleBinding + ClusterExtension). FBC catalog image (`ghcr.io/keiailab/mongodb-operator-catalog:v1.5.0`) 재사용.
6. **CRD adopt (transition state)** — 기존 helm chart v1.4.20 (data ns) 의 라이브 CRDs 에 *helm v3 adopt annotation* 부여 — `meta.helm.sh/release-name=mongodb-operator + release-namespace=mongodb-system + managed-by=Helm`. OLM v1 의 helm controller 가 자체 release 자원으로 인식. 영구 cutover (helm chart 제거) 는 사용자 git PR 영역.

### Installer RBAC 결정

Phase C 단순화: `mongodb-operator-installer` SA → `cluster-admin` binding. 본 결정의 근거:
- 기존 helm chart v1.4.20 도 cluster-admin scope 으로 deploy → *동등 권한*.
- narrow RBAC 작성은 *bundle-derived ClusterRole* (각 자원 type 별 verb 명시) — 시간 들지만 production security 표준.
- **후속 ADR 분리**: narrow installer RBAC 별 phase (별 ADR, operator-controller `docs/howto/derive-service-account.md` 정합).

## Consequences

### 긍정

- *진정한 현대 표준* — v0.30 → v1.8 의 18개월 + 12 minor + architecture 단계 도약.
- *단순한 mental model* — install 단 2 자원 (ClusterCatalog + ClusterExtension).
- *Helm-based adopt mechanism* — 기존 helm chart 자원 재사용 가능 (Phase C 의 CRD takeover 실용성).
- *GitOps 정합* — ArgoCD App-of-Apps 의 native 통합 (Helm release 와 동일 패턴).
- *security-by-default* — installer SA + RBAC 의 명시 강제로 권한 surface 가시화.

### 부정 / 트레이드오프

- *helm v0 의 다양한 Subscription / approval / channel 기반 upgrade* 정책 부재 가능 — OLM v1 의 catalog.channels + version pin/range 으로 보완.
- *기존 helm chart 와의 transition state* — CRD adopt annotation 은 *임시*. 사용자 git PR 으로 helm chart 영구 제거까지 *cluster ↔ git drift*.
- *cluster-admin installer 단순화* — narrow RBAC 후속 phase 의무.
- *neoder OLM 의 docs 정리 부족* — operator-framework.github.io/operator-controller/howto/private-registry/ 등 404, GitHub 직접 조회 + log inspection 으로 standard 추출.

### 후속 작업

- [ ] **narrow installer RBAC ADR** — `cluster-admin` → bundle-derived ClusterRole.
- [ ] **helm chart 영구 cutover** — `keiailab/platform/data` 의 helm chart 제거 PR + ArgoCD App 의 `deploy/olm-v1/` source 등록 PR.
- [ ] **CRD adopt annotation 정리** — helm chart 제거 후 `meta.helm.sh/release-name/release-namespace` annotation 제거 (OLM v1 단독 소유).
- [ ] **olmv1-system NetworkPolicy** — narrow NP (catalogd grpc + image pull egress + kube-apiserver) — 현재 default-deny cluster 의 호환 보완.
- [ ] **PoC CR reconcile 검증** — helm chart 제거 후 `database` ns 의 `test-mongodb` CR 적용 → status.phase=Ready.
- [ ] **mailstory FerretDB cutover** — `docs/operations/mailstory-ferretdb-to-native-mongodb-cutover.md` Draft.
- [ ] **OLM v1 release-yml 자동화** — community-operators upstream PR (ADR-0027) 의 OLM v1 변형.

## Alternatives Considered

**A. v0.30 유지** — *18개월 stale + 12 minor + architecture 미진보*. Reject.

**B. v0.42.0 upgrade** — v0 의 latest patch, 단 *maintenance mode* (신규 feature 없음). v1 migration 시간 미루기만. Reject.

**C. OLM v1 (본 결정)** — 가장 현대 표준 + Helm/GitOps 정합 + KeiaiLab cluster 의 ArgoCD App-of-Apps 자세에 적합.

**D. OLM 단념 (helm chart only)** — 본 ADR-0028 의 Phase A 산출물 (bundle/catalog images) invalidate, community-operators upstream 활용 무관. KeiaiLab Cluster 만 considering 시 valid 하나 *외부 사용자 노출* (ADR-0028 의 정신) 정합 안 됨. Reject.

## Verification

**라이브 검증** (2026-05-15, KeiaiLab Cluster):

```fish
$ kubectl get deployment -n olmv1-system
NAME                                     READY   UP-TO-DATE   AVAILABLE
catalogd-controller-manager              1/1     1            1
operator-controller-controller-manager   1/1     1            1

$ kubectl get clustercatalog
NAME                 LASTUNPACKED   SERVING   AGE
keiailab-operators   *m**s          True      *m**s

$ kubectl get clusterextension
NAME               INSTALLED BUNDLE          VERSION   INSTALLED   PROGRESSING
mongodb-operator   mongodb-operator.v1.5.0   1.5.0     True        True

$ kubectl get pod -n mongodb-system
NAME                                                   READY   STATUS    RESTARTS
mongodb-operator-controller-manager-6b65567bd8-wq8n5   1/1     Running   0
```

ClusterExtension `Installed=True/Succeeded`, operator pod healthy `v1.5.0`, mailstory-ferretdb 무영향.

`<!-- live-verified: 2026-05-15 -->`

## References

- [Red Hat: Announcing OLM v1 (2025-11)](https://www.redhat.com/en/blog/announcing-olm-v1-next-generation-operator-lifecycle-management)
- [operator-framework/operator-controller v1.8.0 release](https://github.com/operator-framework/operator-controller/releases/tag/v1.8.0)
- [OLM v1 Getting Started](https://operator-framework.github.io/operator-controller/getting-started/olmv1_getting_started/)
- [About OLM v1 (OKD docs)](https://docs.okd.io/latest/operators/olm_v1/index.html)
- ADR-0028: OLM 외부 사용자 운영 수준 (본 ADR 의 Phase B 까지)
- `deploy/olm-v1/README.md` — 적용 절차 + helm 영구 cutover 가이드
