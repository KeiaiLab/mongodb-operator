#!/bin/bash
# backup-pvc.sh.tpl — base 스냅샷을 backup PVC 에 디렉터리 덤프.
#
# 레이아웃 (restore 트랙과 공유하는 *계약*):
#   /backup/${BACKUP_NAME}/            ← mongodump --out 디렉터리 덤프
#   /backup/${BACKUP_NAME}/oplog.bson  ← --oplog 시 (dump 중 write 캡처)
#
#   BACKUP_NAME = MongoDBBackup CR 이름 (env — operator 주입).
#   구 구현은 "<cluster>-$(date +%Y%m%d-%H%M%S)" 를 컨테이너 안에서 만들어
#   restore 가 실제 경로를 알 수 없었다. restore Job 은 같은 PVC 를
#   /data/source 에 마운트하고 /data/source/${SOURCE_BACKUP} 을 읽는다.
#
# PITR 미지원 — oplog 세그먼트는 S3 전용이다 (oplog-stream.sh.tpl). PVC 백업은
# base 시점 복원만 가능하며, PITR 요청은 BuildRestoreJob 이 빌드 시점에 거부한다.
set -euo pipefail

: "${BACKUP_NAME:?BACKUP_NAME 필요 — operator 가 MongoDBBackup CR 이름을 주입한다}"

echo "Starting backup: ${BACKUP_NAME} → /backup/${BACKUP_NAME}"
mongodump --uri="${MONGODB_URI}" --out="/backup/${BACKUP_NAME}" {{.CompressionFlag}}{{if .WithOplog}} --oplog{{end}}
echo "Backup completed: ${BACKUP_NAME}"
