#!/bin/bash
set -eu
PORT="${MONGO_PORT:-{{.Port}}}"
RS_NAME="${MONGO_REPLSET:-}"
MEMBERS="${MONGO_MEMBERS:-}"
CONFIGSVR_FLAG="${MONGO_CONFIGSVR:-}"

# mongod이 응답할 때까지 최대 120초 대기 (60회 × 2초).
for i in $(seq 1 60); do
  if mongosh --quiet --host 127.0.0.1 --port "$PORT" --eval "db.adminCommand('ping').ok" > /dev/null 2>&1; then
    break
  fi
  sleep 2
done

ORDINAL="${HOSTNAME##*-}"
if [ "$ORDINAL" != "0" ]; then
  echo "ordinal=$ORDINAL — bootstrap is ordinal-0 only, skipping"
  exit 0
fi

# RS init 상태 확인. 본 스크립트는 *최초 부트스트랩 1회용*.
# 이미 초기화된 RS 위에서 WP 대기 / createUser 를 실행하면 ordinal-0 가 SECONDARY 로
# 재부팅된 cycle (cfg-1/2 가 PRIMARY holding) 에서 NotWritablePrimary(10107) /
# Unauthorized(13) 로 createUser 가 실패 → quit(3) → kubelet FailedPostStartHook →
# 영구 CrashLoopBackOff. v1.4.4 까지 createUser 가 first-init 가드 밖에서 실행됐던
# 결함 (v1.4.5 fix).
RS_OK=$(mongosh --quiet --host 127.0.0.1 --port "$PORT" --eval 'try{rs.status().ok}catch(e){if(e.code===94){print("init")}else{print("err:"+e.code)}}' 2>/dev/null || echo "err:dial")
if [ "$RS_OK" != "init" ]; then
  echo "RS already initialized (RS_OK=$RS_OK) — first-init bootstrap skipped, exit 0"
  exit 0
fi

if [ -z "$RS_NAME" ] || [ -z "$MEMBERS" ]; then
  echo "FATAL: MONGO_REPLSET or MONGO_MEMBERS unset" >&2
  exit 1
fi
echo "rs.initiate: replSet=$RS_NAME members=$MEMBERS configsvr=$CONFIGSVR_FLAG"
# JS literal 안에 환경변수를 안전하게 주입 (process.env 사용).
RS_NAME="$RS_NAME" MEMBERS="$MEMBERS" CONFIGSVR_FLAG="$CONFIGSVR_FLAG" \
  mongosh --quiet --host 127.0.0.1 --port "$PORT" <<'EOF'
const rsName = process.env.RS_NAME;
const members = process.env.MEMBERS.split(',').map((host, i) => ({ _id: i, host: host.trim() }));
const cfg = { _id: rsName, members: members };
if (process.env.CONFIGSVR_FLAG === 'true') { cfg.configsvr = true; }
const r = rs.initiate(cfg);
if (r.ok !== 1) { print('rs.initiate FAILED: ' + JSON.stringify(r)); quit(2); }
print('rs.initiate OK');
EOF

# PRIMARY 대기 (writable). 최초 init 직후이므로 ordinal-0 가 PRIMARY 로 등극해야 함.
for i in $(seq 1 60); do
  WP=$(mongosh --quiet --host 127.0.0.1 --port "$PORT" --eval 'db.adminCommand({hello:1}).isWritablePrimary' 2>/dev/null || echo "")
  [ "$WP" = "true" ] && break
  sleep 2
done

# createUser (idempotent — UserAlreadyExists/DuplicateKey 에러는 무시).
mongosh --quiet --host 127.0.0.1 --port "$PORT" admin <<'EOF'
const fs = require('fs');
const pw = fs.readFileSync('/etc/mongodb-admin/password', 'utf8').trim();
try {
  db.createUser({ user: 'admin', pwd: pw, roles: [{ role: 'root', db: 'admin' }] });
  print('createUser OK');
} catch (e) {
  if (e.code === 11000 || e.code === 51003 || /already exists/.test(e.message || '')) {
    print('createUser: already exists, idempotent skip');
  } else {
    print('createUser FAILED: ' + e.message);
    quit(3);
  }
}
EOF
echo "bootstrap complete"
