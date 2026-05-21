<p align="center">
  <a href="SECURITY.md">English</a> |
  <a href="SECURITY.ko.md">한국어</a> |
  <a href="SECURITY.ja.md">日本語</a> |
  <b>中文</b>
</p>

# 安全策略

## 支持的版本

MongoDB Operator 团队为以下版本提供安全更新:

| 版本 | 支持状态 |
|---------|----------------|
| 当前系列 | ✅ 持续提供安全支持 |
| MongoDB 8.2 | ✅ 已测试并支持 |
| Kubernetes 1.26+ | ✅ 已测试并支持 |

安全更新以补丁形式针对支持的版本发布。我们强烈建议您保持 operator 和 MongoDB 集群处于最新状态,以获得最新的安全修复。

## 报告漏洞

我们高度重视安全报告。如果您发现安全漏洞,请在公开披露之前以非公开方式向我们报告。

### 如何报告

**首选方法**: 使用 GitHub 的 private vulnerability reporting
1. 访问 https://github.com/keiailab/mongodb-operator/security/advisories
2. 点击 "Report a vulnerability"
3. 按照提示提交您的报告

**备选方法**: 直接发送邮件至 security@keiailab.com

### 应包含的内容

请尽可能提供详细信息:
- 漏洞描述
- 复现问题的步骤
- 漏洞的潜在影响
- 任何 proof-of-concept 或 exploit 代码(如有)

### 隐私

所有漏洞报告均以严格的保密方式处理。您的报告仅会与负责处理该问题的 maintainer 共享。未经您的许可,我们不会公开披露您的身份。

## 用户安全最佳实践

为了保护您的 MongoDB 部署:

1. **启用 TLS**: 通过 cert-manager 集成,始终为传输中的数据启用 TLS 加密
2. **强认证**: 使用 SCRAM-SHA-256,并将强且唯一的密码以 Kubernetes Secrets 形式存储
3. **RBAC**: 配置合适的 Kubernetes RBAC,以最小权限原则限制 operator 权限
4. **NetworkPolicy**: 实施 network policy 以限制 pod 之间的通信
5. **定期更新**: 同时保持 operator 和底层 MongoDB 版本更新到最新
6. **备份安全**: 安全管理备份存储凭据,并为备份启用加密
7. **监控**: 启用 Prometheus 监控以检测异常活动模式
8. **资源限制**: 设置适当的资源限制以防止 DoS 攻击

## Operator 的安全功能

MongoDB Operator 包含若干安全功能:

- **TLS 加密**: 通过 cert-manager 集成自动管理证书
- **认证 (Authentication)**: 用于安全用户访问的 SCRAM-SHA-256 认证
- **内部认证**: 用于集群间通信的基于 keyfile 的认证
- **RBAC 集成**: 遵循 Kubernetes RBAC 进行访问控制
- **Secret 管理**: 将凭据安全地存储在 Kubernetes Secrets 中
- **Prometheus 监控**: 导出用于安全监控和告警的指标
- **安全的基础镜像**: 使用 distroless Docker 镜像以最小化攻击面

## 披露策略

我们的披露流程遵循以下准则:

1. **初次响应**: 我们力争在 48 小时内确认收到漏洞报告
2. **评估**: 我们将评估漏洞的严重程度和影响
3. **修复**: 我们将开发并测试针对该漏洞的修复方案
4. **协调披露**: 我们将与您协作以确定披露时间线
5. **公开发布**: 我们将发布 security advisory 并发布修复
6. **致谢**: 我们将为您的发现给予致谢(在您许可的情况下)

## Apache 2.0 安全免责声明

本项目根据 Apache License 2.0 许可。根据该许可证第 7 条,本项目按「现状 (AS IS)」基础提供,不附带任何明示或暗示的保证或条件,包括但不限于 TITLE、NON-INFRINGEMENT、MERCHANTABILITY 或 FITNESS FOR A PARTICULAR PURPOSE 的任何保证或条件。

尽管我们努力维持高水准的安全标准,但您需自行判断使用或再分发本项目的适当性,并自行承担在该许可证下行使权限所伴随的任何风险。

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
