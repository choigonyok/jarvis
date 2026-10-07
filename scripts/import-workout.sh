#!/bin/sh
# 볼륨에 남아 있는 workout.json 을 workout-svc 로 옮긴다.
#
# 한 번만 돌리면 되고, 여러 번 돌려도 같은 결과다 - 서비스가 책 전체를 받아
# 대체하므로 같은 파일을 두 번 보내도 기록이 두 배가 되지 않는다.
#
# 파일을 지우지는 않는다. 옮긴 뒤 화면에서 기록이 제대로 보이는 것을 확인하기
# 전에 원본을 버리는 것은, 되돌릴 수 없는 일을 검증 전에 하는 것이다.
set -eu

FILE="${WORKOUT_FILE:-/data/workout.json}"
TARGET="${WORKOUT_URL:-http://workout:8091}/workout"

if [ ! -f "$FILE" ]; then
  echo "옮길 것이 없습니다: $FILE 이 없습니다."
  exit 0
fi

# 빈 파일이나 반쯤 쓰인 파일을 그대로 보내면 서비스가 400 을 돌려주지만,
# 여기서 먼저 걸러내면 무엇이 문제였는지가 더 분명하다.
if [ ! -s "$FILE" ]; then
  echo "$FILE 이 비어 있습니다. 옮기지 않았습니다." >&2
  exit 1
fi

echo "→ $FILE ($(wc -c < "$FILE" | tr -d ' ') 바이트) → $TARGET"

if [ -n "${API_TOKEN:-}" ]; then
  set -- -H "authorization: Bearer $API_TOKEN"
else
  set --
fi

curl -fsS -X PUT "$TARGET" \
  -H 'content-type: application/json' \
  "$@" \
  --data-binary "@$FILE"

echo ""
echo "옮겼습니다. 화면에서 기록이 보이는 것을 확인한 뒤에 $FILE 을 지우세요."
