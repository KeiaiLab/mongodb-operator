# 3-Way Feature Parity Summary: `mongodb-operator` vs Bitnami vs CloudPirates

## 개요

본 문서는 `mongodb-operator` v1.4.23 가 두 reference Helm chart 와 비교하여 *어느 영역에서 동등/우위/갭* 인지를 한 표로 요약한다. 자세한 분석은 다음 자매 문서에 분리:

- [`bitnami-mongodb-sharded.md`](./bitnami-mongodb-sharded.md) — Bitnami `mongodb-sharded` 9.4.12 (44행)
- [`cloudpirates-mongodb.md`](./cloudpirates-mongodb.md) — CloudPirates `mongodb` 0.17.1 (28행)

본 summary 는 두 분석을 통합하여 **ROADMAP cycle 매핑** 을 제공한다. 사용자 목표 "기능과 퀄리티 전체 안정성, 운영 안정성 확보 + 모든 기능 테스트 통과" 의 진행 추적용 SSOT.

## 작성 컨텍스트

- 작성일: 2026-05-12 (cycle 0)
- mongodb-operator 기준 버전: v1.4.23
- Bitnami chart 기준: 9.4.12 (app version 8.0.x)
- CloudPirates chart 기준: 0.17.1 (app version 8.3.1)
- 작성 cycle: cycle 0 baseline (12-cycle program 의 첫 단계, [plan](../../../../../.claude/plans/nifty-snuggling-ocean.md))

## 통합 갭 → cycle 매핑

각 갭은 두 reference 중 하나 또는 양쪽에서 발견. ROADMAP 의 [ ] / [~] 항목과 1:1 대응.

| Gap ID | 기능 영역 | Bitnami 갭 | CloudPirates 갭 | ROADMAP 매핑 | 본 program cycle |
|---|---|---|---|---|---|
| G-01 | PITR 완전 구현 (oplog uploader + restore) | — | ✗ 양쪽 미지원 | L67-71 ([~]+[ ]) | **cycle 1** |
| G-02 | Grafana dashboard 5종 | — | — | L75-79 ([ ]×5) | **cycle 2** |
| G-03 | Cluster 용 Helm chart (CR 배포) | ✗ Bitnami 우위 | ✗ CloudPirates 우위 | L (신규 — TASKS 추가) | **cycle 3** |
| G-04 | LDAP/OIDC auth | — | ✗ 양쪽 미지원 (Op ROADMAP) | L105-117 ([ ]×9) | **cycle 4** |
| G-05 | MongoDBFederation (multi-region) | — | — | L121+ ([ ]) | **cycle 5** |
| G-06 | KMS encryption-at-rest | — | — | (ROADMAP Phase 2) | **cycle 6** |
| G-07 | MongoDBInsights (slow query) | — | — | (ROADMAP Phase 3) | **cycle 7** |
| G-08 | MongoDBClusterGroup | — | — | (ROADMAP Phase 3) | **cycle 8** |
| G-09 | Version upgrade rollback 자동화 | — | — | L84-88 ([ ]×5) | **cycle 9** |
| G-10a | Hidden replica | — | ✗ CP 우위 | (ROADMAP Phase 4) | **cycle 10** |
| G-10b | Delayed replica | — | ✗ CP 부분 | (ROADMAP Phase 4) | **cycle 10** |
| G-10c | externalAccess (Ingress) | — | ✗ CP 우위 | (ROADMAP Phase 4) | **cycle 10** |
| G-10d | initContainer / customInit | ✗ Bitnami 우위 | ✗ CP 우위 | (ROADMAP Phase 4) | **cycle 10** |
| G-10e | lifecycleHooks 확장 | — | ✗ CP 부분 | (ROADMAP Phase 4) | **cycle 10** |
| G-10f | volumePermissions init | ✗ Bitnami 우위 | — | (ROADMAP Phase 4) | **cycle 10** |
| G-10g | Init scripts ConfigMap | ✗ Bitnami 우위 | — | (ROADMAP Phase 4) | **cycle 10** |
| G-10h | NetworkPolicy 자동 생성 enhanced | ✗ Bitnami 우위 (자동) | ✓ 동등 (opt-in) | (ROADMAP) | **cycle 10** |
| G-10i | Sharded arbiter/hidden | ✗ Bitnami 우위 | — | (ROADMAP Phase 4) | **cycle 10** |
| G-10j | PVC retention policy | ✗ Bitnami 우위 | — | (ROADMAP Phase 4) | **cycle 10** |
| G-10k | Diagnostic mode + resourcePresets | ✗ Bitnami 우위 | — | (ROADMAP) | **cycle 10** (presets) / cycle 0 (diagnostic) |
| G-10l | Service 옵션 확장 (sessionAffinity 등) | ✗ Bitnami 우위 | — | (ROADMAP Phase 4) | **cycle 10** |
| G-10m | Scale-in (removeShard 자동화) | ✗ Bitnami 부재 (양쪽) | — | (ROADMAP) | **cycle 9** |
| G-11 | Architecture: standalone 모드 | — | ✗ CP 우위 | (신규 — TASKS 추가) | **cycle 11** |
| G-12 | mongos StatefulSet 옵션 | ✗ Bitnami 우위 | — | (신규 — TASKS 추가) | **cycle 11** |
| G-13 | Cosign chart 서명 (image 외) | — | ✗ CP 우위 | (신규 — TASKS 추가) | **cycle 11** |
| G-14 | sidecars / extraVolumes / extraEnv | ✗ Bitnami 우위 | ⚠️ extraObjects | (ROADMAP Phase 4) | **cycle 11** |
| G-15 | 메트릭 30+ 기본 (controller-side) | — | — | L92 ([~]) | **cycle 11** |

총 26 Gap. 12 cycle 에 분산. Phase 1 (cycle 1-3) = 가치 최대 / Phase 2 (cycle 4-6) = 엔터프라이즈 인증·federation / Phase 3 (cycle 7-9) = 운영 강화 / Phase 4 (cycle 10-12) = polish + parity 완성.

## Operator 우위 항목 (양쪽 reference 대비)

다음은 Operator 가 두 chart 보다 우위인 영역. README/마케팅 강조 포인트이자 cycle 0+ 에 회귀 가드 의무.

| Op 우위 ID | 영역 | Bitnami | CloudPirates | Operator 강점 |
|---|---|---|---|---|
| OP-01 | Built-in Backup CRD | ✗ Velero 외부 | ⚠️ extraObjects CronJob | `MongoDBBackup` S3/PVC, full/incr, compression |
| OP-02 | 선언적 horizontal scaling | ⚠️ helm upgrade | ⚠️ helm upgrade | `spec.shards.count` 변경 시 `sh.addShard` 자동 호출 |
| OP-03 | PrometheusRule 자동 생성 | ✗ PodMonitor only | ⚠️ extraObjects 수동 | 알람 규칙 chart 없이 배포 |
| OP-04 | mongo-go-driver v2 (no pods/exec) | ✗ shell script entrypoint | ✗ shell script | JS injection 표면 0 |
| OP-05 | cert-manager 1급 통합 | ⚠️ 수동 | ⚠️ `config.content` 수동 | `TLSSpec.certManager.issuerRef` 직접 |
| OP-06 | `MongoDB` (ReplicaSet) 단독 CRD | ✗ chart 분리 | ✓ architecture mode | RS-only 1급 모델 |
| OP-07 | HPA / autoscaling | ✗ | ✗ | mongos/shard HPA |
| OP-08 | PDB 자동 | ✓ | ⚠️ extraObjects | 자동 1급 |

회귀 가드: 위 8건은 cycle 0+ 모든 cycle 에서 e2e 또는 unit test 로 *유지 검증*. 새 cycle 진입 시 *기존 우위 손실 없음* 을 확인.

## 동급/동등 항목 (생략)

총 38건은 두 chart 대비 동등. SCRAM auth, PVC, ServiceMonitor, X.509, NetworkPolicy opt-in, RBAC, securityContext 등 표준 Kubernetes 기능 위주.

## Cycle 0 (본 cycle) 의 처리 결과

| 항목 | 처리 | 비고 |
|---|---|---|
| Baseline gate (lint+test+CRD+helm) | ✅ PASS | `make gate` + `make test` 통과 |
| Bitnami 비교문서 version drift | ✅ v1.0.1 → v1.4.23 갱신 | 본문 갭 9건 + 우위 6건 유효 |
| CloudPirates 비교문서 신규 | ✅ [`cloudpirates-mongodb.md`](./cloudpirates-mongodb.md) | 28행 매트릭스 |
| 3-way summary 신규 | ✅ 본 문서 | 26 Gap → 12 cycle 매핑 |
| F-IMP-04 diagnosticMode sharded 확장 | ✅ 처리 | cycle 0 진본 코드 1건 |
| F-IMP-01 PITR API stable | ⏭️ cycle 1 인계 | webhook 통합 cycle 1 함께 |
| F-IMP-02 버전 검증 강화 | ⏭️ cycle 7 인계 | upgrade rollback 묶음 |
| F-IMP-03 30+ 메트릭 | ⏭️ cycle 11 인계 | G-15 (cycle 11) |
| HANDOFF.md + TASKS.md | ✅ 작성 | cycle 1+ 인계 SSOT |

## 검증 절차

1. 본 표의 Gap ID 가 [`bitnami-mongodb-sharded.md`](./bitnami-mongodb-sharded.md) + [`cloudpirates-mongodb.md`](./cloudpirates-mongodb.md) 의 *모든 갭 항목* 을 포함하는지 grep cross-check
2. ROADMAP 의 `[ ]` / `[~]` 항목이 본 표의 Gap ID 에 매핑되는지 cross-check
3. cycle 1+ 진입 시 `Cycle 매핑` 컬럼을 보고 우선순위 결정
4. cycle 종료 시 본 표의 `처리 결과` 행 갱신 (`docs/comparison/three-way-summary.md` 가 cycle program 의 SSOT)

## 참고

- ROADMAP: [`../../ROADMAP.md`](../../ROADMAP.md)
- HANDOFF: [`../../HANDOFF.md`](../../HANDOFF.md)
- TASKS: [`../../TASKS.md`](../../TASKS.md)
- ADR (cycle 0 의사결정): [`../kb/adr/0025-cycle-0-baseline-and-cross-verification.md`](../kb/adr/0025-cycle-0-baseline-and-cross-verification.md)
