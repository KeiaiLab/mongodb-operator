#!/bin/bash
# preStop stepDown (무중단 업그레이드) — pod 종료(RollingUpdate 등) 직전 자기 mongod가
# PRIMARY면 rs.stepDown()으로 secondary에 primary 이양 → election 끊김(~10s) 회피.
# PRIMARY가 아니면 no-op. **모든 에러를 무시**한다 — preStop 실패가 pod 정상 종료를
# 막으면 안 되므로(graceful shutdown 우선). 항상 exit 0.
set +e
PORT="${MONGO_PORT:-{{.Port}}}"

# admin 인증(auth 활성 클러스터). password 없으면 무인증 시도(부트스트랩 전 단계).
PW=""
if [ -f /etc/mongodb-admin/password ]; then
  PW="$(cat /etc/mongodb-admin/password 2>/dev/null | tr -d '\n')"
fi
AUTH_ARGS=""
if [ -n "$PW" ]; then
  AUTH_ARGS="-u admin -p $PW --authenticationDatabase admin"
fi

# 자기 mongod가 PRIMARY인지 확인. 아니면 stepDown 불필요(exit 0).
# shellcheck disable=SC2086
IS_PRIMARY=$(mongosh --quiet --host 127.0.0.1 --port "$PORT" $AUTH_ARGS \
  --eval 'try{db.adminCommand({hello:1}).isWritablePrimary}catch(e){print("false")}' 2>/dev/null || echo "false")

if [ "$IS_PRIMARY" != "true" ]; then
  echo "prestop: not primary (isWritablePrimary=$IS_PRIMARY) — stepDown 불필요"
  exit 0
fi

# PRIMARY → stepDown. stepDown(stepDownSecs=60, secondaryCatchUpPeriodSecs=10):
# 60초간 primary 재선출 금지(이 pod 종료 동안) + 10초간 secondary 동기 대기 후 이양.
# stepDown은 연결을 끊으므로 자체 에러는 정상(무시).
echo "prestop: PRIMARY 감지 → rs.stepDown(60) 으로 primary 이양 시도"
# shellcheck disable=SC2086
mongosh --quiet --host 127.0.0.1 --port "$PORT" $AUTH_ARGS \
  --eval 'try{rs.stepDown(60,10);print("stepDown OK")}catch(e){print("stepDown returned (expected on disconnect): "+e.message)}' 2>/dev/null || true

# stepDown 후 새 primary 선출 시간 확보(graceful). terminationGracePeriod 내.
sleep 3
echo "prestop: stepDown 완료"
exit 0
