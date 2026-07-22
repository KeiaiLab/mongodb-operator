<p align="center">
  <b>English</b> |
  <a href="i18n/ko/branding.md">한국어</a> |
  <a href="i18n/ja/branding.md">日本語</a> |
  <a href="i18n/zh/branding.md">中文</a>
</p>

# Branding Guide — `mongodb-operator`

> Visual identity, voice, and tone for the keiailab project.

This document is the canonical reference for `mongodb-operator` branding decisions. It applies to the README, release notes, marketing material, and any third-party communication that represents the project.

## 1. Identity

**Organization**: [keiailab](https://github.com/keiailab) — Kubernetes-native data platform operators (MIT, license-clean, vanilla-upstream compatible).

**Project**: `mongodb-operator` — MIT MongoDB Operator for Kubernetes — ReplicaSet + Sharded Cluster + Backup, vanilla MongoDB 7.0+.

**Family**: One of four sister operators sharing the [`keiailab-commons`](https://github.com/keiailab/keiailab-commons) shared library:

| Project | Database | Repository |
|---|---|---|
| `mongodb-operator` | MongoDB 7.0+ | https://github.com/keiailab/mongodb-operator |
| `keiailab-commons` | Shared Go library | https://github.com/keiailab/keiailab-commons |

## 2. Logo & Visual Assets

| Asset | URL | Usage |
|---|---|---|
| Current primary logo | `docs/branding/symbol.png` | README header, slides |
| Keiailab base symbol | `docs/branding/base-symbol.png` | Source reference for the outer rotating-arrow mark |
| Repository wordmark | `docs/branding/logo.png` | Project pages and docs cards |
| Social cover | `docs/branding/cover.png` | Social cards and launch posts |
| Current favicon | `https://keiailab.com/favicon.ico` | Favicon, social cards |
| Planned SVG kit | `https://keiailab.com/assets/{logo,mark,wordmark}.svg` | Future replacement after URLs return 200 |

**Logo placement**: Top-center of README, width 96px. Always link to https://keiailab.com.

**Symbol note (2026-06-15)**: the primary symbol keeps the Keiailab rotating-arrow mark and replaces only the center sphere with the service icon.

**Clear space**: Minimum padding around logo = 25% of logo width.

**Do not**:
- Recolor the logo
- Add drop shadows or filters
- Place on backgrounds with insufficient contrast
- Combine with other logos without keiailab brand approval

## 3. Color Palette

| Role | Hex | Usage |
|---|---|---|
| Primary (keiailab teal) | `#0EA5A8` | Headers, primary actions, links |
| Secondary (deep navy) | `#0F172A` | Dark backgrounds, code blocks |
| Accent (warm amber) | `#F59E0B` | Highlights, badge accents |
| Neutral grey | `#64748B` | Body text on light backgrounds |
| Background light | `#F8FAFC` | Documentation page background |
| Background dark | `#020617` | Code editor theme, dark mode |

GitHub README 의 shield.io badge 는 위 hex 사용 권장.

## 4. Typography

- **Headings**: System default (GitHub 의 default `-apple-system, BlinkMacSystemFont, Segoe UI, ...`)
- **Body**: 동일 (GitHub-native 정합)
- **Code**: `ui-monospace, SFMono-Regular, Consolas, ...` (GitHub 의 default monospace)

별도 webfont 사용 안 함 (GitHub README rendering 정합).

## 5. Voice & Tone

**Audience**: Kubernetes platform engineers / DBAs / SRE.

**Voice principles**:
- **Direct** — bullet-point over paragraph where possible
- **Evidence-based** — claims include benchmark / SLA / link
- **Vendor-neutral** — reference upstream (PostgreSQL, MongoDB, Valkey) but do not embed/wrap third-party operators
- **License-aware** — MIT/BSD/Apache-2.0/PG-license dependencies only

**Avoid**:
- Marketing superlatives ("blazing fast", "revolutionary", "best-in-class")
- Vague comparisons ("X-class quality")  — *qualify with specific metric or benchmark*
- Time-based deadlines in roadmap (use `standards/roadmap.md §1.1` — feature checklist instead)

## 6. README Header Standard

모든 README 의 첫 문단은 다음 형식 (Wave 3 표준):

```markdown
<p align="center">
  <img src="docs/branding/symbol.png" alt="keiailab" width="96"/>
</p>

# mongodb-operator

> **MIT MongoDB Operator for Kubernetes — ReplicaSet + Sharded Cluster + Backup, vanilla MongoDB 7.0+**

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License"/></a>
  <!-- 기존 shield.io badges 유지 + 정합 -->
</p>

<p align="center">
  <b>English</b> |
  <a href="README.ko.md">한국어</a> |
  <a href="README.ja.md">日本語</a> |
  <a href="README.zh.md">中文</a>
</p>
```

## 7. README Footer Standard

모든 README + root-level .md 파일의 마지막에 다음 footer 부착 (Wave 3 표준):

```markdown
```

## 8. Badges 표준 순서

README 의 shield.io badge 순서 (좌→우):

1. License (MIT)
2. Go Version (1.25+)
3. Database (MongoDB 7.0+)
4. Kubernetes Version (1.26+)
5. Container Image (ghcr.io/keiailab)
6. Helm Chart (Chart.yaml version + Artifact Hub link)
7. OpenSSF Scorecard
8. GitHub Discussions

## 9. Discussions / Issues / PR Templates

- **Discussions**: `https://github.com/keiailab/mongodb-operator/discussions` — feature ideas, Q&A
- **Issues**: bug reports + concrete feature requests with use case
- **PR template**: `.github/PULL_REQUEST_TEMPLATE.md` 표준 (사용자 시나리오 + 검증 명령 인용 의무, `standards/checklist.md §3`)

## 10. Social & External

- **Website**: https://github.com/keiailab
- **GitHub Org**: https://github.com/keiailab
- **Artifact Hub** (Helm): https://artifacthub.io/packages/search?repo=keiailab-mongodb-operator
- **GHCR** (Container): https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator

## 11. License & Attribution

- License: [MIT](../LICENSE)
- Copyright: © 2026 keiailab contributors
- Third-party attributions: see `NOTICE` (if applicable)
