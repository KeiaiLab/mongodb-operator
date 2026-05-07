# TASKS — mongodb-operator

> 작업 ID: F=기능 / I=개선 / B=버그 / T=그 외. 부여 후 재사용 금지.
> 단계: 설계(10%) / 구현(60%) / 테스트(90%) / 완료(100%).

## 현재 Phase: **1.4.x stable** (production-ready, argos 운영 중)

목표: production 안정성 유지 + 보안 패치 빠른 release. 새 기능은 신중히 추가.

## 작업 표

| ID | 기능명 / 요약 | 단계 | 완성도 | 의존 | 영향 | 비고 |
|----|---------------|------|--------|------|------|------|
| B17 | PodSecurity restricted 위반 — copy-keyfile init container 4 곳 SC 누락 | 완료 | 100% | - | F12 | 2026-05-07 commit 85c692d. argos-mongo-cfg StatefulSet pod 0/3 admission 거부 → 3/3 회복. helper 함수 통일 + TestPodSecurityRestrictedCompliance 회귀 가드. |
| T18 | v1.4.6 release — chart bump + ghcr.io push + helm-publish | 완료 | 100% | B17 | - | 2026-05-07 commit c5b26de. ghcr.io/keiailab/mongodb-operator:1.4.6@c4d59112. gh-pages 1.4.6.tgz publish. |
| T19 | argos-platform-data/mongodb dependency 1.4.5 → 1.4.6 | 완료 | 100% | T18 | - | 2026-05-07 argos-platform-data b378590 + 87ce471 (Chart.lock helm v3 재생성). ArgoCD auto-sync 통과, image rollout 확인. |
| T20 | 3-repo governance 자산 정합 — GOVERNANCE / MAINTAINERS / AGENTS / TASKS | 완료 | 100% | - | - | 2026-05-07. 본 commit. CoC ✓ / Gov ✓ / Maint ✓ / Roadmap ✓ / AGENTS ✓ / TASKS ✓ — 6/6. |
| F12 | Sharded P0 회귀 가드 강화 — controller 측 podSpec 변환 경로도 PodSecurity 검증 | 완료 | 100% | B17 | - | 2026-05-07. envtest 로 controller-created cfg/shard/mongos PodSpec restricted 검사 추가. Monitoring enabled 시 mongos exporter sidecar `SecurityContext=nil` 공백 발견 → restricted SC 적용. |
| T21 | latest 기본값 정렬 — MongoDB 8.3.1 + 8.2/8.0 milestone 렌더 가드 | 완료 | 100% | F12 | chart/API/deploy | 2026-05-07. 공식 current 8.3.1 기준으로 values/docs/samples/GitOps workload/default backup image/ArtifactHub images 를 8.3.1 로 정렬. `mongo:8.3.1`, `mongo:8.2`, `mongo:8.0` manifest 확인. builder matrix test 로 cfg/shard/mongos image 렌더 검증. `make test`, `make lint`, `make validate`, deploy overlay render PASS. |
| F13 | release-smoke-test.sh 자동화 강화 — flaky 회피 + retry policy | 완료 | 100% | - | - | 2026-05-07 it45. retry_check 헬퍼 + 단계 3/4/5 적용. SMOKE_RETRY_ATTEMPTS(default 12)/SMOKE_RETRY_SLEEP(default 15) env override. fast-path 첫 시도 통과 시 회귀 0 (latest v1.4.11 11 PASS / 1 FAIL — SBOM asset 누락 사전이슈). 3×2s override 13s 빠른 fail PASS. |
| I14 | webhook validation rule 통합 — replicaset count / sharded.shards 하한 / storageClass | 구현 | 60% | - | - | 2026-05-07 it45-46. 8 invariant 구현 (version 화이트리스트 / members quorum / shards.count <=64 / membersPerShard) + commit 50b3498/7096bb7. coverage 84.8%. 잔여 (40%) = storageClass / storage.size 하한 / replicaset hostnames 충돌 — 별 cycle. |
| F15 | webhook server 부트스트랩 — admission validation + cert-manager + chart wiring | 완료 | 100% | I14 | F01-F14 | 2026-05-07 it45-46 commit 50b3498/7096bb7/e81bb13/7e9e0da/8ac15ba. webhook scaffold + main wiring + chart template + ADR-0015 + NOTES UX. helm lint PASS / go test PASS (22) / golangci-lint 0 issues / coverage 84.8%. 다음 cycle: image build/push (사용자 승인 필요) → kind+cert-manager e2e (Phase 1 M2). |
| I16 | MonitoringSpec orphan — `Spec.Monitoring.{ServiceMonitor,PrometheusRules,Exporter}` controller 미사용 | 발견 | 0% | - | F15 | 2026-05-07 it46 발견. `api/v1alpha1/common_types.go:185-201` 의 MonitoringSpec / ServiceMonitorSpec / PrometheusRulesSpec / ExporterSpec 가 CRD 정의되어 있으나 internal/ 어느 controller 도 reconcile 안 함 — 사용자가 `spec.monitoring.serviceMonitor.labels: {...}` 설정해도 무시. valkey-operator `internal/resources/servicemonitor.go` (commons.monitoring 위임) 가 대조군. 결정 필요 (삭제 vs 구현 M4 vs deprecate). principles.md §3 — 본 cycle 발견사항 보고만. |

## 차단됨

(없음)

## 영향도 추적

- B17 (SC fix) → F12 (회귀 가드 강화) 의 baseline. F12 진행 시 B17 helper 변경 금지.
- T19 (chart dependency) → 향후 1.4.x 패치 시 동일 경로 (umbrella chart bump → ArgoCD sync).

## 운영 사고 (참고)

| INC | 요약 | 영향 | 해소 commit |
|---|---|---|---|
| INC-2026-05-07 | etcd 노드 e121/e122 tailscaled watchdog false positive → /var/log/syslog 847GB 폭주 → DiskPressure → mongodb-operator 11회 evict | argos data plane 일시 영향 (PodSecurity admission 사고 와 동시 발생) | argos-infra-bootstrap ADR-0055 (65dec7e), argos-infra-ansible daa29a3 (template fix). 11 노드 통일. |
| (B17 sib) | argos-mongo-cfg StatefulSet 0/3 — copy-keyfile init container PodSecurity admission 거부 | argos data plane MongoDB sharded cluster 가용성 영향 | mongodb-operator 85c692d (코드 fix), c5b26de (1.4.6 release), argos-platform-data 87ce471 (deploy) |

이전 phase (1.4.0 ~ 1.4.5) 작업 내역은 [CHANGELOG.md](CHANGELOG.md) 참조.
