"""파일 입력 칸에 사진을 넣는다.

browser-use MCP에는 업로드 도구가 없다. 파일 선택 창은 운영체제 창이라
클릭으로는 채울 수 없고, CDP의 DOM.setFileInputFiles만이 길이다. 그래서
guard와 같은 방식으로 같은 Chromium에 따로 붙어, 지금 보이는 탭의
<input type=file>에 파일 경로를 꽂는다.

넣을 수 있는 파일은 UPLOADS_DIR 아래뿐이다. 이 도구를 부르는 모델이 경로를
지어낼 수 있으므로, 프로필(쿠키)이나 시스템 파일을 웹페이지에 올리는 통로가
되지 않게 한다.
"""

import json
import os
import urllib.request

import websockets

from guard import find_cdp_port

UPLOADS_DIR = os.path.realpath(os.environ.get("UPLOADS_DIR", "/uploads"))

TOOL = {
    "name": "browser_upload_files",
    "description": (
        "지금 보고 있는 탭의 파일 선택 칸(<input type=file>)에 사진을 넣는다. "
        "파일 선택 창을 열려고 버튼을 누르지 말고 이 도구를 바로 부른다. "
        "paths 는 작업에 적힌 /uploads/... 경로 그대로. 넣은 뒤 browser_get_state 로 미리보기가 생겼는지 확인한다."
    ),
    "inputSchema": {
        "type": "object",
        "properties": {
            "paths": {"type": "array", "items": {"type": "string"}, "description": "올릴 파일 경로들"},
            "selector": {
                "type": "string",
                "description": "파일 칸 CSS 선택자. 기본 input[type=file]. 칸이 여러 개면 이미지용(accept 에 image)을 먼저 고른다.",
            },
        },
        "required": ["paths"],
    },
}


def _allowed(paths: list[str]) -> list[str]:
    out = []
    for p in paths:
        real = os.path.realpath(p)
        if not real.startswith(UPLOADS_DIR + os.sep):
            raise ValueError(f"{p}: {UPLOADS_DIR} 아래 파일만 올릴 수 있습니다")
        if not os.path.isfile(real):
            raise ValueError(f"{p}: 파일이 없습니다")
        out.append(real)
    if not out:
        raise ValueError("올릴 파일이 없습니다")
    return out


class _Session:
    def __init__(self, ws):
        self.ws = ws
        self.seq = 0

    async def call(self, method: str, params: dict | None = None) -> dict:
        self.seq += 1
        mid = self.seq
        await self.ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
        while True:
            msg = json.loads(await self.ws.recv())
            if msg.get("id") != mid:
                continue
            if "error" in msg:
                raise RuntimeError(f"{method}: {msg['error'].get('message')}")
            return msg.get("result", {})


async def _visible(ws_url: str) -> bool:
    async with websockets.connect(ws_url, max_size=None) as ws:
        s = _Session(ws)
        r = await s.call("Runtime.evaluate", {"expression": "document.visibilityState", "returnByValue": True})
        return r.get("result", {}).get("value") == "visible"


async def upload(paths: list[str], selector: str | None = None) -> str:
    files = _allowed(paths)
    port = find_cdp_port()
    if port is None:
        raise RuntimeError("브라우저가 떠 있지 않습니다")
    targets = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/list", timeout=5))
    pages = [t for t in targets if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]
    if not pages:
        raise RuntimeError("열린 탭이 없습니다")
    # 사람이 보는 창에서 앞에 나와 있는 탭이 browser-use가 조작 중인 탭이다.
    page = None
    for t in pages:
        if await _visible(t["webSocketDebuggerUrl"]):
            page = t
            break
    page = page or pages[0]

    async with websockets.connect(page["webSocketDebuggerUrl"], max_size=None) as ws:
        s = _Session(ws)
        root = (await s.call("DOM.getDocument", {"depth": 0}))["root"]["nodeId"]
        wanted = selector or 'input[type=file][accept*="image"]'
        nodes = (await s.call("DOM.querySelectorAll", {"nodeId": root, "selector": wanted}))["nodeIds"]
        if not nodes and not selector:
            nodes = (await s.call("DOM.querySelectorAll", {"nodeId": root, "selector": "input[type=file]"}))["nodeIds"]
        if not nodes:
            raise RuntimeError(f"{page.get('url', '')} 에 파일 선택 칸이 없습니다")
        await s.call("DOM.setFileInputFiles", {"files": files, "nodeId": nodes[0]})
    return f"{len(files)}개 파일을 넣었습니다 ({page.get('url', '')})"
