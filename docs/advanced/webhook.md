# Admission Webhook

MongoDB Operator 의 *validating admission webhook* 은 MongoDB / MongoDBSharded
CR 의 spec 검증을 *etcd 도달 전* 수행합니다. invalid spec (e.g., split-brain
member 수, 1Gi 미만 storage, missing secret ref) 이 controller 에 도달하기
전 *즉시 reject* — operational risk 제거 + UX 개선.

> **opt-in default**: helm chart 의 `webhook.enabled=false` 가 기본값. 활성화
> 시 cert-manager 클러스터 사전 설치 필수.

## Quick Start

### Prerequisites

- cert-manager v1.0+ 클러스터 차원 설치:
  ```bash
  kubectl get crd certificates.cert-manager.io
  ```
  미설치 시 [cert-manager docs](https://cert-manager.io/docs/installation/) 참조.

### 활성화

```bash
helm upgrade --reuse-values mongodb-operator keiailab/mongodb-operator \
  --set webhook.enabled=true
```

자동 생성 리소스:
- cert-manager `Issuer` (selfSigned) + `Certificate` (webhook serving cert).
- `Service` (port 443 → 9443).
- `ValidatingWebhookConfiguration` (CABundle 자동 주입).

검증:
```bash
kubectl get certificate -n <namespace> mongodb-operator-webhook-cert
kubectl get validatingwebhookconfiguration mongodb-operator-validating
```

## Validation Invariants

### MongoDB CR

| Field | Rule | Reason |
|---|---|---|
| `spec.version.version` | 화이트리스트 (8.0/8.2/8.3) | 검증된 major.minor 만 production 권장 |
| `spec.members` | 1 또는 odd >= 3 | even count split-brain risk |
| `spec.storage.size` | >= 1Gi | data dir + oplog floor |
| `spec.auth.adminCredentialsSecretRef.name` | non-empty | controller startup 의존 |
| `spec.tls.certManager.issuerRef.name` | non-empty (CertManager 활성 시) | omitempty trap |
| `spec.tls.customCert.secretName` | non-empty (CustomCert 활성 시) | omitempty trap |
| `spec.backup.schedule` | non-empty cron expression (Enabled 시) | silent failure 방지 |
| `spec.backup.storage.s3.bucket` | non-empty (Enabled+s3 시) | omitempty trap |
| `spec.backup.storage.s3.credentialsRef.name` | non-empty (Enabled+s3 시) | omitempty trap |

### MongoDBSharded CR

위 invariants 외 추가:

| Field | Rule | Reason |
|---|---|---|
| `spec.shards.count` | <= 64 | operational limit (capacity planning) |
| `spec.shards.membersPerShard` | 1 또는 odd >= 3 | per-shard split-brain risk |
| `spec.configServer.storage.size` | >= 1Gi | cfg replica set floor |
| `spec.shards.storage.size` | >= 1Gi | shard data floor |

### MongoDBBackup CR (restore / PITR)

`spec.restore` 설정 시에만 적용 — 복구 목표 시점이 source 백업의 복원 가능
window 밖이면 reject. 상세: [Backup and Restore](backup.md#point-in-time-recovery-pitr).

| Field | Rule | Reason |
|---|---|---|
| `spec.restore.pointInTime` / `pointInTimeTimestamp` | source 백업의 `[status.earliestRestore, status.latestRestore]` 안 | 도달 불가 시점 요청을 restore Job 실패 전에 차단 |
| `spec.restore.sourceBackupName` | 같은 namespace 에 존재 + `Phase=Completed` | 미완료 백업 기점 복원 차단 |
| `spec.clusterRef.kind=MongoDBSharded` + PITR | **Warning** (reject 아님) | shard 별 oplog ts 독립 — cluster-wide 일관 시점 불가 |

> **본 webhook 은 *권고* 게이트다 (fail-open).** window 가 아직 기록되지 않았
>거나 source 클러스터를 관측할 수 없으면 *통과*시킨다 — window 는 S3 segment
> 관측에서 유도한 값이라 진본이 아니기 때문. 진본 판정은 restore Job (도달 불가
> 시 `Phase=Failed`). admission 통과 = 복원 성공 보장 **아님**.

## Admission Denial 시 메시지 형식

K8s `apierrors.NewInvalid` 표준 형식 — 사용자 시점:

```
Error from server (Invalid): admission webhook "vmongodb-v1alpha1.kb.io"
denied the request: MongoDB.mongodb.keiailab.com "keiailab-mongo" is invalid:
[spec.members: Invalid value: 4: members must be 1 (single-instance) or
odd >= 3 (quorum) — even count risks split-brain,
spec.storage.size: Invalid value: "512Mi": storage.size must be >= 1Gi —
production mongodb requires minimum data dir + oplog headroom]
```

복수 invariant 위반 시 *모두* 한 번에 보고 (accumulate-errors 패턴) — 사용자가
apply 반복 cycle 줄임.

## failurePolicy=Fail 영향

webhook server pod 가 down 또는 도달 불가 시 *모든 mongodb CR CRUD 차단*
(ADR-0015). 운영 영향:

- 정상 운영: `replicaCount: 1` (default) 의 operator pod 가 OOMKilled / node
  drain 시 ~15-30s 동안 CR CRUD 거부. CI/CD 의 `kubectl apply` retry 권장.
- HA 권장: production 환경에서 `replicaCount: 2` + `podDisruptionBudget.
  enabled: true` 로 webhook 가용성 보장.

## 비활성화 (rollback)

```bash
helm upgrade --reuse-values mongodb-operator keiailab/mongodb-operator \
  --set webhook.enabled=false
```

cert-manager Certificate / ValidatingWebhookConfiguration 자동 제거. 기존
mongodb CR 영향 0 (controller reconcile 정상).

## Troubleshooting

### `kubectl apply` 가 webhook 도달 못 함

```
Error from server (InternalError): Internal error occurred: failed
calling webhook "vmongodb-v1alpha1.kb.io": failed to call webhook: ...
```

원인:
1. webhook pod down — `kubectl get pods -n <ns> -l app.kubernetes.io/name=mongodb-operator`
2. CABundle 미주입 — cert-manager 미설치 또는 ca-injector 비활성. `kubectl get
   validatingwebhookconfiguration mongodb-operator-validating -o
   jsonpath='{.webhooks[0].clientConfig.caBundle}'` 로 확인 (비어있으면
   문제).

### 의도된 invariant 가 admission 도달 안 함

ADR-0017 의 *Type A' 조건부 unreachable* 가능성 — webhook.enabled=false 환경
에서만 도달. CRD `+kubebuilder:default=` 가 zero-value 채우는 path 점검.

## 개발자 가이드

새 invariant 추가 시 ADR-0016 cross-cut audit 표 + ADR-0017 type 분류
의무. 자세한 패턴 [AGENTS.md](../../AGENTS.md#webhook-invariant-추가-시-의무-audit-it46-47).

## 관련 문서

- [ADR-0015](../kb/adr/0015-webhook-failure-policy-fail.md) — failurePolicy=Fail trade-off.
- [ADR-0016](../kb/adr/0016-cross-cut-audit-pattern.md) — Cross-cut audit pattern.
- [ADR-0017](../kb/adr/0017-crd-default-vs-webhook-invariant.md) — CRD default vs webhook invariant.
