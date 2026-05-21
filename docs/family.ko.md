<p align="center">
  <img src="https://github.com/keiailab.png" alt="keiailab" width="120"/>
</p>

<p align="center">
  <a href="family.md">English</a> |
  <b>한국어</b> |
  <a href="family.ja.md">日本語</a> |
  <a href="family.zh.md">中文</a>
</p>

# keiailab operator family (한국어)

> English: [family.md](family.md) — canonical / 정본

> 공통 기반 위에 구축된 4 개의 자매 Kubernetes operator — `operator-commons` (Go 라이브러리) + Helm partial + Apache-2.0 스택.

본 페이지는 **`mongodb-operator`** 저장소에서 보고 계십니다. 본 문서는 family 전체의 정본 cross-link 입니다.

## Family 개요

| 프로젝트 | 데이터베이스 | 상태 | 저장소 |
|---|---|---|---|
| **`postgres-operator`** | PostgreSQL 18+ | active | https://github.com/keiailab/postgres-operator |
| **`mongodb-operator`** | MongoDB 7.0+ | active | https://github.com/keiailab/mongodb-operator |
| **`valkey-operator`** | Valkey 8.0+ (Redis fork, BSD-3) | active | https://github.com/keiailab/valkey-operator |
| **`operator-commons`** | 공용 Go 라이브러리 | v0.7.0 | https://github.com/keiailab/operator-commons |

## 공유 항목

4 개 프로젝트 모두 동일한 운영 primitive 위에서 수렴합니다:

- **Apache-2.0** 엔드-투-엔드 — SSPL 없음, SaaS surface 에 copyleft 없음
- **`operator-commons`** 공용 Go 라이브러리 (v0.7.0+) — finalizer, label, status sugar, security context builder, NetworkPolicy / ServiceMonitor partial
- **Helm chart 골격** — RFC-0027 `default` falsy-toggle 방지, RFC-0026 component-keyed values, cycle 26 hardening 6 marker (priorityClassName / lifecycle / SA / minReadySeconds / automount / revisionHistoryLimit)
- **OLM bundle parity** — scorecard v1alpha3 의 6-test matrix
- **i18n** — README + 11 개의 정본 문서를 English / 한국어 / 日本語 / 中文 으로 제공 (cleanup supercycle 2026-05-21 의 Wave 4)

## 하지 않는 것

- ❌ **상용 operator 의 embed 또는 wrapping** (PGO, CloudNativePG, MongoDB Community Operator, Sentinel) — license-clean, copyleft 의무 없음
- ❌ **release gate 용 GitHub Actions** — 로컬 4 계층 + GitLab CI L5 (RFC-0002, RFC-0043 참조)
- ❌ **시간 기반 로드맵 데드라인** — feature checklist + 완료율 (% ) (`standards/roadmap.md §1.1` 참조)
- ❌ **Bitnami chart / image** — 레지스트리 deprecation 위험, Broadcom 인수 (ADR-0136 / ADR-0057 참조)

## 시작하기

| 작업 | 진입점 |
|---|---|
| Kubernetes 에 `mongodb-operator` 배포 | [README.md](../README.md) Quickstart 섹션 |
| 아키텍처 학습 | [ARCHITECTURE.md](../ARCHITECTURE.md) |
| 이슈 또는 feature request 등록 | https://github.com/keiailab/mongodb-operator/issues |
| 디자인 또는 로드맵 논의 | https://github.com/keiailab/mongodb-operator/discussions |
| 코드 기여 | [CONTRIBUTING.md](../CONTRIBUTING.md) |
| 보안 이슈 제보 | [SECURITY.md](../SECURITY.md) |
| 브랜드 / 보이스 학습 | [BRANDING.md](../BRANDING.md) |
| 채택자 / 사용 현황 추적 | [ADOPTERS.md](../ADOPTERS.md) |
| 메인테이너 확인 | [MAINTAINERS.md](../MAINTAINERS.md) |
| 거버넌스 모델 검토 | [GOVERNANCE.md](../GOVERNANCE.md) |
| 향후 작업 확인 | [ROADMAP.md](../ROADMAP.md) |

## Family 간 호환성 (operator-commons)

3 개의 데이터베이스 operator 는 모두 동일 버전의 `github.com/keiailab/operator-commons` 를 import 합니다 (현재 `v0.7.0+`):

```go
import (
    "github.com/keiailab/operator-commons/pkg/version"
    "github.com/keiailab/operator-commons/pkg/security"
    "github.com/keiailab/operator-commons/pkg/labels"
    "github.com/keiailab/operator-commons/pkg/monitoring"
    "github.com/keiailab/operator-commons/pkg/finalizer"
    "github.com/keiailab/operator-commons/pkg/status"
)
```

`operator-commons` 의 breaking change 는 3 개 데이터베이스 operator 의 동기화된 bump 가 필요합니다 — supercycle Wave 5 의 `make cross-validation` 타깃으로 검증됩니다.

## i18n

정본 프로젝트 문서 (README, CONTRIBUTING, SECURITY, GOVERNANCE, MAINTAINERS, ROADMAP, SUPPORT, BRANDING) 는 4 개 언어로 제공됩니다 — 각 파일 상단의 언어 스위처를 참고하세요. 본 family 개요는 English 전용이며, 모국어 진입점은 각 저장소의 현지화된 README 를 참조하세요.

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="../LICENSE">Apache-2.0</a> · <a href="https://keiailab.com">keiailab.com</a>
</p>
