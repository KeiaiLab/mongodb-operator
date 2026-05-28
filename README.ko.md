<p align="center">
  <img src="https://github.com/keiailab.png" alt="keiailab" width="120"/>
</p>

# mongodb-operator (한국어)

> English README: [README.md](README.md) — canonical / 정본

> **Apache-2.0 MongoDB Operator for Kubernetes — ReplicaSet + Sharded Cluster + Backup, vanilla MongoDB 7.0+**

<p align="center">
  <a href="https://opensource.org/licenses/Apache-2.0"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="라이선스"/></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go 버전"/></a>
  <a href="https://www.mongodb.com/"><img src="https://img.shields.io/badge/MongoDB-7.0%2B-47A248?logo=mongodb" alt="MongoDB"/></a>
  <a href="https://kubernetes.io/"><img src="https://img.shields.io/badge/Kubernetes-1.26+-326CE5?logo=kubernetes" alt="Kubernetes"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator"><img src="https://img.shields.io/badge/ghcr.io-keiailab%2Fmongodb--operator-blue?logo=github" alt="컨테이너 이미지"/></a>
  <a href="https://github.com/keiailab/mongodb-operator"><img src="https://img.shields.io/badge/dynamic/yaml?url=https://raw.githubusercontent.com/keiailab/mongodb-operator/main/charts/mongodb-operator/Chart.yaml&label=helm%20v" alt="Helm 차트"/></a>
  <a href="https://artifacthub.io/packages/search?repo=mongodb-operator"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/mongodb-operator" alt="Artifact Hub"/></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/keiailab/mongodb-operator"><img src="https://api.scorecard.dev/projects/github.com/keiailab/mongodb-operator/badge" alt="OpenSSF Scorecard"/></a>
  <a href="https://github.com/keiailab/mongodb-operator/discussions"><img src="https://img.shields.io/github/discussions/keiailab/mongodb-operator?label=discussions&logo=github" alt="GitHub Discussions"/></a>
  <a href="https://github.com/keiailab/operator-commons"><img src="https://img.shields.io/badge/keiailab-v3.x--stable-success?style=flat-square" alt="keiailab v3.x-stable"/></a>
  <a href="https://github.com/keiailab/operator-commons"><img src="https://img.shields.io/badge/audit-100%25-success?style=flat-square" alt="audit"/></a>
</p>

<p align="center">
  <a href="README.md">English</a> |
  <b>한국어</b> |
  <a href="README.ja.md">日本語</a> |
  <a href="README.zh.md">中文</a>
</p>

---

Kubernetes 위에서 MongoDB ReplicaSet 과 Sharded Cluster 의 배포·관리를 자동화하는 Operator 입니다.

> ## ⚠️ 베타 출시 — v1.3.2-beta.x (carve-out)
>
> 현재 최신 release는 **prerelease 베타**입니다 — 정식 1.4.0 GA 출시 전까지 *비프로덕션 데이터* 한정 사용을 권장합니다.
>
> **베타 scope (기본 활성)**: MongoDB ReplicaSet
>
> **베타 scope 밖 (기본 비활성, RBAC + reconciler feature gate로 차단)**:
> - `MongoDBSharded` — ConfigServer init/HPA ordering 미해결 (`features.sharded.enabled=true`로 활성)
> - `MongoDBBackup` — 자동 테스트 0건, connectionString 평문 노출 위험 (`features.backup.enabled=true`로 활성)
> - HorizontalPodAutoscaler — RS/cfg drift mutex 부재 (`features.autoscaling.enabled=true`로 활성)
>
> 자세한 잔여 위험은 [CHANGELOG.md](CHANGELOG.md) 의 Known Issues 섹션 참조.

## Overview (개요)

MongoDB Operator 는 Kubernetes 위에서 MongoDB 클러스터의 배포, 스케일링, 관리를 자동화합니다. CRD(Custom Resource Definitions)를 사용해 MongoDB 인프라를 선언적으로 관리할 수 있는 방법을 제공합니다.

### Features (기능)

- **MongoDB ReplicaSet**: 자동 페일오버를 갖춘 3개 이상 멤버의 고가용성 replica set 배포
- **Sharded Cluster** *(베타 비활성)*: config server, shard, mongos 라우터를 포함한 분산 클러스터 배포
- **TLS 암호화**: cert-manager 연동을 통한 TLS 인증서 자동 관리
- **인증**: 클러스터 내부 통신을 위한 keyfile 지원 SCRAM-SHA-256 인증
- **모니터링**: ServiceMonitor 지원 Prometheus 메트릭 내보내기
- **백업/복원** *(베타 비활성)*: S3 호환 스토리지 또는 PVC 대상 자동 백업
- **자동 스케일링**: Mongos 라우터를 위한 Horizontal Pod Autoscaler 지원

## Architecture (아키텍처)

```
┌─────────────────────────────────────────────────────────────────┐
│                    MongoDB Operator                              │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │  MongoDB    │  │ MongoDBShar │  │    MongoDBBackup        │  │
│  │  Controller │  │ Controller  │  │    Controller           │  │
│  └──────┬──────┘  └──────┬──────┘  └───────────┬─────────────┘  │
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  Resource Builder                           ││
│  │  (StatefulSets, Deployments, Services, Secrets, Jobs)       ││
│  └─────────────────────────────────────────────────────────────┘│
│         │                │                      │                │
│         ▼                ▼                      ▼                │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                  MongoDB Package                            ││
│  │  (Executor, ReplicaSet, Auth, Sharding)                     ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                            │
├─────────────────────────────────────────────────────────────────┤
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │  StatefulSet  │  │  StatefulSet  │  │  Deployment   │        │
│  │  (ReplicaSet) │  │  (Shards)     │  │  (Mongos)     │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
│                                                                  │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐        │
│  │   Services    │  │    Secrets    │  │  ConfigMaps   │        │
│  └───────────────┘  └───────────────┘  └───────────────┘        │
└─────────────────────────────────────────────────────────────────┘
```

### 자동 초기화 (Automatic Initialization)

Operator 는 MongoDB 클러스터 초기화를 자동으로 처리합니다.

**ReplicaSet 초기화:**
```
1. Keyfile Secret 생성 (내부 인증용)
2. ConfigMap 생성 (mongod.conf)
3. Service 생성 (headless + client)
4. StatefulSet 생성
5. 모든 pod 준비 완료 대기
6. primary 후보에서 rs.initiate() 실행
7. primary 선출 대기
8. admin 사용자 생성 (localhost exception 활용)
```

**Sharded Cluster 초기화:**
```
1. 공유 Keyfile Secret 생성
2. Config Server StatefulSet 배포 (포트 27019)
3. Shard StatefulSet 배포 (포트 27018)
4. Mongos Deployment 배포 (포트 27017)
5. Config Server ReplicaSet 초기화
6. 각 Shard ReplicaSet 초기화
7. Mongos 에서 admin 사용자 생성
8. 각 shard 에 대해 sh.addShard() 실행
```

### 포트 구성 (Port Configuration)

| 컴포넌트 | 포트 | 플래그 |
|-----------|------|------|
| Mongos | 27017 | - |
| Shard | 27018 | `--shardsvr` |
| Config Server | 27019 | `--configsvr` |

## Quick Start (빠른 시작)

### Prerequisites (사전 요구사항)

- Kubernetes 클러스터 v1.26+
- 클러스터 접근이 구성된 kubectl
- *설치 방법* 별 추가 요구사항:
  - **OLM v1** (권장, 최신): cert-manager 가동 중 + cluster admin (1회 bootstrap)
  - **Helm**: Helm v3.8+
  - **OLM v0** (레거시): Helm 의 단순함과 OLM v1 의 단순함 사이의 중간 — *권장하지 않음*

### Installation (설치) — 3가지 방법 (매트릭스)

| 방법 | 대상 | 현대성 | 단계 수 |
|---|---|---|---|
| **OLM v1** *(권장)* | 외부 사용자, GitOps 플랫폼 (ArgoCD App-of-Apps), Day-0 프로덕션 | **차세대** (v1.8.0, 2026-02 GA) | 2개 매니페스트 (ClusterCatalog + ClusterExtension) |
| Helm 차트 | 로컬 개발, 단일 클러스터 간단 배포 | 안정 | 1개 명령 (`helm install`) |
| OLM v0 | OpenShift 레거시, OperatorHub.io 커뮤니티 | 유지보수 모드 (v0.42, 2026-04) | 4개 매니페스트 + InstallPlan 승인 |

**상세 절차**: [INSTALL.md](INSTALL.md). 본 절은 *Quick Start* 입니다.

#### 방법 1 — OLM v1 (현대 표준, 권장)

```bash
# (1) OLM v1 클러스터 설치 — 1회 bootstrap
curl -L -s https://github.com/operator-framework/operator-controller/releases/latest/download/install.sh | bash -s

# (2) ClusterCatalog + ClusterExtension 적용
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clustercatalog.yaml
kubectl apply -f https://raw.githubusercontent.com/keiailab/mongodb-operator/v1.5.0/deploy/olm-v1/clusterextension.yaml

# (3) 설치 검증
kubectl wait --for=condition=Installed=True clusterextension/mongodb-operator --timeout=180s
```

#### 방법 2 — Helm 차트

```bash
# Helm 레포지토리 추가
helm repo add mongodb-operator https://keiailab.github.io/mongodb-operator
helm repo update

# Operator 설치
helm install mongodb-operator mongodb-operator/mongodb-operator \
  --namespace mongodb-operator-system \
  --create-namespace
```

<!-- 방법 3 (OLM v0 레거시) 제거 — ADR-0028 Phase D, v1 전용. helm 또는 OLM v1 사용. -->


### MongoDB ReplicaSet 배포

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  members: 3
  version:
    version: "8.3.1"
  storage:
    storageClassName: standard
    size: 10Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin
  monitoring:
    enabled: true
```

```bash
# 네임스페이스 및 자격증명 생성
kubectl create namespace database
kubectl create secret generic mongodb-admin \
  --from-literal=username=admin \
  --from-literal=password=your-secure-password \
  -n database

# MongoDB 배포
kubectl apply -f mongodb-replicaset.yaml
```

### Sharded Cluster 배포

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: my-sharded
  namespace: database
spec:
  version:
    version: "8.3.1"
  configServer:
    members: 3
    storage:
      size: 5Gi
  shards:
    count: 3
    membersPerShard: 3
    storage:
      size: 50Gi
  mongos:
    replicas: 2
    service:
      type: LoadBalancer
```

## Custom Resource Definitions (CRD)

### MongoDB (ReplicaSet)

| 필드 | 설명 | 기본값 |
|-------|-------------|---------|
| `spec.members` | replica set 멤버 수 | `3` |
| `spec.version.version` | MongoDB 버전 | `8.3.1` |
| `spec.storage.storageClassName` | 스토리지 클래스 이름 | - |
| `spec.storage.size` | 멤버당 PVC 크기 | `10Gi` |
| `spec.auth.mechanism` | 인증 메커니즘 | `SCRAM-SHA-256` |
| `spec.tls.enabled` | TLS 활성화 | `false` |
| `spec.monitoring.enabled` | Prometheus 메트릭 활성화 | `false` |
| `spec.arbiter.enabled` | arbiter 노드 활성화 | `false` |

### MongoDBSharded

| 필드 | 설명 | 기본값 |
|-------|-------------|---------|
| `spec.configServer.members` | Config server replica 수 | `3` |
| `spec.shards.count` | shard 수 | `2` |
| `spec.shards.membersPerShard` | shard당 멤버 수 | `3` |
| `spec.mongos.replicas` | Mongos 라우터 replica 수 | `2` |
| `spec.mongos.autoScaling.enabled` | mongos HPA 활성화 | `false` |

## Scaling (스케일링)

### 수평 스케일 아웃 (Shard 추가)

Operator 는 동적 shard 스케일링을 지원합니다. `spec.shards.count` 를 늘리면 operator 가 자동으로 다음을 수행합니다:

1. 새 Shard StatefulSet 과 headless Service 생성
2. 모든 pod 준비 완료 대기
3. 새 shard 의 ReplicaSet 초기화 (`rs.initiate()`)
4. mongos 에 새 shard 등록 (`sh.addShard()`)
5. MongoDB balancer 가 자동으로 새 shard 로 chunk 이동

**예시: 3개에서 5개 shard 로 스케일 아웃**

```bash
# 현재 shard 수 확인
kubectl get mongodbsharded my-cluster -o jsonpath='{.spec.shards.count}'
# 출력: 3

# 5개 shard 로 스케일 아웃
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"shards":{"count":5}}}'

# 새 shard pod 모니터링
kubectl get pods -l app.kubernetes.io/component=shard

# shard 등록 확인
kubectl exec -it my-cluster-mongos-xxx -c mongos -- \
  mongosh -u admin -p $PASSWORD --eval 'sh.status()'
```

**상태 추적:**
```yaml
status:
  shardsInitialized: [true, true, true, true, true]
  shardsAdded: [true, true, true, true, true]
  shards:
    - name: my-cluster-shard-0
      phase: Running
    - name: my-cluster-shard-1
      phase: Running
    - name: my-cluster-shard-2
      phase: Running
    - name: my-cluster-shard-3
      phase: Running
    - name: my-cluster-shard-4
      phase: Running
```

### 수직 스케일링 (리소스 조정)

리소스 requests/limits 업데이트 (rolling restart 트리거):

```bash
kubectl patch mongodbsharded my-cluster --type='merge' -p '{
  "spec": {
    "shards": {
      "resources": {
        "requests": {"memory": "2Gi", "cpu": "1"},
        "limits": {"memory": "4Gi", "cpu": "2"}
      }
    }
  }
}'
```

### Mongos Replica 스케일링

Mongos 라우터 수 증가 또는 감소:

```bash
# 스케일 업
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":3}}}'

# 스케일 다운
kubectl patch mongodbsharded my-cluster --type='merge' \
  -p '{"spec":{"mongos":{"replicas":1}}}'
```

## Resource Recommendations (리소스 권장사항)

### 최소 요구사항

| 컴포넌트 | 메모리 | CPU | 비고 |
|-----------|--------|-----|-------|
| Config Server | 256Mi | 100m | 3개 멤버 필수 |
| Shard Member | 512Mi | 250m | replica당 |
| Mongos | 512Mi | 250m | 256Mi 시 OOM 발생 |

### 프로덕션 권장사항

| 컴포넌트 | 메모리 | CPU | 스토리지 |
|-----------|--------|-----|---------|
| Config Server | 1Gi | 500m | 10Gi SSD |
| Shard Member | 4Gi | 2 | 100Gi+ SSD |
| Mongos | 1Gi | 500m | - |

## Tested Features (검증된 기능)

상태 표기 기준:
- **✅ Stable**: envtest 회귀 + 단위 테스트 + 실제 mongod workload(testcontainers/kind/실 클러스터)에서 부하/내구 검증 완료. 증거(stress test 결과/incident 후처리) 보존.
- **✅ Implemented**: 코드 + envtest 회귀 + 단위 테스트로 *기능적* 정확성 확인. *부하 검증은 운영자 책임*.
- **⚠️ Beta**: 코드는 동작하나 단위 테스트만 일부, 실 환경 검증 없음 — 운영 환경에 적용 전 추가 검증 필요.

| 기능 | 상태 | 비고 |
|---------|--------|-------|
| ReplicaSet 자동 초기화 | ✅ Implemented | `rs.initiate()` 자동. envtest + driver 단위 테스트. |
| Sharded cluster 초기화 | ✅ Implemented | Config server, shard, mongos. envtest 검증. |
| Admin 사용자 생성 | ✅ Implemented | K8s Lease lock + post-bootstrap usersInfo verify 포함 driver 기반 bootstrap. |
| Shard 스케일 아웃 (2→5) | ⚠️ Beta | 자동 `sh.addShard()` — driver 호출 검증, *실 cluster 부하* 미검증. |
| Shard 스케일 인 (5→2) | ⚠️ Beta | 자동 `removeShard()` + ShardDraining condition + 리소스 정리 (PVC 보존). chunk 마이그레이션 long-running polling은 30s 고정(backoff 미적용). |
| Mongos replica 스케일링 | ✅ Implemented | Deployment replicas 변경 → rolling. |
| 리소스 업데이트 | ✅ Implemented | STS UpdateStrategy 통한 Rolling restart. |
| 스케일링 중 데이터 무결성 | ⚠️ Beta | 코드 흐름상 데이터 손실 차단(PVC retain, removeShard drain wait) — *실 데이터 부하 검증* 미수행. |
| 스케일 중 동시 쓰기 | ⚠️ Beta | stress test 증거 없음. 향후 testcontainers-go 기반 부하 시험 예정. |
| PodDisruptionBudget 자동화 | ✅ Implemented | `spec.podDisruptionBudget` 으로 opt-in (MongoDB + Sharded). builder 단위 테스트로 4 컴포넌트 생성 검증. |
| NetworkPolicy 자동화 | ✅ Implemented | `spec.networkPolicy` 으로 opt-in (deny-by-default + additional peers). 단위 테스트로 cfg=27019/shard=27018/mongos=27017 포트 검증. *실 통신 차단 검증 미수행*. |
| Admin bootstrap race-free | ✅ Implemented | K8s Lease 분산락(30s TTL) + post-bootstrap `usersInfo` verify. fake-client 단위 테스트(busy/takeover/release). holder pod crash 시 30s까지 다른 reconcile은 backoff. |

## Limitations (제한사항)

### 미지원 기능

| 기능 | 상태 | 우회 방법 |
|---------|--------|------------|
| ReplicaSet 멤버 제거 | ❌ 미구현 | 수동 `rs.remove()` 필요 |
| 자동 백업 스케줄링 | ❌ 계획됨 | 외부 CronJob 사용 |
| 크로스 클러스터 복제 | ❌ 계획됨 | - |
| Sharded Arbiter/Hidden 토폴로지 | ⚠️ ReplicaSet 전용 | Arbiter는 MongoDB CR에서 지원; Sharded 확장은 로드맵에 있음 |

### 알려진 이슈

1. **Mongos 메모리**: 최소 512Mi 권장. 256Mi 는 부하 시 OOM 발생.
2. **ReplicaSet 멤버 우아한 제거 없음**: ReplicaSet 멤버 스케일 다운 시 `rs.remove()` 를 호출하지 않음 — StatefulSet replicas 만 감소.
3. **스케일 인 PVC 보존**: `removeShard` 완료 후 drain된 shard 의 PVC 는 의도적으로 보존되어 우발적 데이터 손실을 방지합니다. 운영자는 검증 후 수동으로 삭제해야 합니다.

### MongoDBBackup

| 필드 | 설명 | 기본값 |
|-------|-------------|---------|
| `spec.clusterRef.name` | 대상 클러스터 이름 | - |
| `spec.clusterRef.kind` | 대상 클러스터 종류 | `MongoDB` |
| `spec.type` | 백업 유형 (full/incremental) | `full` |
| `spec.compression` | 압축 활성화 | `true` |
| `spec.storage.type` | 스토리지 유형 (s3/pvc) | `s3` |

## Configuration (설정)

### cert-manager 를 이용한 TLS

```yaml
spec:
  tls:
    enabled: true
    certManager:
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
```

### Prometheus 모니터링

```yaml
spec:
  monitoring:
    enabled: true
    prometheusRule:
      enabled: true
    serviceMonitor:
      interval: 30s
```

### S3 백업

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBBackup
metadata:
  name: daily-backup
spec:
  clusterRef:
    name: my-mongodb
    kind: MongoDB
  storage:
    type: s3
    s3:
      bucket: mongodb-backups
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      credentialsRef:
        name: s3-credentials
```

## Development (개발)

### 사전 요구사항

- Go 1.21+
- Docker
- kubectl
- Kind 또는 Minikube (로컬 테스트용)

### 빌드

```bash
# Operator 빌드
make build

# 테스트 실행
make test

# Docker 이미지 빌드
make docker-build IMG=your-registry/mongodb-operator:tag

# Docker 이미지 push
make docker-push IMG=your-registry/mongodb-operator:tag
```

### 로컬 개발

```bash
# CRD 설치
make install

# Operator 로컬 실행
make run

# 샘플 MongoDB 생성
kubectl apply -f config/samples/mongodb_replicaset.yaml
```

## License (라이선스)

본 프로젝트는 Apache License 2.0 하에 배포됩니다 — 자세한 내용은 [LICENSE](LICENSE) 파일을 참조하세요.

### 써드파티 라이선스

본 operator 는 MongoDB 데이터베이스를 관리하지만 MongoDB 소프트웨어를 포함하거나 배포하지 않습니다. MongoDB Community Server 는 [Server Side Public License (SSPL)](https://www.mongodb.com/licensing/server-side-public-license) 하에 배포됩니다.

**중요한 라이선스 참고사항:**
- 본 operator (Apache 2.0) 는 MongoDB 배포를 조율하는 독립 소프트웨어입니다
- MongoDB 컨테이너 이미지는 공식 MongoDB 레포지토리에서 가져옵니다
- 사용자는 MongoDB 라이선스 조건 준수에 책임이 있습니다
- Operator 는 MongoDB 바이너리를 수정하거나 재배포하지 않습니다

## Contributing (기여)

기여를 환영합니다! 행동 강령과 pull request 제출 절차에 대한 자세한 내용은 [Contributing Guide](CONTRIBUTING.md) 를 참조하세요.

## Support (지원)

- **이슈**: [GitHub Issues](https://github.com/keiailab/mongodb-operator/issues)
- **토론**: [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)

## Roadmap (로드맵)

- [x] ReplicaSet 자동 초기화
- [x] Sharded Cluster 자동 초기화
- [x] 수평 shard 스케일링 (스케일 아웃)
- [x] Admin 사용자 자동 생성
- [ ] Point-in-Time Recovery (PITR)
- [ ] 자동화된 버전 업그레이드
- [ ] 크로스 클러스터 복제
- [ ] Grafana 대시보드 템플릿
- [ ] CronJob 을 이용한 백업 스케줄링
- [ ] 데이터 마이그레이션을 포함한 스케일 다운

## Acknowledgments (감사의 말)

- [Kubernetes](https://kubernetes.io/)
- [Operator SDK](https://sdk.operatorframework.io/)
- [MongoDB](https://www.mongodb.com/)
- [Bitnami MongoDB Charts](https://github.com/bitnami/charts) — 설계 영감

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a> ·
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/forgewise">forgewise</a>
</p>

<p align="center">© 2026 keiailab · Apache-2.0 · <a href="https://github.com/keiailab">keiailab.com</a></p>
