<p align="center">
  <a href="ADOPTERS.md">English</a> |
  <a href="ADOPTERS.ko.md">한국어</a> |
  <a href="ADOPTERS.ja.md">日本語</a> |
  <b>中文</b>
</p>

# mongodb-operator 的采用组织

本文档列出在生产环境或评估环境中使用 `keiailab/mongodb-operator` 的组织 / 项目的 *公开* 名单。欢迎自行登记 — 通过 PR 添加行即可。

> 非公开的用户可以通过 GitHub Discussions 或 SECURITY.md 中提供的私有渠道告知我们。

## Production Users

在生产环境以 *production-grade SLA* 运行 mongodb-operator 的用户。

| 采用者 | 组件 | 使用模式 | 起始版本 | 当前版本 | 登记日期 |
|---|---|---|---|---|---|
| **argos-platform-data** ([keiailab](https://github.com/keiailab)) | MongoDB 8.3 ReplicaSet + Sharded (Config Server + Shard + Mongos) | argos 的元数据存储。ArgoCD GitOps 自动同步。PodSecurity restricted、KEYFILE auth、ServiceMonitor active。 | v1.4.5 | v1.4.11 | 2026-05-07 |

## Evaluators

在 PoC / 评估 / non-production 环境中使用的用户。

| 采用者 | 阶段 | 备注 |
|---|---|---|
| _欢迎自行登记_ | — | 通过 PR 添加行 |

## How to add yourself

请提交 PR,在上表中添加一行:

```markdown
| **<组织 / 项目>** ([profile](<URL>)) | <组件 + 拓扑> | <使用模式> | <起始版本> | <当前版本> | <登记日期 YYYY-MM-DD> |
```

如希望以非公开或匿名方式登记,请通过 SECURITY.md 中提供的安全渠道告知 maintainer,我们会以 *organization-anonymized* 的形式收录。

## CNCF Sandbox Reference

本 ADOPTERS 名单同时作为公开 reference,用于满足 CNCF graduation criteria 中 "≥1 public adopter" 的要求。

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://keiailab.com">keiailab.com</a>
</p>
