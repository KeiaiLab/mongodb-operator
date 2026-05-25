<p align="center">
  <b>English</b> |
  <a href="SUPPORT.ko.md">한국어</a> |
  <a href="SUPPORT.ja.md">日本語</a> |
  <a href="SUPPORT.zh.md">中文</a>
</p>

# Support

> 한국어 사용자: 본 문서의 채널은 영어와 한국어 모두 환영합니다.

Thanks for using `mongodb-operator`. This page explains where to get
help.

## Decide what you need

| Situation | Where to go |
|---|---|
| **You think you found a security vulnerability.** | **Do not open a public issue.** Use [Security Policy](security.md) — GitHub Security Advisory or `security@keiailab.com` (PGP signed). |
| You have an "is this supposed to work like X?" or "how do I configure Y?" question. | [GitHub Discussions](https://github.com/keiailab/mongodb-operator/discussions). Searchable and indexed by future operators. |
| You found a bug — something behaves differently from the docs. | [Open an issue](https://github.com/keiailab/mongodb-operator/issues/new/choose) using the **Bug report** template. |
| You want a feature added or behaviour changed. | [Open an issue](https://github.com/keiailab/mongodb-operator/issues/new/choose) using the **Feature request** template. Check [Roadmap](roadmap.md) first to see if it's already planned. |
| You have a "this should be in the FAQ" question. | [Open an issue](https://github.com/keiailab/mongodb-operator/issues/new/choose) using the **Question** template. |
| You're hitting a Prometheus alert and need the MTTR procedure. | [the [Troubleshooting Guide](troubleshooting.md).
| You're seeing odd behaviour but no alert. | [the [Troubleshooting Guide](troubleshooting.md).
| You want to contribute code or docs. | [Contributing Guide](contributing.md). |

## Before opening an issue, please

1. Search [existing issues](https://github.com/keiailab/mongodb-operator/issues?q=is%3Aissue) and [Discussions](https://github.com/keiailab/mongodb-operator/discussions) — your question may already be answered.
2. Try the [Troubleshooting Guide](troubleshooting.md).
3. Have the following ready in your report:
   - `mongodb-operator` version (`kubectl get deploy -n mongodb-operator-system -o jsonpath='{.items[0].spec.template.spec.containers[0].image}'`)
   - Kubernetes version (`kubectl version`)
   - Helm chart version (`helm list -A | grep mongodb-operator`)
   - The smallest reproduction you can produce
   - The output of `kubectl describe <Valkey|ValkeyCluster> <name>`

## Response expectations

This is an open-source project maintained on best-effort time.
[Governance](governance.md) describes the decision-making and
review process. We typically respond on issues within a few business
days; security reports are handled per the SLA in
[Security Policy](security.md) (initial ack within 72 h, severity triage
within 7 days).

If you need a paid support relationship or a hard SLA, reach out via
`security@keiailab.com` and we can discuss options.

## Commercial vendors

`mongodb-operator` does not endorse a paid support vendor today. If
this changes, an entry will be added here with the vendor's terms
and the upstream feature it supports.

## Code of Conduct

Every channel above is governed by the
[Code of Conduct](code-of-conduct.md). Please read it before
participating.

