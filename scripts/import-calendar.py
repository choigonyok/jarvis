#!/usr/bin/env python3
"""에이전트 볼륨에 남아 있는 calendar.json 을 calendar-svc 로 옮긴다.

파일은 일정의 배열이고 서비스는 한 번에 하나를 받으므로 하나씩 PUT 한다.
id 를 파일에 있는 그대로 보내므로 여러 번 돌려도 같은 결과다 - 서비스의 PUT 이
id 가 있으면 수정으로 처리한다.

원본은 지우지 않는다. 화면에서 일정이 제대로 보이는 것을 확인하기 전에
되돌릴 수 없는 일을 하지 않는다.

curl 이 아니라 표준 라이브러리만 쓰는 이유: 배열을 쪼개려면 JSON 파서가
필요하고, python 이미지에는 curl 도 jq 도 없다. 도구를 두 개 요구하는 대신
하나로 끝낸다.
"""
import json
import os
import sys
import urllib.error
import urllib.request

FILE = os.environ.get("CALENDAR_FILE", "/data/calendar.json")
TARGET = os.environ.get("CALENDAR_URL", "http://calendar:8093").rstrip("/") + "/events"
TOKEN = os.environ.get("API_TOKEN", "")

if not os.path.exists(FILE):
    print(f"옮길 것이 없습니다: {FILE} 이 없습니다.")
    sys.exit(0)

with open(FILE, encoding="utf-8") as fh:
    events = json.load(fh)

if not isinstance(events, list):
    sys.exit(f"{FILE} 이 일정의 배열이 아닙니다.")

moved, failed = 0, []
for event in events:
    body = json.dumps(event, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(TARGET, data=body, method="PUT")
    request.add_header("content-type", "application/json")
    if TOKEN:
        request.add_header("authorization", f"Bearer {TOKEN}")
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            response.read()
        moved += 1
    except urllib.error.HTTPError as err:
        # 어느 일정이 왜 거부됐는지 남긴다. 하나가 실패해도 나머지는 옮긴다 -
        # 전부 멈추면 고칠 수 있는 하나 때문에 옮길 수 있는 전부를 못 옮긴다.
        detail = err.read().decode("utf-8", "replace")[:200]
        failed.append(f"{event.get('id')} ({event.get('title')}): {err.code} {detail}")
    except OSError as err:
        sys.exit(f"{TARGET} 에 연결하지 못했습니다: {err}")

print(f"{moved}/{len(events)} 개를 옮겼습니다.")
for line in failed:
    print(f"  실패: {line}", file=sys.stderr)
if failed:
    sys.exit(1)
print(f"화면에서 일정이 보이는 것을 확인한 뒤에 {FILE} 을 지우세요.")
