# Operations Docs — keiailab data plane

운영자 진입점. mongodb-operator + valkey-operator + postgres-operator 의
*cluster ops audit* + *sprint plan* + *DR snapshot* 통합.

## 목차

### Audit + Plan

- **[cluster-audit.md](cluster-audit.md)** — 현재 상태 (live verification +
  workload inventory + 격차 11건 + clean 영역 15건). Single source of truth
  of *what's the state today*.

- **[production-grade-sprint.md](production-grade-sprint.md)** — 격차 해소
  *step-by-step procedure*. 7 phase (A Quick wins / B valkey GitOps / C
  Observability / D 1.4.12 release / E Backup / F Resource governance / G
  Service mesh). 사용자 명시 승인 시 즉시 실행 ready.

### DR

- **[cluster-snapshots/](cluster-snapshots/README.md)** — git 추적 0 인 CR
  spec 의 disaster recovery 임시 보관소. 마이그레이션 후 제거 정책.

### Webhook (코드 영역, mongodb-operator 한정)

- **[../advanced/webhook.md](../advanced/webhook.md)** — admission webhook
  사용자 가이드 (13 invariants 매트릭스 + failurePolicy + troubleshooting).

## Quick run

```bash
./scripts/audit-cluster-state.sh
```

5 KPI 자동 측정 — 운영 시작/종료 시 1회 실행 권장.

## 빠른 입구

| 사용자 의도 | 시작점 |
|---|---|
| "현재 cluster 상태 보고 싶다" | `./scripts/audit-cluster-state.sh` (자동) 또는 [cluster-audit.md](cluster-audit.md) (정적 정보) |
| "상용제품 수준 도달까지 뭐 남았나?" | [cluster-audit.md](cluster-audit.md) 의 격차 표 → [production-grade-sprint.md](production-grade-sprint.md) |
| "지금 즉시 quick win 1개 진행" | [production-grade-sprint.md#phase-a-quick-wins](production-grade-sprint.md#phase-a--quick-wins-인프라-즉시-활용) (A1 dead RBAC cleanup, 5분) |
| "DR 시 valkey CR 복원" | [cluster-snapshots/README.md](cluster-snapshots/README.md) |
| "webhook 활성화 절차" | [../advanced/webhook.md](../advanced/webhook.md) |

## 진행 측정 (2026-05-07 기준)

- **clean ratio**: 57.7% (15 / 26)
- **격차**: 11건 (3 High / 5 Medium / 3 Low)
- **운영 안정**: ✅ 3 operator log errors 0, events 0, ArgoCD 9 apps Synced/Healthy

Phase A-F 완료 시 예상 *clean ratio*: **92.3%** (24 / 26).

## 갱신 정책

- **cluster-audit.md**: cluster-ops mode 의 *각 cycle 별 audit 결과*.
  격차/clean 영역 발견 시 즉시 추가. live verification 주 1회 갱신 (`<!--
  live-verified: YYYY-MM-DD -->` 마커 갱신).
- **production-grade-sprint.md**: phase 진행 시 *step status* 표시
  (✅/⏳). 새 phase 발견 시 추가.
- **cluster-snapshots/**: 임시 보관 정책 (마이그레이션 후 제거).

## ADR Cross-Reference

cluster-ops 결정 영역의 ADR (mongodb-operator 의 docs/kb/adr/):

- ADR-0015 — failurePolicy=Fail
- ADR-0016 — Cross-cut audit pattern (+ Errata: docs accuracy)
- ADR-0017 — CRD default vs webhook invariant (Type A/A'/B/C)
- ADR-0018 — MonitoringSpec orphan 단계적 해소
