#!/bin/bash
# restore-fetch.sh.tpl — restore Job 의 init container (S3 storage 전용).
#
# 책임: base 스냅샷 + PITR 에 필요한 oplog 세그먼트를 S3 에서 받아 EmptyDir
# 에 펼친다. 실제 mongorestore 는 main 컨테이너(restore-replay.sh.tpl) 몫이다.
#
# 산출물 (/data/source, 같은 pod 의 main 컨테이너와 공유하는 EmptyDir):
#   base.archive.gz     ← 필수
#   base.meta.json      ← 있으면 base 가 --oplog 로 떠졌다는 신호 (PITR 가능)
#   oplog/oplog.bson    ← PITR 시. 세그먼트를 시간순 연접한 단일 oplog
#
# 왜 init container 인가: 이미지가 다르다. mongo 이미지에는 aws CLI 가 없고
# restore pod 는 non-root(999) 라 런타임 설치도 불가하다. fetch(aws) 와
# replay(mongorestore) 는 파이프가 아니라 *순차* 단계라 컨테이너를 나눌 수 있고,
# 나누면 각자 필요한 것만 든 이미지를 쓰면서 PSA restricted 를 유지할 수 있다.
#
# 왜 EmptyDir 인가: 스토리지가 Ceph RBD RWO 라 PVC 를 두 pod 가 공유할 수 없다.
# 같은 pod 안의 init ↔ main 공유면 EmptyDir 로 충분하다.
#
# ─────────────────────────────────────────────────────────────────────────────
# oplog 세그먼트 키 계약 (oplog-stream.sh.tpl 이 소유 — 여기서는 *소비*)
# ─────────────────────────────────────────────────────────────────────────────
#   s3://${S3_BUCKET}/${OPLOG_PREFIX}/<startTs>_<endTs>.bson.gz
#   <ts> = printf '%010d-%010d' <sec> <inc>   ← 고정폭 zero-pad
#
#   불변식 (선택 로직이 전적으로 여기 의존한다):
#     - 고정폭 zero-pad → 사전식 정렬 == 시간순 정렬. 그래서 이 스크립트는
#       ts 를 산술 비교하지 않고 *문자열* 비교만 한다 (0 채움 → 8진수 오해석
#       같은 함정 자체가 없다).
#     - startTs = **배타** 하한, endTs = **포함** 상한.
#       세그먼트 내용 = { op | startTs < op.ts <= endTs }
#     - 체인 연속 조건: seg[n].startTs == seg[n-1].endTs
#     - 내용 = gzip 된 **raw BSON 스트림** (mongodump --out=-). 연접 가능해야
#       한다 — mongodump --archive 포맷은 헤더/prelude 가 있어 연접 불가다.
set -euo pipefail
export LC_ALL=C

SRC="{{.SourceDir}}"

# aws CLI 는 쓰기 가능한 HOME 을 원한다 (uid=999 라 이미지 기본 HOME 이 읽기
# 전용일 수 있다). rootfs 는 writable 이므로 /tmp 로 돌린다.
export HOME=/tmp
export TMPDIR=/tmp

: "${S3_BUCKET:?S3_BUCKET 필요}"
: "${SOURCE_BACKUP:?SOURCE_BACKUP 필요 — 소스 MongoDBBackup CR 이름}"

if [ -n "${S3_REGION:-}" ]; then export AWS_DEFAULT_REGION="${S3_REGION}"; fi

aws_s3() {
  if [ -n "${S3_ENDPOINT:-}" ]; then
    aws s3 --endpoint-url "${S3_ENDPOINT}" "$@"
  else
    aws s3 "$@"
  fi
}

log() { echo "[restore-fetch] $*"; }
die() { echo "[restore-fetch] FATAL: $*" >&2; exit 1; }

PFX="${S3_PREFIX:-}"
PFX="${PFX%/}"
if [ -n "${PFX}" ]; then
  BASE_PREFIX="${PFX}/${SOURCE_BACKUP}"
else
  BASE_PREFIX="${SOURCE_BACKUP}"
fi

mkdir -p "${SRC}"

# ── base 스냅샷 ──────────────────────────────────────────────────────────────
log "base 다운로드: s3://${S3_BUCKET}/${BASE_PREFIX}/base.archive.gz"
aws_s3 cp "s3://${S3_BUCKET}/${BASE_PREFIX}/base.archive.gz" "${SRC}/base.archive.gz"

# base.meta.json 은 optional — 없으면 그 백업은 --oplog 없이 떠진 것이다
# (sharded 등). base 시점 복원은 되지만 PITR 기점으로는 쓸 수 없다.
HAS_META=0
if aws_s3 cp "s3://${S3_BUCKET}/${BASE_PREFIX}/base.meta.json" "${SRC}/base.meta.json" 2>/dev/null; then
  HAS_META=1
  log "base.meta.json 확보 — 이 base 는 --oplog 로 떠졌다 (PITR 기점 가능)"
else
  log "base.meta.json 없음 — 이 base 는 --oplog 없이 떠졌다 (base 시점 복원 전용)"
fi

if [ -z "${OPLOG_LIMIT:-}" ]; then
  log "PIT 미지정 — base 스냅샷 시점으로만 복원한다. 완료."
  exit 0
fi

# ── 여기서부터 PITR ──────────────────────────────────────────────────────────
if [ "${HAS_META}" != "1" ]; then
  die "PIT(${OPLOG_LIMIT}) 를 요청했으나 ${SOURCE_BACKUP} 에 base.meta.json 이 없다.
  이 백업은 --oplog 없이 떠져 oplog 접합점을 모른다 → PITR 기점으로 쓸 수 없다.
  PITREnabled=true 인 ReplicaSet 클러스터에서 새로 뜬 백업을 소스로 지정하라."
fi

# 한 줄 JSON 을 sed 로 판다 (aws-cli 이미지에 jq 가 없다). 작성자는 형제
# 템플릿 backup-s3.sh.tpl 이고 값에 escape 대상이 없음이 보장된다.
meta_get() {
  sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${SRC}/base.meta.json"
}

CLUSTER=$(meta_get cluster)
ANCHOR=$(meta_get oplogAnchor)
OPLOG_END=$(meta_get oplogEnd)
[ -n "${CLUSTER}" ] || die "base.meta.json 에 cluster 없음"
[ -n "${ANCHOR}" ] || die "base.meta.json 에 oplogAnchor 없음"
[ -n "${OPLOG_END}" ] || die "base.meta.json 에 oplogEnd 없음"

# "<sec>:<inc>" → 키 토큰 "%010d-%010d". 10# 은 "007" 같은 입력이 8진수로
# 오해석되는 것을 막는다 (CRD 패턴 ^[0-9]+:[0-9]+$ 는 0 채움을 허용한다).
tok() { printf '%010d-%010d' "$(( 10#${1%%:*} ))" "$(( 10#${1##*:} ))"; }

LIMIT_TOK=$(tok "${OPLOG_LIMIT}")
ANCHOR_TOK=$(tok "${ANCHOR}")
END_TOK_BASE=$(tok "${OPLOG_END}")

log "cluster=${CLUSTER} anchor=${ANCHOR} oplogEnd=${OPLOG_END} limit=${OPLOG_LIMIT}"

# PIT 이 base 의 일관 시점 이전이면 거부한다. base 는 이미 T_end 까지 반영돼
# 있어서 그보다 이른 PIT 를 "복원" 하면 요청보다 새 상태가 나온다 (거짓 복원).
# oplogEnd >= T_end 이므로 limit > oplogEnd 를 요구하는 것이 안전한 판정이다.
if ! [[ "${LIMIT_TOK}" > "${END_TOK_BASE}" ]]; then
  die "PIT(${OPLOG_LIMIT}) 가 base 스냅샷의 일관 시점(${OPLOG_END}) 이후가 아니다.
  base 는 이미 그 시점까지 반영돼 있어 더 이른 시점으로는 되돌릴 수 없다.
  더 이른 PIT 가 필요하면 그 시점 *이전* 에 뜬 base 백업을 소스로 지정하라."
fi

if [ -n "${PFX}" ]; then
  OPLOG_PREFIX="${PFX}/${CLUSTER}/oplog"
else
  OPLOG_PREFIX="${CLUSTER}/oplog"
fi
log "oplog 세그먼트 스캔: s3://${S3_BUCKET}/${OPLOG_PREFIX}/"

SEGS=$(aws_s3 ls "s3://${S3_BUCKET}/${OPLOG_PREFIX}/" 2>/dev/null \
  | awk '{print $4}' \
  | grep -E '^[0-9]{10}-[0-9]{10}_[0-9]{10}-[0-9]{10}\.bson\.gz$' \
  | sort || true)
[ -n "${SEGS}" ] || die "oplog 세그먼트가 하나도 없다 (s3://${S3_BUCKET}/${OPLOG_PREFIX}/).
  이 클러스터에 PITR tailer 사이드카가 돌지 않았다 (BackupSpec.PITREnabled)."

# gzip 확보 — 세그먼트(.bson.gz)를 풀어 oplog.bson 으로 연접하는 데 필요하다.
# 여기 도달 = oplog 세그먼트가 있는 PITR 복원. fetch 컨테이너는 이 경우
# OPLOG_TAILER_IMAGE(mongo 베이스 = gzip 내장 + aws)로 뜨므로(builder_backup.go
# BuildRestoreJob) gzip 이 있어야 정상이다. 런타임 설치는 안 한다 — 클러스터
# egress 정책에 막혀 취약하기 때문(라이브 실측). 없으면 이미지 배선 오류이니
# 명확히 fail-closed 한다.
command -v gzip >/dev/null 2>&1 || die "gzip 부재 — PITR 복원의 fetch 컨테이너는
  aws + gzip 을 모두 갖춘 이미지여야 한다. operator Deployment 에
  OPLOG_TAILER_IMAGE(mongodump/mongosh/aws 통합 이미지)를 설정하라 —
  amazon/aws-cli 폴백엔 gzip 이 없어 oplog 세그먼트를 풀 수 없다."

mkdir -p "${SRC}/oplog"
: > "${SRC}/oplog/oplog.bson"

PREV_END=""
FIRST_START=""
LAST_END=""
COUNT=0
for NAME in ${SEGS}; do
  BASE="${NAME%.bson.gz}"
  START_TOK="${BASE%%_*}"
  END_TOK="${BASE#*_}"

  # 내용 = { op | startTs < op.ts <= endTs } 이므로
  #   - startTs >= limit  → 전부 limit 이상 → 쓸모 없음 (limit 은 배타)
  #   - endTs   <= anchor → 전부 base 에 이미 반영 → 불필요
  if ! [[ "${START_TOK}" < "${LIMIT_TOK}" ]]; then continue; fi
  if ! [[ "${END_TOK}" > "${ANCHOR_TOK}" ]]; then continue; fi

  if [ -z "${FIRST_START}" ]; then
    FIRST_START="${START_TOK}"
  elif [ "${START_TOK}" != "${PREV_END}" ]; then
    # 계약상 seg[n].startTs == seg[n-1].endTs. 어긋나면 그 구간 oplog 는
    # 영영 없다 (capped 덮어씀 / 업로드 실패). 이어 붙이면 조용히 구멍 뚫린
    # 복원이 되므로 거부한다.
    die "oplog 체인에 gap: ${PREV_END} → ${START_TOK} (${NAME}).
  그 구간 oplog 는 유실됐다 → 요청 PIT 까지 정합 복원 불가."
  fi

  log "  + ${NAME}"
  aws_s3 cp "s3://${S3_BUCKET}/${OPLOG_PREFIX}/${NAME}" "${SRC}/oplog/seg.bson.gz" --quiet
  # 시간순 연접 — raw BSON 스트림이라 이어 붙이면 그대로 단일 oplog.bson 이다.
  gzip -dc "${SRC}/oplog/seg.bson.gz" >> "${SRC}/oplog/oplog.bson"
  rm -f "${SRC}/oplog/seg.bson.gz"

  PREV_END="${END_TOK}"
  LAST_END="${END_TOK}"
  COUNT=$(( COUNT + 1 ))
done

[ "${COUNT}" -gt 0 ] || die "PIT(${OPLOG_LIMIT}) 구간을 덮는 oplog 세그먼트가 없다."

# base 접합 검사 — 첫 세그먼트가 anchor 이전에서 시작해야 base 와 겹친다.
# (anchor <= T_end 이므로 startTs <= anchor 면 startTs <= T_end 가 보장된다.)
# 늦게 시작하면 base 종료 ~ 첫 세그먼트 사이가 통째로 빈다.
if ! [[ "${FIRST_START}" < "${ANCHOR_TOK}" || "${FIRST_START}" == "${ANCHOR_TOK}" ]]; then
  die "첫 oplog 세그먼트가 base 이후에서 시작한다 (start=${FIRST_START} > anchor=${ANCHOR_TOK}).
  base 스냅샷과 oplog 체인 사이가 비어 있다 → 정합 복원 불가.
  (base 백업보다 PITR tailer 가 늦게 떴을 때 발생한다.)"
fi

# 도달 검사 — 체인이 PIT 까지 닿아야 한다. 못 닿으면 조용히 더 이른 시점으로
# 복원되므로(요청 위반) 거부한다. 클러스터가 그냥 놀아서 세그먼트가 없는
# 경우와 업로드가 밀린 경우를 구분할 수 없으니 안전한 쪽으로 실패한다.
if ! [[ "${LAST_END}" > "${LIMIT_TOK}" || "${LAST_END}" == "${LIMIT_TOK}" ]]; then
  die "oplog 체인이 PIT(${OPLOG_LIMIT}) 에 닿지 못한다 (마지막 세그먼트 end=${LAST_END}).
  아직 업로드되지 않았거나 그 시점 이후 write 가 없다.
  복원 가능 상한은 MongoDBBackup.status.latestRestore 를 확인하라."
fi

log "세그먼트 ${COUNT} 개 연접 완료 → ${SRC}/oplog/oplog.bson ($(wc -c < "${SRC}/oplog/oplog.bson") bytes)"
log "체인 ${FIRST_START} → ${LAST_END} (limit=${LIMIT_TOK} 배타)"
