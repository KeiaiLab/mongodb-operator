<p align="center">
  <a href="BRANDING.md">English</a> |
  <a href="BRANDING.ko.md">한국어</a> |
  <a href="BRANDING.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 品牌指南 — `mongodb-operator`

> keiailab operator 系列的视觉识别、声音和语调。

本文档是 `mongodb-operator` 品牌决策的 canonical 参考。适用于 README、release notes、市场材料以及代表本项目的所有第三方沟通。

## 1. 身份标识

**Organization**: [keiailab](https://github.com/keiailab) — Kubernetes-native 数据平台 operator (MIT、license-clean、vanilla-upstream 兼容)。

**Project**: `mongodb-operator` — Kubernetes 的 MIT MongoDB Operator — ReplicaSet + Sharded Cluster + Backup,原生 MongoDB 7.0+。

**Family**: 共享 [`keiailab-commons`](https://github.com/keiailab/keiailab-commons) 通用库的四个姊妹 operator 之一:

| Project | Database | Repository |
|---|---|---|
| `mongodb-operator` | MongoDB 7.0+ | https://github.com/keiailab/mongodb-operator |
| `keiailab-commons` | Shared Go library | https://github.com/keiailab/keiailab-commons |

## 2. Logo 与视觉资源

| 资源 | URL | 用途 |
|---|---|---|
| Primary logo (当前) | `https://github.com/keiailab.png` | README header (全部仓库)、幻灯片 |
| Primary logo (SVG, 计划) | `https://keiailab.com/assets/logo.svg` *(尚未发布)* | GitHub avatar 的未来替代 |
| Mono mark (计划) | `https://keiailab.com/assets/mark.svg` *(尚未发布)* | Favicon、社交卡片 |
| Wordmark (计划) | `https://keiailab.com/assets/wordmark.svg` *(尚未发布)* | 页脚、深色背景 |

**Logo placement**: README 顶部居中,宽度 120px。始终链接到 https://github.com/keiailab。

**Migration note (2026-05-21)**: 在 `keiailab.com/assets/*.svg` 发布之前,family 全部仓库均使用 GitHub avatar (`https://github.com/keiailab.png`) 作为 canonical primary logo。SVG 行为未来迁移预留。

**Clear space**: Logo 周围最小留白 = logo 宽度的 25%。

**禁止事项**:
- 修改 logo 颜色
- 添加阴影或滤镜
- 放置于对比度不足的背景上
- 未经 keiailab 品牌批准与其他 logo 组合

## 3. 调色板

| Role | Hex | Usage |
|---|---|---|
| Primary (keiailab teal) | `#0EA5A8` | 标题、primary 操作、链接 |
| Secondary (deep navy) | `#0F172A` | 深色背景、代码块 |
| Accent (warm amber) | `#F59E0B` | 强调、badge 点缀 |
| Neutral grey | `#64748B` | 浅色背景下的正文文字 |
| Background light | `#F8FAFC` | 文档页面背景 |
| Background dark | `#020617` | 代码编辑器主题、暗色模式 |

GitHub README 的 shield.io badge 建议使用上述 hex。

## 4. 字体

- **Headings**: System default (GitHub 默认的 `-apple-system, BlinkMacSystemFont, Segoe UI, ...`)
- **Body**: 同上 (与 GitHub-native 一致)
- **Code**: `ui-monospace, SFMono-Regular, Consolas, ...` (GitHub 默认 monospace)

不使用额外的 webfont (与 GitHub README rendering 保持一致)。

## 5. 声音与语调

**Audience**: Kubernetes 平台工程师 / DBA / SRE。

**声音原则**:
- **Direct (直接)** — 尽可能使用 bullet-point 代替段落
- **Evidence-based (基于证据)** — 论断需附 benchmark / SLA / 链接
- **Vendor-neutral (厂商中立)** — 引用 upstream (PostgreSQL、MongoDB、Valkey),但不 embed / wrap 第三方 operator
- **License-aware (许可证意识)** — 仅使用 MIT/BSD/Apache-2.0/PG-license 依赖

**应避免的表达**:
- 市场化的最高级表述 ("blazing fast"、"revolutionary"、"best-in-class")
- 模糊的比较 ("X-class quality") — *请使用具体指标或 benchmark 加以限定*
- Roadmap 中基于时间的截止期 (改用 `standards/roadmap.md §1.1` 的 feature 清单)

## 6. README Header 标准

所有 README 的首段须采用以下格式 (Wave 3 标准):

```markdown
<p align="center">
  <img src="https://github.com/keiailab.png" alt="keiailab" width="120"/>
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

## 7. README Footer 标准

所有 README 与根级 .md 文件的末尾须附以下 footer (Wave 3 标准):

```markdown
```

## 8. Badges 标准顺序

README 中 shield.io badge 的顺序 (左→右):

1. License (MIT)
2. Go Version (1.25+)
3. Database (MongoDB 7.0+)
4. Kubernetes Version (1.26+)
5. Container Image (ghcr.io/keiailab)
6. Helm Chart (Chart.yaml version + Artifact Hub link)
7. OpenSSF Scorecard
8. GitHub Discussions

## 9. Discussions / Issues / PR 模板

- **Discussions**: `https://github.com/keiailab/mongodb-operator/discussions` — 功能想法、Q&A
- **Issues**: bug 报告 + 带具体用例的 feature request
- **PR template**: `.github/PULL_REQUEST_TEMPLATE.md` 标准 (强制引用用户场景 + 验证命令,`standards/checklist.md §3`)

## 10. 社交与外部链接

- **Website**: https://github.com/keiailab
- **GitHub Org**: https://github.com/keiailab
- **Artifact Hub** (Helm): https://artifacthub.io/packages/search?repo=keiailab-mongodb-operator
- **GHCR** (Container): https://github.com/keiailab/mongodb-operator/pkgs/container/mongodb-operator

## 11. 许可证与归属

- License: [MIT](LICENSE)
- Copyright: © 2026 keiailab contributors
- Third-party attributions: 见 [NOTICE](NOTICE) (如适用)
