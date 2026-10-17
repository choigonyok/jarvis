"""쿠팡 결제 비밀번호(쿠페이)를 화면 키패드로 넣는다.

비밀번호는 이 컨테이너의 환경변수(COUPANG_PAY_PIN, 맥미니 .env 에만 있다)에만 있고
모델에게는 보이지 않는다. 모델은 키패드가 떴을 때 이 도구를 부르기만 한다.

키패드는 숫자 배치가 매번 섞이는 경우가 많아서, 지금 보이는 탭에서 숫자 0~9 가 모두
들어 있는 가장 작은 영역을 키패드로 보고 각 숫자의 실제 화면 위치를 누른다(마우스
이벤트를 CDP 로 보낸다 - 스크립트의 click() 은 보안 키패드가 무시한다).

쿠팡 쿠페이 키패드(rocketpay.coupang.com, 2026-10-18 확인)는 숫자가 글자가 아니라 그림이다.
키 열 개(a.pad-key[data-key])가 한 장의 스프라이트(data: PNG, 흰 숫자)를 나눠 쓰고, 키 N 은
그림의 N 번째 칸을 배경 위치로 보여 주며, 그림 속 숫자 배치가 매번 섞인다. 그래서 그 그림을
칸마다 잘라 숫자를 읽는다(read_sprite). 하나라도 확실하지 않으면 넣지 않는다 - 틀린 숫자를
누르면 비밀번호 오류가 쌓여 쿠페이가 잠긴다.

넣는 곳은 쿠팡뿐이다. 다른 사이트의 화면이면 넣지 않는다 - 비밀번호를 받아 가는
가짜 키패드에 넣는 통로가 되지 않게. 넣은 뒤 결제가 진행되는 요청은 결제 가드가
따로 판정한다(승인 카드).
"""

import asyncio
import base64
import io
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

# 쿠페이 그림 키패드: 키마다 스프라이트 주소와, 그 키가 보여 주는 칸(배경 위치·크기).
SPRITE_JS = r"""
(() => {
  const keys = Array.from(document.querySelectorAll('a.pad-key[data-key]'));
  if (keys.length !== 10) return null;
  const out = [];
  for (const a of keys) {
    const s = a.querySelector('span') || a;
    const cs = getComputedStyle(s);
    const m = /url\("?(data:image\/png;base64,[^")]+)"?\)/.exec(cs.backgroundImage);
    if (!m) return null;
    const [x, y] = cs.backgroundPosition.split(' ').map(parseFloat);
    out.push({key: a.dataset.key, src: m[1], x: -x, y: -y, w: s.offsetWidth, h: s.offsetHeight});
  }
  return JSON.stringify(out);
})()
"""

# 쿠페이 키패드의 숫자 모양(2026-10-18 스프라이트에서 뽑음): 글자가 차지한 범위만 잘라
# GLYPH_W×GLYPH_H 로 맞춘 흑백(1=글자). 서로 140칸 중 최소 32칸 다르다.
GLYPH_W, GLYPH_H = 10, 14
TEMPLATES = {
    0: "00011110000011111100011000011011100001111110000111110000001111000000111100000011110000001111100001111110000111011000011000111111000001111000",
    1: "01111110001111111000000011100000001110000000111000000011100000001110000000111000000011100000001110000000111000000011100000001110000111111111",
    2: "01111110001111111110100000011100000001110000000111000000011100000011100000011100000011100000011100000011100000011100000011111000001111111111",
    3: "00111110000111111100000000111000000001100000001110000000110000011110000000011110000000011100000001110000000111000000011111111111100111111000",
    4: "00000111000000011100000011110000011011000001001100001100110001100011000100001100110000110011111111110000011100000000110000000011000000001100",
    5: "01111111100111000000011000000001100000000111110000011111110000000011100000000111000000011100000001110000000111000000011011111111100111111000",
    6: "00001111100011111110011100000001100000001110000000111011100011111111101110000111111000011111000000111100000011011000011100111111100001111100",
    7: "11111111110000001111000000011000000011100000001110000000110000000111000000011100000001100000001110000000110000000111000000011100000001100000",
    8: "00111111000111111110111000011111000000111110000111011000011000111111000111001110111000011111000000111100000011111000011101111111100011111100",
    9: "00111110000111111100111000011011000000111100000011111000011111100001110111111111000111011100000001110000000110000000111001111111000111110000",
}
# 닫힌 구멍의 수 - 모양 비교와 따로 보는 두 번째 확인.
HOLES = {0: 1, 1: 0, 2: 0, 3: 0, 4: 1, 5: 0, 6: 1, 7: 0, 8: 2, 9: 1}
# 견본과 이만큼까지 달라도 같은 숫자로 보고, 두 번째로 가까운 견본과는 이만큼 더 떨어져야 한다.
MAX_MISS, MIN_MARGIN = 24, 10


def _bits(cell) -> str | None:
    """칸(알파 채널 그림)의 글자 모양. 글자가 없으면 None."""
    from PIL import Image

    box = cell.point(lambda v: 255 if v >= 128 else 0).getbbox()
    if box is None:
        return None
    g = cell.crop(box).resize((GLYPH_W, GLYPH_H), Image.BILINEAR).point(lambda v: 1 if v >= 128 else 0)
    return "".join(str(g.getpixel((x, y))) for y in range(GLYPH_H) for x in range(GLYPH_W))


def _holes(cell) -> int:
    """칸 안에서 바깥과 이어지지 않은 빈 영역의 수."""
    b = cell.point(lambda v: 1 if v >= 128 else 0)
    w, h = b.size
    ink = [[b.getpixel((x, y)) for x in range(w)] for y in range(h)]
    seen = [[False] * w for _ in range(h)]
    n = 0
    for sy in range(h):
        for sx in range(w):
            if ink[sy][sx] or seen[sy][sx]:
                continue
            seen[sy][sx], stack, outside = True, [(sx, sy)], False
            while stack:
                x, y = stack.pop()
                outside = outside or x in (0, w - 1) or y in (0, h - 1)
                for nx, ny in ((x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)):
                    if 0 <= nx < w and 0 <= ny < h and not ink[ny][nx] and not seen[ny][nx]:
                        seen[ny][nx] = True
                        stack.append((nx, ny))
            n += not outside
    return n


def read_sprite(cells: list[dict]) -> dict[str, int]:
    """그림 키패드의 키(data-key) → 그 키에 그려진 숫자. 열 칸이 모두 확실하고 0~9 가 한 번씩
    나와야 돌려준다. 아니면 RuntimeError - 이때는 아무것도 누르지 않는다."""
    from PIL import Image

    unsure = RuntimeError("쿠페이 키패드의 숫자를 확실히 읽지 못해 비밀번호를 넣지 않았습니다(틀리면 쿠페이가 잠길 수 있습니다)")
    sheets = {}
    out = {}
    for c in cells:
        src = c["src"]
        if src not in sheets:
            png = base64.b64decode(src.split(",", 1)[1])
            sheets[src] = Image.open(io.BytesIO(png)).convert("RGBA").split()[3]
        x, y = round(c["x"]), round(c["y"])
        cell = sheets[src].crop((x, y, x + round(c["w"]), y + round(c["h"])))
        bits = _bits(cell)
        if bits is None:
            raise unsure
        scored = sorted((sum(a != b for a, b in zip(bits, t)), d) for d, t in TEMPLATES.items())
        (miss, digit), (next_miss, _) = scored[0], scored[1]
        if miss > MAX_MISS or next_miss - miss < MIN_MARGIN or _holes(cell) != HOLES[digit]:
            raise unsure
        out[str(c["key"])] = digit
    if sorted(out.values()) != list(range(10)):
        raise unsure
    return out


async def _objects(s: _Session, res: dict) -> list[str] | None:
    """평가 결과로 받은 요소 배열(0~9 순서)의 objectId 들."""
    obj = res.get("result", {})
    if obj.get("subtype") == "null" or not obj.get("objectId"):
        return None
    props = (await s.call("Runtime.getProperties", {"objectId": obj["objectId"], "ownProperties": True}))["result"]
    ids = {p["name"]: p["value"]["objectId"] for p in props if p["name"].isdigit() and "objectId" in p.get("value", {})}
    return [ids[str(i)] for i in range(10)] if len(ids) == 10 else None


async def _frames(s: _Session) -> list[str]:
    tree = (await s.call("Page.getFrameTree"))["frameTree"]
    out, stack = [], [tree]
    while stack:
        node = stack.pop()
        out.append(node["frame"]["id"])
        stack.extend(node.get("childFrames", []))
    return out


async def _keypad(s: _Session) -> list[str] | None:
    """키패드 숫자 요소 열 개의 objectId (0~9 순서). 프레임마다 찾아 본다 - 쿠페이 그림
    키패드를 먼저, 그다음 숫자가 글자로 적힌 키패드."""
    for frame in await _frames(s):
        try:
            ctx = (await s.call("Page.createIsolatedWorld", {"frameId": frame, "worldName": "jarvis-pin"}))["executionContextId"]
        except RuntimeError:
            continue
        sprite = await s.call("Runtime.evaluate", {"expression": SPRITE_JS, "contextId": ctx, "returnByValue": True})
        cells = sprite.get("result", {}).get("value")
        if cells:
            digits = read_sprite(json.loads(cells))
            by_digit = json.dumps([k for k, _ in sorted(digits.items(), key=lambda kv: kv[1])])
            res = await s.call("Runtime.evaluate", {
                "expression": f"""{by_digit}.map(k => document.querySelector('a.pad-key[data-key="' + k + '"]'))""",
                "contextId": ctx,
            })
            ids = await _objects(s, res)
            if ids:
                return ids
            continue
        ids = await _objects(s, await s.call("Runtime.evaluate", {"expression": KEYPAD_JS, "contextId": ctx}))
        if ids:
            return ids
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
