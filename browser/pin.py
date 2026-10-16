"""쿠팡 결제 비밀번호(쿠페이)를 화면 키패드로 넣는다.

비밀번호는 이 컨테이너의 환경변수(COUPANG_PAY_PIN, 맥미니 .env 에만 있다)에만 있고
모델에게는 보이지 않는다. 모델은 키패드가 떴을 때 이 도구를 부르기만 한다.

키패드는 숫자 배치가 매번 섞이는 경우가 많아서, 지금 보이는 탭에서 숫자 0~9 가 모두
들어 있는 가장 작은 영역을 키패드로 보고 각 숫자의 실제 화면 위치를 누른다(마우스
이벤트를 CDP 로 보낸다 - 스크립트의 click() 은 보안 키패드가 무시한다).

넣는 곳은 쿠팡뿐이다. 다른 사이트의 화면이면 넣지 않는다 - 비밀번호를 받아 가는
가짜 키패드에 넣는 통로가 되지 않게. 넣은 뒤 결제가 진행되는 요청은 결제 가드가
따로 판정한다(승인 카드).
"""

import asyncio
import json
import os
import random
import re
import urllib.request
from urllib.parse import urlparse

import websockets

from guard import find_cdp_port
from upload import _Session, _visible

SITE = "coupang.com"

TOOL = {
    "name": "browser_enter_payment_pin",
    "description": (
        "쿠팡 결제 비밀번호(쿠페이) 키패드가 화면에 떠 있을 때 비밀번호를 넣는다. "
        "비밀번호는 이 도구가 갖고 있으니 묻거나 추측하지 말고 이 도구만 부른다. 인자는 없다. "
        "쿠팡 화면에서만 동작한다. 넣은 뒤 browser_get_state 로 결제가 진행됐는지 확인한다."
    ),
    "inputSchema": {"type": "object", "properties": {}},
}

# 숫자 하나만 적힌, 눈에 보이는 요소들 중에서 0~9 를 모두 품은 가장 작은 영역을 찾는다.
# 반환: 그 영역 안의 숫자 요소 열 개(0~9 순서). 없으면 null.
KEYPAD_JS = r"""
(() => {
  const label = el => ((el.tagName === 'IMG' ? el.alt : (el.innerText || el.textContent)) || el.getAttribute('aria-label') || '').trim();
  const shown = el => {
    const r = el.getBoundingClientRect(), s = getComputedStyle(el);
    return r.width > 4 && r.height > 4 && s.visibility !== 'hidden' && s.display !== 'none' && Number(s.opacity) > 0.05;
  };
  const keys = [];
  for (const el of document.querySelectorAll('button, a, td, li, span, div, i, em, strong, img, input[type=button]')) {
    if (el.children.length > 1) continue;
    const t = el.tagName === 'INPUT' ? (el.value || '').trim() : label(el);
    if (!/^[0-9]$/.test(t) || !shown(el)) continue;
    keys.push([t, el]);
  }
  let pad = null;
  for (const [, el] of keys) {
    for (let a = el.parentElement; a; a = a.parentElement) {
      const ds = new Set(keys.filter(([, k]) => a.contains(k)).map(([d]) => d));
      if (ds.size === 10) { if (!pad || pad.contains(a)) pad = a; break; }
    }
  }
  if (!pad) return null;
  const best = {};
  for (const [d, el] of keys) {
    if (!pad.contains(el)) continue;
    const r = el.getBoundingClientRect(), area = r.width * r.height;
    if (!best[d] || area < best[d][1]) best[d] = [el, area];
  }
  return [0,1,2,3,4,5,6,7,8,9].map(d => best[String(d)][0]);
})()
"""


async def _frames(s: _Session) -> list[str]:
    tree = (await s.call("Page.getFrameTree"))["frameTree"]
    out, stack = [], [tree]
    while stack:
        node = stack.pop()
        out.append(node["frame"]["id"])
        stack.extend(node.get("childFrames", []))
    return out


async def _keypad(s: _Session) -> list[str] | None:
    """키패드 숫자 요소 열 개의 objectId (0~9 순서). 프레임마다 찾아 본다."""
    for frame in await _frames(s):
        try:
            ctx = (await s.call("Page.createIsolatedWorld", {"frameId": frame, "worldName": "jarvis-pin"}))["executionContextId"]
        except RuntimeError:
            continue
        res = await s.call("Runtime.evaluate", {"expression": KEYPAD_JS, "contextId": ctx})
        obj = res.get("result", {})
        if obj.get("subtype") == "null" or not obj.get("objectId"):
            continue
        props = (await s.call("Runtime.getProperties", {"objectId": obj["objectId"], "ownProperties": True}))["result"]
        ids = {p["name"]: p["value"]["objectId"] for p in props if p["name"].isdigit() and "objectId" in p.get("value", {})}
        if len(ids) == 10:
            return [ids[str(i)] for i in range(10)]
    return None


async def _center(s: _Session, object_id: str) -> tuple[float, float]:
    quads = (await s.call("DOM.getContentQuads", {"objectId": object_id}))["quads"]
    if not quads:
        raise RuntimeError("키패드 숫자의 위치를 읽지 못했습니다")
    q = quads[0]
    return sum(q[0::2]) / 4, sum(q[1::2]) / 4


async def _press(s: _Session, x: float, y: float):
    await s.call("Input.dispatchMouseEvent", {"type": "mouseMoved", "x": x, "y": y})
    await s.call("Input.dispatchMouseEvent", {"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1})
    await asyncio.sleep(random.uniform(0.04, 0.09))
    await s.call("Input.dispatchMouseEvent", {"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1})


async def enter(pin: str | None = None) -> str:
    pin = pin if pin is not None else os.environ.get("COUPANG_PAY_PIN", "")
    if not re.fullmatch(r"[0-9]{6}", pin or ""):
        raise RuntimeError("쿠팡 결제 비밀번호가 설정되어 있지 않습니다(맥미니 .env 의 JARVIS_COUPANG_PAY_PIN)")
    port = find_cdp_port()
    if port is None:
        raise RuntimeError("브라우저가 떠 있지 않습니다")
    targets = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/list", timeout=5))
    pages = [t for t in targets if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]
    page = None
    for t in pages:
        if await _visible(t["webSocketDebuggerUrl"]):
            page = t
            break
    if page is None:
        raise RuntimeError("열린 탭이 없습니다")
    host = urlparse(page.get("url", "")).hostname or ""
    if host != SITE and not host.endswith("." + SITE):
        raise RuntimeError(f"쿠팡 화면이 아니라서 넣지 않았습니다({host})")

    async with websockets.connect(page["webSocketDebuggerUrl"], max_size=None) as ws:
        s = _Session(ws)
        await s.call("Page.enable")
        await s.call("DOM.enable")
        keys = await _keypad(s)
        if keys is None:
            raise RuntimeError("화면에서 숫자 키패드(0~9)를 찾지 못했습니다. 결제 비밀번호 화면이 떠 있는지 확인하세요")
        centers = [await _center(s, k) for k in keys]
        for ch in pin:
            x, y = centers[int(ch)]
            await _press(s, x, y)
            await asyncio.sleep(random.uniform(0.12, 0.25))
    return "결제 비밀번호를 넣었습니다"
