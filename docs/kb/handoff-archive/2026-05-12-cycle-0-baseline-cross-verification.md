# Cycle 0 Handoff Archive — Baseline + 3-way Cross-Verification

> 2026-05-12 cycle 0 의 *git-tracked snapshot*. local `HANDOFF.md` (gitignore) 의 핵심 내용만 영구 보관. 상세 12-cycle program 진행은 local HANDOFF.md / TASKS.md 참조.

## 사용자 목표 (원문)

```
1. https://github.com/keiailab/mongodb-operator
2. https://artifacthub.io/packages/helm/bitnami/mongodb-sharded
3. https://artifacthub.io/packages/helm/cloudpirates-mongodb/mongodb

1번을 2, 3번과 교차검증에도 기능과 퀄리티 전체 안정성, 운영 안정성이 확보와
모든 기능 테스트 통과할때까지 진행
```

## Cycle 0 산출물

| 산출물 | 종류 | 비고 |
|---|---|---|
| docs/comparison/cloudpirates-mongodb.md | 신규 | CloudPirates 0.17.1 28행 매트릭스 |
| docs/comparison/three-way-summary.md | 신규 | 26 Gap-ID → 12 cycle 매핑 SSOT |
| docs/comparison/bitnami-mongodb-sharded.md | 갱신 | operator v1.0.1 → v1.4.23 reference |
| docs/kb/adr/0025-cycle-0-baseline-and-cross-verification.md | 신규 | 12-cycle program 의사결정 ADR |
| docs/kb/adr/INDEX.md | 갱신 | ADR-0025 행 추가 |
| internal/resources/builder.go | 갱신 | F-IMP-04 DiagnosticMode sharded 3 컴포넌트 확장 |
| internal/resources/builder_test.go | 갱신 | TestDiagnosticMode_Sharded_ConfigServer_Shard_Mongos 신규 |
| ROADMAP.md | 갱신 | L223 [~] → [x] |

## 검증 인용

```bash
$ make gate
... 0 issues (golangci) / No vulnerabilities (govulncheck) / 0 CVE (trivy)
✓ All RFC 0002 local gates passed

$ make test
ok  internal/resources        coverage: 73.7%   # 73.0% → 73.7% (F-IMP-04 test 추가)
ok  internal/webhook/v1alpha1 coverage: 96.5%
ok  internal/controller       coverage: 47.0%
...all packages PASS

$ grep -cE '^\s*- \[x\]' ROADMAP.md  # 20 → 21
21
$ grep -cE '^\s*- \[~\]' ROADMAP.md  # 4 → 3
3
$ grep -cE '^\s*- \[ \]' ROADMAP.md  # 81 변동 없음
81
```

## 12-Cycle Program 분해

| Cycle | 주제 | 핵심 산출물 |
|---|---|---|
| 0 ✅ | baseline + matrix + F-IMP-04 | 본 archive |
| 1 | PITR 완전 구현 | F01-F05 (oplog uploader + restore) |
| 2 | Grafana dashboard 5종 | F06-F10 |
| 3 | Cluster Helm chart | F85 |
| 4 | LDAP/OIDC auth | F23-F32 |
| 5 | Federation | F33-F37 |
| 6 | KMS encryption | F38-F42 + F61-F65 |
| 7 | Upgrade automation + Insights | F11-F16 + F51-F55 |
| 8 | ClusterGroup | F56-F60 |
| 9 | Scale-in safety | F74 + F43-F50 |
| 10 | Bitnami/CloudPirates parity polish | F66-F79 |
| 11 | Standalone mode + supply chain | F17-F22 + F80-F84 |
| 12 | Final parity 재검증 + ROADMAP 100% | three-way-summary 재산출 |

## 차단점

- argos cluster 라이브 게이트 N/A — 사용자 명시 "public 은 오픈소스 github 만 사용"
- test/e2e/run-all-tests.sh 가 reference 하는 0[1-9]-*.sh 부재 — cycle 1 진입 시 정리 후보
- gosec G115 builder.go:1690 int→int32 pre-existing — cycle 9+ candidate

## 다음 cycle (cycle 1) 진입점

```bash
cd /Users/phil/WorkSpace/public/mongodb-operator
cat HANDOFF.md TASKS.md | head -100      # local SSOT
grep -n "PITR\|Oplog\|PointInTime" ROADMAP.md | head -10
```

## Refs

- ADR-0025: `docs/kb/adr/0025-cycle-0-baseline-and-cross-verification.md`
- 3-way matrix: `docs/comparison/three-way-summary.md`
- Cycle 0 commits: `10167cb` (feat F-IMP-04), `d6ad96d` (docs comparison), `<this>` (docs handoff archive + ADR)
