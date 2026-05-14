# ADR-0027: community-operators sync 자동화 (RFC 0002 예외 ③ 확장)

- Date: 2026-05-14
- Status: Accepted
- Authors: @eightynine01

## Context

mongodb-operator 의 OLM bundle 을 k8s-operatorhub/community-operators upstream 에 sync 하는 작업이 현재 *수동* — 사용자 시간 비용 + bundle drift 위험.

라이브 evidence (2026-05-14):
- community-operators 의 mongodb-operator 등록 버전: 0.3.0
- mongodb-operator latest release tag: v1.5.0
- **drift: 약 8개 minor version + 1 major** (0.3.0 → 1.5.0)

ADR-0023 'operatorhub-bundle-scaffold' 가 정립한 bundle infrastructure 는 완비됐으나 *upstream sync 자동화* 미구현. 본 ADR 가 그 격차 해소.

## Decision

`.github/workflows/release.yml` 의 `github-release` job 후속에 `sync-community-operators` job 신설:

1. release tag push trigger (기존 release.yml 의 동일 trigger)
2. 본 repo 의 tag 시점 `bundle/` 디렉토리 + `bundle.Dockerfile` 을 fork (`eightynine01/community-operators`) 의 `operators/mongodb-operator/<version>/` 으로 복사
3. fork branch 에 commit + push
4. upstream `k8s-operatorhub/community-operators` 에 PR 생성 (`gh pr create`)
5. **AI 자동 머지 0** — 외부 maintainer 가 review + 머지 책임

조건:
- prerelease tag (alpha/beta/rc) 는 skip (`if: !contains(...)`)
- secret `COMMUNITY_OPERATORS_PAT` 의무 (사용자 영역 등록)

## Consequences

긍정:
- bundle drift 영구 차단 (release atomic sync)
- 사용자 manual PR 부담 0
- sister operator (postgres ADR-0014, valkey ADR-0047) 와 일관 패턴

부정 / 트레이드오프:
- **RFC 0002 §2 GitHub Actions 영구 금지 와 충돌 영역**
  - RFC 0002 §7 예외 3종 ③ "release tag → GitHub Release 본문 자동 생성 (1-step)" 의 *확장 해석*
  - 본 sync 도 release tag trigger + 1-step outbound publish (upstream PR open 만, merge 안 함)
  - billing SPOF 영향: workflow fail 시 release 자체 무관 진행, community-operators sync 만 수동 fallback (acceptable)
- 외부 maintainer review SLA 의존 (1-7일 typical)
- COMMUNITY_OPERATORS_PAT secret 회전 의무 (90일 권장)

## Alternatives Considered

- A (현 수동): 사용자 manual PR — 시간 비용 + drift 위험 (현 0.3.0 stale 라이브 evidence)
- **B (본 결정)**: release.yml step 추가 — RFC 0002 예외 ③ 확장
- C: 별 CI 시스템 (KeiaiLab cluster 내 Tekton/Argo) — cluster 의존 + 외부 build 시 차단 위험
- D: GH Actions 별 workflow file (release-community-operators.yml) — 분리 정합이지만 release.yml 내 통합이 더 atomic

## Refs

- RFC 0002 (no-github-actions, 2026-04-29)
- ADR-0023 (operatorhub-bundle-scaffold, 2026-05-10) — bundle infrastructure 정립
- 라이브 evidence: community-operators#8121 valkey 수동 sync 사례
- 라이브 evidence: community-operators 의 mongodb-operator 0.3.0 stale
- sister: postgres ADR-0014, valkey ADR-0047
