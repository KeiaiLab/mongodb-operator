# ROADMAP — mongodb-operator

본 ROADMAP 은 *날짜 약속이 아니라* 검증 가능한 기능 체크리스트로 진행을 추적한다. Phase 1-4 골격은 가치/도메인 단위 분류이며, *시간 기반 deadline 은 의도적으로 배제*한다 (글로벌 `standards/workflow.md` "시간 기반 로드맵 금지").

## 체크박스 의미

| 마커 | 의미 |
|---|---|
| `[x]` | 코드 + 테스트 양쪽 존재. e2e 또는 unit test 로 회귀 가드 확보 |
| `[~]` | 부분 구현 (CRD 필드만, helper 미통합, 또는 e2e 미완) |
| `[ ]` | 미시작 (설계 또는 PoC 단계) |

각 sub-task 우측 *Verify* 는 검증 명령 또는 e2e 파일을 인용한다.

## 현재 상태 (v1.0.0)

### 핵심 기능 — 구현 완료
- [x] MongoDB ReplicaSet (3-50 멤버) — `api/v1alpha1/mongodb_types.go`, `internal/controller/mongodb_controller.go`
- [x] Sharded Cluster — `api/v1alpha1/mongodbsharded_types.go`, `internal/controller/mongodbsharded_controller.go`
- [x] TLS/SSL (cert-manager 통합) — `internal/controller/tls.go`
- [x] SCRAM-SHA-256 인증 — `internal/controller/mongodb_controller.go` (auth bootstrap)
- [x] S3/PVC 백업 및 복원 — `api/v1alpha1/mongodbbackup_types.go`, `internal/controller/mongodbbackup_controller.go`
- [x] Prometheus 메트릭 노출 — `internal/controller/metrics.go`
- [x] Horizontal Pod Autoscaler — `internal/controller/resources_apply.go` (HPA 자동 생성)
- [x] PVC online resize — `internal/controller/pvc_resize.go`
- [x] Bootstrap race-free (K8s Lease 분산락) — `internal/controller/bootstrap_lease.go`
- [x] PodDisruptionBudget 자동화 — `internal/controller/resources_apply.go` (PDB 분기)

### 강점 (재확인용)
- Kubernetes 네이티브 (CRD + Operator 패턴)
- Prometheus/Grafana 생태계 통합 흐름
- cert-manager 기반 자동 TLS
- 선언적 구성 (GitOps 친화)
- 오픈소스 투명성

## MongoDB Enterprise 비교

| 기능 카테고리 | OSS v1.0.0 | MongoDB Enterprise | 우선순위 |
|--------------|------------|-------------------|----------|
| **보안** | | | |
| LDAP/OIDC 인증 | ❌ | ✅ | 🔴 높음 |
| 저장 데이터 암호화 | ❌ | ✅ | 🔴 높음 |
| 감사 로깅 | ❌ | ✅ | 🟡 중간 |
| **백업/복원** | | | |
| Point-in-Time Recovery | ⚠️ 필드만 | ✅ | 🔴 높음 |
| 쿼리 가능한 백업 | ❌ | ✅ | 🟡 중간 |
| 지속적 백업 | ❌ | ✅ | 🟡 중간 |
| **모니터링** | | | |
| 고급 메트릭 (100+) | ⚠️ 30+ | ✅ | 🟡 중간 |
| Grafana 대시보드 | ❌ | ✅ | 🟢 낮음 |
| 성능 분석 도구 | ❌ | ✅ | 🔴 높음 |
| 인덱스 추천 | ❌ | ✅ | 🟡 중간 |
| **고가용성** | | | |
| 다중 리전 지원 | ⚠️ 수동 | ✅ | 🔴 높음 |
| 무중단 업그레이드 | ⚠️ 부분 | ✅ | 🟡 중간 |
| **운영** | | | |
| 자동 버전 업그레이드 | ❌ | ✅ | 🟡 중간 |
| 멀티 클러스터 관리 | ❌ | ✅ | 🟡 중간 |

범례: 🔴 프로덕션 필수 / 🟡 중요 / 🟢 nice-to-have.

## Phase 1 — 프로덕션 강화

**목표**: 프로덕션 환경의 안정성·운영성 개선.

### 1.1 Point-in-Time Recovery (PITR) 완전 구현
- [~] CRD 필드 정의 (`PITREnabled`, `OplogRetentionHours`) — `api/v1alpha1/common_types.go`
- [ ] Oplog tailing 사이드카 컨테이너 — `internal/resources/oplog_tailer.go` 신규
- [ ] S3 oplog 지속 업로드 controller — `internal/controller/oplog_uploader.go` 신규
- [ ] 타임스탬프 기반 복원 (`Spec.Restore.PointInTime`) — `mongodbbackup_types.go` 확장
- [ ] 복원 검증 자동화 e2e — `test/e2e/pitr_test.go` 신규
- Verify: `test/e2e/pitr_test.go` PASS + restore 후 `db.collection.find({_ts: <T>})` 동등성

### 1.2 Grafana 대시보드 템플릿
- [ ] 클러스터 개요 대시보드 (연결/작업/상태) — `dashboards/cluster-overview.json`
- [ ] ReplicaSet 상태 대시보드 (멤버/복제 지연/oplog) — `dashboards/replicaset.json`
- [ ] Sharded Cluster 대시보드 (샤드 분산/밸런서/청크) — `dashboards/sharded.json`
- [ ] 운영 메트릭 대시보드 (느린 쿼리/잠금/캐시) — `dashboards/operational.json`
- [ ] Helm chart 통합 (`charts/mongodb-operator/templates/dashboards-cm.yaml`)
- Verify: `kubectl apply -f dashboards-cm.yaml` 후 Grafana sidecar 로딩 + 패널 렌더링

### 1.3 자동 버전 업그레이드 (롤백 포함)
- [~] 버전 검증 (`api/v1alpha1/version_validation_test.go`) — 기본 호환성 매트릭스만
- [ ] 롤링 업그레이드 전략 (`spec.upgradeStrategy.type: RollingUpdate`)
- [ ] 업그레이드 전 자동 백업 (`spec.upgradeStrategy.preUpgradeBackup: true`)
- [ ] 파드별 업그레이드 후 검증 기간 (`spec.upgradeStrategy.validationInterval`)
- [ ] 실패 시 자동 롤백 (`spec.upgradeStrategy.rollbackOnFailure: true`)
- [ ] e2e 회귀 가드 (`test/e2e/version_upgrade_test.go` 보강)
- Verify: 8.0 → 8.2 롤링 업그레이드 후 `db.version()` + featureCompatibilityVersion 일치

### 1.4 확장 모니터링 메트릭
- [~] 30+ 기본 메트릭 (`internal/controller/metrics.go`)
- [ ] 쿼리 성능 메트릭 (실행 시간/인덱스 사용)
- [ ] 복제 메트릭 (멤버별 지연/oplog 윈도우)
- [ ] 스토리지 메트릭 (WiredTiger 캐시/압축률)
- [ ] 연결 풀 메트릭 (활성/가용/대기)
- [ ] PrometheusRule 자동 생성 (느린 쿼리 경고 등)
- Verify: 60+ 메트릭 노출 + `prometheus rules list` 출력에 신규 규칙 등록

## Phase 2 — 엔터프라이즈 인증 + 고급 운영

**목표**: 엔터프라이즈 보안 표면 + 다중 리전.

### 2.1 LDAP 인증 지원
- [ ] CRD 필드 (`spec.auth.ldap.{servers, bindMethod, userToDNMapping}`) — `common_types.go` 확장
- [ ] LDAP 서버 연결 helper — `internal/controller/auth/ldap.go` 신규
- [ ] LDAP over TLS 검증
- [ ] 권한 부여 쿼리 매핑
- [ ] e2e (`test/e2e/auth_ldap_test.go` 신규)
- Verify: `mongosh --authenticationMechanism PLAIN -u <ldap-user>` 로그인 + role 매핑 확인

### 2.2 OIDC/OAuth2 인증
- [ ] CRD 필드 (`spec.auth.oidc.{issuerURL, clientID, userClaim, rolesClaim}`)
- [ ] OIDC 토큰 검증
- [ ] 클레임 기반 역할 매핑
- [ ] 외부 IdP 호환 검증 (Keycloak/Okta)
- [ ] e2e (`test/e2e/auth_oidc_test.go` 신규)
- Verify: OIDC 토큰으로 mongosh 인증 + role 매핑

### 2.3 다중 리전 지원 (`MongoDBFederation`)
- [ ] 신규 CRD `MongoDBFederation` — `api/v1alpha1/mongodbfederation_types.go`
- [ ] 다중 cluster kubeconfig 참조 (`spec.regions[].clusterKubeConfigRef`)
- [ ] 지역별 우선순위 (`spec.regions[].priority`)
- [ ] 교차 리전 복제 controller
- [ ] 존 인식 샤딩 통합
- [ ] e2e — kind 다중 클러스터 (`test/e2e/federation_test.go` 신규)
- Verify: 두 클러스터 간 oplog 복제 + 리전 우선순위에 따른 read preference

### 2.4 저장 데이터 암호화 (KMS)
- [ ] CRD 필드 (`spec.storage.encryption.{enabled, keyProvider, kmsConfig}`)
- [ ] Kubernetes Secret 키 스토어
- [ ] HashiCorp Vault 통합
- [ ] 클라우드 KMS (AWS/GCP/Azure)
- [ ] 키 회전 절차 (runbook + controller helper)
- Verify: 디스크 dump 시 평문 미검출 + `db.serverStatus().encryptionAtRest`

## Phase 3 — 고급 엔터프라이즈 기능

**목표**: 엔터프라이즈급 운영 역량.

### 3.1 고급 백업 기능
#### 3.1.1 쿼리 가능한 백업
- [ ] 백업 → 읽기 전용 MongoDB 인스턴스 복원 controller
- [ ] 백업 데이터 검증 + 쿼리 API
- [ ] e2e (`test/e2e/queryable_backup_test.go` 신규)

#### 3.1.2 대역폭 제한
- [ ] CRD 필드 (`spec.backup.throttle.{readMBps, writeMBps}`)
- [ ] 백업 작업 속도 제한 helper
- [ ] 프로덕션 워크로드 영향 측정

#### 3.1.3 자동 백업 검증
- [ ] 주기적 백업 복원 테스트 cron
- [ ] 복원 가능성 보고서 CRD (`MongoDBBackupVerification`)

### 3.2 성능 분석 도구 (`MongoDBInsights`)
- [ ] 신규 CRD `MongoDBInsights`
- [ ] 쿼리 프로파일링 자동 분석
- [ ] 인덱스 추천 엔진
- [ ] 느린 쿼리 감지 + 경고
- [ ] 스키마 디자인 제안
- Verify: `kubectl get mongodbinsights <name> -o yaml` 의 `.status.recommendations` 비어있지 않음

### 3.3 멀티 클러스터 관리 (`MongoDBClusterGroup`)
- [ ] 신규 CRD `MongoDBClusterGroup`
- [ ] 단일 제어 평면 다중 클러스터 reconcile
- [ ] 중앙 모니터링/경고 통합
- [ ] 전역 사용자 관리

### 3.4 고급 감사 로깅
- [ ] MongoDB 감사 로그 구성 helper
- [ ] 중앙 집중 로깅 통합 (Loki/Elasticsearch)
- [ ] 감사 이벤트 분석 + 경고 룰

## Phase 4 — Bitnami `mongodb-sharded` Helm chart 동등성

[Bitnami `mongodb-sharded` 9.4.12 동등성 분석](docs/comparison/bitnami-mongodb-sharded.md) 9건 갭. Helm chart 사용자가 본 Operator 로 *누락 없이 1:1 마이그레이션* 가능해야 한다.

### 4.1 NetworkPolicy 자동 생성 (P0)
- [x] CRD 필드 (`network.policy.enabled`, `allowExternal`, `extraIngress`, `extraEgress`, `ingressNSMatchLabels`) — `api/v1alpha1/common_types.go`
- [x] ResourceBuilder `BuildNetworkPolicy()` — `internal/resources/builder.go`
- [x] Component별 라벨 셀렉터 (mongos/configsvr/shardsvr)
- [x] 기본값 `enabled: false` (기존 클러스터 호환)
- Verify: `internal/resources/builder_test.go` PASS + 신규 가이드는 `enabled: true` 권장

### 4.2 Sharded Arbiter / Hidden member (P0)
- [x] ReplicaSet 의 `ArbiterSpec` — `api/v1alpha1/mongodb_types.go`
- [ ] `MongoDBSharded.spec.shards.arbiter.{enabled,replicas,resources}` 필드 추가
- [ ] `MongoDBSharded.spec.shards.hiddenMembers.{count,priority,votes,tags}`
- [ ] `ShardManager` 분기 — `rs.add({arbiterOnly: true})` / `rs.add({hidden: true, priority: 0})`
- [ ] e2e (`test/e2e/sharded_arbiter_test.go` 신규)
- Verify: `rs.conf()` 에 `arbiterOnly: true` / `hidden: true` 멤버 등록

### 4.3 워크로드 사이드카·extraVolumes·extraEnvVars 주입 (P1)
- [ ] `PodSpec` 확장 — `Sidecars`, `InitContainers`, `ExtraVolumes`, `ExtraVolumeMounts`, `ExtraEnvVars`, `LifecycleHooks`
- [ ] ResourceBuilder StatefulSet/Deployment 합성 로직
- [ ] 보안 가드 — operator admin bootstrap postStart 우선순위
- [ ] 시나리오 e2e (audit/fluentbit/oplog tailer 등 운영 표준)

### 4.4 PVC retention policy 노출 (P1)
- [x] `StorageSpec.PersistentVolumeClaimRetentionPolicy` 필드 — `api/v1alpha1/common_types.go` (Retain/Delete × WhenDeleted/WhenScaled)
- [x] StatefulSet `persistentVolumeClaimRetentionPolicy` 매핑 — `internal/resources/builder.go` (RS/ConfigServer/Shard 3 빌더)
- [x] 단위 테스트 — `internal/resources/builder_test.go::TestPVCRetentionPolicyPropagation` (5 서브테스트: 미설정 nil, 정책 전달)
- [ ] e2e — scale-down 시 PVC 보존/삭제 분기 검증 (후속 PR)

### 4.5 volumePermissions init container (P1)
- [ ] CRD `pod.volumePermissions.{enabled, image, resources}`
- [ ] ResourceBuilder init container 주입 (`chown -R mongodb:mongodb /data/db`)
- [ ] 비활성화 기본값 (fsGroup 우선)
- Verify: non-root/restricted PSA 클러스터에서 pod ready 도달

### 4.6 Init scripts ConfigMap (P2)
- [ ] CRD `initScripts.{configMapRef, secretRef}`
- [ ] `/docker-entrypoint-initdb.d` 마운트 + 컨테이너 entrypoint 순차 실행
- [ ] admin user 부트스트랩 후 1회만 실행 가드
- Verify: 시드 데이터 삽입 후 `db.<col>.countDocuments()` 일치

### 4.7 Service 옵션 확장 (P2)
- [ ] `MongosServiceSpec` 확장 — `sessionAffinity`, `sessionAffinityConfig`, `externalIPs`, `nodePort`, `headless`
- [ ] ResourceBuilder Service 생성 분기

### 4.8 Diagnostic mode + Resource presets (P2)
- [ ] CRD `pod.diagnosticMode.enabled` — `command: ["sleep","infinity"]` + probe 비활성화
- [ ] CRD `pod.resources.preset` — `none/nano/micro/small/medium/large/xlarge/2xlarge`
- [ ] 직접 `resources` 지정 시 preset 무시 우선순위

### 4.9 Scale-in / Member removal (P2)
- [x] `MongoDBSharded.spec.shards.count` 감소 — `removeShard` 호출 + drain 대기 + PVC 정책 — `internal/controller/mongodbsharded_controller.go`
- [ ] `MongoDB.spec.members` 감소 — `rs.remove()` + pod 종료
- [ ] 안전 가드 — drain 미완 시 reconcile 재시도, finalizer 로 stuck 방지
- [ ] e2e (`test/e2e/sharded_scale_in_test.go` 신규)
- Verify: shard 4→3 축소 후 chunk 분포 정합 + 데이터 손실 0

## 우선순위 매트릭스

### 높은 가치, 낮은 난이도 (즉시 실행)
- ✅ Grafana 대시보드 템플릿
- ✅ 확장 모니터링 메트릭
- ✅ 4.4 PVC retention (필드 존재, 매핑만)

### 높은 가치, 높은 난이도 (전략적 투자)
- 🎯 PITR 완전 구현
- 🎯 LDAP/OIDC 인증
- 🎯 다중 리전 (`MongoDBFederation`)
- 🎯 성능 분석 (`MongoDBInsights`)

### 낮은 가치, 낮은 난이도 (빠른 성과)
- 📝 4.7 Service 옵션 확장
- 📝 4.8 Diagnostic mode + presets

### 낮은 가치, 높은 난이도 (회피)
- ❌ Enterprise 바이너리 의존 기능
- ❌ 독점 플랫폼 통합

## 의사결정 기준

1. **사용자 가치** — 프로덕션 환경 실질 필요성
2. **구현 난이도** — 개발 리소스 + 검증 복잡도
3. **커뮤니티 요청** — GitHub Issues 투표
4. **Enterprise 격차** — 엔터프라이즈 비교표 (위)
5. **OSS 실현 가능성** — Enterprise 바이너리 비의존

## Non-Goals (의식적 비대상)

다음은 MongoDB Enterprise 바이너리가 필요하므로 *구현하지 않는다*:

- ❌ In-Memory 스토리지 엔진
- ❌ 필드 레벨 암호화 (CSFLE)
- ❌ FIPS 140-2 준수
- ❌ Ops Manager / Cloud Manager 통합
- ❌ **GitHub Actions 필수 release gate** — RFC 0002 글로벌. 모든 게이트는 로컬 4 계층.
- ❌ **시간 기반 로드맵 deadline** — 글로벌 §workflow.md.

Enterprise 기능이 필요한 경우 MongoDB Enterprise Operator 사용 권장.

## 커뮤니티 기여

- **기능 제안** — GitHub Issues + 사용 사례 + 우선순위 투표
- **코드 기여** — [CONTRIBUTING.md](CONTRIBUTING.md), 작은 PR 부터
- **피드백** — 프로덕션 사용 경험 / 버그 리포트 / 성능 벤치마크

## 참고 자료

- [MongoDB Enterprise Operator](https://github.com/mongodb/mongodb-enterprise-kubernetes)
- [MongoDB 공식 문서](https://www.mongodb.com/docs/)
- [Kubernetes Operators](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
- [Bitnami `mongodb-sharded` 동등성 분석](docs/comparison/bitnami-mongodb-sharded.md)

## 피드백

- **GitHub Issues**: https://github.com/keiailab/mongodb-operator/issues
- **Discussions**: https://github.com/keiailab/mongodb-operator/discussions
- **Email**: support@keiailab.com

## 변경 이력

| Date | Change | Refs |
|---|---|---|
| 2026-05-11 | 전면 재작성 — 분기/주 타임라인 + 날짜 컬럼 완전 제거, sub-task 체크리스트 입자도로 재구성 | parallel-leaping-seal plan |
| 2026-04-28 | Phase 4 부분 완료 — 4.1 NetworkPolicy ✅, 4.9 Sharded scale-in ✅, PDB 자동화 ✅, 부트스트랩 race-free ✅ | production-readiness cycle |

본 ROADMAP 은 살아있는 문서이며, 커뮤니티 피드백과 코드 사실에 따라 갱신된다.
