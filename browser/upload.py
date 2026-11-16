"""파일 입력 칸에 사진을 넣는다.

browser-use MCP에는 업로드 도구가 없다. 파일 선택 창은 운영체제 창이라
클릭으로는 채울 수 없고, CDP의 DOM.setFileInputFiles만이 길이다. 그래서
guard와 같은 방식으로 같은 Chromium에 따로 붙어, 에이전트가 보고 있는 탭의
<input type=file>에 파일 경로를 꽂는다.

어느 탭인가는 page_for 가 고른다: 브리지가 browser-use 에 물어 넘긴 주소(모델이
browser_get_state 로 보는 화면)의 탭. 로그인 도구(login.py)도 같은 것을 쓴다.

넣을 수 있는 파일은 UPLOADS_DIR 아래뿐이다. 이 도구를 부르는 모델이 경로를
지어낼 수 있으므로, 프로필(쿠키)이나 시스템 파일을 웹페이지에 올리는 통로가
되지 않게 한다.
"""

import asyncio
import json
import os
import urllib.request
from urllib.parse import urlparse

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
        # 응답을 기다리는 사이 지나간 알림(이벤트)의 이름별 수 - 켠 도메인의 것만 온다(login 은 Network).
        self.seen: dict[str, int] = {}

    async def call(self, method: str, params: dict | None = None) -> dict:
        self.seq += 1
        mid = self.seq
        await self.ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
        while True:
            msg = json.loads(await self.ws.recv())
            if msg.get("id") != mid:
                if "method" in msg:
                    self.seen[msg["method"]] = self.seen.get(msg["method"], 0) + 1
                continue
            if "error" in msg:
                raise RuntimeError(f"{method}: {msg['error'].get('message')}")
            return msg.get("result", {})


async def _visible(ws_url: str) -> bool:
    async with websockets.connect(ws_url, max_size=None) as ws:
        s = _Session(ws)
        r = await s.call("Runtime.evaluate", {"expression": "document.visibilityState", "returnByValue": True})
        return r.get("result", {}).get("value") == "visible"


# browser-use 가 아는 주소와 /json/list 의 주소는 둘 다 Chrome 의 대상 정보에서 오지만, 페이지가 막
# 넘어가는 중이면 잠깐 어긋난다. 같은 주소의 탭이 없으면 이만큼 쉬며 몇 번 더 본다.
PICK_TRIES, PICK_PAUSE = 3, 0.3


def _pages(port: int) -> list[dict]:
    targets = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/list", timeout=5))
    return [t for t in targets if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]


def _same_page(url: str) -> str:
    """# 뒤를 뺀 주소 - 같은 문서 안의 이동(#…)은 같은 화면이다."""
    return (url or "").split("#", 1)[0]


def _same_place(url: str) -> str:
    """? 와 # 를 뺀 주소(scheme://host/path) - 같은 화면이 주소 끝의 값만 바꿨을 때."""
    u = urlparse(url or "")
    return f"{u.scheme}://{(u.hostname or '').lower()}{u.path}" if u.scheme and u.hostname else ""


async def _front(pages: list[dict]) -> dict:
    """그중 앞에 보이는 첫 탭, 없으면 첫 탭."""
    for t in pages:
        if await _visible(t["webSocketDebuggerUrl"]):
            return t
    return pages[0]


async def page_for(port: int, url: str | None) -> dict | None:
    """도구가 다룰 탭.

    url 은 browser-use 가 보고 있는 탭의 주소다(브리지가 browser_get_state 로 물어 넘긴다 - 모델이 보는
    화면). 그 주소(# 뒤는 보지 않는다)의 탭을 고르고, 없으면 ? 뒤만 다른 같은 화면의 탭을 고른다. 여럿이면
    앞에 보이는 것. 어느 것도 없으면 다른 탭을 고르지 않고 RuntimeError 다.

    url 이 없으면(물어보지 못했으면) 앞에 보이는 첫 탭, 그것도 없으면 None. 이 브라우저는 창 관리자 없이
    떠서 팝업 창과 원래 창의 앞 탭이 함께 '보이는' 탭이라, 이때는 어느 창의 탭이 될지 모른다."""
    if not url:
        pages = _pages(port)
        for t in pages:
            if await _visible(t["webSocketDebuggerUrl"]):
                return t
        return None
    for attempt in range(PICK_TRIES):
        if attempt:
            await asyncio.sleep(PICK_PAUSE)
        pages = _pages(port)
        same = [t for t in pages if _same_page(t.get("url", "")) == _same_page(url)]
        if same:
            return await _front(same)
    near = [t for t in pages if _same_place(url) and _same_place(t.get("url", "")) == _same_place(url)]
    if near:
        return await _front(near)
    where = urlparse(url).hostname or url[:60]
    raise RuntimeError(f"지금 보고 있는 탭({where})을 브라우저의 탭 목록에서 찾지 못해 넣지 않았습니다. "
                       "browser_get_state 로 화면을 확인한 뒤 다시 부르세요")


async def upload(paths: list[str], selector: str | None = None, url: str | None = None) -> str:
    """url 은 에이전트가 보고 있는 탭의 주소(브리지가 browser-use 에 물은 것) - page_for 를 본다."""
    files = _allowed(paths)
    port = find_cdp_port()
    if port is None:
        raise RuntimeError("브라우저가 떠 있지 않습니다")
    page = await page_for(port, url)
    if page is None:
        pages = _pages(port)
        if not pages:
            raise RuntimeError("열린 탭이 없습니다")
        page = pages[0]

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
