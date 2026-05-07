# Cluster Ops Audit — argos data plane (2026-05-07)

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
