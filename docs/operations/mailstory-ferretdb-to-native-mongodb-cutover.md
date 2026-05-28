# mailstory: FerretDB → native MongoDB Cutover Plan (Draft)

- Status: **Draft (사용자 결정 대기)**
- Trigger: ADR-0028 Q2=B 사용자 결정 (2026-05-15)
- Owner: @keiailab
- Scope: cross-repo (mongodb-operator + mailstory app + keiailab-platform-data)
- Promotion: 본 Draft 가 사용자 명시 *cutover 승인* 시 `ai-dev/rfcs/NNNN-mailstory-mongodb-cutover.md` 로 승격.

> 본 문서는 *실행 plan 이 아니라 결정 게이트* — production data migration 은
> 사용자 명시 cutover 승인 + downtime window + rollback evidence 모두
> 확보된 후에만 시작. ADR-0028 Phase B 적용 시 본 cutover *수행하지 않고*
> PoC ns (database) 의 test-mongodb CR 만 검증.

## §1 현재 상태 (라이브 사실, 2026-05-14 memory 기준)

| 항목 | 사실 |
|---|---|
| mailstory MongoDB API | FerretDB v2 (mailstory-ferretdb deployment, `--handler=pg`) |
| 실 storage backend | PostgreSQL (서비스 inventory 별, memory `keiailab-infrastructure`) |
| mailstory app 의 MongoDB driver | (확인 필요) — Pydantic settings `DATABASE__URL` 이 mongodb:// scheme? FerretDB 만 노출? |
| mongodb-operator 도입 시 native MongoDB | StatefulSet replica set, BSON storage, 별도 PVC |
| 데이터 양 | (확인 필요) — mongodump 로 측정 |
| 사용자 영향 윈도우 | (사용자 결정) — 무중단 / 분 단위 / 시간 단위 |

라이브 검증 명령 (사용자 SSH tunnel 후 실행):

```fish
kubectl get pod -n <mailstory-ns> -l app.kubernetes.io/name=mailstory-ferretdb
kubectl get pvc -n <mailstory-ns>
kubectl exec -n <mailstory-ns> deploy/mailstory-ferretdb -- du -sh /var/lib/postgresql/data 2>/dev/null
# 또는 ferretdb 가 사용하는 외부 postgres 의 size 측정.
```

## §2 마이그레이션 분류 결정 (사용자 게이트)

다음 중 *하나* 를 선택 후 진행:

### 옵션 A. Blue-Green (권장)

- 단계: 신규 native MongoDB cluster 를 *별 ns* 에 배포 → mailstory app 의 dual-write → 검증 후 cutover.
- 장점: rollback 가능 (FerretDB 유지), downtime 최소 (cutover 순간만).
- 비용: 데이터 storage 2배 동안 (cutover 후 FerretDB 제거).
- 적용 작업:
  1. `database` ns 에 MongoDB CR (`mailstory-prod-mongodb`) 배포.
  2. mongodump from FerretDB → mongorestore to native MongoDB (initial sync).
  3. mailstory app 의 connection string env 에 `MONGODB_URL_NATIVE` 추가, *dual-write* 모드 enable (app code 변경 필수).
  4. dual-write 검증 (1 주 이상): write delta = 0.
  5. read-only window (수 분) → final delta sync → cutover (mailstory env switch).
  6. 검증 후 FerretDB resources 제거.

### 옵션 B. In-place (FerretDB 중단 + 재배포)

- 단계: FerretDB 정지 → 데이터 dump → native MongoDB 배포 → restore → mailstory 재시작.
- 장점: 단순.
- 비용: downtime = dump+restore 시간 (data size 의존).
- 적용 작업:
  1. mailstory app scale 0 (또는 maintenance page).
  2. mongodump from FerretDB.
  3. FerretDB 정지.
  4. native MongoDB CR 배포 + Ready 대기.
  5. mongorestore.
  6. mailstory connection string 변경 + scale up.
  7. 검증.

### 옵션 C. 보류 — FerretDB 유지, native MongoDB 는 신규 service 만 사용

- 의미: Q2=B 결정 *철회* + Q2=A 로 변경. 본 cutover plan 적용 안 함.

## §3 데이터 호환성 검증 (사용자 결정 전 필수)

FerretDB v2 의 MongoDB wire protocol 구현 vs native MongoDB 의 *동작 차이* 가
mailstory 의 query 패턴에 영향:

- **aggregation pipeline**: FerretDB v2 의 일부 stage 미지원 → native 에서 결과 *동일* 한가?
- **transactions**: FerretDB v2 의 multi-doc transaction 지원 = 부분 (1.x 와 2.x 차이). mailstory 의 transaction 사용 패턴?
- **indexes**: FerretDB v2 의 partial / TTL / compound index 동작.
- **bulk operations**: ordered/unordered bulk 의 동작.
- **connection pooling**: driver 의 reconnect 동작.

검증 방법:
- mailstory 의 모든 mongodb-driver query 호출처 grep + 각 호출처 의 native MongoDB 동작 확인.
- staging ns 에서 native MongoDB + dump/restore 후 mailstory e2e PASS.

## §4 cutover Day-0 체크리스트

```
- [ ] §3 데이터 호환성 검증 PASS
- [ ] 옵션 A/B 결정 + 사용자 명시 cutover window 승인
- [ ] 데이터 양 측정 (mongodump 크기)
- [ ] downtime window 사용자 공지 (사용자 직접)
- [ ] mongodump dry-run (staging 에서, dump 시간 측정)
- [ ] rollback runbook 작성 (실패 시 FerretDB 복귀)
- [ ] mailstory app 의 connection string 변경 PR 작성 (옵션 A 의 dual-write, 옵션 B 의 cutover)
- [ ] keiailab-platform-data ArgoCD App 의 MongoDB CR 추가 PR
- [ ] OLM Subscription 의 installPlanApproval=Manual → InstallPlan 1건 승인 (mongodb-operator v1.5.0)
- [ ] cutover Day-0 실행
- [ ] 검증 (mailstory e2e + 데이터 row count match)
- [ ] rollback 시나리오 *시도해보지 않고* cutover 완료 시 D+1, D+7 모니터링
- [ ] FerretDB resources 제거 (옵션 A 만, D+14 이후)
```

## §5 Rollback Plan

- 옵션 A: mailstory env 다시 `MONGODB_URL` (FerretDB) 로 switch + native MongoDB 정지. dual-write 기간 동안 FerretDB 가 *진본* 유지.
- 옵션 B: FerretDB resources 보존 (단순 scale 0) → 위 §2 옵션 B 의 1~3 단계 역순.
- 데이터 손실 가능 시점: dump 후 cutover 까지의 write — 옵션 B 는 maintenance page 필수.

## §6 Open Questions (사용자 결정 필요)

1. **데이터 양** — 현재 mailstory FerretDB 의 데이터 크기?
2. **downtime 허용 윈도우** — 무중단 (옵션 A) vs 시간 단위 (옵션 B)?
3. **mailstory app code 변경 가능?** — 옵션 A 의 dual-write 코드 추가, 옵션 B 의 connection string 변경 — release 가능 ?
4. **cutover 일정** — 사용자가 직접 결정.
5. **operator 의 backup CR 활용** — MongoDBBackup CR 로 PITR 도 cutover 일부?
6. **모니터링** — Prometheus alert (write rate / replication lag) 의 baseline 설정?

## §7 본 Draft 의 진행 단계

1. **현재 (Draft)** — 본 commit. 사용자 결정 대기.
2. **사용자 §2 옵션 선택 + §3 호환성 검증 명령 PASS 확인** — 본 문서 §1 표 + §3 update.
3. **ai-dev/rfcs/NNNN-mailstory-mongodb-cutover.md 승격** — 본 plan 본문 그대로 + 사용자 명시 cutover window.
4. **별 commit chain**: staging 검증 → Day-0 cutover → 검증 → cleanup.

## §8 ADR-0028 Phase B 와의 관계

본 cutover plan 은 **mongodb-operator OLM 운영 적용 (ADR-0028 Phase B) 의 후속**.

Phase B 의 즉시 적용 범위:
- OLM v0.30.0 cluster-wide 설치
- mongodb-operator v1.5.0 CSV 배포
- CRD 등록
- **PoC CR** (database ns 의 `test-mongodb`) reconcile + 정상구동 검증

Phase B *후속* (별 commit chain):
- 본 cutover plan §2 옵션 결정
- mailstory 운영 적용

따라서 **본 turn 의 ADR-0028 Phase B 작업은 mailstory 접촉 없음** — operator 등록 + PoC ns 검증 한정.
