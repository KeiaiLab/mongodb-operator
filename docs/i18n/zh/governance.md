<p align="center">
  <a href="GOVERNANCE.md">English</a> |
  <a href="GOVERNANCE.ko.md">한국어</a> |
  <a href="GOVERNANCE.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 治理

本文档定义 keiailab/mongodb-operator 项目的决策流程。

## 原则

1. **开放性**: 所有决策均在公开渠道 (GitHub issue / PR / RFC) 中进行。
2. **Lazy Consensus (最小共识)**: 日常变更若无人反对即可推进。
3. **Explicit Consensus (明确共识)**: 架构变更、CRD 变更、安全模型变更、许可证变更需经 RFC / ADR 后,由 Maintainer **2/3 supermajority** 批准。常规 RFC (单一组件 / 工具采纳 / 政策补强) 仅需 **simple majority (>50%)**。GOVERNANCE 自身的变更 (§"本文档的变更") 始终需要 2/3 supermajority。
4. **共同责任**: Maintainer 对代码质量、用户安全和社区健康承担共同责任。

## 决策分类

### 日常变更 (Lazy Consensus)
- bug 修复、文档改进、新增测试、依赖的 minor / patch 升级、重构 (公开 API 不变)
- 流程: PR → 至少 1 位 Maintainer LGTM → 合并
- 时限: 不另设评论窗口 (本地 gate 通过即可立即合并 — 依据 RFC-0002 不使用 GitHub Actions,通过 pre-commit / pre-push hook + Makefile 进行验证)

### 中等变更 (Explicit Consensus)
- 新增 CRD 字段、新增 reconciler、依赖的 major 升级、公开 API 变更
- 流程: 通过 issue 提案 → 7 天评论窗口 → Maintainer 多数 LGTM → 合并
- 若存在 1 票反对,则在 Maintainer 会议上讨论

### 架构变更 (RFC / ADR 必需)
- 引入新组件、安全模型变更、许可证变更、破坏兼容性的变更
- 流程:
  1. 在 `docs/kb/adr/NNNN-title.md` 提交 ADR 或 RFC
  2. 14 天评论窗口
  3. Maintainer 2/3 以上赞成
  4. ADR / RFC Status: `Draft → Accepted` 后进入实现 PR

### 部署模型变更 (ADR + 用户明示 cluster apply,2026-05-15 新增)

OLM v0 ↔ OLM v1 ↔ Helm chart 的 *默认推荐变更* 或 *cluster apply* 属于本领域。

| 决策 | 工具 | gate |
|---|---|---|
| bundle / catalog manifest 变更 | PR + ADR | Conventional Commits + bundle validate PASS |
| 如 OLM v0 → v1 migration 这类 *模型切换* | ADR (cluster-side) | ADR + Maintainer 2/3 + 用户明示 cluster apply |
| installer RBAC 变更 (cluster-admin ↔ narrow) | PR + ADR | bundle CSV derive 验证 + cluster apply 用户明示 |
| NetworkPolicy 新设 / 变更 | PR + ADR | OPRUN-3923 reference + cluster apply 用户明示 |
| 外部用户 *recommended install path* 变更 | RFC | INSTALL.md §1 matrix 更新 + 14 天评论 |

本领域的 ADR chain: ADR-0028 (外部用户运维水平) → ADR-0029 (OLM v1 采用) → ADR-0030 (narrow RBAC + NP)。后续 ADR 的 *cluster apply* 属于用户明示领域 (全局 §2.0 自治宪章 ② 条件 — 不可逆的运维操作)。

## 安全决策

CVE 报告、密钥 / 认证模型变更按照 [SECURITY.md](SECURITY.md) 的流程在非公开渠道优先处理,待补丁发布后再进行公开共识。

### Installer RBAC scope (ADR-0030,2026-05-15)

- **production cluster**: 推荐 `clusterextension-narrow-rbac.yaml` (bundle CSV derive)。
- **PoC / dev cluster**: 允许 `clusterextension.yaml` (cluster-admin) — 优先简洁性。
- **禁止 cluster-admin binding 的长期运行** — bundle 操作时会影响整个 cluster。PR review 时若使用 cluster-admin,需明示确认 *production 之外* 的场景。

### Network surface (ADR-0030,2026-05-15)

- **default-deny cluster**: 必须对 olmv1-system + mongodb-system *明示应用* NetworkPolicy。参见 `deploy/olm-v1/networkpolicies.yaml` (OLM v0 path 已在 ADR-0028 Phase D 永久废弃)。
- **default-allow cluster**: NP 可选,但推荐应用 (security baseline)。

## 发布决策

发布分支 / 版本 bump 可由 1 位 Maintainer 通过 Lazy Consensus 推进。但 LTS 线新设 / EOL 宣告 / Sharded GA 毕业等 *重大里程碑* 必须 Explicit Consensus。

### Release artifact 的可达性 (ADR-0028 后续)

每次 release 必须保证 *3 deployment models* 全部可达:
- **operator container**: ghcr.io public — 所有 path 通用
- **Helm chart**: gh-pages → artifacthub.io 自动 publish
- **OLM bundle + catalog**: `make bundle-push + catalog-push` 后 ArtifactHub + community-operators 的 upstream PR (ADR-0027 自动化已 deferred — 手动 fallback)

上述 3 件 artifact 中 *任一缺失* 时,禁止 release tag。

## 变更历史

| Date | Change | Refs |
|---|---|---|
| 2026-05-15 | 新设 Deployment 模型变更分类 + Installer RBAC scope + Network surface + Release artifact 可达性 | ADR-0028, ADR-0029, ADR-0030 |
| 2026-05-07 | 本文档新设 — 3-repo (mongodb / postgresql / valkey) 治理资产对齐 | INC-2026-05-07 |

