# HANDOFF — mongodb-operator

> 본 문서는 *다음 세션이 컨버세이션 컨텍스트 없이 재개* 가능하도록 작성된다.
> SSOT 는 본 파일 (컨텍스트·결정) + 마지막 commit log (사실).
> 글로벌 `standards/token-budget.md §5` + `standards/workflow.md §2`.

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
