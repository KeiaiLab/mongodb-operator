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
deploy/                          GitOps overlay (kustomize)
docs/kb/adr/                     Architecture Decision Records
PROJECT                          Kubebuilder metadata (DO NOT EDIT)
Makefile                         Build / test / deploy / docker / helm-publish
```

## Critical Rules (절대 위반 금지)

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
- `CHANGELOG.md` 에 신규 버전 섹션 추가
- 별도 commit 으로 `chore(release): vX.Y.Z` 형태

### 배포
- `make docker-push IMG=ghcr.io/keiailab/mongodb-operator:X.Y.Z` (linux/amd64 단일, 글로벌 §2)
- `make helm-publish` (gh-pages branch 갱신)
- `argos-platform-data/mongodb/Chart.yaml` 의 dependency version 갱신 → ArgoCD auto-sync

## E2E / Integration

- envtest 는 `make test` 에 통합 — `internal/controller/*_test.go` 의 EnvTest 사용
- 수동 smoke test: `scripts/release-smoke-test.sh` (image / sbom / trivy / chart index / smoke 6 단)
- argos 클러스터 실측 — `kubectl get mongodbs,mongodbshardeds -n data` + `kubectl logs -l app.kubernetes.io/name=mongodb-operator -n data`

## 작업 순서 (글로벌 §workflow.md 준수)

1. 사용자 시나리오 명세 → TASKS.md 갱신
2. 실패 재현 테스트 작성 (TDD)
3. 구현 → `make lint test` PASS
4. 회귀 검증 (변경 영역 인접 모듈)
5. 커밋 (Conventional Commits + Co-Authored-By trailer)
6. push → ArgoCD sync 또는 수동 helm install 검증
7. CHANGELOG 갱신 (사용자 가시 변경)
8. ADR 작성 (아키텍처 결정 / 글로벌 standards 일탈)

## 회피해야 할 함정

- **Multi-architecture 빌드** — 저장소 종류에 따라 분기:
  - public (GitHub, 본 저장소): `linux/amd64,linux/arm64` 멀티아키 — 외부 사용자 환경 다양성 수용.
  - 사내 (GitLab): `linux/amd64` 단일 — 사내 표준 (글로벌 §2).
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
$EDITOR docs/operations/cluster-audit.md

# 3. 격차 해소 procedure (7 phase)
$EDITOR docs/operations/production-grade-sprint.md
```

진입점 단축: [docs/operations/README.md](docs/operations/README.md) — 운영자
single entry point. 사용자 의도별 *4 시나리오 표* 제공.

cluster-ops mode 의 ADR (cluster-side governance):
- ADR-0015 webhook failurePolicy=Fail
- ADR-0016 cross-cut audit pattern (+ Errata: docs accuracy)
- ADR-0017 CRD default vs webhook invariant (Type A/A'/B/C)
- ADR-0018 MonitoringSpec orphan 단계적 해소
- ADR-0019 operator-commons v0.5.0 helper 승격 (Proposed)

## Refs

- 글로벌 규약: `~/.claude/CLAUDE.md` + `standards/*.md`
- 본 프로젝트 거버넌스: [GOVERNANCE.md](GOVERNANCE.md), [MAINTAINERS.md](MAINTAINERS.md)
- 운영 사고 분석: [HANDOFF.md](HANDOFF.md) (2026-05-07)
- 기능 우선순위: [ROADMAP.md](ROADMAP.md), [TASKS.md](TASKS.md)
- 운영자 진입점: [docs/operations/README.md](docs/operations/README.md)
