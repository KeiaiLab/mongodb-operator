# HANDOFF — mongodb-operator

> 본 문서는 *다음 세션이 컨버세이션 컨텍스트 없이 재개* 가능하도록 작성된다.
> SSOT 는 본 파일 (컨텍스트·결정) + 마지막 commit log (사실).
> 글로벌 `standards/token-budget.md §5` + `standards/workflow.md §2`.

## 현재 상태 (2026-05-07, governance 표준 정합 — P0+P1 baseline 도달)

- **HEAD `0ab77c3`**: `docs(governance): ADR-0011 (pre-commit 분기 정당화) + deps log seed (P0+P1 baseline)`
- **HEAD~1 `6102298`**: `chore(release): smoke-test SBOM+trivy 검증 강화 + step 번호 정합`
- **HEAD~2 `261c61d`**: `fix(deploy): namespace prod/db → data 통합 + storageClass ceph-rbd 정합 (#114)` (data namespace 통합 — 3-repo 일치)

## 본 세션 (상용 제품 수준 trajectory) 작업

### 1. release pipeline 강화 (3-repo 정합)
- SBOM (SPDX `.spdx.json`) asset 검증 step 추가 — supply chain 표준.
- trivy image post-publish HIGH/CRITICAL fixed-only scan 추가 — 운영 후 CVE 모니터링.
- smoke-test step 번호 [N/5] → [N/6] 정합 (재번호 누락 버그 수정).
- 검증: `./scripts/release-smoke-test.sh` → **12 PASS / 0 FAIL** (v1.4.5).

### 2. governance 표준 정합 (P0+P1 baseline)
- ADR-0011 신규: Hook 도구로 pre-commit 채택 (글로벌 lefthook 표준 분기 정당화).
- INDEX.md 갱신 (ADR-0011 추가).
- `docs/kb/deps/2026-05.md` seed (enforcement.md §2.4) — direct 10건 + 총 107 baseline.

## 3-repo governance 정합 매트릭스 (2026-05-07 기준)

| 항목 | mongodb | postgres | valkey | 표준 |
|---|---|---|---|---|
| ADR 경로 | docs/kb/adr ✓ | docs/kb/adr ✓ | docs/kb/adr ✓ | enforcement §2.1 |
| ADR INDEX | ✓ | ✓ | ✓ | adr.md §1 |
| deps log | ✓ | ✓ | ✓ | enforcement §2.4 |
| hook 도구 | pre-commit (ADR-0011) | pre-commit (ADR-0007) | lefthook (정합) | enforcement §1.1 |
| GH workflows | 0 ✓ | 0 ✓ | 0 ✓ | RFC 0002 |
| Makefile L3 게이트 | ✓ | ✓ | ✓ | ci.md v1 |
| CHANGELOG + cliff | ✓ | ✓ | ✓ | enforcement §2.3 |
| smoke-test 12 step | ✓ | ✓ | ✓ | 3-repo 정합 |
| make lint PASS | ✓ (staticcheck) | ✓ (golangci-lint) | ✓ (golangci-lint) | linting.md |

## Quality baseline (2026-05-07 실측)

`enforcement.md §3.4 (Coverage 합산)` 의 P2 측정 — 본 세션 baseline.

```
$ make test    # exit 0 / FAIL: 0
internal/controller    coverage: 31.9% of statements
internal/mongodb       coverage: 41.3% of statements
internal/resources     coverage: 72.7% of statements
internal/webhook       (no test 패키지)
```

**80% 목표 (enforcement §3.4)** 대비:
- ✓ resources 72.7% (근접)
- ✗ controller 31.9% / mongodb 41.3% — envtest 기반 reconcile 시나리오 추가 권장

`enforcement` 의 "절대치보다 *변경된 코드의 커버 여부*가 우선" 원칙 적용 — 본 baseline 은 회귀 비교 기준점.

## 알려진 잔여 gap (별 트랙)

1. **golangci-lint 미설치**: 본 repo 의 `make lint` 는 `go vet + staticcheck` 만 실행 (golangci-lint 가 없으면 silent skip). postgres + valkey 패턴 (golangci-lint v2.x) 으로 정합 권장.
2. **dependabot.yml + renovate.json 중복**: 본 repo 는 둘 다 보유. postgres + valkey 는 renovate.json 만. ADR 로 도구 선택 정당화 또는 dependabot.yml 제거 필요.

## 글로벌 참조

- 글로벌 표준: `~/Documents/ai-dev/standards/{principles,workflow,checklist,linting,testing,ci,adr,enforcement}.md`
- 본 repo ADR INDEX: `docs/kb/adr/INDEX.md`
- 본 repo deps 추적: `docs/kb/deps/`
- 사전 검증 SDK: `sonatype-guide:sonatype-guide` skill — 의존성 추가/업그레이드 전 의무 호출.

## 다음 단계 (열린 트랙)

1. golangci-lint 도입 (3-repo 정합).
2. dependabot vs renovate 정합 ADR.
3. RFC-0004 클러스터 라이브 게이트 발동 시 (운영 주장 추가) 본 HANDOFF 의 `<!-- live-verified: YYYY-MM-DD -->` 마커 + kubectl/argocd 인용 추가.
