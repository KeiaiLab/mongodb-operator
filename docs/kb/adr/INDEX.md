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
| [ADR-0017](0017-crd-default-vs-webhook-invariant.md) | CRD default 가 있는 field 의 zero-value 거부 invariant 는 dead code (envtest 가 unreachable 발견) | Accepted | 2026-05-07 |
| [ADR-0018](0018-monitoringspec-orphan-resolution.md) | MonitoringSpec orphan 의 단계적 해소 — Phase 1 deprecation, Phase 2/3 결정 보류 (Prometheus 도입 후) | Accepted | 2026-05-07 |
| [ADR-0019](0019-keiailab-commons-v0.5-promotion.md) | keiailab-commons v0.5.0 helper 승격 — validateStorageSize + apiError 3-of-3 입증 후 통일 | Proposed | 2026-05-07 |
| [ADR-0020](0020-rfc-0017-tooling-unification-adoption.md) | RFC-0017 operator tooling unification 채택 (lefthook + 18-linter + HEALTHCHECK) | Proposed | 2026-05-09 |
| [ADR-0021](0021-rfc-0018-pkg-finalizer-migration.md) | RFC-0018 채택 — pkg/finalizer migration (controllerutil → commons, 4 controller, PR-A5 first cut, status 별도) | Accepted | 2026-05-09 |
| [ADR-0022](0022-meta-set-status-condition-adoption.md) | meta.SetStatusCondition 전면 채택 — 6 site 마이그레이션 + ShardDraining 백오프 regression fix | Accepted | 2026-05-10 |
| [ADR-0023](0023-operatorhub-bundle-scaffold.md) | OperatorHub.io bundle scaffold cross-cut — operator-sdk 1.42, 3 CRD owned, alm-examples 3 sample (PR-B9, valkey ADR-0037 + postgres ADR-0013 패턴 이식) | Accepted | 2026-05-10 |
| [ADR-0028](0028-olm-external-user-production-readiness.md) | OLM 번들 외부 사용자 운영 수준 — 5 결격 동시 해소, stable 채널 default 승격. (Phase C 의 Decision 부분 ADR-0029 가 supersede — 그 외 bundle/CSV 표준은 유효) | Accepted | 2026-05-14 |
| [ADR-0029](0029-olm-v1-migration-from-v0.md) | OLM v1 (operator-controller v1.8) 채택 — v0.30 (legacy, 18개월 stale) → v1.8 (next-generation, ClusterCatalog + ClusterExtension) migration. 옵션 C 사용자 결정. Phase C 라이브 적용 완료 (KeiaiLab Cluster, 2026-05-15) | Accepted | 2026-05-15 |
| [ADR-0030](0030-olm-v1-narrow-installer-rbac-and-network-policy.md) | OLM v1 narrow installer RBAC + olmv1-system NetworkPolicy — bundle CSV 의 13 clusterPermissions + 3 permissions derive (operator-controller `derive-service-account` 표준), cluster-admin 대체. olmv1-system NP 2종 (operator-controller + catalogd) 으로 zero-trust 정합 | Accepted | 2026-05-15 |
| [ADR-0034](0034-sprint-1-commons-pvc-topology-adoption.md) | Sprint 1 — keiailab-commons pkg/pvc + pkg/topology 채택 (-327 LOC, mongodb callsite 1 + pvc 1 교체) | Accepted | 2026-05-21 |
| [ADR-0036](0036-v3x-stable-baseline.md) | v3.x-stable baseline 인정 (audit ❌ 0 충족, CLAUDE.md §7 v3.x-stable 조건) | Accepted | 2026-05-21 |
| [ADR-0037](0037-gitops-artifacthub-standardization.md) | GitOps overlay + ArtifactHub 검증 파이프라인 표준화 (operator 4종 2-layer 표준, examples/gitops → deploy/overlays/prod 이전, signingKey, GH Actions OSS 정당화) | Accepted | 2026-06-02 |
| [ADR-0038](0038-olm-v1-path1-realignment-and-parity-guard.md) | OLM v1 (Path 1) 재정렬 + 7-CRD parity + drift 가드 (bundle/catalog/ClusterExtension → appVersion 1.13.1 정렬, sync-crds wildcard, verify-bundle-parity CI/pre-push 게이트, ClusterExtension 컷오버 보류) | Accepted | 2026-06-22 |
| [ADR-0039](0039-sharded-search-mongot-fanout-limit.md) | sharded MongoDB Search multi-shard 검색 = Community mongot 0.69.1 upstream 한계 (mongos 단일 endpoint 직접 라우팅=broadcast 아님, mongot localhost 하드코딩=StatefulSet 불가) + config server search-sync 비번 drift fix (precheck 비번 미검증 → 제거, v1.16.3) | Accepted | 2026-06-24 |
| [ADR-0040](0040-mongos-mongot-service-wiring.md) | sharded search 컨트롤면 배선 — mongot Service(`<cluster>-mongot:27028`, **단일 shard pin** `spec.router.mongotShard`) + mongos 4 setParameter 주입 (ADR-0039 #5 코드 해소 → SearchIndex Pending 고착 해소 + unsharded 컬렉션 검색 동작). **multi-shard 분산 컬렉션 검색은 upstream 한계로 여전히 미해결**(ADR-0039 #7·Decision #2 유지) | Accepted | 2026-07-14 |
| [ADR-0041](0041-harbor-registry-gitlab-ci-image-build.md) | 컨테이너 이미지 빌드·발행 이관 — GHCR 로컬 push(키체인 SPOF) → **GitLab CI `build:image`(remote buildkitd, RFC-0127) + Harbor**(`harbor.keiailab.dev/keiailab/platform/mongodb-operator`, RFC-0125). `make validate` 의 유령 차트(`charts/mongodb-cluster`) lint 선재 결함 동시 수정 — 릴리스 blocker. 범위 = *이미지 한정* (코드 canonical = GitHub, chart 발행 = GHCR OCI/ArtifactHub 유지 — RFC-0070 불변). residual: harbor 는 NetBird 내부 전용 → 공개 pull 미러 후속 필요 | Accepted | 2026-07-15 |

## 작성 가이드

- 형식: Nygard 5섹션 (Context / Decision / Consequences / Alternatives Considered).
- 위치: `docs/kb/adr/NNNN-<영어 kebab-case slug>.md`.
- 번호 부여: 4자리 0-padded, 한 번 부여한 번호는 *재사용 금지*.
- 본 INDEX.md는 신규 ADR 추가 시 수동 갱신.
