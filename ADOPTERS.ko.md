<p align="center">
  <a href="ADOPTERS.md">English</a> |
  <b>한국어</b> |
  <a href="ADOPTERS.ja.md">日本語</a> |
  <a href="ADOPTERS.zh.md">中文</a>
</p>

# Adopters of mongodb-operator (한국어)

> English: [ADOPTERS.md](ADOPTERS.md) — canonical / 정본

본 문서는 `keiailab/mongodb-operator` 를 운영 환경 또는 평가 환경에서 사용하는 조직 / 프로젝트의 *공개* 목록입니다. 자가 등록을 환영합니다 — PR 로 row 를 추가해 주세요.

> 비공개 사용자는 GitHub Discussions 또는 SECURITY.md 의 비공개 채널을 통해 알려주실 수 있습니다.

## Production Users

운영 환경에서 mongodb-operator 를 *production-grade SLA* 로 사용하는 사용자.

| 사용자 | 컴포넌트 | 사용 패턴 | 시작 버전 | 현재 버전 | 등재 일자 |
|---|---|---|---|---|---|
| **keiailab-platform-data** ([keiailab](https://github.com/keiailab)) | MongoDB 8.3 ReplicaSet + Sharded (Config Server + Shard + Mongos) | keiailab 의 메타데이터 스토리지. ArgoCD GitOps 자동 sync. PodSecurity restricted, KEYFILE auth, ServiceMonitor active. | v1.4.5 | v1.4.11 | 2026-05-07 |

## Evaluators

POC / 평가 / non-production 환경에서 사용하는 사용자.

| 사용자 | 단계 | 비고 |
|---|---|---|
| _자가 등록 환영_ | — | PR 로 row 추가 |

## How to add yourself

PR 을 열어 위 표에 row 한 개를 추가해 주세요:

```markdown
| **<조직 / 프로젝트>** ([profile](<URL>)) | <컴포넌트 + 토폴로지> | <사용 패턴> | <시작 버전> | <현재 버전> | <등재 일자 YYYY-MM-DD> |
```

비공개 또는 익명 등재를 원하시면 SECURITY.md 의 보안 채널을 통해 알려주시면 maintainer 가 *organization-anonymized* row 로 등재합니다.

## CNCF Sandbox Reference

본 ADOPTERS 목록은 CNCF graduation criteria 의 "≥1 public adopter" 요구사항을 충족하기 위한 공개 reference 로도 활용됩니다.

---

<p align="center">
  <b>keiailab operator family</b><br/>
  <a href="https://github.com/keiailab/postgres-operator">postgres-operator</a> ·
  <a href="https://github.com/keiailab/mongodb-operator">mongodb-operator</a> ·
  <a href="https://github.com/keiailab/valkey-operator">valkey-operator</a> ·
  <a href="https://github.com/keiailab/operator-commons">operator-commons</a>
</p>

<p align="center">
  © 2026 keiailab · <a href="LICENSE">Apache-2.0</a> · <a href="https://github.com/keiailab">keiailab.com</a>
</p>
