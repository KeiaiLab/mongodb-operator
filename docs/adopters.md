<p align="center">
  <b>English</b> |
  <a href="i18n/ko/adopters.md">한국어</a> |
  <a href="i18n/ja/adopters.md">日本語</a> |
  <a href="i18n/zh/adopters.md">中文</a>
</p>

# Adopters of mongodb-operator

Organizations and projects using `keiailab/mongodb-operator` in production or evaluation. Self-registration is welcome — please add a row via Pull Request.

> Private users may report usage via GitHub Discussions or the private channel in [SECURITY.md](security.md).

## Production Users

Organizations running mongodb-operator with production-grade SLA.

| Organization | Components | Usage Pattern | Since Version | Current Version | Listed |
|---|---|---|---|---|---|
| [keiailab](https://github.com/keiailab) | MongoDBSharded (5 shards, 3 mongos, 3 config servers) | Production metadata storage, TLS + SCRAM-SHA-256, PDB, PriorityClass, ceph-rbd storage | v1.4.5 | v1.8.0 | 2026-05 |

## Evaluators

POC / 평가 / non-production 환경에서 사용하는 사용자.

| 사용자 | 단계 | 비고 |
|---|---|---|
| _자가 등록 환영_ | — | PR 로 row 추가 |

## How to add yourself

PR 을 열어 위 표에 한 row 추가:

```markdown
| **<조직 / 프로젝트>** ([profile](<URL>)) | <컴포넌트 + 토폴로지> | <사용 패턴> | <시작 버전> | <현재 버전> | <등재 일자 YYYY-MM-DD> |
```

비공개 또는 익명 등재를 원하시면 SECURITY.md 의 보안 채널로 알려주시면 maintainer 가 *organization-anonymized* row 로 등재합니다.

## CNCF Sandbox Reference

본 ADOPTERS 목록은 CNCF graduation criteria 의 "≥1 public adopter" 요구사항을 충족하기 위한 공개 reference 로도 활용됩니다.

