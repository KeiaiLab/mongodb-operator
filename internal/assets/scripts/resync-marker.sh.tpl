#!/bin/sh
# 재동기 표지 (init container, busybox sh). 선택적 ConfigMap <cr>-resync 의
# <pod 이름>: <표지 값> 이 dbpath 에 남은 값과 다르면 dbpath 를 비운다 — mongod 가 빈
# 데이터로 떠 RS 설정의 자기 호스트명으로 피어에서 initial sync 한다.
#   이 pod 의 표지 없음      -> 데이터 그대로, exit 0
#   표지 값 == 남은 값       -> 이미 그 값으로 비웠다, 그대로, exit 0
#   표지 값 != 남은 값       -> dbpath 비움 → 값 기록, exit 0
#   표지가 모호함            -> 아무것도 지우지 않고 exit 1 (pod 이 뜨지 않는다)
# 표지 값 = 한 줄, [A-Za-z0-9._-] 1–128자 (끝 줄바꿈 하나는 허용).
set -eu
MARKERS="${RESYNC_DIR:-/resync}"
DB="${DBPATH:?DBPATH unset}"
POD="${POD_NAME:?POD_NAME unset}"
STAMP="$DB/.keiailab-resync-token"
MARKER="$MARKERS/$POD"

fail() {
  echo "FATAL: resync: $1 — dbpath untouched (fail-closed)" >&2
  exit 1
}

if [ ! -e "$MARKER" ]; then
  echo "resync: no marker for $POD — data kept"
  exit 0
fi
[ -f "$MARKER" ] || fail "marker $MARKER is not a file"
[ -d "$DB" ] || fail "dbpath $DB is not a directory"

WANT=$(cat "$MARKER")
case "$WANT" in
  *"
"*) fail "marker for $POD has more than one line" ;;
esac
printf '%s\n' "$WANT" | grep -Eqx '[A-Za-z0-9._-]{1,128}' || fail "marker for $POD is not a valid value"

HAVE=""
if [ -e "$STAMP" ]; then
  [ -f "$STAMP" ] || fail "$STAMP is not a file"
  HAVE=$(cat "$STAMP")
fi
if [ "$HAVE" = "$WANT" ]; then
  echo "resync: marker $WANT already applied — data kept"
  exit 0
fi

# 비운 뒤에만 값을 쓴다 — 중간에 죽으면 다음 기동이 다시 비운다.
echo "resync: marker $WANT (was '${HAVE}') — emptying $DB"
find "$DB" -mindepth 1 -maxdepth 1 ! -name lost+found -exec rm -rf {} \;
[ -z "$(find "$DB" -mindepth 1 -maxdepth 1 ! -name lost+found)" ] || fail "dbpath $DB not empty after wipe"
printf '%s' "$WANT" > "$STAMP.tmp"
mv "$STAMP.tmp" "$STAMP"
sync
echo "resync: $DB emptied, marker written — mongod will initial-sync"
