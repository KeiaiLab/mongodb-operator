<p align="center">
  <b>English</b> |
  <a href="AGENTS.ko.md">한국어</a> |
  <a href="AGENTS.ja.md">日本語</a> |
  <a href="AGENTS.zh.md">中文</a>
</p>

# mongodb-operator — AI Agent Guide

본 문서는 AI agent (Claude Code, Cursor, Continue 등) 가 본 repo 에서 안전하고 효과적으로 작업하기 위한 *프로젝트별* 가이드입니다. 글로벌 규약 (`~/.claude/CLAUDE.md` + `standards/*`) 의 *추가 부속*이며, 충돌 시 글로벌 규약 우선.

## Project Structure

```
cmd/main.go                      Manager entry (registers controllers/webhooks)
api/v1alpha1/*_types.go          CRD schemas (MongoDB / MongoDBSharded / MongoDBBackup)
api/v1alpha1/zz_generated.*      Auto-generated (DO NOT EDIT)
internal/controller/*            Reconciliation logic (RS / Sharded / Backup)
internal/resources/builder.go    StatefulSet / Service / ConfigMap builders (PodSecurity helper 포함)
internal/assets/scripts/         Embedded shell scripts (bootstrap-admin / health-probe)
config/crd/bases/*               Generated CRDs (DO NOT EDIT)
config/rbac/role.yaml            Generated RBAC (DO NOT EDIT)
config/samples/*                 Example CRs (edit these)
charts/mongodb-operator/         Helm chart (publish 대상)
examples/gitops/                  GitOps overlay example (kustomize, experimental)
docs/kb/adr/                     Architecture Decision Records
PROJECT                          Kubebuilder metadata (DO NOT EDIT)
Makefile                         Build / test / deploy / docker / helm-publish
```

## Critical Rules (절대 위반 금지)

### Autonomy Constitution (글로벌 §2.0, 2026-05-15)
*사용자 질의 최소화*. AskUserQuestion + 확인 발화는 3 조건만:
1. 외부 권한 부여 (예: cluster admin, GHCR org owner)
2. *돌이킬 수 없는 운영 작업* — `kubectl delete ns/csv/clusterextension`, helm release uninstall, ArgoCD App spec.source 변경, force push main, 운영 DB drop, 시크릿 회전

본 operator 작업의 적용 분기:
- **자동 진행 (사용자 redirect 권한 보유)**: code change + ADR + manifest + 검증 명령 + PR open

### Never Edit These (Auto-Generated)
- `config/crd/bases/*.yaml` — `make manifests` 산출
- `config/rbac/role.yaml` — `make manifests` 산출
- `**/zz_generated.*.go` — `make generate` 산출
- `PROJECT` — `kubebuilder` CLI 산출

### Never Remove Scaffold Markers
`// +kubebuilder:scaffold:*` 마커 삭제 금지. CLI 가 본 마커 위치에 코드 주입.

### PodSecurity restricted compliance
모든 StatefulSet / Deployment / Pod 의 컨테이너 + init container 는 `buildDefaultContainerSecurityContext()` 또는 `buildKeyfileInitContainerSecurityContext()` (`internal/resources/builder.go`) 를 사용. 인라인 SecurityContext 신규 작성 금지. `TestPodSecurityRestrictedCompliance` 회귀 가드가 자동 검증 (INC-2026-05-07).

### bootstrap-admin script 멱등성
`internal/assets/scripts/bootstrap-admin.sh.tpl` 의 `RS_OK != "init"` early-exit 가드 변경 금지. `db.createUser` 가드 *바깥* 호출 시 ordinal-0 SECONDARY 재기동에서 영구 CrashLoopBackOff (v1.4.0~v1.4.4 사고 — CHANGELOG [1.4.5] 참조).

### Webhook invariant 추가 시 의무 audit (it46-47)
- **ADR-0015**: `failurePolicy=Fail` 채택 — 가용성 vs validation 가치 trade-off.
- **ADR-0016**: *Cross-cut audit pattern* — 새 invariant 추가 시 mongodb / valkey / postgres 3 operator 동시 점검 의무. PR 본문에 audit 표 포함.
- **ADR-0017**: *CRD default vs webhook invariant* — `+kubebuilder:default=N` (N≠0) 가 있는 field 의 zero-value 거부 invariant 는 admission unreachable. type A/B/C 분류 후 작성.
- 작성 위치: `internal/webhook/v1alpha1/`. helper 는 `validateXxx` 패턴 (mongodb_webhook.go 의 validateStorageSize / validateAuthSecretRef 참조).
- **검증 dual-layer 의무**: unit (`mongodb_validation_test.go`) + envtest round-trip (`admission_roundtrip_test.go`). unit-only 통과는 false positive 가능 (ADR-0017).

## After Making Changes

### Always run
```bash
make generate manifests        # CRD/RBAC/deepcopy 재생성
make lint                      # ruff/biome/clippy 미적용 (Go) — gofmt + go vet + golangci-lint
make test                      # go test ./internal/... + envtest
```

### Helm chart 변경 시
- `charts/mongodb-operator/Chart.yaml` 의 `version` + `appVersion` 동기 bump
- `docs/changelog.md` 에 신규 버전 섹션 추가
- 별도 commit 으로 `chore(release): vX.Y.Z` 형태

### 배포 — 3 deployment models (ADR-0028~0030)

**operator container image** (모든 model 공통):
- `make docker-push IMG=ghcr.io/keiailab/mongodb-operator:X.Y.Z` (linux/amd64 단일, 글로벌 §2)

**Path 1 — OLM v1 (recommended, current modern standard)**:
- `make bundle VERSION=X.Y.Z && make bundle-build VERSION=X.Y.Z && make bundle-push VERSION=X.Y.Z`
- `make catalog-build VERSION=X.Y.Z && make catalog-push VERSION=X.Y.Z`
- ClusterCatalog + ClusterExtension manifests: `deploy/olm-v1/`
- 외부 사용자: `kubectl apply -k deploy/olm-v1/` (cluster-admin) 또는 `clusterextension-narrow-rbac.yaml` (production)
- ADR 분기: ADR-0029 (채택) + ADR-0030 (narrow RBAC + NetworkPolicy)

**Path 2 — Helm chart**:
- `make helm-publish` (gh-pages branch 갱신)
- `keiailab/platform/data` (또는 sister) 의 helm chart dependency version 갱신 → ArgoCD auto-sync
- 본 cluster 의 *현재 활성 path* — 영구 cutover 전까지 helm v1.4.20 라이브 (data ns).

<!-- Path 3 (OLM v0 legacy) 영구 폐기 — ADR-0028 Phase D. -->
<!-- 외부 사용자는 Path 1 (OLM v1) 또는 Path 2 (Helm) 선택. community-operators sync 자동화 폐기. -->

상세: [docs/install.md](docs/install.md) 2-path matrix (OLM v1 / Helm).

## E2E / Integration

- envtest 는 `make test` 에 통합 — `internal/controller/*_test.go` 의 EnvTest 사용
- 수동 smoke test: `scripts/release-smoke-test.sh` (image / sbom / trivy / chart index / smoke 6 단)
- argos 클러스터 실측 — `kubectl get mongodbs,mongodbshardeds -n data` + `kubectl logs -l app.kubernetes.io/name=mongodb-operator -n data`

## 작업 순서 (글로벌 §workflow.md 준수)

### 세션 시작 의식 — worktree base stale 검사 (의무, 2026-05-17)

본 repo 는 multi-worktree 운영 (`.claude/worktrees/commons-bump-v0.7`, `fix-tls-pem-securitycontext`, `agents-base-stale` 등). 세션 시작 시 *반드시* 다음 verify:

```bash
# 1. 본 worktree branch 가 origin/main 보다 N commit behind 인지
git fetch origin main
git rev-list --count HEAD..origin/main
# N >= 5 → rebase 의무 (작업 진입 전): git rebase origin/main
# N == 0 → safe, 진입

# 2. 다른 active worktree race 검사
git worktree list --porcelain | grep -c "^worktree "
# 2+ 면 동일 topic 사용자 active branch 존재 여부 grep → match 시 진본 양보
```

**라이브 evidence (트리거 사고, 2026-05-17)**: OLM v1 only 전환 cycle (PR #173, ADR-0028 Phase D, squash merge `a39c7f2`) 에서 본 검사 누락 → 본 worktree 가 merge-base `6478d9a` 위에서 9 commit 만드는 동안 main 에 7 insights commit 머지 → 9h silent drift → `mergeable_state=dirty` → Phase E squash + rebase --onto origin/main + force-push-with-lease + ROADMAP 4 entry 보존 resolve 로 해소. `standards/workflow.md §2 시작 의식 §6` + `principles.md §1.1` multi-worktree namespace 가정 명시 의 repo-local echo.

### 표준 작업 흐름

1. 사용자 시나리오 명세 → TASKS.md 갱신
2. 실패 재현 테스트 작성 (TDD)
3. 구현 → `make lint test` PASS
4. 회귀 검증 (변경 영역 인접 모듈)
5. 커밋 (Conventional Commits + Co-Authored-By trailer)
6. push → ArgoCD sync 또는 수동 helm install 검증
7. CHANGELOG 갱신 (사용자 가시 변경)
8. ADR 작성 (아키텍처 결정 / 글로벌 standards 일탈)

## 회피해야 할 함정

- **Multi-architecture 빌드** — 글로벌 §2 + RFC-0002 §2 정합: **`linux/amd64` 단일 아키 강제**.
  `.lefthook.yml` 의 `platforms-amd64-guard` 가 `Makefile` 의 multi-arch 재발을 grep 으로 차단.
  Apple Silicon / Graviton 사용자는 emulation (kind, podman 의 `--platform linux/amd64`) 로 운영.
- **GitHub Actions** — 저장소 종류에 따라 분기:
  - public (GitHub, 본 저장소): CI/release/security-scan workflow 사용 가능.
  - 사내 (GitLab): RFC 0002 영구 금지. 모든 게이트 lefthook 로컬 4 계층 (pre-commit + pre-push) — 외부 의존 회피.
- **Mongo built-in 인증 우회** — keyfile + admin user 둘 다 필수. AdminCredentialsSecretRef 비워두면 bootstrap 자동 skip 되지만 production 에선 필수.
- **인라인 SecurityContext** — 위 PodSecurity helper 사용 강제. 회귀 가드가 catch.

## Cluster Ops Mode (운영자 / 인계 시)

argos data plane 의 *상태 audit + 격차 추적 + sprint plan* 진입.

```bash
# 1. 자동 KPI 측정 (5 영역, 30초 이내)
./scripts/audit-cluster-state.sh

# 2. 격차 + clean 영역 표 + KPI 정의
$EDITOR docs/troubleshooting.md

# 3. 격차 해소 procedure (7 phase)
$EDITOR docs/roadmap.md
```

진입점 단축: [docs/README.md](docs/README.md) — 운영자
single entry point. 사용자 의도별 *4 시나리오 표* 제공.

cluster-ops mode 의 ADR (cluster-side governance):
- ADR-0015 webhook failurePolicy=Fail
- ADR-0016 cross-cut audit pattern (+ Errata: docs accuracy)
- ADR-0017 CRD default vs webhook invariant (Type A/A'/B/C)
- ADR-0018 MonitoringSpec orphan 단계적 해소
- ADR-0019 operator-commons v0.5.0 helper 승격 (Proposed)

### Deployment governance (ADR-0028 ~ ADR-0030 chain, 2026-05-14~15)

- **ADR-0028**: 외부 사용자 운영 수준 OLM 번들 (5 결격 동시 해소 — containerImage/alm-examples/replaces+skipRange/channels stable+alpha/maturity stable)
- **ADR-0029**: OLM v1 채택 (operator-controller v1.8 + catalogd) — *현대 표준*, ClusterCatalog + ClusterExtension 단 2 자원. v0.30 legacy 와 분리 path
- **ADR-0030**: narrow installer RBAC + olmv1-system NetworkPolicy — bundle CSV derive + operator-controller `derive-service-account` 표준 정합

cluster-ops 진입 시 *deployment model* 확인:
```bash
kubectl get clustercatalog 2>/dev/null     # OLM v1 → mongodb-operator 있나
kubectl get csv -A 2>/dev/null              # OLM v0 → mongodb-operator.vX.Y.Z 있나
kubectl get deployment -A -l app.kubernetes.io/name=mongodb-operator  # helm 또는 OLM 의 manager pod
```

본 cluster 의 *현재 deployment matrix* — `deploy/olm-v1/README.md` §2 라이브 검증 (2026-05-15).

## Refs

- 글로벌 규약: `~/.claude/CLAUDE.md` + `standards/*.md`
- 본 프로젝트 거버넌스: [Governance](docs/governance.md), [Maintainers](docs/maintainers.md)
- 운영 사고 분석: [HANDOFF.md](HANDOFF.md) (2026-05-07)
- 기능 우선순위: [Roadmap](docs/roadmap.md), TASKS.md
- 운영자 진입점: [docs/README.md](docs/README.md)

