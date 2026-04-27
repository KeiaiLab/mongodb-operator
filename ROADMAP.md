# MongoDB Operator 확장 로드맵

## 개요

이 문서는 MongoDB Operator의 장기 확장 계획을 설명합니다. v1.0.0 GA 릴리스 이후, MongoDB Enterprise 기능과의 격차를 줄이고 프로덕션 환경에서의 운영 경험을 개선하는 것을 목표로 합니다.

## 현재 상태 (v1.0.0)

### 구현 완료
- ✅ MongoDB ReplicaSet (3-50 멤버)
- ✅ Sharded Cluster
- ✅ TLS/SSL 암호화 (cert-manager 통합)
- ✅ SCRAM-SHA-256 인증
- ✅ S3/PVC 백업 및 복원
- ✅ Prometheus 모니터링
- ✅ Horizontal Pod Autoscaler
- ✅ 프로덕션급 CI/CD 파이프라인

### 주요 강점
- Kubernetes 네이티브 통합 (CRD, Operator 패턴)
- Prometheus/Grafana 생태계 통합
- cert-manager를 통한 자동 TLS 관리
- 선언적 구성 (GitOps 친화적)
- 오픈소스 투명성

## MongoDB Enterprise 비교

| 기능 카테고리 | OSS v1.0.0 | MongoDB Enterprise | 우선순위 |
|--------------|------------|-------------------|----------|
| **보안** |
| LDAP/OIDC 인증 | ❌ | ✅ | 🔴 높음 |
| 저장 데이터 암호화 | ❌ | ✅ | 🔴 높음 |
| 감사 로깅 | ❌ | ✅ | 🟡 중간 |
| **백업/복원** |
| Point-in-Time Recovery | ⚠️ 부분 | ✅ | 🔴 높음 |
| 쿼리 가능한 백업 | ❌ | ✅ | 🟡 중간 |
| 지속적 백업 | ❌ | ✅ | 🟡 중간 |
| **모니터링** |
| 고급 메트릭 (100+) | ⚠️ 30+ | ✅ | 🟡 중간 |
| Grafana 대시보드 | ❌ | ✅ | 🟢 낮음 |
| 성능 분석 도구 | ❌ | ✅ | 🔴 높음 |
| 인덱스 추천 | ❌ | ✅ | 🟡 중간 |
| **고가용성** |
| 다중 리전 지원 | ⚠️ 수동 | ✅ | 🔴 높음 |
| 무중단 업그레이드 | ⚠️ 부분 | ✅ | 🟡 중간 |
| **운영** |
| 자동 버전 업그레이드 | ❌ | ✅ | 🟡 중간 |
| 멀티 클러스터 관리 | ❌ | ✅ | 🟡 중간 |

**범례**:
- 🔴 높음: 프로덕션 필수, 즉시 구현 필요
- 🟡 중간: 중요하지만 우선순위 낮음
- 🟢 낮음: Nice-to-have

## Phase 1: 프로덕션 강화 (2026 Q1 - 3개월)

**목표**: 프로덕션 환경에서의 안정성 및 운영성 개선

### 1.1 Point-in-Time Recovery (PITR) 완전 구현

**설명**: 특정 시점으로 데이터베이스 복원 가능

**구현 사항**:
- 지속적 oplog 백업 (S3/PVC)
- Oplog 테일링 사이드카 컨테이너
- 타임스탬프 기반 복원 기능
- 복원 검증 자동화

**CRD 변경**:
```yaml
spec:
  backup:
    pitrEnabled: true
    oplogRetentionHours: 24
    oplogStorageLocation:
      type: s3
      s3:
        bucket: mongodb-oplog-backups
```

**예상 기간**: 2-3주

### 1.2 Grafana 대시보드 템플릿

**설명**: 사전 구성된 Grafana 대시보드 제공

**대시보드 목록**:
1. 클러스터 개요 (연결, 작업/초, 상태)
2. ReplicaSet 상태 (멤버, 복제 지연, oplog)
3. Sharded Cluster (샤드 분산, 밸런서, 청크)
4. 운영 메트릭 (느린 쿼리, 잠금, 캐시)

**예상 기간**: 1주

### 1.3 자동 버전 업그레이드

**설명**: MongoDB 버전 자동 업그레이드 with 롤백

**구현 사항**:
- 롤링 업그레이드 전략
- 업그레이드 전 자동 백업
- 검증 기간 (각 파드 업그레이드 후)
- 실패 시 자동 롤백

**CRD 변경**:
```yaml
spec:
  version:
    version: "8.2"
    autoUpgrade: true
    upgradeStrategy:
      type: RollingUpdate
      preUpgradeBackup: true
      rollbackOnFailure: true
```

**예상 기간**: 2주

### 1.4 확장 모니터링 메트릭

**설명**: 60+ 추가 메트릭 수집

**메트릭 카테고리**:
- 쿼리 성능 (실행 시간, 인덱스 사용)
- 복제 (멤버별 지연, oplog 윈도우)
- 스토리지 (WiredTiger 캐시, 압축률)
- 연결 (풀 사용, 활성/가용)

**예상 기간**: 1주

**Phase 1 총 기간**: 6-7주

## Phase 2: 엔터프라이즈 인증 및 고급 운영 (2026 Q2 - 3개월)

**목표**: 엔터프라이즈 보안 및 다중 리전 지원

### 2.1 LDAP 인증 지원

**설명**: LDAP/Active Directory 통합

**구현 사항**:
- LDAP 서버 연결
- 사용자-DN 매핑
- 권한 부여 쿼리
- LDAP over TLS

**CRD 변경**:
```yaml
spec:
  auth:
    ldap:
      servers:
        - ldap://ldap.example.com
      bindMethod: simple
      userToDNMapping: '[{match: "(.+)", ldapQuery: "DC=example,DC=com??sub?(uid={0})"}]'
```

**예상 기간**: 3-4주

### 2.2 OIDC/OAuth2 인증

**설명**: OpenID Connect 통합

**구현 사항**:
- OIDC 토큰 검증
- 클레임 기반 역할 매핑
- 외부 IdP 지원 (Keycloak, Okta)

**CRD 변경**:
```yaml
spec:
  auth:
    oidc:
      issuerURL: https://auth.example.com
      clientID: mongodb-operator
      userClaim: sub
      rolesClaim: roles
```

**예상 기간**: 2-3주

### 2.3 다중 리전 지원

**새 CRD**: `MongoDBFederation`

**구현 사항**:
- 여러 Kubernetes 클러스터 관리
- 지역별 읽기/쓰기 선호도
- 교차 리전 복제
- 존 인식 샤딩

**예시**:
```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBFederation
metadata:
  name: global-mongodb
spec:
  regions:
    - name: us-east-1
      clusterKubeConfigRef:
        name: us-east-1-kubeconfig
      priority: 1
    - name: eu-west-1
      clusterKubeConfigRef:
        name: eu-west-1-kubeconfig
      priority: 2
```

**예상 기간**: 4-5주

### 2.4 저장 데이터 암호화

**설명**: 디스크 암호화 with KMS

**구현 사항**:
- Kubernetes Secret 키 스토어
- HashiCorp Vault 통합
- 클라우드 KMS (AWS KMS, GCP KMS, Azure Key Vault)

**CRD 변경**:
```yaml
spec:
  storage:
    encryption:
      enabled: true
      keyProvider: aws-kms
      kmsConfig:
        aws:
          region: us-east-1
          keyId: arn:aws:kms:...
```

**예상 기간**: 3주

**Phase 2 총 기간**: 12-15주

## Phase 3: 고급 엔터프라이즈 기능 (2026 Q3-Q4 - 6개월)

**목표**: 엔터프라이즈급 운영 역량

### 3.1 고급 백업 기능

#### 3.1.1 쿼리 가능한 백업
- 백업을 읽기 전용 MongoDB 인스턴스로 복원
- 백업 데이터 검증 및 쿼리

#### 3.1.2 대역폭 제한
- 백업 작업 속도 제한
- 프로덕션 워크로드 영향 최소화

#### 3.1.3 자동 백업 검증
- 주기적으로 백업 복원 테스트
- 복원 가능성 보고

**예상 기간**: 5-6주

### 3.2 성능 분석 도구

**새 CRD**: `MongoDBInsights`

**구현 사항**:
- 쿼리 프로파일링 자동 분석
- 인덱스 추천 엔진
- 느린 쿼리 감지 및 경고
- 스키마 디자인 제안

**예시**:
```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBInsights
metadata:
  name: production-insights
spec:
  clusterRef:
    name: production-mongodb
  profilingLevel: 1  # slow queries
  slowQueryThreshold: 100  # ms
  analysisSchedule: "0 2 * * *"  # 매일 02:00
```

**예상 기간**: 6-8주

### 3.3 멀티 클러스터 관리

**새 CRD**: `MongoDBClusterGroup`

**구현 사항**:
- 단일 제어 평면에서 여러 클러스터 관리
- 중앙 집중식 모니터링 및 경고
- 전역 사용자 관리

**예상 기간**: 8주

### 3.4 고급 감사 로깅

**구현 사항**:
- MongoDB 감사 로그 구성
- 중앙 집중식 로깅 (Loki, Elasticsearch)
- 감사 이벤트 분석 및 경고

**예상 기간**: 3-4주

**Phase 3 총 기간**: 22-26주

## Phase 4: Bitnami `mongodb-sharded` Helm chart 동등성 (2027 Q1 - 3개월)

[Bitnami `mongodb-sharded` 9.4.12 동등성 분석](docs/comparison/bitnami-mongodb-sharded.md)에서 도출된 9건의 갭. Helm chart 사용자가 본 Operator로 전환할 때 누락 없는 1:1 마이그레이션이 가능하도록 한다.

### 4.1 NetworkPolicy 자동 생성 (P0)

**구현 사항**:
- `MongoDB`/`MongoDBSharded` CRD에 `network.policy.enabled`, `allowExternal`, `extraIngress`, `extraEgress`, `ingressNSMatchLabels` 필드 추가
- ResourceBuilder에 `BuildNetworkPolicy()` 추가 (mongos/configsvr/shardsvr 컴포넌트별 라벨 셀렉터)
- 기본값: `enabled: false` (기존 클러스터 호환성), 신규 가이드는 `true` 권장

**근거**: 매트릭스 #18 (Bitnami 기본 enabled, 본 프로젝트 미지원).
**예상 기간**: 2-3주.

### 4.2 Sharded Arbiter / Hidden member 지원 (P0)

**구현 사항**:
- `MongoDBSharded.spec.shards.arbiter.{enabled,replicas,resources}` 필드 추가
- `MongoDBSharded.spec.shards.hiddenMembers.{count,priority,votes,tags}` 필드 추가
- `ShardManager`에서 `rs.add({arbiterOnly: true})` / `rs.add({hidden: true, priority: 0})` 호출 분기

**근거**: 매트릭스 #1 (비용 최적화 토폴로지, 분석/백업 격리 시나리오 누락).
**예상 기간**: 3-4주.

### 4.3 워크로드 사이드카·extraVolumes·extraEnvVars 주입 (P1)

**구현 사항**:
- `PodSpec`에 `Sidecars []corev1.Container`, `InitContainers []corev1.Container`, `ExtraVolumes []corev1.Volume`, `ExtraVolumeMounts []corev1.VolumeMount`, `ExtraEnvVars []corev1.EnvVar`, `LifecycleHooks *corev1.Lifecycle` 필드 추가
- ResourceBuilder가 StatefulSet/Deployment 생성 시 합성
- 보안 가드: operator가 주입하는 admin bootstrap postStart는 항상 우선

**근거**: 매트릭스 #26-28 (audit 로그, fluentbit, oplog tailer 등 운영 표준 갭).
**예상 기간**: 2-3주.

### 4.4 PVC retention policy 노출 (P1)

**구현 사항**:
- `StorageSpec`에 `retentionPolicy.{whenScaled,whenDeleted}` 필드 추가 (`Retain` | `Delete`)
- StatefulSet `persistentVolumeClaimRetentionPolicy` 매핑

**근거**: 매트릭스 #11 (scale-down 시 데이터 손실 방지).
**예상 기간**: 1주.

### 4.5 volumePermissions init container (P1)

**구현 사항**:
- CRD `pod.volumePermissions.{enabled,image,resources}` 필드 추가
- ResourceBuilder가 활성화 시 `os-shell` 또는 `busybox` init container 주입 (`chown -R mongodb:mongodb /data/db`)
- 비활성화 기본값 (fsGroup만으로 충분한 환경 우선)

**근거**: 매트릭스 #12 (non-root/restricted PSA 클러스터 대응).
**예상 기간**: 1주.

### 4.6 Init scripts ConfigMap (P2)

**구현 사항**:
- `MongoDB`/`MongoDBSharded`에 `initScripts.{configMapRef, secretRef}` 필드 추가
- pod에 `/docker-entrypoint-initdb.d`로 마운트, 컨테이너 entrypoint가 `.sh`/`.js` 순차 실행
- admin user 부트스트랩 후 1회만 실행

**근거**: 매트릭스 #25 (인덱스/시드 자동화).
**예상 기간**: 1-2주.

### 4.7 Service 옵션 확장 (P2)

**구현 사항**:
- `MongosServiceSpec`에 `sessionAffinity`, `sessionAffinityConfig`, `externalIPs`, `nodePort`, `headless` 필드 추가
- ResourceBuilder Service 생성 로직 확장

**근거**: 매트릭스 #17 (외부 노출 시나리오).
**예상 기간**: 1주.

### 4.8 Diagnostic mode + Resource presets (P2)

**구현 사항**:
- CRD `pod.diagnosticMode.enabled` 필드 (활성화 시 command `["sleep","infinity"]`, probe 비활성화)
- `pod.resources.preset` 필드 (`none`/`nano`/`micro`/`small`/`medium`/`large`/`xlarge`/`2xlarge`) — 직접 `resources` 지정 시 preset 무시

**근거**: 매트릭스 #30, #31 (트러블슈팅 편의 + Bitnami preset 호환).
**예상 기간**: 1-2주.

### 4.9 Scale-in / Member removal (P2)

**구현 사항**:
- `MongoDBSharded.spec.shards.count` 감소 시 `removeShard` 호출 → drain 완료 대기 → StatefulSet 삭제 + PVC 정책에 따라 처리
- `MongoDB.spec.members` 감소 시 `rs.remove()` 호출 후 멤버 pod 종료
- 안전 가드: drain 미완 시 reconcile 재시도, finalizer로 stuck 방지

**근거**: 매트릭스 #37, #38 (현재 README가 명시한 한계 직접 해소).
**예상 기간**: 4-5주 (가장 위험한 작업, e2e 테스트 필수).

**Phase 4 총 기간**: 16-22주 (P0 5-7주, P1 4-5주, P2 7-10주)

## 타임라인 요약

```
2026 Q1 (1-3월) - Phase 1: 프로덕션 강화
├─ Week 1-3:  PITR 완전 구현
├─ Week 4:    Grafana 대시보드
├─ Week 5-6:  자동 버전 업그레이드
└─ Week 7:    확장 모니터링 메트릭

2026 Q2 (4-6월) - Phase 2: 엔터프라이즈 인증
├─ Week 1-4:  LDAP 인증
├─ Week 5-7:  OIDC/OAuth2
├─ Week 8-12: 다중 리전 지원
└─ Week 13-15: 저장 데이터 암호화

2026 Q3 (7-9월) - Phase 3A: 고급 백업
├─ Week 1-6:  쿼리 가능한 백업, 대역폭 제한, 자동 검증
└─ Week 7-14: 성능 분석 도구

2026 Q4 (10-12월) - Phase 3B: 멀티 클러스터
├─ Week 1-8:  멀티 클러스터 관리
└─ Week 9-12: 고급 감사 로깅

2027 Q1 (1-3월) - Phase 4: Bitnami Helm chart 동등성
├─ Week 1-3:   NetworkPolicy + Arbiter/Hidden member (P0)
├─ Week 4-7:   사이드카·extraVolumes·PVC retention·volumePermissions (P1)
└─ Week 8-12:  Init scripts·Service 옵션·Diagnostic mode·Scale-in (P2)
```

## 우선순위 매트릭스

### 높은 가치, 낮은 난이도 (즉시 실행)
- ✅ Grafana 대시보드 템플릿
- ✅ 확장 모니터링 메트릭

### 높은 가치, 높은 난이도 (전략적 투자)
- 🎯 PITR 완전 구현
- 🎯 LDAP/OIDC 인증
- 🎯 다중 리전 지원
- 🎯 성능 분석 도구

### 낮은 가치, 낮은 난이도 (빠른 성과)
- 📝 추가 스토리지 백엔드
- 📝 더 많은 인증 메커니즘

### 낮은 가치, 높은 난이도 (회피)
- ❌ Enterprise 바이너리 필요 기능
- ❌ 독점 플랫폼 통합

## 커뮤니티 기여

우리는 커뮤니티 기여를 환영합니다! 다음과 같은 방법으로 참여할 수 있습니다:

### 기능 제안
- GitHub Issues에 기능 요청 제출
- 사용 사례 및 요구사항 설명
- 우선순위 투표 참여

### 코드 기여
- [CONTRIBUTING.md](CONTRIBUTING.md) 참조
- 작은 PR부터 시작 (버그 수정, 문서 개선)
- 로드맵 기능 구현

### 피드백
- 프로덕션 사용 경험 공유
- 버그 리포트
- 성능 벤치마크

## 의사결정 기준

로드맵 우선순위는 다음 기준으로 결정됩니다:

1. **사용자 가치**: 프로덕션 환경에서의 실질적 필요성
2. **구현 난이도**: 개발 리소스 및 시간
3. **커뮤니티 요청**: GitHub Issues 투표 및 피드백
4. **MongoDB Enterprise 격차**: 엔터프라이즈 기능과의 차이
5. **오픈소스 실현 가능성**: Enterprise 바이너리 없이 구현 가능한지

## 제외 사항

다음 기능은 MongoDB Enterprise 바이너리가 필요하므로 구현하지 않습니다:

- ❌ In-Memory 스토리지 엔진
- ❌ 필드 레벨 암호화 (CSFLE)
- ❌ FIPS 140-2 준수
- ❌ Ops Manager / Cloud Manager 통합

이러한 기능이 필요한 경우, MongoDB Enterprise Operator를 사용하십시오.

## 버전 계획

| 버전 | 릴리스 예정 | 주요 기능 |
|------|------------|----------|
| v1.0.0 | 2026-01 | GA 릴리스 |
| v1.1.0 | 2026-04 | PITR, Grafana 대시보드, 자동 업그레이드 |
| v1.2.0 | 2026-07 | LDAP/OIDC, 다중 리전, 저장 암호화 |
| v1.3.0 | 2026-10 | 고급 백업, 성능 분석 도구 |
| v2.0.0 | 2027-01 | 멀티 클러스터, 감사 로깅, 주요 API 변경 |
| v2.1.0 | 2027-04 | Bitnami Helm chart 동등성 (NetworkPolicy, Arbiter/Hidden, 사이드카, Scale-in) |

## 참고 자료

- [MongoDB Enterprise Operator](https://github.com/mongodb/mongodb-enterprise-kubernetes)
- [MongoDB 문서](https://www.mongodb.com/docs/)
- [Kubernetes Operators](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

## 피드백 및 제안

로드맵에 대한 피드백이나 제안이 있으시면:

- **GitHub Issues**: https://github.com/keiailab/mongodb-operator/issues
- **Discussions**: https://github.com/keiailab/mongodb-operator/discussions
- **Email**: support@keiailab.com

이 로드맵은 살아있는 문서이며, 커뮤니티 피드백과 기술 발전에 따라 업데이트됩니다.
