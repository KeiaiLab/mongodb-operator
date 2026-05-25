<p align="center">
  <a href="MAINTAINERS.md">English</a> |
  <a href="MAINTAINERS.ko.md">한국어</a> |
  <a href="MAINTAINERS.ja.md">日本語</a> |
  <b>中文</b>
</p>

# Maintainers

本文档管理 keiailab/mongodb-operator 中具有决策权的维护者名单。

## 当前维护者

| 姓名/团队 | GitHub | 角色 | 负责领域 |
|---|---|---|---|
| keiailab maintainers | [@keiailab/maintainers](https://github.com/orgs/keiailab/teams/maintainers) | Lead | 全部 |

GitHub team `@keiailab/maintainers` 拥有本项目所有领域的合并/审批权限。个人维护者的添加按照以下流程进行。

## 维护者资格

满足以下条件 6 个月以上的 contributor 可被推荐为维护者:

- 已合并的 PR ≥ 20 个 (有意义的代码/文档贡献)
- 已审查的 PR ≥ 30 个 (附带建设性反馈)
- 遵守本项目的 [GOVERNANCE.md](GOVERNANCE.md) 与 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- 对一个以上的核心领域 (controller, resource builder, sharded reconcile, bootstrap-admin script, observability 等) 有深入理解

## 添加流程

1. 由现有维护者或 candidate 本人通过 issue 或 ADR 提案
2. `@keiailab/maintainers` 团队的 lazy consensus (7 天评论窗口)
3. 无反对意见则添加到 GitHub team,并提交 MAINTAINERS.md 更新 PR

## 非活跃维护者

连续 6 个月无活动的维护者将转为 emeritus (回收权限,保留荣誉名单)。回归流程与新增流程相同。

## 领域负责人 (与 CODEOWNERS 同步)

请参阅 `.github/CODEOWNERS` (如存在)。各目录将自动分配审查者。

## Emeritus

(暂无)

