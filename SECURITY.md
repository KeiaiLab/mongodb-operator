<p align="center">
  <b>English</b> |
  <a href="SECURITY.ko.md">한국어</a> |
  <a href="SECURITY.ja.md">日本語</a> |
  <a href="SECURITY.zh.md">中文</a>
</p>

# Security Policy

## Supported Versions

The MongoDB Operator team provides security updates for the following versions:

| Version | Support Status |
|---------|----------------|
| Current series | ✅ Active security support |
| MongoDB 8.2 | ✅ Tested and supported |
| Kubernetes 1.26+ | ✅ Tested and supported |

Security updates are released as patches for supported versions. We strongly recommend keeping your operator and MongoDB clusters up-to-date to benefit from the latest security fixes.

## Reporting a Vulnerability

We take security reports seriously. If you discover a security vulnerability, please report it privately to us before disclosing it publicly.

### How to Report

**Preferred Method**: Use GitHub's private vulnerability reporting
1. Visit https://github.com/eightynine01/mongodb-operator/security/advisories
2. Click "Report a vulnerability"
3. Follow the prompts to submit your report

**Alternative**: Email us directly at security@eightynine01.com

### What to Include

Please include as much detail as possible:
- Description of the vulnerability
- Steps to reproduce the issue
- Potential impact of the vulnerability
- Any proof-of-concept or exploit code (if available)

### Privacy

All vulnerability reports are handled with strict confidentiality. Your report will only be shared with the maintainers responsible for addressing the issue. We will not publicly disclose your identity without your permission.

## Security Best Practices for Users

To secure your MongoDB deployments:

1. **Enable TLS**: Always enable TLS encryption for data in transit using cert-manager integration
2. **Strong Authentication**: Use SCRAM-SHA-256 with strong, unique passwords stored as Kubernetes Secrets
3. **RBAC**: Configure proper Kubernetes RBAC to limit operator permissions to least privilege
4. **Network Policies**: Implement network policies to restrict pod-to-pod communication
5. **Regular Updates**: Keep both the operator and underlying MongoDB versions updated
6. **Backup Security**: Secure backup storage credentials and enable encryption for backups
7. **Monitoring**: Enable Prometheus monitoring to detect unusual activity patterns
8. **Resource Limits**: Set appropriate resource limits to prevent DoS attacks

## Security Features of the Operator

The MongoDB Operator includes several security features:

- **TLS Encryption**: Automatic certificate management with cert-manager integration
- **Authentication**: SCRAM-SHA-256 authentication for secure user access
- **Internal Auth**: Keyfile-based authentication for inter-cluster communication
- **RBAC Integration**: Respects Kubernetes RBAC for access control
- **Secret Management**: Stores credentials securely in Kubernetes Secrets
- **Prometheus Monitoring**: Export metrics for security monitoring and alerting
- **Secure Base Image**: Uses distroless Docker images to minimize attack surface

## Disclosure Policy

Our disclosure process follows these guidelines:

1. **Initial Response**: We aim to acknowledge vulnerability reports within 48 hours
2. **Assessment**: We will assess the severity and impact of the vulnerability
3. **Remediation**: We will develop and test a fix for the vulnerability
4. **Coordinated Disclosure**: We will work with you to determine a disclosure timeline
5. **Public Release**: We will publish a security advisory and release a fix
6. **Credit**: We will credit you for the discovery (with your permission)

## Apache 2.0 Security Disclaimer

This project is licensed under the Apache License 2.0. As per Section 7 of the license, the project is provided on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied, including, without limitation, any warranties or conditions of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A PARTICULAR PURPOSE.

While we strive to maintain high security standards, you are solely responsible for determining the appropriateness of using or redistributing this project and assume any risks associated with your exercise of permissions under the license.

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
