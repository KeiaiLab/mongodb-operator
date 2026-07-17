#!/bin/bash
# restore-replay.sh.tpl — restore Job 의 main 컨테이너 (mongorestore).
#
# 2 단계로 복원한다:
#   1. base  — mongodump 스냅샷 + archive 에 임베드된 oplog 를 replay 해서
#              dump 중 들어온 write 까지 반영 (= 일관 스냅샷).
#   2. oplog — init container 가 S3 세그먼트를 이어 붙인 oplog.bson 을
#              --oplogLimit 까지 replay (= PITR). PIT 미지정이면 생략.
#
# 입력은 /data/source (S3 면 init container 가 채운 EmptyDir, PVC 면 백업 PVC).
#
# --oplogLimit 은 **배타**다 — "newer than or equal to" 인 entry 를 적용하지
# 않는다. 값 계산은 bash 가 아니라 Go(resources.OplogLimitArg)가 하고 여기엔
# 이미 "<sec>:<ordinal>" 로 도착한다 (구 구현의 `date -d` + ordinal 0 고정
# 부정확성 제거).
set -euo pipefail

SRC="{{.SourceDir}}"

: "${MONGODB_URI:?MONGODB_URI 필요}"
: "${SOURCE_BACKUP:?SOURCE_BACKUP 필요}"
STORAGE_TYPE="${STORAGE_TYPE:-pvc}"

log() { echo "[restore] $*"; }
die() { echo "[restore] FATAL: $*" >&2; exit 1; }

log "source=${SOURCE_BACKUP} storage=${STORAGE_TYPE} oplogLimit=${OPLOG_LIMIT:-none}"

# ── 1. base ──────────────────────────────────────────────────────────────────
BASE_ARGS=()
if [ "${STORAGE_TYPE}" = "s3" ]; then
  [ -f "${SRC}/base.archive.gz" ] || die "base 아카이브 없음: ${SRC}/base.archive.gz (init container 실패?)"
  BASE_ARGS=(--archive="${SRC}/base.archive.gz" --gzip)
  # base.meta.json 존재 = 이 archive 에 oplog.bson 이 임베드돼 있다
  # (backup-s3.sh.tpl 이 --oplog 로 떴을 때만 meta 를 올린다).
  if [ -f "${SRC}/base.meta.json" ]; then
    BASE_ARGS+=(--oplogReplay)
  else
    log "base.meta.json 없음 — --oplog 없이 뜬 base 라 --oplogReplay 생략"
  fi
else
  DUMP_DIR="${SRC}/${SOURCE_BACKUP}"
  [ -d "${DUMP_DIR}" ] || die "덤프 디렉터리 없음: ${DUMP_DIR} (backup PVC 가 마운트됐는가?)"
  BASE_ARGS=(--dir "${DUMP_DIR}" --gzip)
  if [ -f "${DUMP_DIR}/oplog.bson" ]; then
    BASE_ARGS+=(--oplogReplay)
  else
    log "oplog.bson 없음 — --oplog 없이 뜬 base 라 --oplogReplay 생략"
  fi
fi

log "base 복원 시작"
mongorestore --uri "${MONGODB_URI}" --drop "${BASE_ARGS[@]}"
log "base 복원 완료"

# ── 2. oplog (PITR) ──────────────────────────────────────────────────────────
# --drop 금지 — 방금 복원한 컬렉션을 지우게 된다. oplog 는 그 위에 이어 적용한다.
# --dir 아래 oplog.bson 하나만 두면 mongorestore 가 그것을 oplog 로 인식한다.
if [ -s "${SRC}/oplog/oplog.bson" ]; then
  [ -n "${OPLOG_LIMIT:-}" ] || die "oplog.bson 이 있는데 OPLOG_LIMIT 이 비었다 (배관 오류)"
  log "oplog replay 시작 (limit=${OPLOG_LIMIT} 배타)"
  mongorestore --uri "${MONGODB_URI}" \
      --oplogReplay --oplogLimit="${OPLOG_LIMIT}" \
      --dir "${SRC}/oplog"
  log "oplog replay 완료 — PIT ${OPLOG_LIMIT} 직전 상태"
else
  log "oplog 세그먼트 없음 — base 시점 복원으로 종료"
fi

log "completed"
