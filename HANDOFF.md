# HANDOFF — mongodb-operator

> 본 문서는 *다음 세션이 컨버세이션 컨텍스트 없이 재개* 가능하도록 작성된다.
> SSOT 는 본 파일 (컨텍스트·결정) + 마지막 commit log (사실).
> 글로벌 `standards/token-budget.md §5` + `standards/workflow.md §2`.

## 2026-05-07 ralph-loop iteration 7 — valkey 차단요인 2 진단 + 회귀 가드

### 진입점

iteration 6 HANDOFF "다음 iteration 자연 진입점" 의 1번 (valkey 차단요인 2 — version
upgrade reconcile). 사용자 명시 선택 (단일 트랙 집중, T2 등급).

### Step 별 결과

| Step | 결과 | 핵심 인용 / commit |
|---|---|---|
| 1. Kind 진단 (가설 A/B/C 좁히기) | **차단요인 2 fresh 시나리오 재현 안됨** — STS image propagate + Pod rotation 정상 | `kubectl patch valkey vk-bak-target ... 9.0.4` → 5s 후 STS image=9.0.4, 60s 후 Pod 재생성 + image=9.0.4 + Phase=Running |
| 2. 가설 별 fix | **불필요** (Step 1 결과로 skip) | — |
| 3. e2e 회귀 가드 | `test/e2e/version_upgrade_test.go` 신규 — 3 가설 (STS image / Pod image / CR spec preservation) 모두 회귀 가드 | valkey-operator `d5fbbf8` |
| 4. ROADMAP 갱신 + commit + push | ROADMAP P0 → narrow scope 명시 (`[~]`), commit + push + lefthook 6 hooks PASS | valkey-operator `d5fbbf8` |

### 핵심 발견

1. **Plan 의 차단점 분기 적용**: plan.md 에 명시한 "Step 1 진단이 재현 안됨 시" 시나리오 발동.
   d8fa7e8 → 1.0.1 → ab3c18b 사이 어떤 commit 에서 우발 fix 됐거나, 또는 차단요인이 *처음부터*
   narrow scope (bitnami RDB restore → valkey-migrated → patch chain) 한정이었음.
2. **envtest 한계 확인**: `internal/controller/valkey_controller_test.go:89-135` 의 version
   upgrade test 가 PASS 였으나, 이는 fake client 의 in-memory CreateOrUpdate 만 검증. 실제
   K8s API server 의 server-side merge 거부 행동은 envtest 가 모사 못함 — *e2e 가 유일한
   회귀 가드*.
3. **Cross-operator 패턴**: mongodb-operator HEAD (327d639 등) 의 최근 4 commit 이 모두
   *deployment template propagation* 관련 fix (`preserve deployment revision annotation`,
   `overlay owned deployment template fields`, `preserve deployment pod template defaults`).
   valkey 차단요인 2 의 Template propagation 과 동일 카테고리 — K8s operator 의 일반 함정.

### 다음 iteration 자연 진입점

1. **valkey narrow scope 검증** (Phase B 본격 진입 시): `test/e2e/backup_restore_test.go` 확장 —
   bitnami RDB restore → 자체 operator valkey-migrated → version patch chain 시나리오 재현.
   ROADMAP `[~]` → `[x]` 또는 P0 별 fix.
2. **e2e_suite_test.go KIND_CLUSTER override fix**: `kind load --name kind` 가 hardcoded —
   `KIND_CLUSTER` env 무시. 본 iteration 의 e2e 코드를 *실제로 실행하려면* prerequisite.
3. **mongodb-operator template fix 패턴 → valkey/postgresql 점검**: 본 iteration 에서 발견한
   cross-operator 패턴 — 동일 종류 fix 가 valkey/postgresql 에 필요한지 코드 review.
4. **ADR-0058 Phase 1 잔여** (LimitRange + RBAC) — iteration 6 미완료 잔여.
5. **postgres staging smoke** — `hack/smoke.sh` ns override + sample CR ns 매개변수화.

### 검증 인용 (CLAUDE.md §7 클러스터 라이브 사실 게이트)

```
$ kubectl --context kind-valkey-operator-test-e2e get pod vk-bak-target-0 \
    -o jsonpath='{.spec.containers[0].image}'
docker.io/valkey/valkey:9.0.4

$ go vet -tags=e2e ./...                   (0 errors)
$ go test -tags=e2e -count=0 ./test/e2e    (compile PASS)
$ go test -count=1 ./internal/...          (PASS, 0 fail)

$ git log --oneline -1
d5fbbf8 test(e2e): version upgrade 회귀 가드 + ROADMAP 차단요인 2 narrow scope 명시
```

<!-- live-verified: 2026-05-07 -->

---

## 2026-05-07 T21 — MongoDB latest default 정렬 완료

- **구현**:
  - chart values, ArtifactHub examples/images, README/guide samples, GitOps `deploy/mongodb-sharded.yaml`, backup helper image, internal backup default image 를 MongoDB `8.3.1` 로 정렬.
  - builder matrix test 추가: `8.3.1`, `8.2`, `8.0` 에 대해 config server / shard / mongos image 가 각각 `mongo:<version>` 으로 렌더되는지 검증.
  - chart image annotation 의 operator tag 를 chart `appVersion=1.4.6` 과 맞췄다.
- **검증 인용**:
  ```
  $ docker manifest inspect mongo:8.3.1 && docker manifest inspect mongo:8.2 && docker manifest inspect mongo:8.0
  mongo manifests ok: 6

  $ go test ./internal/resources ./internal/controller -run 'TestGetMongoDBImage|TestBuildMongoDBShardedVersionMatrix|TestPodSecurityRestrictedCompliance|MongoDBSharded' -count=1
  ok ./internal/resources ./internal/controller

  $ make test
  PASS

  $ make lint
  0 issues

  $ make validate
  helm lint PASS, helm template OK

  $ kustomize build deploy/overlays/prod
  namespace_count=0, line_count=8773
  ```

## 2026-05-07 F12 — Sharded PodSecurity controller-path 회귀 가드 완료

- **구현**:
  - `MongoDBSharded` envtest에 controller-created PodSpec 검증 추가: cfg StatefulSet → shard StatefulSet → mongos Deployment 순서로 실제 reconcile 산출물을 조회하고 restricted 필수 필드(`capabilities.drop=ALL`, `seccompProfile=RuntimeDefault`, `allowPrivilegeEscalation=false`)를 검사.
  - 테스트 데이터 식별자: `test-sharded-podsecurity`, admin secret password `test_password_20260507`.
  - 실패 재현: Monitoring enabled 시 mongos exporter sidecar가 `SecurityContext=nil`.
  - 수정: mongos exporter sidecar에도 `buildDefaultContainerSecurityContext()` 적용.
- **검증 인용**:
  ```
  $ go test ./internal/controller -run TestControllers -ginkgo.focus 'controller-created cfg, shard, and mongos PodSpecs' -count=1
  ok github.com/keiailab/mongodb-operator/internal/controller 16.711s
  ```
- **해석**:
  - B17의 copy-keyfile init container fix는 builder 단위에만 머무르지 않고 controller reconcile 산출물까지 회귀 가드가 생겼다.
  - 추가로 monitoring exporter sidecar의 restricted 누락도 닫혔다.

## 2026-05-07 옵션 C — 4 트랙 병렬 진입 (ralph-loop iteration 6)

### 진행 결과

| 트랙 | 결과 | Commit / 인용 |
|---|---|---|
| 1. valkey 차단요인 1 (9.x 화이트리스트 + DRY) | ✅ 5 hardcoding → const, SupportedValkeyVersions 도입, webhook validation, 4 신규 test PASS | valkey-operator `d8fa7e8` |
| 1. valkey 차단요인 2 (version upgrade reconcile) | ⏸️ 본 iteration 보류 — 30 일 soak 인스턴스 영향 회피, kind 격리 환경 진단 필요 (다음 iteration) | — |
| 2. valkey 30 일 soak | ✅ 8 pod 1/1 Running (operator + valkey-test + 6 valkey-chaos), 50m+ uptime 유지 | sanity 통과 |
| 3. ADR-0058 Phase 1 (data-staging ns) | ✅ ns Active + ResourceQuota (0/4 cpu, 0/16Gi mem, 0/5 PVC) + NetworkPolicy `deny-from-data` | argos-platform-base `4f8af8d` (main + stable) |
| 4. postgres A1→A2 helm install | ✅ pod 1/1 Running, image 0.3.0-alpha.1 pull 6.875s, leader election won | helm release `postgresql-operator-staging` |
| 4. postgres smoke step 7/8 실측 | ⏸️ 다음 iteration — `hack/smoke.sh` 가 kind 전용 hardcoded, sample CR ns override 추가 작업 필요 | — |

### 핵심 발견 + 운영 이슈

**ArgoCD branch 추적 발견** (인프라 이해 갱신):
- argos-platform-base 의 ArgoCD app 들이 `main` 이 아닌 **`stable` 브랜치 추적**.
- main commit 이 stable 로 promote 되지 않으면 ArgoCD sync 정체 (operationPhase=Error).
- 본 iteration 에서 `git push origin main:stable` 명시 promote 후 sync 정상화 (~30 초).
- 다음 iteration 부터 argos-platform-base commit 시 main + stable 동시 push 패턴 적용.

**repo-server cache 진단**:
- argocd-repo-server 가 manifest cache 를 revision 단위로 hold (`manifest cache hit: ...repoURL/<rev>`).
- stable promote 만으로는 즉시 picked up 안 됨 → `kubectl annotate ... refresh=hard` 추가 필요.

### 검증 인용 (CLAUDE.md §7 클러스터 라이브 사실 게이트)

```
$ kubectl config current-context
argos

$ kubectl get ns data-staging
NAME           STATUS   AGE
data-staging   Active   9s

$ kubectl describe resourcequota -n data-staging
Resource                Used  Hard
persistentvolumeclaims  0     5
requests.cpu            0     4
requests.memory         0     16Gi

$ kubectl get networkpolicy -n data-staging
NAME             POD-SELECTOR   AGE
deny-from-data   <none>         9s

$ kubectl get pods -n data-staging
postgresql-operator-staging-controller-manager-98dd675fc-vmlt8   1/1   Running   0     22s

$ go test ./... (valkey-operator)
ok  github.com/keiailab/valkey-operator/api/v1alpha1                0.458s
ok  github.com/keiailab/valkey-operator/internal/controller         6.961s
ok  github.com/keiailab/valkey-operator/internal/webhook/v1alpha1   2.474s
(전 패키지 PASS, FAIL 0 건)
```

### 다음 iteration 자연 진입점

1. **valkey 차단요인 2 진단** (P0, kind 격리): valkey-test 동등 standalone 인스턴스를 kind 에 띄워 `kubectl patch valkey ... spec.version.version: "9.0.4"` → STS image field 변화 + pod 재생성 관찰 → 가설 A/B/C 중 실제 원인 좁히기.
2. **postgres staging smoke**: `hack/smoke.sh` 의 NAMESPACE override 지원 추가 + sample CR `config/samples/...` 의 ns 매개변수화. SMOKE_FAILOVER=1 으로 step 7 (WAL lag) + step 8 (Failover RTO < 30s) 실측. F02 90% → 100% 게이트.
3. **PostgresCluster CR 적용** (data-staging): 3 replica 단일 shard 인스턴스 → 24h 안정성 + backup → restore 검증 (ADR-0058 Phase 3 진입점).
4. **ADR-0058 LimitRange + RBAC 격리** 추가 (Phase 1 의 잔여 — 본 iteration 은 ns + Quota + NetworkPolicy 만).

### 보류 / 차단요인

- valkey-operator 차단요인 2 의 root cause 미규명 — 가설 A/B/C 중 어느 것인지 진단 후 ADR-0059 작성 가능성.
- argos-platform-base main↔stable 브랜치 동기화 자동화 미설정 — ADR 또는 RFC 후보.
- staging-smoke wrapper script + sample CR ns override 미작성.

<!-- live-verified: 2026-05-07 -->

---

## 2026-05-07 옵션 C — 4 작업 동시 진입 (ralph-loop iteration 5)

### 진행 결과 (4 작업 모두 첫 단계 통과 또는 식별)

| 작업 | 결과 | Commit |
|---|---|---|
| 1. valkey Phase A3 chaos | ✓ 3 shard ValkeyCluster 6 pod 90s, primary kill → 자동 master 재선출 + 데이터 잔존, cluster_state=ok 유지 | valkey-operator `dac77c5` |
| 2. valkey Phase B RDB PoC | ⚠️ 차단 요인 2 종 발견 + ROADMAP 격상 (Valkey 9.x 지원 prerequisite, version upgrade reconcile 결함). ValkeyRestore mechanism 자체는 ✓ | valkey-operator `dac77c5` |
| 3. postgresql Phase A1 결정 | ✓ ADR-0058 채택 — argos `data-staging` ns 분리 (별도 클러스터 비용 ↑ 회피, 인프라 동질성 ↑) | argos-infra-bootstrap `e949d99` |
| 4. postgresql F02 → 100% | ✓ smoke.sh 에 [7/8] WAL lag + [8/8] Failover RTO 측정 step 추가 (kind 실측은 다음 iteration) | postgres-operator `b4317dc` |

### Phase B 차단 요인 (다음 iteration 영역)

1. **Valkey 9.x 지원 1.x 라인 격상** — bitnami/valkey 9.0.4 의 RDB format v80 → 자체 operator 8.1.6 호환 불가. 마이그레이션 prerequisite. ROADMAP 갱신 완료.
2. **version upgrade reconcile 결함** — `spec.version.version` patch 가 STS template image 로 propagate 안 됨. P0.

### 현재 클러스터 상태 (2026-05-07)

```
valkey-operator-system ns:
  valkey-operator (0.1.0-alpha.2, 1/1 Running 14m)
  valkey-test (Standalone, Running 27m) — Phase A2 smoke 유지
  valkey-chaos (3 shard, slots=16384, Running 9m) — Phase A3 30 일 soak 진입
data ns: 변경 없음 (mongodb 21 pod, postgres-default 3 instance, shared-valkey 2 pod 모두 정상)
```

### 다음 iteration 자연 진입점

- **valkey 차단 요인 fix** (P0): Valkey 9.x 지원 1.x 라인 격상 + version upgrade reconcile 결함. 두 차단 요인 fix 후 Phase B 재시도.
- **valkey 30 일 soak**: valkey-chaos / valkey-test 유지 + 주간 chaos test 반복.
- **postgresql Phase A1 구현**: ADR-0058 Phase 1 (data-staging ns + ResourceQuota + NetworkPolicy 매니페스트 적용).
- **postgresql Phase A1 → A2**: data-staging ns 에 postgresql-operator side-by-side 배포 + smoke.sh 실측 (WAL lag + RTO).

<!-- live-verified: 2026-05-07 -->

---

## 2026-05-07 옵션 C — valkey-operator Phase A1+A2 통과 (ralph-loop iteration 4)

### 배경

운영자 명시 결정 (옵션 C — "디버깅 진행하면서 상용제품 수준의 완성도까지").
ADR-0056 (CNPG → 자체 postgresql, 8 단계 게이트) + ADR-0057 (bitnami/valkey
→ 자체 valkey, 6 단계 게이트) 작성 후 *valkey 부터* 단계적 진행 (캐시 특성상
postgresql 보다 위험 낮음).

### Phase A1+A2 진행 — valkey-operator 측 (PASS)

| 게이트 | 결과 |
|---|---|
| A1: side-by-side 사전 배포 (valkey-operator-system ns) | ✓ helm install local chart → pod 1/1 Running, Certificate/Issuer/ValidatingWebhookConfiguration Ready |
| A2: 빈 ValkeyCluster (실제 Valkey CR Standalone) 동작 검증 | ✓ valkey-test 1/1 Running, valkey 8.1.6, SET/GET smoke round-trip 정상 |

### 발견 + Fix (chart RBAC P0 결함)

published 0.1.0-alpha.1 chart 가 `features.cluster.enabled` /
`features.backup.enabled` 조건부로 RBAC 부여 — 그러나 operator 코드
(`cmd/main.go`) 는 *항상* 모든 controller 등록 → flag=false 시
informer forbidden startup 실패. RBAC 와 코드 mismatch 가
production-grade 차단 요인.

해소 (commit valkey-operator `06237be`):
- `charts/valkey-operator/templates/clusterrole.yaml` features 분기 제거
- chart version 0.1.0-alpha.1 → 0.1.0-alpha.2
- 새 image 빌드 (linux/amd64) + ghcr.io push (`bd630f9d76228365`)
- gh-pages 0.1.0-alpha.2.tgz publish
- 클러스터 helm upgrade → 새 pod 1/1 Running, valkey-test 12m 유지 (zero-downtime)

### 이 iteration 의 모든 commit

| Repo | Commit | 내용 |
|---|---|---|
| argos-infra-bootstrap | `43fd542` | ADR-0056 + ADR-0057 (자체 operator 채택 로드맵) |
| valkey-operator main | `06237be` | RBAC chart fix + 0.1.0-alpha.2 |
| valkey-operator gh-pages | `55e8327` | 0.1.0-alpha.2.tgz publish |

### 클러스터 현재 상태

```
namespace valkey-operator-system:
  pod/valkey-operator-5c85c786c7-mjtvn  1/1 Running  (0.1.0-alpha.2)
  pod/valkey-test-0                      1/1 Running  (valkey 8.1.6)
  Certificate/Issuer/ValidatingWebhookConfiguration  Ready
기존 namespace data 의 shared-valkey-primary/replicas 영향 없음 (side-by-side).
```

### 다음 단계 (다음 iteration / 차단점)

#### Phase A3 — 1.0 stable 졸업 (30 일 soak 권장)
- valkey-test 인스턴스 *유지* — 다음 iteration 들에서 chaos engineering / fault injection / 메모리 압박 테스트
- ValkeyCluster (3 shard, sharded mode) 인스턴스 추가 검증 — Standalone 외 cluster mode 도 검증
- backup/restore 시나리오 — S3 dump / restore init container 실측

#### Phase B — bitnami/valkey → 자체 operator 마이그레이션 도구 (다음 iteration 영역)
- RDB dump from `shared-valkey-primary-0` (bitnami)
- 자체 ValkeyCluster CR 의 restore init container 로 데이터 복원
- staging 환경에서 1 회 검증

#### Phase C — argos cutover (Phase A3 + B 완료 후)
- 새 ArgoCD app `platform-data-valkey-operator` 추가
- 비-critical 워크로드 cutover → bitnami app 제거

### postgresql-operator Phase A (다음 iteration들)

ADR-0056 8 단계 게이트는 더 큰 작업:
- F02 (instance manager) 100% — kind 실측 + WAL lag 측정 (현재 90%)
- F03 (election/fencing) 100% — chaos-mesh primary kill 시나리오 (현재 10%)
- F04 (pgBackRest 통합) 100% — backup CR + WAL archive (현재 10%)
- F05 (chaos-mesh failover RTO < 30s) 100% — 시나리오 5 종 (현재 10%)
- 0.3.0-alpha.1 → 1.0.0 stable 90 일 soak

production-mirror 클러스터 별도 필요. 현재 argos 1 클러스터 만으로는 soak 불가. 인프라 결정 필요.

<!-- live-verified: 2026-05-07 -->

---

## 2026-05-07 운영 장애 + PodSecurity 사고 종료 (ralph-loop iteration 2 — 사용자 권한 부여 후 자동 진행)

### 실행 결과 요약 (모든 차단점 해소)

| 단계 | 작업 | 결과 |
|---|---|---|
| **P0 운영 장애** | e121/e122 etcd 노드 tailscaled 폭주 진단 | systemd watchdog restart counter 210, wireguard-go panic stack JSON dump → /var/log/syslog 847GB |
| | tailscaled.service stop + disable + mask | ✓ 양 노드 (`active=inactive enabled=masked`) |
| | /var/log/syslog truncate (847GB → ~1KB) | ✓ 양 노드 디스크 100% → 4% |
| | kubelet DiskPressure transition 확인 | ✓ e121 + e122 `DiskPressure=False` |
| | Evicted/Failed pod 35건 일괄 정리 | ✓ 1차 정리 완료 |
| **P1 코드 fix** | mongodb-operator commit `85c692d` (PodSecurity SC fix + 회귀 가드) | ✓ go test 4 패키지 PASS |
| | mongodb-operator commit `c5b26de` (chart 1.4.5 → 1.4.6 + CHANGELOG) | ✓ push origin main |
| | docker buildx + push (`ghcr.io/keiailab/mongodb-operator:1.4.6@c4d59112`) | ✓ ghcr.io 등록 |
| | `make helm-publish` (gh-pages 1.4.6.tgz publish) | ✓ index.yaml + chart fetchable |
| **P2 GitOps deploy** | argos-platform-data commit `b378590` (Chart.yaml: dependency + appVersion 1.4.6) | ✓ push main + stable |
| | helm v4.1.4 dep update 버그 회피 — helm v3.18.4 로 정확한 Chart.lock 재생성 | digest sha256:897ed69a... |
| | argos-platform-data commit `87ce471` (Chart.lock digest 정합) | ✓ push main + stable |
| | ArgoCD `platform-data-mongodb` 강제 sync | ✓ `Synced/Progressing rev=87ce471...` |
| | mongodb-operator pod image rollout (1.4.5 → 1.4.6) | ✓ 새 ReplicaSet `868bdf55b-rmbg6` 1/1 Running |
| | argos-mongo-cfg StatefulSet pod admission 회복 | ✓ 0/3 → 3 pod 생성 (Init phase 진입) |
| **부수 처리** | mongodb-admin K8s Secret 누락 발견 (ADR-0041 Phase 3 차단의 manual secret) | ✓ `kubectl create secret generic mongodb-admin -n data ...` 생성 |

### 검증 인용

```
kubectl get nodes (e121,e122):
  e121: DiskPressure=False, Ready=True
  e122: DiskPressure=False, Ready=True

ArgoCD platform-data-mongodb:
  Synced/Progressing rev=87ce4716f44e03e7c96889bf600d46cf20b9e458

mongodb-operator pod images:
  platform-data-mongodb-mongodb-operator-868bdf55b-rmbg6: ghcr.io/keiailab/mongodb-operator:1.4.6  (1/1 Running 69s)

argos-mongo-cfg StatefulSet:
  argos-mongo-cfg-0: Init:0/1 (이전 admission 거부 → 현재 정상 Init phase)
  argos-mongo-cfg-1: Init:0/1
  argos-mongo-cfg-2: Init:0/1
```

### 후속 (다음 iteration 또는 사용자 판단)

- argos-mongo-cfg-* pod 의 Init phase → Running 도달 확인 (monitor 진행 중).
- valkey-operator 동일 결함 fix 적용 commit `eeaade8` (4 곳 SC + helper + 회귀 가드) 별도 진행 — 운영 영향 없음 (CRD 미배포).
- valkey-operator 동등 수준화 잔여 항목: CODE_OF_CONDUCT / GOVERNANCE / MAINTAINERS / ROADMAP 문서 추가 + operator 자체 클러스터 배포.
- ADR-0041 Phase 3 (Infisical Cloud / ESO ExternalSecret 자동화) — manual secret 의 영구 해소 경로.

<!-- live-verified: 2026-05-07 -->

---

## 2026-05-07 운영 장애 진단 + PodSecurity 코드 결함 수정 (ralph-loop iteration 1)

### 장애 사실 (kubectl 검증, context: argos)

```
e121, e122 (control-plane,etcd) → DiskPressure=True
영향 pod 41분 내 evict 17건+: mongodb-operator x11, vector x4, kube-vip x4, postgres-default-1 x1, cnpg-tpf46 x1
이벤트 메시지: "The node was low on resource: ephemeral-storage. Threshold quantity: 49033350669, available: ~50118320Ki"
지속 발생: 41m → 38m → 33m → 25m → 10m 까지 evict 반복 — 미해결 진행 중 사고
```

### 근본 원인 분리

| 카테고리 | 원인 | 책임 영역 | 본 iteration 처리 |
|---|---|---|---|
| **운영 장애** | etcd 노드 e121/e122 ephemeral-storage 부족 | 클러스터 인프라 (operator 무관) | **차단점 — 사용자 확인 필요** (CLAUDE.md §self-repair 금지영역: 이미지 GC) |
| **operator 코드 결함** | `argos-mongo-cfg` StatefulSet의 `copy-keyfile` init container 가 PodSecurity restricted 위반 (capabilities.drop, seccompProfile 누락) | mongodb-operator 코드 | **수정 완료** (본 iteration) |
| **GitOps 구조** | 클러스터 배포는 `argos-platform-data` repo 의 chart 가 ArgoCD source — `WorkSpace/public/mongodb-operator` 는 이미지 빌드 출처일 뿐 직접 배포 경로 아님 | 외부 repo | **확인 완료, 변경 보류** |

### PodSecurity 수정 내용 (본 iteration commit 대상)

`internal/resources/builder.go`:
- `buildDefaultContainerSecurityContext()` 에 `SeccompProfile: {Type: RuntimeDefault}` 추가 (mongodb 컨테이너 대응).
- 신규 helper `buildKeyfileInitContainerSecurityContext()` — capabilities.drop=[ALL] + seccompProfile + RunAsGroup 통합. PodSecurity restricted 만족.
- 4곳 인라인 SecurityContext 정의 (`MongoDB` ReplicaSet + `MongoDBSharded` ConfigServer/Shard/Mongos) → helper 호출로 통일. DRY 회복.

`internal/resources/builder_test.go`:
- `TestPodSecurityRestrictedCompliance` 신규 — 3개 sub-test (ReplicaSet, ConfigServer, Shard StatefulSet) 가 모든 컨테이너 + init container 의 capabilities.drop=[ALL] + seccompProfile.type=RuntimeDefault + AllowPrivilegeEscalation=false 를 검증. 회귀 가드.

### 검증 (PASS 인용)

```
go test ./internal/... 결과:
ok  	github.com/keiailab/mongodb-operator/internal/assets	0.524s
ok  	github.com/keiailab/mongodb-operator/internal/controller	8.102s
ok  	github.com/keiailab/mongodb-operator/internal/mongodb	1.610s
ok  	github.com/keiailab/mongodb-operator/internal/resources	1.066s

신규 회귀 가드:
--- PASS: TestPodSecurityRestrictedCompliance (0.00s)
    --- PASS: .../MongoDB_ReplicaSet_StatefulSet
    --- PASS: .../MongoDBSharded_ConfigServer_StatefulSet
    --- PASS: .../MongoDBSharded_Shard_StatefulSet
```

### 다음 단계 (사용자 확인 필요 영역 = 차단점)

1. **etcd 노드 디스크 청소** (P0, 운영 장애 직접 원인). 옵션: containerd 이미지 GC, 로그 로테이션, etcd snapshot purge. SSH 접근 + sudo 권한 영역. **CLAUDE.md §self-repair 금지영역**.
2. **이미지 빌드/push** (P1): 본 수정은 코드만. `ghcr.io/keiailab/mongodb-operator:1.4.6` (or next) 빌드 + push 필요. 노드 디스크 청소 후 진행 권장 (push 가 또 디스크 채울 위험).
3. **`argos-platform-data` repo 의 mongodb chart values** 가 새 이미지 태그를 참조하도록 업데이트 — 외부 repo 권한 필요.
4. **postgresql/valkey operator 동등 수준화** (다음 iteration 영역, 본 repo 외부).

## 현재 상태 (2026-05-07, governance 표준 정합 — P0+P1 baseline 도달)

- **HEAD `0ab77c3`**: `docs(governance): ADR-0011 (pre-commit 분기 정당화) + deps log seed (P0+P1 baseline)`
- **HEAD~1 `6102298`**: `chore(release): smoke-test SBOM+trivy 검증 강화 + step 번호 정합`
- **HEAD~2 `261c61d`**: `fix(deploy): namespace prod/db → data 통합 + storageClass ceph-rbd 정합 (#114)` (data namespace 통합 — 3-repo 일치)

## 본 세션 (상용 제품 수준 trajectory) 작업

### 1. release pipeline 강화 (3-repo 정합)
- SBOM (SPDX `.spdx.json`) asset 검증 step 추가 — supply chain 표준.
- trivy image post-publish HIGH/CRITICAL fixed-only scan 추가 — 운영 후 CVE 모니터링.
- smoke-test step 번호 [N/5] → [N/6] 정합 (재번호 누락 버그 수정).
- 검증: `./scripts/release-smoke-test.sh` → **12 PASS / 0 FAIL** (v1.4.5).

### 2. governance 표준 정합 (P0+P1 baseline)
- ADR-0011 신규: Hook 도구로 pre-commit 채택 (글로벌 lefthook 표준 분기 정당화).
- INDEX.md 갱신 (ADR-0011 추가).
- `docs/kb/deps/2026-05.md` seed (enforcement.md §2.4) — direct 10건 + 총 107 baseline.

## 3-repo governance 정합 매트릭스 (2026-05-07 기준)

| 항목 | mongodb | postgres | valkey | 표준 |
|---|---|---|---|---|
| ADR 경로 | docs/kb/adr ✓ | docs/kb/adr ✓ | docs/kb/adr ✓ | enforcement §2.1 |
| ADR INDEX | ✓ | ✓ | ✓ | adr.md §1 |
| deps log | ✓ | ✓ | ✓ | enforcement §2.4 |
| hook 도구 | pre-commit (ADR-0011) | pre-commit (ADR-0007) | lefthook (정합) | enforcement §1.1 |
| GH workflows | 0 ✓ | 0 ✓ | 0 ✓ | RFC 0002 |
| Makefile L3 게이트 | ✓ | ✓ | ✓ | ci.md v1 |
| CHANGELOG + cliff | ✓ | ✓ | ✓ | enforcement §2.3 |
| smoke-test 12 step | ✓ | ✓ | ✓ | 3-repo 정합 |
| make lint PASS | ✓ (staticcheck) | ✓ (golangci-lint) | ✓ (golangci-lint) | linting.md |

## Quality baseline (2026-05-07 실측)

`enforcement.md §3.4 (Coverage 합산)` 의 P2 측정 — 본 세션 baseline.

```
$ make test    # exit 0 / FAIL: 0
internal/controller    coverage: 31.9% of statements
internal/mongodb       coverage: 41.3% of statements
internal/resources     coverage: 72.7% of statements
internal/webhook       (no test 패키지)
```

**80% 목표 (enforcement §3.4)** 대비:
- ✓ resources 72.7% (근접)
- ✗ controller 31.9% / mongodb 41.3% — envtest 기반 reconcile 시나리오 추가 권장

`enforcement` 의 "절대치보다 *변경된 코드의 커버 여부*가 우선" 원칙 적용 — 본 baseline 은 회귀 비교 기준점.

## 알려진 잔여 gap (별 트랙)

1. **golangci-lint 미설치**: 본 repo 의 `make lint` 는 `go vet + staticcheck` 만 실행 (golangci-lint 가 없으면 silent skip). postgres + valkey 패턴 (golangci-lint v2.x) 으로 정합 권장.
2. **dependabot.yml + renovate.json 중복**: 본 repo 는 둘 다 보유. postgres + valkey 는 renovate.json 만. ADR 로 도구 선택 정당화 또는 dependabot.yml 제거 필요.

## 글로벌 참조

- 글로벌 표준: `~/Documents/ai-dev/standards/{principles,workflow,checklist,linting,testing,ci,adr,enforcement}.md`
- 본 repo ADR INDEX: `docs/kb/adr/INDEX.md`
- 본 repo deps 추적: `docs/kb/deps/`
- 사전 검증 SDK: `sonatype-guide:sonatype-guide` skill — 의존성 추가/업그레이드 전 의무 호출.

## 다음 단계 (열린 트랙)

1. golangci-lint 도입 (3-repo 정합).
2. dependabot vs renovate 정합 ADR.
3. RFC-0004 클러스터 라이브 게이트 발동 시 (운영 주장 추가) 본 HANDOFF 의 `<!-- live-verified: YYYY-MM-DD -->` 마커 + kubectl/argocd 인용 추가.
