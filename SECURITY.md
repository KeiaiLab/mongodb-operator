# Security Policy

For full security policy, vulnerability reporting, and supported versions, see [docs/security.md](docs/security.md).

To report a vulnerability: **Do not open a public issue.** Use [GitHub Security Advisories](https://github.com/keiailab/mongodb-operator/security/advisories/new) or email security@keiailab.com.

## Upstream images

`mongo` and `percona/mongodb_exporter` are upstream images, not built by
keiailab. The chart marks them `whitelisted` in `artifacthub.io/images`, so
they are excluded from the Artifact Hub security scan summary; only the
operator image is scanned there. Track the upstream advisories
([MongoDB](https://www.mongodb.com/resources/products/alerts),
[Percona exporter](https://github.com/percona/mongodb_exporter/security)) and
override `spec.version.image` / `spec.monitoring.exporter.image` when a
patched image is needed.
