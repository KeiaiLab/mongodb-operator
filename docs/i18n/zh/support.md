<p align="center">
  <a href="(../support.md)">English</a> |
  <a href="SUPPORT.ko.md">한국어</a> |
  <a href="SUPPORT.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 支持

> 韩语用户提示:本文档的渠道同时欢迎使用英语和韩语。

感谢您使用 `mongodb-operator`。本页说明在哪里可以获得帮助。

## 确定您需要什么

| 情况 | 前往位置 |
|---|---|
| **您认为发现了一个安全漏洞。** | **请勿打开公开 issue。** 使用 [Security](../security.md) — GitHub Security Advisory 或 `security@keiailab.com` (PGP 签名)。 |
| 您有"这应该像 X 那样工作吗?"或"我该如何配置 Y?"之类的问题。 | [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions)。可搜索,并为将来的运维人员建立索引。 |
| 您发现了一个 bug — 行为与文档不一致。 | 使用 **Bug report** 模板 [打开 issue](https://github.com/keiailab/mongodb-operator/issues/new/choose)。 |
| 您希望添加功能或更改行为。 | 使用 **Feature request** 模板 [打开 issue](https://github.com/keiailab/mongodb-operator/issues/new/choose)。请先查看 [Roadmap](../roadmap.md) 确认是否已在计划中。 |
| 您有"这应该写进 FAQ"之类的问题。 | 使用 **Question** 模板 [打开 issue](https://github.com/keiailab/mongodb-operator/issues/new/choose)。 |
| 您遇到 Prometheus 告警,需要 MTTR 操作流程。 | [`../troubleshooting.md`](../troubleshooting.md) §9 (每条告警的 `runbook_url` annotation 都指向此处)。 |
| 您看到异常行为但没有告警。 | [`../troubleshooting.md`](../troubleshooting.md) — 症状 → 原因 → 诊断 → 处置流程图。 |
| 您想为代码或文档做贡献。 | [Contributing](../contributing.md)。 |

## 在打开 issue 之前,请

1. 搜索 [已有的 issues](https://github.com/keiailab/mongodb-operator/issues?q=is%3Aissue) 和 [Discussions](https://github.com/keiailab/mongodb-operator/discussions) — 您的问题可能已被回答。
2. 尝试 [troubleshooting 流程图](../troubleshooting.md)。
3. 在您的报告中准备好以下内容:
   - `mongodb-operator` 版本 (`kubectl get deploy -n mongodb-operator-system -o jsonpath='{.items[0].spec.template.spec.containers[0].image}'`)
   - Kubernetes 版本 (`kubectl version`)
   - Helm chart 版本 (`helm list -A | grep mongodb-operator`)
   - 您能提供的最小可复现案例
   - `kubectl describe <Valkey|ValkeyCluster> <name>` 的输出

## 响应预期

这是一个以 best-effort 时间维护的开源项目。
[Governance](../governance.md) 描述了决策与
评审流程。通常我们会在几个工作日内
回复 issue;安全报告按
[Security](../security.md) 中的 SLA 处理 (initial ack 72 h 以内,severity triage
7 天以内)。

如果您需要付费支持关系或硬性 SLA,请通过
`security@keiailab.com` 联系,我们可以讨论方案。

## 商业供应商

`mongodb-operator` 目前不推荐任何付费支持供应商。
如果这一情况发生变化,我们会在此处添加条目,
并附带该供应商的条款及其支持的 upstream 功能。

## Code of Conduct (行为准则)

上述所有渠道均受
[Code of Conduct](../code-of-conduct.md) 约束。请在
参与之前阅读。

