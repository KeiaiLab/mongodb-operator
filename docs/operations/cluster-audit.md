# Cluster Ops Audit — argos data plane (2026-05-07)

> **Quick run**:
> ```bash
> ./scripts/audit-cluster-state.sh
> ```
> 5 영역 자동 측정 (kube-context / ns / ArgoCD coverage / operator errors / events / chart version / stale ratio). exit 0 = PASS / 1 = FAIL.

본 문서는 argos 클러스터의 data plane (data ns) 운영 상태 + 상용제품 수준
도달 격차 audit 결과. cluster-ops mode iteration (it45-47 cycle) 누적.

## Live Verification (CLAUDE.md §7 게이트)

```bash
$ kubectl config current-context
argos
$ kubectl get ns data
NAME   STATUS   AGE
data   Active   29h
$ kubectl get application -n argocd | grep platform-data
argos-platform-data                Synced   Healthy
platform-data-clickhouse           Synced   Healthy
platform-data-cnpg                 Synced   Healthy
platform-data-gitlab-postgres      Synced   Healthy
platform-data-gitlab-redis         Synced   Healthy
platform-data-mongodb              Synced   Healthy
platform-data-nats                 Synced   Healthy
platform-data-postgres-operator    Synced   Healthy
platform-data-valkey               Synced   Healthy
```

전 9 apps Synced/Healthy. 운영 안정 (errors 0, events 0, 1h+).

<!-- live-verified: 2026-05-07 -->

## Workload Inventory

| 워크로드 | Type | Phase | Pods | 비고 |
|---|---|---|---|---|
| `mongodbsharded/argos-mongo` | keiailab/mongodb-operator | Running | 21 (5×3 + 3 cfg + 3 mongos) | 21h, 13h 전 shard-1/2/4-0 1회 restart |
| `valkeycluster/keiailab-valkey-prod` | keiailab/valkey-operator | Running ok | 6 (3 shards × 2) | 16384 slots, 6h42m |
| `postgrescluster/argos-postgres` | keiailab/postgres-operator | Ready | 1 (shard-0-0) | 6h29m |
| mongodb-operator | controller-manager | Running 1.4.11 | 1 | 3h23m, webhook 비활성 |
| valkey-operator-prod | controller-manager | Running 1.0.3 | 1 | 87m, webhook 비활성, helm-direct (C24 격차) |
| postgres-operator | controller-manager | Running 0.3.0-alpha.4 | 1 | 118m, webhook 비활성 |

추가 (bitnami):
- `shared-valkey-primary-0` + `shared-valkey-replicas-0` (bitnami valkey 5.6.1, ArgoCD platform-data-valkey 추적).
- `gitlab-redis-primary-0` (bitnami redis), `postgres-default-1..3` (cnpg), `gitlab-postgres-2/3` (cnpg).

## 상용제품 수준 격차 표

| ID | 영역 | 상태 | 영향 | 외부 effect | 우선순위 |
|---|---|---|---|---|---|
| **C24** | data plane GitOps — valkey-operator chart helm-direct + ValkeyCluster CR manual apply | 발견 | drift detection 부재, DR 불가 | argos-platform-data 에 `valkey-operator/` umbrella 추가 | High |
| **C25** | observability — Prometheus Operator 부재 (monitoring.coreos.com group 부재), metrics scrape 0 | 발견 | 모든 metrics endpoint expose 만, Grafana 데이터 소스 부재 | platform-observability 에 kube-prometheus-stack 추가 ArgoCD app | High |
| **T27** | mongodb 1.4.12 release pipeline (image push / GH release / gh-pages / argos-platform-data 0.1.13) | 발견 | webhook + 11 invariant 실 운영 미적용 (1.4.11 anchored) | `make release VERSION=v1.4.12` 4 외부 effect | Medium |
| **I26** | MonitoringSpec orphan (mongodb) — Phase 1 deprecation marker | **완료 100%** | UX 함정 해소 | — (코드 only) | — |
| **I28** | MonitoringSpec orphan Phase 2 trigger — C25 + 30일 사용 측정 | 차단 (C25) | I26 후속 결정 | C25 해소 후 trigger | Low (차단) |
| **F23** | webhook server 도입 (mongodb 11 + valkey 4 + postgres 1 invariant + 18 envtest specs) | **완료 100%** | spec 검증 dual-layer | — (코드 only) | — |
| **C29** | dead RBAC — `valkey-operator` ClusterRole/Binding (helm chart 0.1.0-alpha.2 잔존) | 발견 | cluster pollution, 보안 risk 0 (SA 부재로 권한 행사 불가) | `kubectl delete clusterrole/clusterrolebinding valkey-operator valkey-operator-metrics-auth` | Low |
| **C30** | NetworkPolicy 비대칭 — mongodb/valkey 인스턴스에 NP 부재 (operator 코드 보유 but spec opt-in 미설정) | 발견 | zero-trust 미충족, lateral movement 위험 (security defense in depth 영역) | argos-mongo + keiailab-valkey-prod CR 에 `spec.networkPolicy.enabled=true` 설정 (외부 effect, GitOps 통해) | Medium |
| **C31** | data ns ResourceQuota / LimitRange 0건 — runaway resource consumption 잠재 | 발견 | 단일 워크로드 OOM cluster-wide 영향 가능 | `data` ns 에 ResourceQuota + LimitRange 추가 (argos-platform-data 의 ns manifest, 외부 effect) | Medium |
| **C32** | TLS encryption in transit 부재 — mongodb (27017/27018/27019) + valkey (6379) 평문 | 발견 | data plane 내부 통신 보안 표면 | argos-mongo + keiailab-valkey-prod CR 에 `spec.tls.{enabled,certManager.issuerRef}` 설정. cert-manager 의 letsencrypt-prod 또는 별도 internal CA ClusterIssuer 사용. operator 코드 + webhook invariant 모두 보유 (it46) — chart values 만 활성화. | Medium |
| **C33** | Service mesh 부재 — Istio/Linkerd/Envoy 0건. e2e mTLS / observability sidecar 영역 | 발견 | application-level TLS (C32) 와 별개의 *infrastructure-level mTLS* 부재 | platform stack 결정 (별 RFC). 단일 DC onprem-seoul 환경에서 mesh ROI 검토 후 진행. | Low (장기) |
| **C34** | data plane Backup CronJob 0건 — mongodb pitr / postgres backup spec 미활성 | 발견 | DR 시점 데이터 손실 가능 (현재 oplog tailer 만 보유) | argos-mongo + argos-postgres CR 에 `spec.backup.{enabled,schedule,storage.s3}` 설정. mongodb-operator webhook backup invariant (it46 step 9) 즉시 가드. | High |
| **C35** | keiailab-valkey-prod anti-affinity 부재 — 우연 7-node spread, scheduler 의존 | 발견 | node failure 시 *동일 노드 다중 pod* 위험 (현재 e121/e122 각 2 pods) | keiailab-valkey-prod CR 에 affinity 추가 또는 chart values 의 `affinity.podAntiAffinity` 활성. argos-mongo 의 preferredDuringScheduling weight=100 + hostname topologyKey 패턴 차용. | Medium |
| **C36** | application-level PriorityClass 부재 — data ns 54 pods 모두 priority 0 (default) | 발견 | preemption 시 critical workload (argos-mongo, gitlab-postgres) 와 secondary (gitlab-redis, postgres-default) 동등 우선순위 | argos-platform-data 의 ns manifest 또는 platform-base-namespaces 에 PriorityClass 정의 (`argos-data-critical=10000`, `argos-data-default=1000`) + 워크로드 spec 에 priorityClassName 적용. | Low |

## Clean 영역 (격차 0, 상용제품 수준 충족)

| 영역 | 검증 |
|---|---|
| PodSecurity Standards | data ns `pod-security.kubernetes.io/enforce=restricted` (latest) — B17/F12 회귀 가드 정합 |
| RBAC least privilege | 3 operator ClusterRole 의 *wildcard verbs/resources 0건* — `kubectl get clusterrole <op> -o yaml \| grep '\*'` 결과 empty |
| ImagePullSecrets governance | 모든 SA imagePullSecrets 비어있음 — public ghcr 사용 (인증 secret leak risk 0) |
| ArgoCD GitOps (mongodb / postgres-operator) | argos-platform-data umbrella + platform-data-mongodb / platform-data-postgres-operator app Synced/Healthy. **Drift 0 검증** (2026-05-07): git values vs live spec 비교 — mongodb (version 8.2 / shards 5×3 / cfg 3 / mongos 3 일치) + postgres (PG 18 / initialCount 1 / replicas 0 의도된 dev) |
| controller-runtime + envtest dual-layer | 3 operator 통일 (mongodb / valkey 95.1% / 클린, postgres 94.3% coverage) |
| webhook ADR 7건 (0013-0018) | 결정 추적성 + cross-cut audit pattern 자동화 candidate |
| 운영 안정 | 3 operator log errors 0 (5min), data ns events 0 (1h+) |
| StorageClass governance | 4 SC (ceph-rbd default / ceph-fs / ceph-rgw / cold-rbd) 모두 `Retain` reclaim policy + ALLOWVOLUMEEXPANSION (DR-friendly) |
| Ingress data ns | 0건 (의도된 cluster-internal only — 외부 노출 부재) |
| ServiceAccount tokens | 모든 SA `secrets` 비어있음 (K8s 1.24+ BoundServiceAccountTokenVolume 사용 — legacy long-lived token 부재) |
| Node disk pressure | DiskPressure=False (sample 5 nodes) |
| cert-manager infrastructure | platform-system 의 argos-wildcard-tls / trust-manager + 2 ClusterIssuer (letsencrypt-prod/staging) 보유. 우리 operator 는 미활용 (C32 격차) |
| ImagePullPolicy 일관성 | 3 operator 모두 `IfNotPresent` (production 권장). image bump 시 tag 변경으로 강제 재pull (latest tag 미사용) |
| Liveness/Readiness probes | 3 operator controller-runtime 표준 `/healthz` + `/readyz` 구비 — kubelet 자동 health check |
| Resource requests/limits | 3 operator 동일 (`requests: cpu=100m memory=128Mi`, `limits: cpu=500m memory=512Mi`) — cross-cut consistency. 모든 data ns pods 자체 명시 (C31 ns Quota 부재 환경에서 best practice) |

## Audit trail — 격차 발견 commit 매핑

각 격차의 *발견 cycle commit*. 후속 변경 추적 baseline.

| ID | 발견 commit(s) | 발견 일자 | 비고 |
|---|---|---|---|
| C24 | `0e15552` (1차) + `a0337b6` (CR 라벨) + `7213df8` (manual apply 확정) + `82b3f46` (DR snapshot) | 2026-05-07 | 격차 4-step 점진 발견 |
| C25 | `14ff831` | 2026-05-07 | Prometheus Operator 부재 |
| T27 | `f234517` (TASKS 등록) + `14ff831` (readiness 표) | 2026-05-07 | 1.4.12 release |
| C29 | `212406e` | 2026-05-07 | dead RBAC (helm 0.1.0-alpha.2 잔존) |
| C30 | `5f20e0b` | 2026-05-07 | NetworkPolicy 비대칭 |
| C31 | `5f20e0b` | 2026-05-07 | ns Quota 부재 (C30 동반 발견) |
| C32 | `03e0334` | 2026-05-07 | TLS in transit 부재 |
| C33 | `248f61b` | 2026-05-07 | Service mesh 부재 |
| C34 | `248f61b` | 2026-05-07 | Backup CronJob 0 |
| C35 | `248f61b` | 2026-05-07 | valkey anti-affinity 부재 |
| C36 | `925813c` | 2026-05-07 | PriorityClass 부재 |

**완료 격차** (audit trail 보존):
| ID | 발견 → 해소 commit | 비고 |
|---|---|---|
| I26 | 발견 `f234517` → ADR-0018 `64e34af` → Phase 1 `165631a` → TASKS 갱신 `744a380` | MonitoringSpec orphan Phase 1 deprecation marker |
| F23 | webhook server 도입 it45-47 (5 cycles, 11 commits 종합) | 11 invariant + 18 envtest specs + ADR 6건 |

## 상용제품 수준 KPI 정의

본 audit 의 *진전 측정 baseline*. 이상 도달이 아닌 *측정 가능한 진전* 추구.

### 필수 KPI (production blocker)

| KPI | 현재 | 목표 | 격차 ID |
|---|---|---|---|
| ArgoCD GitOps coverage | 9/9 apps (100%, umbrella + 8 sub) | 9/9 (100%) ✅ | C24 (별 격차 — keiailab-valkey-prod helm-direct, ArgoCD app 자체 부재) |
| Disaster recovery 가능성 | 2/3 operator | 3/3 | C24 (valkey CR git 추적 0) |
| Backup CronJob 활성 | 0/3 operator | 3/3 (mongodb pitr + postgres + valkey RDB) | C34 |
| TLS in transit | 0/3 operator | 3/3 | C32 |
| Webhook 검증 invariants | 16건 (mongodb 11 + valkey 4 + postgres 1) | 20+ (3 operator 통일) | F23 후속 |
| Production release lag | 1.4.11 (1.4.12 main 미적용) | 0 cycles lag | T27 |

### Quality KPI (shouldness)

| KPI | 현재 | 목표 |
|---|---|---|
| envtest admission round-trip coverage | 18 specs (3 operator) | 25+ specs (각 operator 의 잔여 invariant) |
| Test coverage (webhook 패키지) | mongodb 95.1% / valkey clean / postgres 94.3% | 95%+ 일관 |
| ADR cross-reference 의무 | 8 ADR 보유 | 신규 결정 즉시 ADR 의무 |
| Cross-cut audit (ADR-0016) 적용 | 5+ 사례 | 100% (모든 invariant 도입 PR) |
| Docs accuracy (ADR-0016 Errata) | 1 정정 (monitoring.md) | 0 false claim |

### Scaling KPI (장기)

| KPI | 현재 | 목표 |
|---|---|---|
| operator-commons helper 승격 | 4 패키지 (security/labels/networkpolicy/version/webhook/monitoring) | 추가 helper 2건 (validateStorageSize + apiError, ADR-0019) |
| Multi-DC / topology spread | 1 DC (onprem-seoul, 11 nodes) | (장기 요구사항 별 RFC) |
| Service mesh | 미설치 | (C33 RFC 결정) |
| Resource governance | LimitRange/Quota 0 | C31 + C36 |

### *상용제품 수준* 도달 = 다음 모두 충족

1. **필수 KPI**: 모두 목표 도달.
2. **Quality KPI**: 5/5 영역 *목표 80%+* 도달.
3. **Scaling KPI**: *각 항목 명시 결정* (도달 또는 *의도된 미도달* 의 ADR/RFC 기록).
4. **운영 안정**: 30일 연속 errors 0 / events 0 / ArgoCD 9/9 Synced/Healthy.

본 KPI baseline 은 *progress measurement* 의 SSoT. 후속 cycle 마다 본 표
갱신 (해소 시 ✅ 표시).

## 갱신 정책 (audit 신선도)

본 audit 의 *stale 위험* 차단 — 운영 환경 변화 시 audit 재실행 의무.

### Mandatory trigger (즉시 갱신)

| Trigger | 영역 |
|---|---|
| 새 ArgoCD application 추가/삭제 | live verification + workload inventory |
| ns 설정 변경 (PSS / Quota / LimitRange / labels) | 해당 영역 격차 + clean |
| 3 operator chart version bump | release readiness 표 + KPI |
| 격차 해소 commit (예: C24 마이그레이션 완료) | 격차 표 → 완료 격차 + audit trail |
| 새 invariant 추가 (webhook 영역) | F23 후속 + KPI |
| ADR 신규 또는 Errata | ADR cross-reference 표 |

### Periodic trigger (정기 갱신)

| Trigger | 영역 | 주기 |
|---|---|---|
| live verification 재실행 (`<!-- live-verified: YYYY-MM-DD -->` 마커) | kubectl 4-step + workload inventory | 7일 |
| KPI 측정 (errors-free 기간 카운터, ArgoCD coverage 비율) | KPI 표 + clean ratio | 7일 |
| 격차 발견 retroactive sweep (이전 cycle audit 결과 재검증) | 모든 표 | 30일 |

### Skip 가능 (의도된 미갱신)

| 시점 | 사유 |
|---|---|
| 코드 영역 commit (operator 내부 변경) | cluster-side state 영향 0 |
| 다른 ns 변경 (data ns 외) | 본 audit 의 scope 밖 |
| 상용제품 수준 도달 후 | KPI 충족 이후 *유지* mode (별 정책) |

### 자동화 후속 (ADR-0017 governance-report 메트릭 candidate)

- **stale ratio**: `(today - live-verified date) / 7` — 1.0 초과 시 적색.
- **clean ratio 변화**: 매 cycle 의 ratio delta 추적.
- **격차 신규/해소 cycle 분포**: 30일 audit 평균.

본 자동화는 별 cycle 의 `scripts/audit-cluster-state.sh` 작성 영역. 수동
갱신이 baseline.

## DR Snapshots (임시 보관)

git 추적 0 인 CR spec 의 disaster recovery snapshot:

- `docs/operations/cluster-snapshots/2026-05-07/keiailab-valkey-prod.yaml`
  (ValkeyCluster, data ns) — C24 마이그레이션 후 제거.

## ADR Cross-Reference (it45-47)

| ADR | 영역 | 결정 |
|---|---|---|
| [0013](../kb/adr/0013-conditions-last-transition-time-fix.md) | controller status conditions | meta.SetStatusCondition 위임 |
| [0014](../kb/adr/0014-controller-create-pattern-boundary.md) | CreateOrUpdate vs intentional 수동 | bootstrap_lease + helpers 보존 |
| [0015](../kb/adr/0015-webhook-failure-policy-fail.md) | failurePolicy=Fail | 가용성 vs validation 가치 |
| [0016](../kb/adr/0016-cross-cut-audit-pattern.md) | 3 operator 동시 점검 의무 | + Errata: docs accuracy audit |
| [0017](../kb/adr/0017-crd-default-vs-webhook-invariant.md) | dead invariant 분류 | Type A/A'/B/C |
| [0018](../kb/adr/0018-monitoringspec-orphan-resolution.md) | MonitoringSpec orphan | Phase 1 deprecation, 2/3 보류 |

## 운영 chain (1.4.11 anchored)

```
mongodb-operator/main (1.4.12)          ⏳ T27 release pending
  ↓ ghcr 미푸시
mongodb-operator stable (1.4.11)        ✅ keiailab.github.io/mongodb-operator
  ↓ helm chart 1.4.11
argos-platform-data/mongodb stable      ✅ Chart.yaml dependency 1.4.11
  ↓ ArgoCD platform-data-mongodb (Synced/Healthy)
data ns / mongodb-operator 1.4.11       ✅ Running 3h23m
  ↓ MongoDBSharded reconcile
argos-mongo (5 shards × 3 + 3 cfg + 3 mongos)  ✅ Running 21h
```

valkey 영역은 C24 격차로 *chain 단절* — argos-platform-data 부재.

## 후속 cycle 진입점

- **C24 통합 작업** (외부 effect, 사용자 명시 승인):
  1. argos-platform-data 에 `valkey-operator/` 디렉토리 + Chart.yaml + values.yaml.
  2. ValkeyCluster manifest (DR snapshot 기반) templates 흡수.
  3. helm release adoption (`helm.sh/release.v1.valkey-operator-prod` 라벨 인계).
  4. `cluster-snapshots/` 에서 keiailab-valkey-prod.yaml 제거.
- **C25 통합** (외부 effect): platform-observability stack ArgoCD app 추가.
- **T27 release** (외부 effect): `make release VERSION=v1.4.12` 1단계.
