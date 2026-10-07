#!/usr/bin/env python3
"""에이전트 볼륨에 남아 있는 thread.json 을 chat-svc 로 옮긴다.

파일은 {"turns": [...], "seq": n} 이고 서비스는 한 번에 하나를 받는다.

**여러 번 돌리면 대화가 두 배가 된다.** 캘린더·운동과 다른 점이다: 서비스가
id 를 새로 발급하므로 같은 턴을 두 번 보내면 두 개가 된다. 턴에는 자연키가
없고 - 같은 사람이 같은 말을 두 번 할 수 있다 - 그래서 멱등하게 만들 방법이
없다. 대신 이미 턴이 있으면 멈춘다.
"""
import json
import os
import sys
import urllib.error
import urllib.request

FILE = os.environ.get("THREAD_FILE", "/data/thread.json")
BASE = os.environ.get("CHAT_URL", "http://chat:8094").rstrip("/")
TOKEN = os.environ.get("API_TOKEN", "")
FORCE = os.environ.get("FORCE", "") == "true"


def request(path, data=None, method="GET"):
    req = urllib.request.Request(BASE + path, data=data, method=method)
    if data is not None:
        req.add_header("content-type", "application/json")
    if TOKEN:
        req.add_header("authorization", f"Bearer {TOKEN}")
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.loads(response.read() or "{}")


if not os.path.exists(FILE):
    print(f"옮길 것이 없습니다: {FILE} 이 없습니다.")
    sys.exit(0)

with open(FILE, encoding="utf-8") as fh:
    turns = json.load(fh).get("turns") or []

if not turns:
    print(f"{FILE} 에 턴이 없습니다.")
    sys.exit(0)

try:
    existing = request("/turns").get("turns") or []
except OSError as err:
    sys.exit(f"{BASE} 에 연결하지 못했습니다: {err}")

if existing and not FORCE:
    sys.exit(
        f"이미 {len(existing)}개의 턴이 있습니다. 두 번 옮기면 대화가 중복됩니다.\n"
        "정말 덧붙이려면 FORCE=true 로 다시 실행하세요."
    )

moved, failed = 0, []
for entry in turns:
    # 서비스가 id 와 시각을 발급하므로 보내지 않는다. 원본의 시각은 날짜가 없는
    # 벽시계("15:04")라서 어차피 옮길 수 없다 - 그래서 옮긴 대화의 시각은 옮긴
    # 시점이 된다. 대화의 순서는 보존되고, 이것이 이 이동의 알려진 손실이다.
    body = {
        "role": entry.get("role"),
        "text": entry.get("text", ""),
        "paragraphs": entry.get("paragraphs") or [],
        "proposalId": entry.get("proposalId", ""),
    }
    try:
        request("/turns", json.dumps(body, ensure_ascii=False).encode("utf-8"), "POST")
        moved += 1
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", "replace")[:160]
        failed.append(f"{entry.get('id')}: {err.code} {detail}")
    except OSError as err:
        sys.exit(f"{BASE} 에 연결하지 못했습니다: {err}")

print(f"{moved}/{len(turns)} 개를 옮겼습니다.")
for line in failed:
    print(f"  실패: {line}", file=sys.stderr)
print(f"화면에서 대화가 보이는 것을 확인한 뒤에 {FILE} 을 지우세요.")
if failed:
    sys.exit(1)
