# Changelog

All notable changes to mongodb-operator will be documented in this file.

## [Unreleased]

## [1.16.9] - 2026-08-26

### Changed

- **컨테이너 이미지 빌드·발행 경로 이관 — GHCR 로컬 push → GitLab CI + Harbor** (ADR-0041).
  구 `make release` 는 `docker buildx build --push ghcr.io/...` 로 이미지를 냈는데, GHCR 자격이
  로컬 macOS 키체인에 묶여 **push 마다 GUI 승인 프롬프트**가 떴다 — 릴리스가 *사람이 앉아 있는
  특정 머신*에 종속된 SPOF 이자, 어떤 CI 경로로도 이미지를 낼 수 없는 구조였다. 이제
  `.gitlab-ci.yml` 의 `build:image` 잡이 클러스터 remote buildkitd(RFC-0127, mTLS) + Harbor robot
  자격(RFC-0125)으로 빌드·push 한다 (`stable` push = 발행 / MR = build-only 검증).
  이미지 경로: `harbor.keiailab.dev/keiailab/platform/mongodb-operator{,-bundle,-catalog}`.
  **범위 = 이미지 한정** — 코드 canonical 은 GitHub, helm chart 발행도 GHCR OCI + ArtifactHub
  유지 (RFC-0070 불변).

### Fixed

- **mongod exec 프로브가 건강한 DB 를 죽였다** (ADR-0042). liveness `mongosh ... ping` 의
  타임아웃 5s 는 mongosh(Node.js 런타임) 기동 비용을 감당하지 못했다. 노드의 컨테이너 exec
  경로가 잠깐 막히면 프로브만 느려지는데 kubelet 이 그걸 mongod 장애로 읽고 SIGKILL 했다 —
  라이브 실측: `exit 137 / reason=Error`(OOMKilled 아님), 파드 1본당 재시작 148회,
  클러스터 exec 타임아웃 10,007건 중 **99.6%가 단일 노드**, 해당 컨테이너 CPU 스로틀은 1%.
  시간축을 명명 상수(`probeLiveness*` / `probeReadiness*`) 하나로 모으고 창을 넓혔다:
  liveness `30s/15s/4회`(구 `10s/5s/6회`), readiness `15s/10s`(구 `10s/5s`).
  hang 감지가 목적이라 liveness 의 exec 은 **유지**한다(TCP 는 멈춘 mongod 도 accept).
  mongosh 를 부르는 전 프로브에 `--norc` 추가 — mongos 가 이미 쓰던 규약을 cfg·shard·RS 로 확장.
  가드 = `internal/resources/builder_probe_timing_test.go`.

- **mongot 사이드카에 자원 지정이 아예 없었다.** mongot 은 JVM 인데 `-Xmx` 를 주지 않으므로
  힙 상한을 컨테이너 cgroup 에서 읽는다 — limit 이 없으면 **노드 전체 RAM 기준**으로 잡는다.
  실측: limit 없는 mongot 15본이 571Mi~3956Mi 로 제각각 부풀었고 상한을 아무도 몰랐다.
  파드 QoS 도 이 컨테이너 하나 때문에 Burstable 로 떨어져 노드 압박 시 mongod 까지 축출
  후보가 됐다. 기본값 `requests 50m/512Mi`, `limits 1/6Gi` 지정(limit 은 Lucene mmap 페이지
  캐시가 cgroup 에 함께 계상되므로 mongod 와 같은 눈금으로 여유를 둔다).

- **`make validate` 선재 결함 (v1.16.6 릴리스 blocker)** — `validate` 가 존재하지 않는
  `charts/mongodb-cluster` 를 lint 해 `no such file or directory` 로 **항상 FAIL** 했다.
  `validate` ⊂ `gate` ⊂ `release` Step 1 이라 릴리스 파이프라인 전체가 이 한 줄에 막혀 있었다.
  실재하는 `charts/*/Chart.yaml` 만 wildcard 순회하도록 수정 (차트 추가/삭제에 자동 정합).

### Known gaps (정직 표기)

- GitLab 미러 프로젝트(`keiailab/platform/mongodb-operator`) 미생성 — 생성 전까지 `build:image` 미가동.

## [1.16.8] - 2026-07-30

### Fixed

- **ArtifactHub 스캔 에러 — `artifacthub.io/images` 가 내부 전용 레지스트리를 광고** (차트 전용 릴리스,
  operator 코드 무변경). 1.16.7 이 차트 기본 이미지를 GHCR 로 되돌리면서 이 어노테이션은 함께 고치지
  않아, `harbor.keiailab.dev/.../mongodb-operator:v1.16.4`(내부망 전용 + stale 태그)가 1.16.5~1.16.7
  세 릴리스에 그대로 실렸다. ArtifactHub 스캐너는 외부에서 도는지라 호스트를 해석하지 못해
  **매 스캔이 실패**했다(`no such host` 알림 반복). 실제 배포 기본값은 이미 GHCR 이라 사용자 영향은
  없었고 *스캔 메타데이터 한정* 결함이었다. `ghcr.io/keiailab/mongodb-operator:v1.16.6`(appVersion 정합)
  으로 교정.
- **재발 방지** — `artifacthub-verify` lint 잡에 ① images 어노테이션 ↔ appVersion 정합 ② `charts/`
  내부 전용 레지스트리(`*.keiailab.dev`) 이미지 참조 금지 게이트 추가. 어노테이션은 정적 텍스트라
  릴리스 bump 를 따라가지 않고 조용히 썩는다는 것이 이 사고의 뿌리다.

## [1.16.7] - 2026-07-24

### Changed

- **공개 차트 기본 이미지 harbor → GHCR 복귀** — 공개 차트 기본값이 사설 `harbor.keiailab.dev` 를
  가리키던 결함 수리(`values.yaml` `image.repository` → `ghcr.io/keiailab/mongodb-operator`,
  v1.16.6 digest 보존 미러 완료). 형제 valkey/postgres/qdrant-operator 동형. 이로써 위 Known gaps 의
  "GHCR=공개 / Harbor=내부 2-plane" 열린 결정이 **공개=GHCR 로 확정**됐다.

## [1.16.6] - 2026-07-14

### Fixed

- **mongos → mongot 배선 (SearchIndex Pending 고착 근본 해소)** — operator 가 shard mongod 에만 mongot
  파라미터(`mongotHost` / `searchIndexManagementHostAndPort`)를 주입하고 **mongos 에는 주입하지 않아**,
  `MongoDBSearchIndex` 컨트롤러가 mongos 경유로 보내는 인덱스 명령이 `SearchNotEnabled` 로 거부되어
  CR 이 Pending 에서 고착했다(라이브 19일). `builder.go` 의 *"mongos setParameter 불요"* 가정은
  ADR-0039 #5 가 이미 실측으로 반증한 것이었다.

### Added

- `MongoDBSearch.spec.router.mongotShard` (optional, default `shard-0`) — mongot Service 를
  **단일 shard 의 mongot pod 로 pin**. ClusterIP 로드밸런싱은 채택하지 않았다: ADR-0039 #7 실측상
  mongos 는 단일 mongot endpoint 에 **직접 연결**(fan-out 아님)하므로, LB 는 연결마다 임의 shard 로
  라우팅되어 **데이터가 없는 shard 를 만나면 조용히 빈 결과**를 낸다.
- `BuildMongotService()` — mongot(27028) 결정적 엔드포인트.

### 범위 (정직 표기)

- ✅ SearchIndex Pending 고착 해소 (인덱스 관리 경로)
- ✅ **unsharded(단일 shard 상주) 컬렉션**에 한한 `$search` / `$vectorSearch`
- ⛔ **multi-shard 분산 컬렉션 검색은 여전히 미해결** — Community mongot 0.69.1 upstream 한계
  (ADR-0039 Decision #2 불변). 본 릴리스는 이를 해결하지 않는다.

**배포 주의**: `spec.router.mongotShard` 는 **검색 대상 DB 의 primary shard 와 일치**해야 질의가 결과를
낸다(`config.databases` 확인). 불일치 시 인덱스는 Ready 여도 질의는 빈 결과.
search 활성 클러스터는 shard pod template 라벨 추가로 **shard STS 1회 롤링**(RS 순차 = 무중단).

## [1.16.5] - 2026-06-25

### Features

- **선언적 컬렉션 샤딩 (`spec.shardedCollections`)** — MongoDBSharded CR 에 샤딩할 컬렉션을 선언하면 operator 가 `EnableSharding(database)` 선행 후 `shardCollection` 을 idempotent 하게 적용한다. ordered shard key(`shardKey: [{field, order}]`, order ∈ {1,-1,hashed}), `unique` 제약, timeseries 컬렉션 샤딩(`timeseries: {timeField, metaField, granularity}`) 지원. v1alpha1 + v1beta1 양 API 에 byte-동일 추가(storageversion=v1beta1).
  - **키 drift 비흡수** — `config.collections` 사전 조회로 현재 shard key 와 desired 를 *순서까지* 비교. 이미 *다른 키*로 샤딩된 컬렉션은 흡수하지 않고 `ShardKeyDrift` status condition 으로 노출한다(silent absorption 차단).
  - **진입 가드** — mongos `ReadyReplicas>=1` AND 모든 `Status.ShardsAdded[i]==true` 충족 후에만 적용. 컬렉션 미생성(`NamespaceNotFound`)·연결 실패는 비치명 requeue.
  - **status 실측** — `updateStatus` 가 `config.collections` 를 관측해 `Status.ShardedCollections` 를 채운다(관측 실패 시 기존 값 보존).
  - **webhook 형식 검증** — db/coll non-empty, order enum, 중복 namespace 금지, timeseries+unique 모순 차단, timeField hashed 금지. timeseries shard key 적합성은 런타임 검증.

## [1.15.2] - 2026-06-23

### Fixed

- **[CRITICAL] adopt된 클러스터 무중단 업그레이드 영구 skip** — Helm→OLM 등으로 operator 가 인수(adopt)한 기존 클러스터는 `Status.Version` 이 비어 있어(완료 시 `completeUpgrade` 에서만 set) reconcile 진입 시 `currentVersion=""` → 업그레이드 orchestration 이 영구 skip 됐다. 특히 **Sharded 컨트롤러는 `Status.Version` 백필 경로가 전혀 없어**(RS 의 `updateStatus` 보강 부재) 사용자가 `spec.Version` 을 올려도 업그레이드가 감지되지 않았다(keiailab-mongo 8.3.4 패치 사고). reconcile 진입부에서 `Status.Version` 을 `EffectiveVersion`(=desired)으로 seed 해 이후 spec 변경이 업그레이드로 감지되게 한다(`upgrade.go` + `sharded_upgrade.go`). builder 는 `EffectiveVersion` 만 사용하므로 무롤링 유지. fake-client 회귀 테스트 3건 추가(`adopted_upgrade_unit_test.go`).
- **chart CRD `UpgradeStrategy.maxRetries` 누락 sync** — 1.15.1 이 `maxRetries` 를 v1beta1 API + OLM 번들 CRD 에는 추가했으나 packaged Helm CRD(`charts/mongodb-operator/crds/`)는 stale 하게 남아 ArtifactHub 차트가 불완전한 CRD 를 배포했다. `make manifests` 의 `sync-crds` 로 정합.

## [1.13.1] - 2026-06-16

### Features

- sharded topology v2 순수 결정 함수 — zone-aware placement / pre-flight 안전검증 / balancer advisory / chunk migration throttle (`internal/topology`, #322). 모두 순수 함수 + advisory(DryRun 기본), 실 mongos 명령은 controller opt-in 시에만. 96.6% 커버리지 / 27 unit tests + envtest 전체 green + Kind 실 구동 검증(ReplicaSet reconcile→Running).

### Chores

- 브랜치 모델 RFC-0069 전환 — `main` 폐기, `stable`(production) + `dev`(integration) 2-브랜치, CI 트리거 재배선
- Dependabot 비활성화 (.github/dependabot.yml 제거)

## [1.13.0] - 2026-06-11

### Refactor

- Keiailab-commons v0.11.0 채택 — 자체구현 7 표면 제거 (-1,025 LOC) (#312)

## [1.12.5] - 2026-06-11

### CI

- Nudge ArtifactHub repository tracking
- Use workflow ref for ArtifactHub dispatch
- Retry stale ArtifactHub metadata
- Sign Helm charts for ArtifactHub

### Chores

- *(chart)* Publish signed 1.12.5 chart

## [1.12.4] - 2026-06-10

### Bug Fixes

- *(ci)* Enable kube-linter default checks (#303)
- *(helm)* Preserve signing key armor header
- *(helm)* Align mongodb chart source with OCI release

### CI

- Harden ArtifactHub release verification
- Target untagged runners (drop keiailab tag)

### Chores

- Vendor helm commons dependency
- Adopt keiailab commons module
- *(artifacthub)* Refresh mongodb OCI metadata tag
- *(helm)* Publish signed chart metadata refresh 1.12.3
- *(helm)* Rotate artifacthub signing key
- *(helm)* Harden signed publish preflight
- *(license)* Apache-2.0 잔존 표기 → MIT 전수 정렬 (relicense 문서 마무리)
- Finish MIT license metadata alignment
- Replace short-form Apache headers with MIT
- Replace Apache license headers with MIT reference
- Align license metadata to MIT (SPDX, manifests, NOTICE)
- Standardize license to MIT, add GitLab CI shadow pipeline

### Documentation

- Align project license references with MIT
- Rewrite README for accuracy over marketing

### Features

- *(ops)* Add MongoDB ExternalSecret credential sourcing

## [1.11.0] - 2026-06-03

### Bug Fixes

- *(controller)* Reconcile 정확성 버그 14건 수정 (#9~#14 + MEDIUM 8)
- *(controller)* UpdateStatusWithRetry conflict 시 status mutation 유실 차단
- *(security)* BuildRestoreJob nil패닉 외 CRITICAL+보안 결함 8건 수정
- *(deps)* Govulncheck reachable CVE 4건 해소 (toolchain go1.26.4 + x/net v0.55.0) (#290)
- *(resources)* PEM init non-root(999) 회귀 차단 + HPA/PDB apiVersion 가드 (#285)
- *(release)* Chart OCI 자동 push 추가로 ArtifactHub 정체 해결 (#276)

### CI

- *(lint)* Insights goconst semantic 상수 추출 + goconst ignore-tests (#295)
- *(lint)* Staticcheck QF1012 전건 해소 (WriteString(Sprintf) → Fprintf) (#293)
- *(lint)* Ci.yml lint-debt 주석 정정 — #288 stale-cache false 0 수정 (#291)
- *(lint)* Full golangci-lint gate clean — 마지막 goconst 부채 정리 (#288)

### Chores

- *(generate)* Deepcopy + RBAC를 소스와 동기화 (controller-gen v0.17.0) (#282)

### Documentation

- *(roadmap)* Phase 5 brainstorm 시작 — 6 카테고리 우선순위 권장안 (#294)
- *(roadmap)* Phase 5 brainstorm gate 명시 (⛔ 배너 + 해소 절차) (#289)
- *(roadmap)* UnusedIndex [x] + Auto Pilot A등급 advisory 배선 반영 (#281 #283) (#284)
- *(roadmap)* ROADMAP drift 8건 코드 실측 정정 (#278)

### Features

- *(verification)* Backup verification Verifying 단계 실 쿼리 검증 구현
- *(insights)* MongoDBSharded per-shard 프로파일링 (mongos system.profile 부재 해소) (#292)
- *(backup)* QueryableBackup — verification controller 가 read-only mongod 자동 생성 (#287)
- *(insights)* Auto Pilot A등급 advisory — 조치 계획 surface (dry-run) (#283)
- *(insights)* UnusedIndex 추천 reconcile 통합 (dead code → 라이브) (#281)
- *(gitops)* Deploy/overlays/prod 통일 + artifacthub-verify (#277)

### Refactor

- *(cleanup)* 죽은 코드 제거 + 로깅/연결 정리 (LOW 5건)

### Testing

- *(insights)* 실 mongo 통합 테스트 하네스 — index/profile round-trip E2E (#286)

## [1.10.3] - 2026-05-28

### Bug Fixes

- *(chart)* Controller image tag mismatch — annotation + helper default 동시 정정 (#274)
- *(chart)* Correct operator image tag (1.9.0 → 1.9.1) (#273)
- *(chart)* Correct containsSecurityUpdates annotation (true → false for v1.10.x) (#271)

### Features

- *(autopilot)* Level V Auto Pilot 구현 — 14건 + 21 unit tests (#269)
- *(chart)* Bump capability level to Deep Insights (#260) (#268)

### Refactor

- Argos → keiailab rename + lint debt cleanup (v2) (#272)

## [1.9.1] - 2026-05-28

### Bug Fixes

- *(upgrade)* Rollback 조건 검사 + validation timeout + CustomConfig builder (#261)
- *(backup)* Replace plaintext password in Job env with Secret reference (#242)
- *(exporter)* Use secretKeyRef instead of plaintext MONGODB_URI (#241)
- *(tls)* Make AllowInvalidHostnames and TLS mode configurable via CRD (#240)
- *(rbac)* Separate secrets from configmaps/services, remove delete verb (#239)
- *(chart)* Secure defaults — PDB enabled, insecureSkipVerify configurable (#238)
- *(chart)* Always grant HPA read verbs to fix informer cache timeout

### Chores

- Operator-commons v1.0.0 업그레이드 계획 TODO 추가 (#236) (#257)

### Documentation

- 커뮤니티 배포 계획 문서 추가 (#237) (#258)
- *(incident)* INC-0001 RCA — GitHub account flagged (evidence-based)
- *(blog)* Awesome-list submission runbook with accurate status
- *(community)* Automation — release, topics, submissions, operatorhub guide
- *(blog)* Add dev.to launch post draft

### Features

- UnusedIndex analyzer + QueryableBackup builder + password rotation detection (#267)
- *(backup)* Implement MongoDBBackupVerification controller (#266)
- *(monitoring)* Auto-create exporter URI Secret for authenticated metrics (#265)
- *(backup)* Integrate BuildBackupCronJob into reconcile loop (#264)
- *(sharded-upgrade)* Add upgrade orchestration for MongoDBSharded (#263)
- *(tls)* Add cert-manager Certificate CR for MongoDB ReplicaSet (#262)
- *(upgrade)* Implement controller-level upgrade orchestration (#259)
- Security hardening v2 skeleton 패키지 생성 (#235) (#256)
- Sharded topology v2 skeleton 패키지 생성 (#234) (#255)
- DR v2 skeleton 패키지 생성 (#233) (#254)
- Observability v2 skeleton 패키지 생성 (#232) (#253)
- ClusterGroup 정책 적용 타입 상수 추가 (#227) (#252)
- MongoDBInsights에 TargetKind 필드 추가 (#226) (#251)
- KMS provider 상호 배타 검증 추가 (#225) (#250)
- Federation status condition type 상수 추가 (#224) (#249)
- OIDC issuerURL 형식 검증 강화 (#223) (#248)
- LDAP 설정 시 TLS 필수 검증 추가 (#222) (#247)
- ShardSpec에 DrainTimeoutSeconds 필드 추가 (#228) (#246)
- V1alpha1 타입 파일에 deprecation 주석 추가 (#231) (#245)
- *(config)* Add CustomConfig inline/configMapRef for mongod.conf (#244)
- *(storage)* Add accessModes, subPath, selector to StorageSpec (#243)
- *(branding)* Add logo + social card + dev.to publish script

## [1.9.0] - 2026-05-25

### Features

- *(release)* V1.9.0 — PITR + CRD v1beta1 + CNCF readiness
- *(api)* Graduate CRD from v1alpha1 to v1beta1
- *(backup)* Add BuildBackupCronJob for automated backup scheduling

## [1.8.0] - 2026-05-25

### Bug Fixes

- *(community)* CODE_OF_CONDUCT contact email + CITATION.cff version sync

### Features

- *(release)* V1.8.0 — community launch ready
- *(readme)* GA-ready — remove beta warning, English-first, feature updates

## [1.7.0] - 2026-05-25

### Bug Fixes

- *(mongos)* Harden startup/readiness probes to reduce unnecessary restarts

### Documentation

- *(gap-analysis)* Close gap #27 — resource presets already implemented

### Features

- *(release)* V1.7.0 — production hardening + all Bitnami gaps closed
- *(configsvr)* Add external config server support (#25) + gap-analysis 0 gaps
- *(mongos)* Add service-per-replica support (#24)
- *(leader-election)* Add --leader-election-namespace flag

## [1.6.0] - 2026-05-25

### Bug Fixes

- *(docs)* Fix all broken links in i18n and gap-analysis docs
- *(lefthook)* Pre-push 차단점 일괄 해소 + eightynine01 흔적 전수 정리 (#208)
- *(builder)* Tls-pem-merge init container SecurityContext 명시 추가
- *(insights)* Address Codex adversarial review challenges (RFC-0045)
- *(bundle)* Regen CSV v1.5.0 + PROJECT cliVersion 4.14.0 — 4 버전 stale 해소 (#168)
- *(chart)* Add monitoring.* default values to prevent nil deref in prometheusrule.yaml (#166)

### CI

- Add kube-linter + markdown-link-check + helm-install-test workflows (P1) (#158)
- *(security)* Add codeql + scorecard + dependency-review + dco workflows + ISSUE_TEMPLATE/config.yml (P0 critical) (#157)

### Chores

- *(cleanup)* CHANGELOG v0.0.x 본문 + v1.0.0 specific release guides + ISSUE_TEMPLATE 옛 dropdown (#213)
- *(security)* .gitleaks.toml — 공개 PGP fingerprint false positive 처리 (#212)
- *(cleanup)* 완료된 plan 3건 + handoff archive 3건 + valkey DR snapshot 제거 (#211)
- *(docs)* Templatize docker-hub-setup username (security audit) (#206)
- *(deps)* Bump operator-commons to v0.9.0 (Sprint 1 release)
- *(ci)* C7 RFC-0002 GitHub Actions cleanup — 9 workflow 제거 (§7 narrow exception 3종 보존, postgres sister) (#200)
- *(gitignore)* .claude-flow/ session artifact 추가 (Codex Major #6, .claude/ sister) (#201)
- *(docs)* Wave 2 cleanup — remove deprecated pre-commit config (#187)
- *(license)* Add Apache-2.0 boilerplate to 3 Go files (#161)
- Add .editorconfig + Chart annotations (#160)

### Documentation

- *(gap-analysis)* Final code verification — only 3 gaps remain
- *(gap-analysis)* Rewrite based on code verification — 7 gaps resolved
- *(i18n)* CODE_OF_CONDUCT 4개국어 — Contributor Covenant v2.1 공식 번역 채택 (#214)
- *(i18n)* Wave 4 — ADOPTERS / ARCHITECTURE / DESIGN / INSTALL / family 4개국어 (#210)
- *(i18n)* 19개 문서 4개국어 완성 + base와 keiailab 정합 동기화 (#209)
- *(adr)* V3.x-stable baseline 인정 (ADR-0036) + README v3.x-stable 배지 (#207)
- *(adr)* ADR-0033 Accepted — GHA 유지 + operator family v2.0 통합 정합 (ADR-0032 Superseded) (#203)
- *(adr)* ADR-0032 Accepted — GHA 전면 제거 → 로컬 4계층 단일 운영 (RFC-0002 strict) (#197)
- *(adr)* ADR-0031 integrated GHA retention rationale for public OSS operator
- *(i18n)* README.zh.md full native 翻訳 (566 LOC) (#192)
- *(i18n)* README.ja.md full native 翻訳 (557 LOC, base v0.8.0-consume) (#191)
- *(i18n)* README 4-lang 재구성 — ja/zh placeholder 신규 + 5-link family footer (#189)
- *(agents)* Worktree base stale 검사 의무 sub-section 추가 (OLM cycle root-cause prevention) (#174)
- *(roadmap)* Correct stale markers per §3.2/§4.3/§4.5 code reality
- *(roadmap)* Add Phase 5 candidate baseline (P-E.7.1) (#171)
- Add ARCHITECTURE.md — single-page architecture description (#170)
- *(crd)* Propagate G-11 standalone godoc to mongodb CRD description (#165)
- Add SUPPORT.md + .ko.md (README/CONTRIBUTING/SECURITY) + .github/FUNDING.yml (P2 docs+i18n) (#159)

### Features

- *(release)* V1.6.0 — docs restructure + gap analysis verification
- *(lefthook)* RFC-0002 gha-block pre-commit hook + ADR-0035 (P2-2) (#205)
- *(commons)* Sprint 1 Phase 2 — operator-commons pkg/pvc + pkg/topology 채택 (-327 LOC)
- *(ci)* 로컬 4계층 보강 — kube-linter + go-licenses + markdown-link-check (lefthook + Makefile) (#196)
- *(release)* Scripts/helm-publish.sh + scripts/release.sh 신규 — RFC-0002 GHA 대체 (#195)
- *(ci)* RFC-0002 — .github/workflows 전체 제거 (12 파일) — 로컬 4계층 이관 (#194)
- *(deps)* Operator-commons v0.7.0 → v0.8.0 + pkg/probes 2 Exec site 적용 (#190)
- *(docs)* Keiailab 브랜딩 — README header/footer + BRANDING.md + docs/family.md (Wave 3) (#188)
- *(olm)* OLM v1 only 전환 — v0 cluster path + community-operators sync 영구 폐기 (ADR-0028 Phase D) (#173)
- *(insights)* MongoDBSharded kind support via mongos + Codex P4 fixes
- *(insights)* Auto-apply profiling level + slowms before Fetch
- *(insights)* Prometheus metrics — recommendations/analysis/sampled
- *(insights)* Real system.profile analysis engine — first sub-task
- *(release)* Community-operators upstream sync automation (ADR-0027) (#169)
- *(bundle)* Add scorecard v1alpha3 config — 6 OLM test parity with postgres ADR-0013 (#167)
- *(chart)* Add vaultTransit values section (bitnami parity 100%, cycle 17 SDK 활성) (#164)
- *(chart)* Add PrometheusRule template (bitnami mongodb-sharded parity, 8 alerts) (#162)

### Refactor

- *(docs)* Restructure all docs into docs/ — only README.md at root
- *(insights)* Narrow ProfileFetcher interface + 4 Codex re-review fixes
- *(chart)* Remove mongodb-cluster wrapper chart, add artifacthub-repo.yml ignore (BREAKING) (#163) (**BREAKING**)

### Reverts

- *(ci)* Re-restore 9 workflows after PR #200 race — operator family v2.0 정합 (사용자 결정 2026-05-21) (#204)
- *(ci)* Restore .github/workflows (12 files) — operator family v2.0 정합 (사용자 결정 2026-05-21) (#199)

### Deps

- *(deps)* Bump github.com/onsi/ginkgo/v2 from 2.28.3 to 2.29.0 (#179)
- *(deps)* Bump github.com/keiailab/operator-commons (#180)
- *(deps)* Bump github.com/onsi/gomega from 1.40.0 to 1.41.0 (#178)
- *(deps)* Bump the kubernetes group with 4 updates (#177)

## [1.5.0] - 2026-05-13

### Bug Fixes

- *(e2e)* Cycle 14 — mongosh exec requires admin credentials after auth-required CRD
- *(e2e)* Cycle 14 — auth secret required + apiVersion typo + ensureAdminSecret helper

### Chores

- *(gitignore)* Exclude .claude/ session lock files

### Documentation

- *(retrospective)* 🎯 cycle 12 final — ROADMAP 100% + ADR-0026
- *(adr)* ADR-0025 cycle 0 baseline + 12-cycle program 분해
- *(comparison)* 3-way cross-verification matrix (operator vs Bitnami vs CloudPirates)

### Features

- *(cycle-19)* G-11 standalone-aware webhook + G-13 cosign sign target (NEW CODE)
- *(cycle-18)* G-12 mongos StatefulSet + webhook integration validators
- *(cycle-17)* Real external system SDK integration — LDAP probe + OIDC discovery + Vault Transit + cross-cluster
- *(cycle-16)* Sharded ConfigServer/Shard/Mongos sidecar inject (oplog tailer + audit forwarder)
- *(cycle-15)* Mongorestore Job + oplog tailer + audit forwarder sidecar inject
- *(cycle-14)* Sharded builder integration — ConfigServer + Shard + Mongos
- *(cycle-13)* Builder 실 통합 — auth/encryption/audit args + PodSpec extension merge
- *(metrics)* F17-F22 30+ Prometheus metrics + PrometheusRule generator
- *(cycle-10)* Phase 4 Bitnami/CloudPirates parity expansion (18건)
- *(cycle-9)* F43-F50 advanced backup + F74 scale-in (11건)
- *(cycle-8)* F56-F60 MongoDBClusterGroup + F61-F65 audit logging
- *(cycle-7)* F11-F16 upgrade automation + F51-F55 MongoDBInsights
- *(encryption)* F38-F42 cycle 6 KMS encryption-at-rest CRD + helpers
- *(federation)* F33-F37 cycle 5 MongoDBFederation CRD + skeleton
- *(auth)* F23-F32 cycle 4 LDAP + OIDC 인증 CRD + helpers + e2e stub
- *(charts)* F85 cycle 3 mongodb-cluster Helm sub-chart (Bitnami/CP parity)
- *(grafana)* F06-F10 cycle 2 Grafana dashboards 5종 + Helm ConfigMap
- *(controller)* F03/F04 oplog uploader + MongoDBBackup restore branch
- *(resources)* F02 PITR oplog tailing sidecar 빌더
- *(api)* F01/F04 MongoDBBackup.Spec.Restore + PointInTime API stable
- *(sharded)* F-IMP-04 DiagnosticMode를 ConfigServer/Shard/Mongos까지 확장

### Testing

- *(e2e)* Cycle 18 webhook reject e2e — real cluster invalid spec rejection
- *(e2e)* Cycle 18 final — federation Phase expectation includes Degraded/Failed
- *(verify)* Real external system round-trip verification scripts
- *(e2e)* F05 PITR e2e — Restore CR API path + Restoring phase 검증

## [1.4.23] - 2026-05-12

### Bug Fixes

- *(security-scan)* Replace govulncheck-action with binary install for dependabot compat (#154)
- *(makefile)* Docker-build uses host LOCAL_PLATFORM, --load can't load manifest lists (#153)

### Build

- *(makefile)* Parameterise PLATFORMS, default amd64+arm64 (follow-up to #151) (#152)

### Documentation

- *(agents)* Unify multi-arch policy — amd64+arm64 across repo types (#151)

### Deps

- *(deps)* Bump github.com/go-openapi/swag from 0.25.4 to 0.26.0 (#100)
- *(deps)* Bump github.com/prometheus/procfs from 0.19.2 to 0.20.1 (#35)
- *(deps)* Bump github.com/go-openapi/jsonreference (#41)
- *(deps)* Bump go.yaml.in/yaml/v2 from 2.4.3 to 2.4.4 (#54)
- *(deps)* Bump golang.org/x/time from 0.14.0 to 0.15.0 (#60)
- *(deps)* Bump github.com/fxamacker/cbor/v2 from 2.9.0 to 2.9.2 (#74)
- *(deps)* Bump github.com/go-openapi/swag/cmdutils (#94)
- *(deps)* Bump github.com/go-openapi/swag/stringutils (#95)
- *(deps)* Bump golang.org/x/tools from 0.44.0 to 0.45.0 (#80)
- *(deps)* Bump go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp (#82)
- *(deps)* Bump github.com/google/cel-go from 0.26.1 to 0.28.0 (#83)
- *(deps)* Bump golang.org/x/mod from 0.35.0 to 0.36.0 (#86)
- *(deps)* Bump github.com/go-openapi/swag/loading (#89)
- *(deps)* Bump github.com/go-openapi/swag/fileutils (#92)
- *(deps)* Bump github.com/grpc-ecosystem/grpc-gateway/v2 (#93)
- *(deps)* Bump github.com/go-openapi/swag/typeutils (#98)
- *(deps)* Bump github.com/go-openapi/swag/jsonname (#99)
- *(deps)* Bump golang.org/x/crypto from 0.50.0 to 0.51.0 (#106)
- *(deps)* Bump github.com/klauspost/compress from 1.18.0 to 1.18.6 (#107)
- *(deps)* Bump go.mongodb.org/mongo-driver/v2 from 2.5.1 to 2.6.0 (#108)
- *(deps)* Bump go.uber.org/zap from 1.27.1 to 1.28.0 (#109)
- *(deps)* Bump github.com/fsnotify/fsnotify from 1.9.0 to 1.10.1 (#110)
- *(deps)* Bump github.com/onsi/ginkgo/v2 from 2.27.2 to 2.28.3 (#111)
- *(deps)* Bump github.com/onsi/gomega from 1.38.2 to 1.40.0 (#112)

## [1.4.22] - 2026-05-12

### Chart

- *(security)* Bump mongodb_exporter 0.40 → 0.51.0 (release 1.4.22) (#150)

## [1.4.21] - 2026-05-12

### Bug Fixes

- Dedup renovate config — remove orphan renovate.json5 (cycle 22) (#129)
- *(bundle)* CSV base 의 빈 icon 블록 제거 (community-operators lint 정합) (#126)

### CI

- Add release + helm-publish workflows (follow-up to #140) (#143)
- Add GitHub Actions workflows and standardize Dependabot config (#140)

### Chores

- *(repo)* Remove internal AI working notes from main (#139)
- *(repo)* Move deploy/ to examples/gitops/ and fix dangling docs link (#142)
- *(lefthook)* Enforce DCO_STRICT by default (0 → 1)

### Documentation

- *(roadmap)* 분기/주 타임라인 제거 + sub-task 체크리스트 입자도로 전면 재작성 (#135)
- *(governance)* Align Lazy Consensus 시한 with RFC-0002 (no GH Actions)
- *(contributing)* Replace pre-commit framework guidance with lefthook
- *(releases)* Fix broken relative path to ROADMAP
- *(rfc)* RFC-0001 Auto RS reconfig on host change detection (ADR-0024 Phase 2) (#128)
- *(handoff)* 본 세션 cumulative work product SSOT 동기 (v1.4.20 + ADR-0024)

### Features

- *(api)* MongoDBSharded.spec.shards.arbiter 필드 추가 (#138)
- *(api,resources)* PodSpec.DiagnosticMode 필드 + ReplicaSet 적용 (#137)
- *(api)* StorageSpec에 PVC retention policy 노출 (#136)
- *(observability)* Reconcile latency Histogram for SLO 추적 (cross-operator 이식) (#132)
- *(storage)* PVC auto-resize on Spec.Storage.Size 증가 (cross-operator 이식) (#131)
- *(ha)* Default TopologySpreadConstraints (valkey PR #48 + postgres PR #30 이식) (#130)
- *(bundle)* CSV icon 추가 (실제 logo base64) — community-operators 정합 (#127)

### Refactor

- *(controller)* Migrate to events.EventRecorder (RFC-0023 Phase 2) (#134)

### Chart

- *(metrics)* Expose ServiceMonitor bearer token and authorization options (#141)

## [1.4.20] - 2026-05-10

### Bug Fixes

- *(controller)* Meta.SetStatusCondition 전면 채택 + ShardDraining 백오프 regression fix (PR-A5.2, ADR-0022) (#120)

### Chores

- *(oss)* CITATION.cff 추가 (#121)

### Documentation

- *(adr)* ADR-0024 INC-0001 cross-cut audit — mongodb ReplicaSetInitialized once-shot 패턴 (#124)
- *(readme)* MongoDB version badge 추가 (cross-repo 정합) (#122)

### Features

- *(bundle)* OperatorHub.io bundle scaffold + ADR-0023 (PR-B9 cross-cut) (#123)
- *(controller)* RFC-0018 pkg/finalizer migration (PR-A5 first cut, ADR-0021) (#119)
- *(controller)* RFC-0018 pkg/finalizer migration (PR-A5 first cut, ADR-0021) (#118)

## [1.4.19] - 2026-05-09

### Chores

- *(deps)* Operator-commons v0.5.0 → v0.6.0 (RFC-0018 SetAvailable + SetReadyFalse 사용 가능) (#117)
- *(chart)* Bump to 1.4.18

### Documentation

- *(handoff)* Sprint A PR-A5 진입점 (commons RFC-0018 채택) (#116)

### Features

- BuildShardStatefulSet + BuildConfigServerStatefulSet Probe 추가 (argos cycle 21)

## [1.4.18] - 2026-05-09

### Chores

- *(deps)* Operator-commons v0.4.0 → v0.5.0
- *(audit)* CHANGELOG entry + deps log + .pre-commit archive (RFC-0017 안정 적용)
- *(audit)* RFC-0017 채택 + codecov 절대 floor + HANDOFF 압축

### Documentation

- *(audit)* Live-verified 마커 인라인 삽입 (SSOT-gap 57.1% → 0%)

## [1.4.17] - 2026-05-08

### Bug Fixes

- *(tls)* Mongos hostname verify bypass + startupProbe — race fix

## [1.4.16] - 2026-05-08

### Features

- *(builder)* Pillar P7 Phase 3b — preferTLS + PEM merge + 4 mongod args
- *(renovate)* Auto-update PR 진입점 (Go modules + image tag)

### Style

- Gofmt builder.go

## [1.4.15] - 2026-05-08

### Features

- *(builder)* Pillar P7 Phase 3a — STS TLS server cert volume mount

## [1.4.14] - 2026-05-08

### Features

- *(controller)* Pillar P7 Phase 2 — cert-manager Certificate CR 자동 emit

## [1.4.13] - 2026-05-08

### Bug Fixes

- *(security)* Copy-keyfile busybox 1.36 → 1.37 + const 단일화

## [1.4.12] - 2026-05-07

### Bug Fixes

- *(audit)* Script ArgoCD count + KPI 표 갱신 — umbrella 포함 9/9 정합
- *(webhook)* Controller-runtime v0.23+ generic API 정합 (build 회복)
- *(scripts)* Release-smoke-test.sh retry policy for gh-pages CDN flake (it45)
- *(controller)* Race-tolerant Create — IsAlreadyExists guard (it41 cross-cut)
- *(controller)* Conditions LastTransitionTime → upstream 위임 (ADR-0013, it33)

### Build

- *(make)* Release pipeline 에 SBOM 자동 첨부 + git-cliff release notes (T0-2)
- *(make)* Add `make sbom` target — syft SPDX-2.3 (T0-1)

### Chores

- *(security)* Mongodb release gate Go 1.25.10 동기화
- *(chart)* Mongodb operator image annotation 1.4.12 동기화
- *(lint)* 의도적 deprecated 사용 명시
- *(crd)* MonitoringSpec deprecation 출력 동기화
- *(chart)* Values.schema.json 정밀화 — postgres 73줄 패턴 차용 (C-P0-7)
- *(chart)* OperatorCapabilities Full Lifecycle → Seamless Upgrades (C-P0-8 over-claim 정정)
- *(hooks)* PLATFORMS amd64-only 가드 (C-P0-12, C-P0-1 후속)
- *(hooks)* DCO Signed-off-by warn-only commit-msg gate (A-P0-5)
- *(deps)* Controller-runtime v0.22.4 → v0.23.3 (4-repo drift 봉인, C-P0-3)
- *(go)* Go directive 1.26.2 → 1.25.7 + Dockerfile builder SHA 동기 (C-P0-2)

### Documentation

- *(audit)* C37 완료 100% 갱신 + KPI 표 conditions 추가
- *(audit)* C37 진행률 80% + envtest 한계 명시
- *(audit)* C37 정정 + 진행률 60% — RS 영역 정상 / Sharded 한정 격차
- *(audit)* C37 해소 path 정밀화 — helpers.go infrastructure 보유 명시
- *(audit)* C37 발견 — mongodb CR status conditions 빈약 (visibility 격차)
- *(audit)* Clean 영역 4건 추가 (Distroless / Readiness / Mounts / Node spread)
- *(audit)* Clean 영역 3건 추가 (Probe timing / podManagementPolicy / label component)
- *(audit)* Clean 영역 4건 추가 (Image / SA permission 직접 검증 / ArgoCD tracking-id / revision)
- *(audit)* Clean 영역 3건 추가 (Admission denial / Pod failure / PVC retention)
- *(audit)* Clean 영역 3건 추가 (Services / Endpoints / Cert validity)
- *(audit)* Clean 영역 2건 추가 (ConfigMap usage / HPA capability)
- *(audit)* Clean 영역 4건 추가 (CNI / Node / CoreDNS / PSS enforcement)
- *(audit)* Clean 영역 3건 추가 (CSI / STS READY / Finalizer)
- *(audit)* Clean 영역 2건 추가 (Leader election leases / PV provisioning)
- *(audit)* Clean 영역 3건 추가 (Pod restart rate / OOMKilled 0 / uptime distribution)
- *(changelog)* Controller-runtime v0.23+ API 진화 명시 (Internal 섹션)
- *(adr)* 0017 Type A Errata — API 진화 차원 (compile-time guarantee 격상)
- *(operations)* Release-checklist-1.4.12.md — pre-flight + pipeline + rollback
- *(changelog)* 1.4.12 entry 보강 — invariant 11건 + envtest + ADR + cluster-ops
- *(agents)* Cluster Ops Mode 섹션 — 운영자 진입 절차 + ADR 5건 link
- *(operations)* Quick run 섹션 — audit-cluster-state.sh 진입점 명시
- *(audit)* 갱신 cadence + trigger 정책 — audit 신선도 차단
- *(audit)* 상용제품 수준 KPI 정의 — 측정 가능한 진전 baseline
- *(audit)* 격차 발견 commit hash 매핑 — audit trail 강화
- *(sprint)* Mermaid 시각화 — phase 의존성 그래프 + class 레전드
- *(adr)* 0019 Proposed — operator-commons v0.5.0 helper 승격 plan
- *(audit)* Mongodb / postgres GitOps drift 0 검증 추가
- *(handoff)* Cluster-ops mode 14 cycles 누적 final summary
- *(readme)* Main 인덱스에 Operations Documentation 섹션 추가
- *(operations)* README — 운영자 single entry point + navigation hub
- *(operations)* 통합 sprint plan — 상용제품 수준 도달 step-by-step
- *(audit)* Clean 영역 1건 추가 — 3 operator Resource requests/limits 통일
- *(audit)* C36 PriorityClass 부재 + clean 2건 (ImagePullPolicy/probes)
- *(audit)* Clean 영역 5건 추가 (Storage/Ingress/SA token/DiskPressure/cert-mgr)
- *(audit)* C33 Service mesh + C34 Backup CronJob + C35 valkey anti-affinity 발견
- *(audit)* Clean 영역 표 추가 — 격차 0 영역 명시 (PSS/RBAC/imagePull/GitOps)
- *(audit)* C32 TLS in transit 부재 — 3 keiailab operator 인스턴스 평문 통신
- *(audit)* C30 NetworkPolicy 비대칭 + C31 ns Quota 부재 발견
- *(readme)* GitHub Discussions badge (A-P0-6)
- *(audit)* C29 dead RBAC 발견 — valkey-operator 0.1.0-alpha.2 잔존
- CODEOWNERS team 정합 — @eightynine01 → @keiailab/maintainers (A-P0-2)
- *(operations)* 통합 cluster ops audit (2026-05-07) — 상용제품 수준 도달 plan
- *(adr)* 0016 Errata — Docs accuracy audit sub-clause 추가
- *(monitoring)* I16/I26 정정 — controller 미구현 명시 + ADR-0018 reference
- *(tasks)* I26 100% (Phase 1 완료) + I28 신규 (Phase 2 trigger 차단)
- *(adr)* 0018 MonitoringSpec orphan 단계적 해소 — Phase 1 deprecation 즉시
- *(tasks)* Cluster-ops audit 결과 5건 등록 (F23/C24/C25/I26/T27)
- *(readme)* OpenSSF Scorecard badge (A-P0-7)
- *(handoff)* ServiceMonitor fail-soft 확정 + 3 operator cross-cut 비대칭 audit
- ADOPTERS.md 신설 — argos-platform-data production user 등재 (A-P0-4)
- *(handoff)* Observability 격차 #2 — Prometheus Operator 부재로 metrics scrape 무력
- *(governance)* Voting 임계 수치화 — simple majority / 2/3 supermajority (A-P0-3)
- *(snapshots)* Keiailab-valkey-prod CR disaster recovery snapshot
- *(handoff)* Keiailab-valkey-prod manual apply 확정 — disaster recovery 불가
- *(handoff)* Valkey CR 자체도 GitOps 라벨 부재 — 2단계 격차 명시
- *(handoff)* Data plane GitOps 격차 발견 — keiailab-valkey-prod helm 직접 install
- *(handoff)* Cluster ops mode — 운영 상태 라이브 검증 + 1.4.12 release readiness
- *(readme)* Docs/README 의 Advanced Topics 에 webhook 가이드 link
- *(webhook)* User-facing admission webhook 가이드 (13 invariants 매트릭스)
- *(handoff)* Iteration 48 — T22 sbom target + v1.4.11 SBOM backfill
- *(adr)* 0017 Errata — Type A' 조건부 unreachable (omitempty 부재 + defaulter)
- *(agents)* Webhook invariant 추가 의무 audit 안내 (ADR-0015/0016/0017)
- *(handoff)* It46 step 11-13 + it47 step 1-5 — ADR + envtest + cross-cut
- *(adr)* 0017 CRD default vs webhook invariant — dead-code 패턴
- *(adr)* 0016 cross-cut audit pattern — invariant 의 3 operator 동시 점검
- *(handoff)* It46 cycle 종합 — webhook invariant 10건 + cross-cut audit
- *(tasks)* I14 60% + F15 100% + I16 MonitoringSpec orphan 발견 (it46)
- *(chart)* NOTES.txt 에 webhook 활성/비활성 가이드 추가 (it46)
- *(handoff)* Iteration 46 cycle 결과 — webhook coverage + lint sweep
- *(handoff)* Iteration 45 step 1-3 — webhook server 부트스트랩 (commit 50b3498)
- *(handoff)* Iteration 45 — commit hash 기록 (b01f5cd)
- *(handoff)* Iteration 44 — ADR-0014 (intentional design 보존)
- *(adr)* ADR-0014 — Controller Create 패턴 boundary (intentional 보존)
- *(handoff)* Iteration 43 — valkey CreateOrUpdate 마이그레이션
- *(handoff)* Iteration 42 — mongodbbackup CreateOrUpdate 마이그레이션
- *(handoff)* Iteration 41 — race-tolerant cross-operator audit
- *(handoff)* Iteration 40 — valkey backup e2e 검증 + race-tolerant fix
- *(handoff)* Iteration 39 — cluster live-verified snapshot (RFC-0004 ssot-gap)
- *(handoff)* Iteration 38 — docs ship (incident reasoning 영구 기록)
- *(handoff)* Iteration 36-37 — docker-build 통일 + valkey backup PodSecurity fix
- *(handoff)* Iteration 35 — data 통합 + postgres incident 디버깅 (cluster 운영)
- *(adr)* INDEX.md add ADR-0013 (it33 follow-up)
- *(handoff)* Iteration 32 — valkey setCondition upstream 위임 + boundary 분석
- *(handoff)* Iteration 30-31 — commons v0.4.0 webhook + valkey 6/6 완성
- *(handoff)* Iteration 27-29 — 3 operator labels deepening (valkey 5/5 첫 완성)
- *(handoff)* Iteration 26 — mongodb NetworkPolicy → commons v0.3.0 위임
- *(handoff)* Iteration 25 — valkey NetworkPolicy → commons v0.3.0 위임
- *(handoff)* Iteration 24 — operator-commons v0.3.0 (networkpolicy 패키지)
- *(handoff)* Iteration 23 — commons v0.2.1 + valkey ServiceMonitor 위임 (실 사용처 첫 적용)
- *(handoff)* Iteration 22 — operator-commons v0.2.0 (labels + monitoring)
- *(handoff)* Iteration 18+20 — multi-version e2e 회귀 가드 (valkey V2 + postgres P3)
- *(handoff)* Iteration 17-19 — Phase 2+3 chart values parity (3 operator 동등화)
- *(handoff)* Iteration 13-15 — M2 완성 + M3 (chart values parity)
- *(handoff)* Iteration 9-12 — Phase 1 M1 + M2 #1-3 (mongodb)
- *(handoff)* Iteration 8 — operator-commons 부트스트랩 + 3 operator cross-cut
- *(handoff)* Iteration 7 — valkey 차단요인 2 진단 + 회귀 가드

### Features

- *(controller)* C37 100% — evaluateShardedConditions pure function + isolated unit tests
- *(controller)* C37 3차 — TLSReady / BackupReady 조건부 conditions
- *(controller)* C37 2차 — ConfigServerReady / ShardsReady / MongosReady conditions
- *(controller)* C37 1차 해소 — MongoDBSharded Ready / Progressing conditions 추가
- *(scripts)* Audit-cluster-state.sh — KPI 자동 측정 도구
- *(api)* MonitoringSpec godoc Deprecated marker (ADR-0018 Phase 1)
- *(webhook)* Backup.schedule non-empty invariant (cross-cut, postgres 패턴)
- *(webhook)* TLS / Backup omitempty trap audit + invariant 4건
- *(webhook)* Auth.adminCredentialsSecretRef.name 비어있지 않음 invariant
- *(webhook)* Storage.size 하한 1Gi invariant (I14 잔여 진행)
- *(webhook)* MongoDB / MongoDBSharded validating admission webhook (it45)
- *(chart)* Bitnami parity operational keys (iteration 15 / Phase 1 M3)
- *(api)* MongoDB 화이트리스트 + IsSupportedMongoDBVersion (commons 위임)

### Refactor

- *(controller)* B-P0-5 requeue cadence 상수화
- *(api)* Finalizers.go 신설 — Finalizer 상수 export (B-P0-1)
- *(api)* Conditions.go 신설 — Reason 상수 export (B-P0-2)
- *(webhook)* ApiError group string 하드코딩 → GroupVersion.Group 참조
- *(controller)* Mongodbbackup createOrUpdate → controllerutil.CreateOrUpdate (it42)
- *(resources)* BuildLabels → commons labels v0.3.0 위임 (it27)
- *(resources)* NetworkPolicy → operator-commons v0.3.0 위임 (it26)
- *(resources)* SecurityContext builder → operator-commons v0.1.1 위임

### Testing

- *(controller)* C37 RS conditions isolated tests
- *(controller)* C37 conditions test envtest 한계 명시 — manual / 별 cycle 검증
- *(webhook)* MongoDBSharded admission round-trip 시나리오 (3건)
- *(webhook)* Admission round-trip 시나리오 확장 (storage / TLS trap / backup trap)
- *(webhook)* Envtest admission round-trip suite (Setup 100% coverage)
- *(webhook)* CustomValidator entry point + type-assertion path 가드 (it46)
- *(e2e)* Mongodb version upgrade rolling 시나리오 (iteration 14 / Phase 1 M2 #5)
- *(e2e)* Mongodb backup PVC round-trip 시나리오 (iteration 13 / Phase 1 M2 #4)
- *(e2e)* Mongodb sharded topology 시나리오 (iteration 12 / Phase 1 M2 #3)
- *(e2e)* Mongodb failover 시나리오 (iteration 11 / Phase 1 M2 #2)
- *(e2e)* Mongodb e2e 프레임워크 부트스트랩 + bootstrap_test 시나리오 (iteration 10 M2)

### Style

- *(api)* Copyloopvar 위반 3건 제거 (Go 1.22+)
- *(webhook)* Golangci-lint clean (it46) — copyloopvar / gofmt / unused

## [1.4.11] - 2026-05-07

### Bug Fixes

- Preserve deployment revision annotation

## [1.4.10] - 2026-05-07

### Bug Fixes

- Overlay owned deployment template fields

## [1.4.9] - 2026-05-07

### Bug Fixes

- Clear reconcile error on success

### Chores

- Use condition type constants

## [1.4.8] - 2026-05-07

### Bug Fixes

- Preserve deployment pod template defaults

## [1.4.7] - 2026-05-07

### Bug Fixes

- *(security)* PodSecurity restricted 위반 해소 — copy-keyfile init container
- *(security)* Gosec G115 — int → int32 overflow conversion 회피 (P0 §1.5 audit)
- *(deploy)* Namespace prod/db → data 통합 + storageClass ceph-rbd 정합 (#114)
- *(audit)* Trivy fail-handling 보강 — silent-fail 제거 (3-repo 정합)

### Chores

- Bump mongodb operator to 1.4.7
- Align mongodb defaults with latest 8.3.1
- *(deps)* Renovate 단일 채택 + Dependabot 제거 (ADR-0012, 3-repo 정합)
- *(governance)* CODEOWNERS + release-smoke-test (3-repo 통일)
- *(deps)* Renovate 자동 의존성 갱신 (RFC 0002 §7 예외, 3-repo 정합)

### Documentation

- Add mongodb 1.4.7 changelog
- *(handoff)* Iteration 6 — 4 트랙 결과 (Track 1+2+3+4 helm install 완료)
- *(handoff)* 옵션 C 4 작업 동시 진입 — Phase A3 chaos PASS + Phase B 차단 요인 식별 + ADR-0058 + smoke.sh 갱신 (iteration 5)
- *(handoff)* ADR-0057 Phase A1+A2 통과 — valkey-operator side-by-side 배포 검증 (iteration 4)
- *(governance)* GOVERNANCE / MAINTAINERS / AGENTS / TASKS 신설 — 3-repo 정합 (INC-2026-05-07)
- *(handoff)* 2026-05-07 운영 장애 + PodSecurity 사고 종료 (iteration 2)
- *(handoff)* Quality baseline 추가 — make test PASS + coverage (P2 §3.4)
- *(governance)* ADR-0011 (pre-commit 분기 정당화) + deps log seed (P0+P1 baseline)
- *(deploy)* GitOps 배포 디렉터리 운영 런북 추가 (3-repo 정합) (#113)

### Features

- *(lint)* Golangci-lint 도입 + HANDOFF.md 신규 (3-repo 정합)
- *(release)* Git-cliff release notes + helm chart .prov 서명 plumbing (3-repo 통일)

### Refactor

- *(lint)* Goconst+gocyclo+unparam 강화 + 4 production const 도입 (3-repo lint 정합)

### Merge

- Release-1.4.5 → main (v1.4.5)

## [1.4.5] - 2026-05-04

### Bug Fixes

- *(sharded,p0)* Bootstrap-admin.sh 비-멱등 createUser (v1.4.5)

## [1.4.4] - 2026-04-30

### Bug Fixes

- *(sharded,p0)* Scale-back-out cycle stuck (v1.4.4)

### Merge

- Release-1.4.4 → main (v1.4.4)

## [1.4.3] - 2026-04-30

### Bug Fixes

- *(sharded,p0)* Chart CRD drift + addShards index panic (v1.4.3)

### Merge

- Release-1.4.3 → main (v1.4.3)

## [1.4.2] - 2026-04-30

### Bug Fixes

- *(sharded,p0)* ApplyDeployment server-default ping-pong (v1.4.2)

### Merge

- Release-1.4.2 → main (v1.4.2)
- Release-1.4 → main (v1.4.1)

## [1.4.1] - 2026-04-30

### Bug Fixes

- *(sharded,p1)* HPA informer cache + reconcile ordering + status truth source (v1.4.1)

### Documentation

- *(readme)* Add Artifact Hub badge

## [1.4.0-rc.1] - 2026-04-30

### Documentation

- *(readme)* Add Artifact Hub badge

### Refactor

- *(resources)* Embed bash scripts as text/template (-160 LoC)
- *(controller)* Consolidate updateStatusError via applyErrorCondition (-32 LoC)
- *(controller)* Consolidate handleDeletion via finalizer helper (-32 LoC)
- *(controller)* Consolidate reconcileKeyfileSecret via helper (-32 LoC)
- *(controller)* Introduce helpers.go (Statusable + 3 generic helpers)
- *(mongodb)* Drop unused retry/wait helpers (-80 prod, -375 test)
- *(resources)* Replace local ptr helpers with k8s.io/utils/ptr.To

## [1.3.2-beta.6] - 2026-04-30

### Bug Fixes

- *(helm,docs)* Gh-pages publish 자동화 + 옛 owner URL 잔존 5건 정정

### Features

- *(release)* Make release VERSION=... 단일명령 자동화 (v1.3.2-beta.6)

## [1.3.2-beta.5] - 2026-04-30

### Features

- *(observability,quality)* EventRecorder + gosec G115 fix + Makefile setup (v1.3.2-beta.5)

## [1.3.2-beta.4] - 2026-04-30

### Bug Fixes

- *(security,p0)* Carve-out autoscaling flag 미주입 + 옛 owner 참조 + L3 게이트 (v1.3.2-beta.4)

## [1.3.2-beta.3] - 2026-04-30

### Features

- *(carve-out,docs)* Code-level feature gate + 베타 경고 + otel v1.43 (v1.3.2-beta.3)

## [1.3.2-beta.2] - 2026-04-30

### Bug Fixes

- *(security)* Stdlib CVE 8건 + otel exporter mismatch + dependabot owner (v1.3.2-beta.2)

## [1.3.2-beta.1] - 2026-04-30

### Bug Fixes

- *(security,carve-out)* CVE 2건 fix + ReplicaSet 한정 베타 carve-out (v1.3.2-beta.1)

### Chores

- *(ci)* Remove GitHub Actions workflows per RFC 0002

## [1.3.1] - 2026-04-29

### Bug Fixes

- *(release,helm-publish)* Owner eightynine01 → keiailab — 배포 경로 복구 (v1.3.1)

## [1.3.0] - 2026-04-29

### Features

- *(operator,sharded)* Auto-scaling 통합 사이클 — RS/cfg HPA 이중 가드 + drift 방지 + deliberate scale + status 노출 (v1.3.0)

## [1.2.0] - 2026-04-29

### Features

- *(operator,sharded)* Mongos HorizontalPodAutoscaler reconcile (v1.2.0)

## [1.1.2] - 2026-04-29

### Bug Fixes

- *(operator,sharded)* PostStart hook 안에서 RS init + createUser 통합 (v1.1.2)

## [1.1.1] - 2026-04-29

### Bug Fixes

- *(operator)* Bootstrap-resilience — IsInitialized hello + anon hasPrimary auth-fallback (v1.1.1)
- *(operator,sharded)* Chart 권한 분류 + ScaleIn backoff + verifyAdminUser retry (P0-6)
- *(operator)* Leader-election 기본값 false → true (P0-2)
- *(controller)* ApplyService에서 LoadBalancer/ExternalIP/Traffic 필드 누락 보강 (P0-1)
- *(deploy)* Kustomization newTag dev-5cdeb71-final → latest (Track E1 후속)
- *(controller)* Silent skip 제거 + condition 누적 차단 + isClusterReady nil 안전성 (Track B2-B3)
- *(sharded)* Cfg/shard StatefulSet에 admin user 부트스트랩 추가
- *(controller)* Silent error 패턴 일괄 정정
- *(examples,status,build)* Stale 필드 제거 + Phase 상수화 + buildx amd64
- *(mongodb-driver)* SetAuth 호출 패턴 정비 + AuthDB 기본값 보강
- *(config)* RBAC roleRef 이름 정정 + kustomization 이미지 매핑 보강
- *(ci)* Helm 패키지 검증 경로를 차트명 디렉토리 기준으로 정정
- *(ci)* Helm yamllint document marker + GHCR namespace 정정
- *(controller)* Status update를 RetryOnConflict로 감싸 conflict 무한 루프 차단 (C5)
- *(controller)* Shard init/add 사일런트 실패 차단 (C4)
- *(security)* Mongosh password를 stdin으로 전달해 audit log 노출 차단 (C3)
- *(security)* Mongosh JS 인젝션 차단 (C1·C2)
- *(release)* Chart 자동 버전 업데이트와 helm index merge base 수정
- 운영자 배포 매니페스트를 GHCR로 통일

### Chores

- *(deploy,build)* Kustomization dev 태그 정정 + buildx 기본 빌더 통일 (Track E1)
- Stale 루트 index.yaml 삭제

### Documentation

- *(readme)* Tested Features 정직성 회복 — Stable/Implemented/Beta 3단계 (P0-5)
- *(adr)* Production-readiness 사이클 결정 3건 (Track E4)
- *(readme,roadmap)* Production-readiness 사이클 결과 반영 (Track E2)
- *(roadmap)* Phase 4 — Bitnami Helm chart 동등성 갭 9건 추가
- *(comparison)* Bitnami mongodb-sharded 9.4.12 동등성 분석 추가
- *(changelog)* Mongo-go-driver phase 완료 기록 (Commit 7/7)
- *(security)* Pods/exec RBAC 알려진 한계 명시 (C6)

### Features

- *(sharded)* Sharded scale-in 자동화 + ShardDraining condition (Track C3)
- *(workload-sharded)* Sharded PDB + NetworkPolicy 자동화 (Track C2 부분)
- *(workload)* MongoDB ReplicaSet NetworkPolicy 자동화 (Track C4)
- *(workload)* MongoDB ReplicaSet PodDisruptionBudget 자동화 (Track C1)
- *(controller,driver)* 부트스트랩 race 봉쇄 + typed auth error + post-verify (Track A)
- *(security)* Executor 제거 + pods/exec RBAC 권한 완전 삭제 (Commit 6/7)
- *(mongodb)* ShardManager driver 전환 (Commit 5/7)
- *(mongodb)* AuthManager driver 전환 + mongos pod self-bootstrap (Commit 4/7)
- *(mongodb)* ReplicaSetManager을 mongo-go-driver로 전환 (Commit 3/7)
- *(resources)* Mongodb pod self-bootstrap admin user (Commit 2/7)
- *(mongodb)* Mongo-go-driver v2 의존성 + connection helper 추가

### Refactor

- *(controller)* Controllerutil.CreateOrUpdate 마이그레이션 (Track B1)

### Testing

- *(resources)* Sharded PDB/NetworkPolicy + MongoDB PDB/NP 단위 테스트 보강 (P0-4)
- *(rbac)* 본 사이클 신규 권한 회귀 가드 추가 (P0-3)
- *(controller)* Bootstrap lease 회귀 가드 4건 (Track D2)
- *(controller)* Envtest 활성화 + reconcile 회귀 봉쇄망 가동 (D1)
- *(mongodb)* Driver layer 단위 테스트 보강 (9.6% → 55.1%)

### Style

- Gofmt 자동 정렬 (주석 공백 정정)

## [1.0.1] - 2026-01-22

### Bug Fixes

- Correct YAML indentation in release.yml permissions
- Correct multi-arch image build in release workflow
- Resolve multi-arch Docker build issues
- Correct Docker metadata action SHA tag prefix

### Documentation

- Add comprehensive guides and roadmap for v1.0.0+

### Features

- Migrate from Docker Hub to GitHub Container Registry (GHCR) (**BREAKING**)

## [1.0.0] - 2026-01-22

### Bug Fixes

- Add pods/exec permission to ClusterRole
- Update image repository to eightynine01/mongodb-operator

### Chores

- Prepare for v1.0.0 GA release
- Save final state (enable serviceMonitor, update CRDs)
- Bump builder image to golang:1.25
- Release 0.0.7 with security updates and RBAC fix

### Documentation

- Update changelog and chart metadata for 0.0.7 release

### Features

- Add deployment manifests for sharded mongodb and production overlays
- Update helm repo index for 0.0.7 release

## [0.0.5] - 2025-12-31

### Bug Fixes

- *(backup)* Add authentication support and backup all databases

### Chores

- Release v0.0.5
- Ignore gh-pages worktree
- Bump helm chart version to 0.0.4

### Documentation

- Update helm repo instructions

## [0.0.3] - 2025-12-31

### Bug Fixes

- Preserve shard status arrays during scale out

### Chores

- Release v0.0.3
- Release v0.0.2 with Verified Publisher configuration

### Documentation

- Add resource recommendations, tested features, and limitations
- Add auto-initialization and scaling documentation

### Features

- Add MongoDB initialization and authentication support
- Initial release of MongoDB Operator v0.0.1

<!-- generated by git-cliff -->
