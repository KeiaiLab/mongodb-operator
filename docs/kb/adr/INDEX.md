# Architecture Decision Records — INDEX

본 디렉터리는 mongodb-operator의 비역행 결정(architecture decisions)을 Nygard
5섹션 형식으로 보존한다. 결정의 *이유*가 코드보다 오래 살아남도록 한다.

## 활성 ADR

| 번호 | 제목 | 상태 | 날짜 |
|------|------|------|------|
| [ADR-0001](0001-controllerutil-createorupdate-pattern.md) | 자체 createOrUpdate 헬퍼 폐기 + controllerutil.CreateOrUpdate(mutateFn) 채택 | Accepted | 2026-04-28 |
| [ADR-0002](0002-bootstrap-admin-user-lease.md) | Admin user 부트스트랩 race-free를 위한 K8s Lease 분산락 도입 | Accepted | 2026-04-28 |
| [ADR-0003](0003-sharded-arbiter-hidden-deferred.md) | MongoDBSharded Arbiter/Hidden 토폴로지 후속 사이클 이연 | Accepted | 2026-04-28 |
| [ADR-0004](0004-isinitialized-unauthorized-as-init-completed.md) | `IsInitialized` Unauthorized 응답을 init-completed 시그널로 해석 | Accepted | 2026-04-29 |
| [ADR-0005](0005-skip-primary-check-before-admin-bootstrap.md) | admin user 부트스트랩 전 primary 체크 (anon + auth-fallback) | Accepted | 2026-04-29 |
| [ADR-0006](0006-poststart-bootstrap-rs-init.md) | postStart hook 안에서 RS init + createUser 통합 | Accepted | 2026-04-29 |
| [ADR-0007](0007-mongos-hpa-autoscaling.md) | mongos HorizontalPodAutoscaler 지원 (RS 멤버는 제외) | Accepted | 2026-04-29 |
| [ADR-0008](0008-rs-deliberate-scale-policy.md) | ReplicaSet 멤버 수 변경의 deliberate 가드 | Accepted | 2026-04-29 |
| [ADR-0009](0009-shard-cfg-hpa-with-deliberate-guard.md) | shard / cfg HPA의 RS 부작용과 이중 가드 | Accepted | 2026-04-29 |
| [ADR-0010](0010-sharded-hpa-ordering-and-status-truth.md) | Sharded HPA — informer cache + reconcile ordering + status truth source (P1 #1/#2/#3 fix, 베타 carve-out 종료) | Accepted | 2026-04-30 |
| [ADR-0011](0011-pre-commit-instead-of-lefthook.md) | Hook 도구로 pre-commit 채택 (글로벌 lefthook 표준 분기) | Accepted | 2026-05-06 |
| [ADR-0012](0012-renovate-as-single-dependency-bot.md) | 의존성 봇은 Renovate 단일 채택 (Dependabot 제거, 3-repo 정합) | Accepted | 2026-05-07 |
| [ADR-0013](0013-conditions-last-transition-time-fix.md) | Conditions LastTransitionTime — K8s convention 정합 (upstream meta.SetStatusCondition 위임) | Accepted | 2026-05-07 |
| [ADR-0014](0014-controller-create-pattern-boundary.md) | Controller Create 패턴 boundary — CreateOrUpdate vs intentional 수동 (bootstrap_lease + helpers 보존) | Accepted | 2026-05-07 |
| [ADR-0015](0015-webhook-failure-policy-fail.md) | ValidatingWebhookConfiguration failurePolicy=Fail (가용성 vs validation 가치) | Accepted | 2026-05-07 |
| [ADR-0016](0016-cross-cut-audit-pattern.md) | Cross-cut Audit Pattern — invariant 도입 시 3 operator 동시 점검 의무화 | Accepted | 2026-05-07 |
| [ADR-0017](0017-crd-default-vs-webhook-invariant.md) | CRD default 가 있는 field 의 zero-value 거부 invariant 는 dead code (envtest 가 unreachable 발견) | Accepted | 2026-05-07 |
| [ADR-0018](0018-monitoringspec-orphan-resolution.md) | MonitoringSpec orphan 의 단계적 해소 — Phase 1 deprecation, Phase 2/3 결정 보류 (Prometheus 도입 후) | Accepted | 2026-05-07 |
| [ADR-0019](0019-operator-commons-v0.5-promotion.md) | operator-commons v0.5.0 helper 승격 — validateStorageSize + apiError 3-of-3 입증 후 통일 | Proposed | 2026-05-07 |
| [ADR-0020](0020-rfc-0017-tooling-unification-adoption.md) | RFC-0017 operator tooling unification 채택 (lefthook + 18-linter + HEALTHCHECK) | Proposed | 2026-05-09 |
| [ADR-0021](0021-rfc-0018-pkg-finalizer-migration.md) | RFC-0018 채택 — pkg/finalizer migration (controllerutil → commons, 4 controller, PR-A5 first cut, status 별도) | Accepted | 2026-05-09 |
| [ADR-0022](0022-meta-set-status-condition-adoption.md) | meta.SetStatusCondition 전면 채택 — 6 site 마이그레이션 + ShardDraining 백오프 regression fix | Accepted | 2026-05-10 |
| [ADR-0023](0023-operatorhub-bundle-scaffold.md) | OperatorHub.io bundle scaffold cross-cut — operator-sdk 1.42, 3 CRD owned, alm-examples 3 sample (PR-B9, valkey ADR-0037 + postgres ADR-0013 패턴 이식) | Accepted | 2026-05-10 |
| [ADR-0024](0024-inc-0001-cross-cut-audit.md) | INC-0001 cross-cut audit — mongodb 의 ReplicaSetInitialized once-shot 패턴, milder anti-pattern (hasPrimary mitigation), Phase 2 auto reconfig 별 RFC | Accepted | 2026-05-10 |
| [ADR-0025](0025-cycle-0-baseline-and-cross-verification.md) | Cycle 0 — baseline 검증 + Bitnami/CloudPirates 3-way cross-verification matrix + 12-cycle program 분해 + F-IMP-04 diagnosticMode sharded 확장 | Implemented | 2026-05-12 |
| [ADR-0026](0026-cycle-0-through-12-program-retrospective.md) | Cycle 0~12 retrospective — ROADMAP 100% 도달 (105 [x]) + 4 신규 CRD + 5 e2e + 5 Grafana dashboard + 33 metric + 15 alert rule. Action items 5건 (실 시스템 통합) deferred | Implemented | 2026-05-12 |
| [ADR-0028](0028-restore-github-actions-for-oss-ci.md) | RFC-0002 일탈 — 13 workflow OSS CI 유지 (valkey ADR-0045 sister). external contributor surface + OpenSSF Scorecard + required_status_checks 근거. release.yml 7-job §7 ③ 1-step 명시 일탈 | Accepted | 2026-05-14 |

## 작성 가이드

- 형식: Nygard 5섹션 (Context / Decision / Consequences / Alternatives Considered).
- 위치: `docs/kb/adr/NNNN-<영어 kebab-case slug>.md`.
- 번호 부여: 4자리 0-padded, 한 번 부여한 번호는 *재사용 금지*.
- 본 INDEX.md는 신규 ADR 추가 시 수동 갱신.
