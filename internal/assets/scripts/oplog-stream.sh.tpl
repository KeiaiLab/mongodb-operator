#!/bin/bash
# oplog-stream.sh.tpl — PITR oplog 증분 스트리밍 tailer (아키텍처 A / PBM 방식).
#
# 구 구현(30s 마다 oplog.rs 전량 재덤프 → EmptyDir)의 3 결함을 제거:
#   1. 전량 재덤프 → {ts: {$gt: HWM}} 증분 쿼리
#   2. EmptyDir 경유(pod 재시작 = 유실) → S3 직접 스트리밍 (capture+upload 원자)
#   3. HWM 상태 저장소 부재 → S3 최신 세그먼트 키의 endTs 에서 부팅 시 복원
#
# ─────────────────────────────────────────────────────────────────────────────
# S3 키 스킴 (restore / uploader 트랙과 공유하는 *계약* — 변경 시 3 트랙 동시 수정)
# ─────────────────────────────────────────────────────────────────────────────
#   s3://${S3_BUCKET}/${OPLOG_PREFIX}/<startTs>_<endTs>.bson.gz
#
#   OPLOG_PREFIX = "${S3_PREFIX%/}/<cluster>/oplog"  (S3_PREFIX 빈 값이면 "<cluster>/oplog")
#   <ts>         = printf '%010d-%010d' <sec> <inc>   ← BSON Timestamp 의 (t, i)
#
#   예) rs0/oplog/1752710400-0000000001_1752710430-0000000012.bson.gz
#
#   불변식:
#     - 고정폭 zero-pad → 사전식(lexicographic) 정렬 == 시간순 정렬.
#       (`aws s3 ls | sort | tail -1` = 최신 세그먼트)
#     - inc 는 uint32 → 10 자리. sec 도 10 자리(9999999999 = 서기 2286).
#     - startTs = **배타(exclusive)** 하한, endTs = **포함(inclusive)** 상한.
#       세그먼트 내용 = { op | startTs < op.ts <= endTs }
#     - 체인 연속 조건: seg[n].startTs == seg[n-1].endTs
#       불일치 = gap (그 구간 oplog 는 영영 없음 — uploader 가 window 계산 시 감지)
#     - **동일 startTs 가 복수면 endTs 가 가장 큰 것이 진본** (restore/uploader 필독):
#       업로드 실패 시 상류(mongodump)가 죽어도 aws 가 받은 조각으로 업로드를
#       완료해 잘린 객체가 남을 수 있다. 실패 경로에서 best-effort 로 지우지만
#       pod kill 등으로 잔존 가능. 재시도는 HWM 이 그대로라 *같은 startTs 에 더
#       넓은 endTs* 로 쓰므로, max endTs = 마지막 성공분 = 유일하게 온전한 조각.
#
# ─────────────────────────────────────────────────────────────────────────────
# 단일 writer 보장
# ─────────────────────────────────────────────────────────────────────────────
#   사이드카는 RS 의 *모든* member pod 에 배치되지만, 세그먼트를 쓰는 것은
#   **PRIMARY 1 개뿐**이다 (매 batch 마다 db.hello().isWritablePrimary 확인).
#   secondary 는 idle. failover 시 새 primary 가 S3 에서 HWM 을 읽어 이어받는다.
#   → 동일 prefix 에 N 개 tailer 가 중복/충돌 세그먼트를 쓰는 사고를 차단.
set -euo pipefail

PORT={{.Port}}
BATCH_SECONDS={{.BatchSeconds}}
CLUSTER="{{.ClusterName}}"
WORK_DIR="{{.WorkDir}}"

# aws CLI / mongodump 가 쓸 수 있는 HOME·TMPDIR (컨테이너 uid=999 non-root).
export HOME="${WORK_DIR}"
export TMPDIR="${WORK_DIR}"

: "${S3_BUCKET:?S3_BUCKET 필요 — PITR oplog tailer 는 S3 storage 전용}"
if [ -n "${S3_REGION:-}" ]; then export AWS_DEFAULT_REGION="${S3_REGION}"; fi

PFX="${S3_PREFIX:-}"
PFX="${PFX%/}"
if [ -n "${PFX}" ]; then
  OPLOG_PREFIX="${PFX}/${CLUSTER}/oplog"
else
  OPLOG_PREFIX="${CLUSTER}/oplog"
fi

log() { echo "[oplog-tailer] $*"; }

# ── aws CLI 확보 ─────────────────────────────────────────────────────────────
# 공식 mongo 이미지에는 aws CLI 가 없다. backup Job 은 root 라 apt-get 으로
# 설치하지만, 본 사이드카는 mongod 와 같은 pod 의 non-root(999) 라 설치 불가다.
# 업로드 불가 상태로 계속 도는 것이 최악(= silent gap: 복원 window 를 주장하나
# 세그먼트가 없음)이므로 **즉시 실패**한다.
ensure_aws() {
  if command -v aws >/dev/null 2>&1; then return 0; fi
  if [ "$(id -u)" = "0" ]; then
    # apt-get install awscli 는 Ubuntu Noble 에서 실패(패키지 제거됨) →
    # aws CLI v2 zip (backup-s3.sh.tpl 와 동일 경로). 단 tailer 는 보통
    # 비-root 사이드카라 이 경로는 거의 안 타고, OPLOG_TAILER_IMAGE(mongodump+
    # aws 내장)를 쓰는 게 정상 경로다.
    log "aws CLI 부재 — aws v2 zip 설치 (root fallback)"
    case "$(uname -m)" in
      x86_64) _awsarch=x86_64 ;;
      aarch64|arm64) _awsarch=aarch64 ;;
      *) log "FATAL: unsupported arch $(uname -m)"; exit 1 ;;
    esac
    apt-get update && apt-get install -y --no-install-recommends curl unzip ca-certificates
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${_awsarch}.zip" -o /tmp/awscliv2.zip
    unzip -q /tmp/awscliv2.zip -d /tmp
    /tmp/aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli
    rm -rf /tmp/awscliv2.zip /tmp/aws
    return 0
  fi
  log "FATAL: aws CLI 부재 + non-root(uid=$(id -u)) → 런타임 설치 불가"
  log "  PITR tailer 이미지는 mongodump + mongosh + aws CLI 를 모두 제공해야 한다."
  log "  operator Deployment 에 OPLOG_TAILER_IMAGE=<image> 를 지정하라."
  log "  (침묵 gap 방지 — 업로드 못 하면서 도는 대신 즉시 죽는다)"
  exit 1
}

aws_s3() {
  if [ -n "${S3_ENDPOINT:-}" ]; then
    aws s3 --endpoint-url "${S3_ENDPOINT}" "$@"
  else
    aws s3 "$@"
  fi
}

# ── mongod 접속 ──────────────────────────────────────────────────────────────
PASS_FILE=/etc/mongodb-admin/password
ADMIN_USER=admin
if [ -f "${PASS_FILE}" ]; then
  ADMIN_PASS=$(cat "${PASS_FILE}")
else
  ADMIN_PASS=""
fi
# 인증 미사용 클러스터에 -u/-p 를 넘기면 오히려 실패하므로 조건부 구성.
AUTH_ARGS=()
if [ -n "${ADMIN_PASS}" ]; then
  AUTH_ARGS=(-u "${ADMIN_USER}" -p "${ADMIN_PASS}" --authenticationDatabase admin)
fi
# ${arr[@]+"${arr[@]}"} — set -u 하에서 *빈 배열* 전개가 bash<4.4 에서 unbound
# variable 로 죽는 것을 피하는 표준 관용구. 베이스 이미지의 bash 버전을 가정하지
# 않는다 (OPLOG_TAILER_IMAGE 로 임의 이미지가 주입될 수 있으므로).
mongo_eval() {
  mongosh --quiet --port "${PORT}" ${AUTH_ARGS[@]+"${AUTH_ARGS[@]}"} --eval "$1"
}

is_primary() {
  [ "$(mongo_eval 'print(db.hello().isWritablePrimary === true)' 2>/dev/null || echo false)" = "true" ]
}

# oplog_edge_ts <dir> — dir=-1 최신 / dir=1 최초 entry 의 ts 를 "<sec> <inc>" 출력.
# $natural 역스캔이라 O(1). 클라이언트측 Timestamp API 편차를 피해 Long 의
# high/low 비트로 (t, i) 를 직접 뽑고 uint32 로 보정한다.
oplog_edge_ts() {
  mongo_eval 'const c = db.getSiblingDB("local").oplog.rs.find({}, {ts: 1}).sort({$natural: '"$1"'}).limit(1);
if (!c.hasNext()) { quit(3); }
const ts = c.next().ts;
const u32 = function (n) { return n < 0 ? n + 4294967296 : n; };
print(u32(ts.getHighBits()) + " " + u32(ts.getLowBits()));'
}

# ── ts 유틸 ──────────────────────────────────────────────────────────────────
ts_key() { printf '%010d-%010d' "$1" "$2"; }

# ts_lt <s1> <i1> <s2> <i2> — (s1,i1) < (s2,i2) 이면 0.
ts_lt() {
  if [ "$1" -lt "$3" ]; then return 0; fi
  if [ "$1" -gt "$3" ]; then return 1; fi
  [ "$2" -lt "$4" ]
}

# ts_prev <sec> <inc> — HWM 을 (sec,inc) 바로 *직전* 으로 세팅.
# $gt HWM 쿼리가 (sec,inc) 자기 자신을 포함하도록 만들기 위함.
ts_prev() {
  if [ "$2" -gt 0 ]; then
    HWM_SEC="$1"; HWM_INC=$(( $2 - 1 ))
  else
    HWM_SEC=$(( $1 - 1 )); HWM_INC=4294967295
  fi
}

# ── HWM 복원 (별도 상태 저장소 0 — S3 가 진본) ───────────────────────────────
s3_latest_segment() {
  aws_s3 ls "s3://${S3_BUCKET}/${OPLOG_PREFIX}/" 2>/dev/null \
    | awk '{print $4}' \
    | grep -E '^[0-9]{10}-[0-9]{10}_[0-9]{10}-[0-9]{10}\.bson\.gz$' \
    | sort | tail -1
}

restore_hwm() {
  local latest base end
  latest=$(s3_latest_segment || true)
  if [ -z "${latest}" ]; then
    return 1
  fi
  base="${latest%.bson.gz}"
  end="${base#*_}"
  HWM_SEC=$(( 10#${end%%-*} ))
  HWM_INC=$(( 10#${end#*-} ))
  log "HWM 복원 (S3 최신 세그먼트 ${latest}) → $(ts_key "${HWM_SEC}" "${HWM_INC}")"
  return 0
}

# ── batch 업로드 (capture+upload 원자) ───────────────────────────────────────
# mongodump → gzip → aws s3 cp - 를 한 파이프로 흘린다. pipefail 로 어느 단계가
# 죽어도 실패 처리 → HWM 전진 금지 → 다음 사이클에 같은 구간 재시도 (gap 방지).
# 중간 파일이 없으므로 pod 재시작 유실창도 없다.
#
# `--out=-` = raw BSON to stdout (단일 컬렉션 덤프라 성립). `--archive` 가
# *아닌* 이유: restore-fetch 가 세그먼트들을 gunzip 해 하나의 oplog.bson 으로
# 연접한 뒤 mongorestore --oplogReplay --dir 로 replay 한다. archive 포맷은
# prelude/헤더가 붙어 연접하면 깨지고 --dir 로 읽히지도 않는다.
upload_batch() {
  local start_key end_key key query
  start_key=$(ts_key "${HWM_SEC}" "${HWM_INC}")
  end_key=$(ts_key "${NOW_SEC}" "${NOW_INC}")
  key="s3://${S3_BUCKET}/${OPLOG_PREFIX}/${start_key}_${end_key}.bson.gz"
  # $lte 상한 고정이 필수 — 덤프 중 들어온 write 가 세그먼트에 섞이면 endTs 가
  # 내용을 과소 표기하고, 다음 배치가 그 구간을 재덤프해 중복 replay 가 된다.
  query=$(printf '{"ts": {"$gt": {"$timestamp": {"t": %d, "i": %d}}, "$lte": {"$timestamp": {"t": %d, "i": %d}}}}' \
    "${HWM_SEC}" "${HWM_INC}" "${NOW_SEC}" "${NOW_INC}")
  log "batch ${start_key} → ${end_key}"
  if mongodump --port "${PORT}" ${AUTH_ARGS[@]+"${AUTH_ARGS[@]}"} \
      --db=local --collection=oplog.rs \
      --query="${query}" \
      --quiet --out=- \
      | gzip -c \
      | aws_s3 cp - "${key}"; then
    HWM_SEC="${NOW_SEC}"; HWM_INC="${NOW_INC}"
    log "uploaded ${key}"
    return 0
  fi
  # 파이프 상류(mongodump)가 죽어도 aws 는 이미 받은 조각으로 업로드를 *완료*할
  # 수 있다 → 잘린 객체가 S3 에 남는다. HWM 은 그대로라 다음 재시도는 같은
  # startTs 에 더 넓은 endTs 로 쓰므로, 지우지 않으면 startTs 가 같고 endTs 만
  # 짧은 잘린 세그먼트가 쌓여 restore 가 그걸 집을 수 있다. best-effort 로 제거.
  log "batch 업로드 실패 — HWM 유지(${start_key}), 다음 사이클 재시도: ${key}"
  aws_s3 rm "${key}" >/dev/null 2>&1 || true
  return 1
}

# ── gap 감지 ─────────────────────────────────────────────────────────────────
# oplog 는 capped — HWM 이 현재 oplog 의 최초 entry 보다 오래됐다면 그 사이
# 구간은 이미 덮어써져 영영 없다 (pod 장기 다운 / 업로드 지연 / 쓰기 폭주).
# HWM 을 그대로 두면 다음 세그먼트 키가 startTs=HWM 이라 *연속인 척* 하는
# 침묵 gap 이 된다 → HWM 을 실제 최초 entry 직전으로 rewind 해서 키 체인에
# 구멍이 드러나게 만든다 (seg[n].startTs != seg[n-1].endTs).
check_gap() {
  local old old_sec old_inc
  old=$(oplog_edge_ts 1) || return 0
  old_sec="${old%% *}"; old_inc="${old##* }"
  if ts_lt "${HWM_SEC}" "${HWM_INC}" "${old_sec}" "${old_inc}"; then
    log "GAP DETECTED: HWM=$(ts_key "${HWM_SEC}" "${HWM_INC}") < oplog 최초 entry=$(ts_key "${old_sec}" "${old_inc}")"
    log "  → 그 사이 oplog 는 이미 덮어써짐. 해당 구간 PITR 불가."
    log "  → HWM 을 최초 entry 직전으로 rewind — 키 체인에 gap 이 그대로 노출된다."
    ts_prev "${old_sec}" "${old_inc}"
  fi
}

# ── main ─────────────────────────────────────────────────────────────────────
ensure_aws

until mongo_eval 'db.adminCommand({ping: 1})' >/dev/null 2>&1; do
  log "waiting for mongod (port ${PORT})..."
  sleep 5
done
log "mongod ready — cluster=${CLUSTER} prefix=s3://${S3_BUCKET}/${OPLOG_PREFIX} batch=${BATCH_SECONDS}s"

HWM_SEC=0
HWM_INC=0
if ! restore_hwm; then
  # 최초 부팅 — S3 에 세그먼트가 없다. 현재 oplog 최신 ts 부터 시작한다.
  # (base 백업이 이 시점보다 앞서면 그 사이가 gap 인데, 그 판정은 base 의
  #  OplogStart 와 첫 세그먼트 startTs 를 비교하는 uploader/restore 몫이다.)
  until NOW=$(oplog_edge_ts -1); do
    log "oplog 최신 ts 조회 실패 — 재시도"
    sleep 5
  done
  HWM_SEC="${NOW%% *}"; HWM_INC="${NOW##* }"
  log "S3 세그먼트 없음 (최초 부팅) → 현재 oplog 최신 ts 부터 시작: $(ts_key "${HWM_SEC}" "${HWM_INC}")"
fi

while true; do
  if is_primary; then
    if NOW=$(oplog_edge_ts -1); then
      NOW_SEC="${NOW%% *}"; NOW_INC="${NOW##* }"
      check_gap
      if ts_lt "${HWM_SEC}" "${HWM_INC}" "${NOW_SEC}" "${NOW_INC}"; then
        upload_batch || true
      else
        log "신규 oplog entry 없음 — skip (HWM=$(ts_key "${HWM_SEC}" "${HWM_INC}"))"
      fi
    else
      log "oplog 최신 ts 조회 실패 — 다음 사이클 재시도"
    fi
  else
    log "not PRIMARY — skip batch (세그먼트는 primary 만 쓴다)"
  fi
  sleep "${BATCH_SECONDS}"
done
