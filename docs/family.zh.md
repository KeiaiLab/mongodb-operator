<p align="center">
  <img src="https://github.com/keiailab.png" alt="keiailab" width="120"/>
</p>

<p align="center">
  <a href="family.md">English</a> |
  <a href="family.ko.md">한국어</a> |
  <a href="family.ja.md">日本語</a> |
  <b>中文</b>
</p>

# keiailab operator family

> 构建于共享基础之上的四个姊妹 Kubernetes operator — `operator-commons` (Go 库) + Helm partial + Apache-2.0 技术栈。

您正从 **`mongodb-operator`** 仓库阅读本页。本文档是整个 family 的 canonical cross-link。

## Family 概览

| 项目 | 数据库 | 状态 | 仓库 |
|---|---|---|---|
| **`postgres-operator`** | PostgreSQL 18+ | active | https://github.com/keiailab/postgres-operator |
| **`mongodb-operator`** | MongoDB 7.0+ | active | https://github.com/keiailab/mongodb-operator |
| **`valkey-operator`** | Valkey 8.0+ (Redis fork, BSD-3) | active | https://github.com/keiailab/valkey-operator |
| **`operator-commons`** | 共享 Go 库 | v0.7.0 | https://github.com/keiailab/operator-commons |

## 共享内容

全部四个项目都收敛于同一组运维 primitive:

- **Apache-2.0** 端到端 — 没有 SSPL,SaaS surface 上不带 copyleft
- **`operator-commons`** 共享 Go 库 (v0.7.0+) — finalizer、label、status sugar、security context builder、NetworkPolicy / ServiceMonitor partial
- **Helm chart 骨架** — RFC-0027 的 `default` falsy-toggle 防护、RFC-0026 的 component-keyed values、cycle 26 hardening 的 6 个 marker (priorityClassName / lifecycle / SA / minReadySeconds / automount / revisionHistoryLimit)
- **OLM bundle parity** — scorecard v1alpha3 6-test matrix
- **i18n** — README + 11 份 canonical 文档,涵盖 English / 한국어 / 日本語 / 中文 (cleanup supercycle 2026-05-21 的 Wave 4)

## 不做的事

- ❌ **嵌入或包装 upstream operator** (PGO、CloudNativePG、MongoDB Community Operator、Sentinel) — license-clean,无 copyleft 义务
- ❌ **用于 release gate 的 GitHub Actions** — 本地 4 层 + GitLab CI L5 (参阅 RFC-0002、RFC-0043)
- ❌ **基于时间的 roadmap 截止日期** — feature 清单 + 完成百分比 (参阅 `standards/roadmap.md §1.1`)
- ❌ **Bitnami chart / image** — registry deprecation 风险、Broadcom 收购 (参阅 ADR-0136 / ADR-0057)

## 从哪里开始

| 任务 | 入口 |
|---|---|
| 在 Kubernetes 上部署 `mongodb-operator` | [README.md](../README.md) 的 Quickstart 章节 |
| 阅读架构 | [ARCHITECTURE.md](../ARCHITECTURE.md) |
| 提交 issue 或 feature request | https://github.com/keiailab/mongodb-operator/issues |
| 讨论设计或 roadmap | https://github.com/keiailab/mongodb-operator/discussions |
| 贡献代码 | [CONTRIBUTING.md](../CONTRIBUTING.md) |
| 报告安全问题 | [SECURITY.md](../SECURITY.md) |
| 了解品牌 / 声音 | [BRANDING.md](../BRANDING.md) |
| 跟踪采用者 / 使用情况 | [ADOPTERS.md](../ADOPTERS.md) |
| 寻找 maintainer | [MAINTAINERS.md](../MAINTAINERS.md) |
| 审阅治理模型 | [GOVERNANCE.md](../GOVERNANCE.md) |
| 查看近期工作 | [ROADMAP.md](../ROADMAP.md) |

## Family 间兼容性 (operator-commons)

三个数据库 operator 均以匹配版本 import `github.com/keiailab/operator-commons` (当前为 `v0.7.0+`):

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

`operator-commons` 的 breaking change 需要三个数据库 operator 同步 bump — 通过 supercycle Wave 5 的 `make cross-validation` target 进行验证。

## i18n

canonical 项目文档 (README、CONTRIBUTING、SECURITY、GOVERNANCE、MAINTAINERS、ROADMAP、SUPPORT、BRANDING) 提供四种语言版本 — 请参阅各文件顶部的语言切换器。本 family 概览仅有 English 版,请参阅各仓库的本地化 README 获取母语入口。

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
