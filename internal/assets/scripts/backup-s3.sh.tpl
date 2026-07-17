#!/bin/bash
# backup-s3.sh.tpl — base 스냅샷을 S3 로 스트리밍 업로드 (PITR 의 *기점*).
#
# ─────────────────────────────────────────────────────────────────────────────
# S3 키 스킴 (restore / uploader 트랙과 공유하는 *계약* — 변경 시 동시 수정)
# ─────────────────────────────────────────────────────────────────────────────
#   s3://${S3_BUCKET}/${BASE_PREFIX}/base.archive.gz   ← 스냅샷 (mongodump --archive --gzip)
#   s3://${S3_BUCKET}/${BASE_PREFIX}/base.meta.json    ← 접합 메타 (아래 스키마)
#
#   BASE_PREFIX = "${S3_PREFIX%/}/${BACKUP_NAME}"  (S3_PREFIX 빈 값이면 "${BACKUP_NAME}")
#   BACKUP_NAME = MongoDBBackup CR 이름 (env — operator 주입)
#
#   구 구현은 "<cluster>-$(date +%Y%m%d-%H%M%S)" 를 *컨테이너 안에서* 만들어
#   operator 도 restore 도 실제 키를 알 수 없었다 (= 복원 불가의 근본 원인).
#   CR 이름은 namespace 내 유일하고 restore 가 SourceBackupName 으로 그대로
#   참조하므로 키를 결정론적으로 계산할 수 있다.
#
#   oplog 세그먼트는 별도 prefix (oplog-stream.sh.tpl 소유):
#     s3://${S3_BUCKET}/${S3_PREFIX%/}/<cluster>/oplog/<startTs>_<endTs>.bson.gz
#   base 를 <cluster>/ 아래 두지 *않는* 이유: restore 는 SourceBackupName 만 알고
#   *소스* 클러스터 이름은 모른다 (restore CR 의 clusterRef 는 복원 *대상*이다).
#   따라서 base 는 backup 이름만으로 찾을 수 있어야 하고, 소스 클러스터 이름은
#   base.meta.json 의 "cluster" 로 넘겨 거기서 oplog prefix 를 유도한다.
#
# ─────────────────────────────────────────────────────────────────────────────
# base.meta.json 스키마 (restore init container + controller 가 파싱)
# ─────────────────────────────────────────────────────────────────────────────
#   {"backup":"<CR 이름>","cluster":"<클러스터>","oplogAnchor":"<sec>:<inc>",
#    "oplogEnd":"<sec>:<inc>","createdAt":"<RFC3339>"}
#
#   **파일 존재 자체가 "이 base 는 --oplog 로 떠졌다"는 신호**다. 없으면 restore
#   는 --oplogReplay 를 붙이지 않고 PITR 요청은 거부한다.
#
#   anchor 와 end 가 둘 다 필요한 이유 — base 의 진짜 일관 시점 T_end 는 archive
#   *안에* 있어 밖에서 읽을 수 없다. 대신 T_end 를 양쪽에서 감싼다:
#
#       oplogAnchor  <=  T_end  <=  oplogEnd
#
#     - oplogAnchor (dump 시작 *직전* head) = 세그먼트 체인 접합 하한.
#       **이르게** 잡아야 안전하다 — 겹치는 구간은 oplog 가 멱등이라 재적용해도
#       무해하지만, 늦게 잡으면 그 사이 write 가 영영 누락된다 (silent gap).
#     - oplogEnd (dump 종료 *직후* head) = 유효 PIT 하한.
#       **늦게** 잡아야 안전하다 — base 는 이미 T_end 까지 반영돼 있으므로 그
#       이전 시점을 PIT 로 요청하면 "요청 시점보다 새 상태" 라는 거짓 복원이
#       된다. restore 가 이 값으로 그런 요청을 거부한다.
set -euo pipefail
export LC_ALL=C

CLUSTER="{{.ClusterName}}"

: "${S3_BUCKET:?S3_BUCKET 필요}"
: "${BACKUP_NAME:?BACKUP_NAME 필요 — operator 가 MongoDBBackup CR 이름을 주입한다}"

# aws CLI 확보 — 공식 mongo 이미지에 없다. 본 Job 은 root 라 설치 가능하다
# (같은 pod 의 non-root 사이드카 / restore init 는 불가 → 그쪽은 다른 경로).
# NOTE: `apt-get install awscli` 는 Ubuntu Noble(mongo:8.x 베이스)에서 실패한다 —
# awscli(v1) 패키지가 저장소에서 제거됐다("has no installation candidate"). 공식
# aws CLI v2 zip 으로 설치한다(arch-aware, oplog-tailer.Dockerfile 과 동일 방식).
if ! command -v aws >/dev/null 2>&1; then
  case "$(uname -m)" in
    x86_64) _awsarch=x86_64 ;;
    aarch64|arm64) _awsarch=aarch64 ;;
    *) echo "unsupported arch $(uname -m) for aws CLI install" >&2; exit 1 ;;
  esac
  apt-get update && apt-get install -y --no-install-recommends curl unzip ca-certificates
  curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${_awsarch}.zip" -o /tmp/awscliv2.zip
  unzip -q /tmp/awscliv2.zip -d /tmp
  /tmp/aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli
  rm -rf /tmp/awscliv2.zip /tmp/aws
fi

# aws CLI 는 S3_REGION 을 읽지 않는다 (AWS_DEFAULT_REGION 을 읽는다).
if [ -n "${S3_REGION:-}" ]; then export AWS_DEFAULT_REGION="${S3_REGION}"; fi

# S3_ENDPOINT 는 optional (실 AWS 면 빈 값) — 빈 값에 --endpoint-url= 를 넘기면
# aws 가 Invalid endpoint 로 죽으므로 조건부로 붙인다.
aws_s3() {
  if [ -n "${S3_ENDPOINT:-}" ]; then
    aws s3 --endpoint-url "${S3_ENDPOINT}" "$@"
  else
    aws s3 "$@"
  fi
}

PFX="${S3_PREFIX:-}"
PFX="${PFX%/}"
if [ -n "${PFX}" ]; then
  BASE_PREFIX="${PFX}/${BACKUP_NAME}"
else
  BASE_PREFIX="${BACKUP_NAME}"
fi

echo "Starting backup: ${BACKUP_NAME} → s3://${S3_BUCKET}/${BASE_PREFIX}/"
{{if .WithOplog}}
# oplog head 를 "<sec>:<inc>" 로 출력. $natural 역스캔이라 O(1).
# 클라이언트측 Timestamp API 편차를 피해 Long 의 high/low 비트에서 (t, i) 를
# 직접 뽑고 uint32 로 보정한다 (oplog-stream.sh.tpl 와 동일 규약).
oplog_head_ts() {
  mongosh "${MONGODB_URI}" --quiet --eval '
const c = db.getSiblingDB("local").oplog.rs.find({}, {ts: 1}).sort({$natural: -1}).limit(1);
if (!c.hasNext()) { quit(3); }
const ts = c.next().ts;
const u32 = function (n) { return n < 0 ? n + 4294967296 : n; };
print(u32(ts.getHighBits()) + ":" + u32(ts.getLowBits()));'
}

OPLOG_ANCHOR=$(oplog_head_ts)
echo "[backup] oplog anchor (dump 직전) = ${OPLOG_ANCHOR}"
{{end}}
# pipefail 필수 — 없으면 mongodump 가 죽어도 파이프의 exit status 는 aws 의
# 것이라 *잘린 아카이브를 올리고 성공을 보고*한다 (침묵 데이터 손실).
mongodump --uri="${MONGODB_URI}" {{.CompressionFlag}} {{if .WithOplog}}--oplog {{end}}--archive \
    | aws_s3 cp - "s3://${S3_BUCKET}/${BASE_PREFIX}/base.archive.gz"
{{if .WithOplog}}
OPLOG_END=$(oplog_head_ts)
echo "[backup] oplog end (dump 직후) = ${OPLOG_END}"

# 한 줄 JSON — restore init container 가 sed 로 파싱한다 (aws-cli 이미지에 jq 가
# 없다). 값은 모두 k8s 이름 / "숫자:숫자" / RFC3339 라 escape 대상이 없다.
printf '{"backup":"%s","cluster":"%s","oplogAnchor":"%s","oplogEnd":"%s","createdAt":"%s"}\n' \
    "${BACKUP_NAME}" "${CLUSTER}" "${OPLOG_ANCHOR}" "${OPLOG_END}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    | aws_s3 cp - "s3://${S3_BUCKET}/${BASE_PREFIX}/base.meta.json"
{{end}}
echo "Backup completed: ${BACKUP_NAME} (s3://${S3_BUCKET}/${BASE_PREFIX}/base.archive.gz)"
