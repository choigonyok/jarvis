"""결제 비밀번호(쿠팡 쿠페이, 네이버페이)를 화면 키패드로 넣는다.

비밀번호는 보관함(vault-svc 의 결제 비밀번호 - 운영자가 대화의 save_payment_pin 카드 비밀 칸으로
넣는다)이나 이 컨테이너의 환경변수(COUPANG_PAY_PIN·NAVER_PAY_PIN, 맥미니 .env)에만 있고 모델에게는
보이지 않는다. 보관함은 키패드가 있는 프레임의 호스트가 그 결제의 것일 때만 내준다. 모델은 키패드가
떴을 때 이 도구를 부르기만 한다.

키패드는 숫자 배치가 매번 섞이는 경우가 많아서, 숫자 0~9 가 모두 들어 있는 가장 작은 영역을
키패드로 보고 각 숫자의 실제 화면 위치를 누른다(마우스 이벤트를 CDP 로 보낸다 - 스크립트의
click() 은 보안 키패드가 무시한다). 누를 때마다 배치를 다시 읽는다 - 누를 때마다 숫자를 다시
섞는 키패드가 있다. 누르기 전에 그 자리에 그 키가 있는지(다른 것이 덮고 있지 않은지) 본다.

쿠팡 쿠페이 키패드(rocketpay.coupang.com, 2026-10-18 확인)는 숫자가 글자가 아니라 그림이다.
키 열 개(a.pad-key[data-key])가 한 장의 스프라이트(data: PNG, 흰 숫자)를 나눠 쓰고, 키 N 은
그림의 N 번째 칸을 배경 위치로 보여 주며, 그림 속 숫자 배치가 매번 섞인다. 그래서 그 그림을
칸마다 잘라 숫자를 읽는다(read_sprite). 하나라도 확실하지 않으면 넣지 않는다 - 틀린 숫자를
누르면 비밀번호 오류가 쌓여 쿠페이가 잠긴다. 네이버페이는 다섯 번 연속 틀리면 결제가 잠긴다.

네이버페이 키패드(pay.naver.com/authentication/pw/check 새 창, 2026-10-30 확인)도 그림이다.
키 열 개(span.SecureKeyboard_number__*)가 한 장의 스프라이트(data: PNG 120×200, 흰 숫자, 3칸×4줄,
칸 40×50 - 화면에는 background-size 60×100 으로 반만 그려 칸 20×25)를 나눠 쓰고, 키 R-C 는 그림의
R-C 칸을 보여 주며 그림 속 숫자 배치가 매번 섞인다(빈 칸 둘). 쿠페이와 같은 방법으로 칸을 잘라
네이버페이 글꼴의 견본(12×16)으로 읽는다. 숫자가 글자·alt·aria-label 로 적힌 키패드면 그대로 읽고,
어느 것으로도 읽지 못하면 누르지 않고 키패드의 모양(요소·그림·화면)을 CAPTURE_DIR 에 남긴다 -
두 그림 키패드 모두 그렇게 남은 모양으로 읽는 법을 만들었다.

넣는 곳은 키패드가 그 결제의 화면에 있을 때뿐이다: 키패드가 있는 프레임의 출처가 쿠팡이면
coupang.com, 네이버페이면 pay.naver.com 아래여야 한다(SITES). 탭 주소가 아니라 프레임을 보는
것은, 키패드가 쇼핑몰 화면 위의 결제 창 안에 있을 수 있어서다. naver.com 전체가 아니라
pay.naver.com 인 것은, 같은 naver.com 의 블로그·카페는 누구나 글을 올리는 곳이라 키패드처럼
보이는 것을 둘 수 있어서다. 다른 곳이면 넣지 않는다 - 비밀번호를 받아 가는 가짜 키패드에
넣는 통로가 되지 않게. 넣은 뒤 결제가 진행되는 요청은 결제 가드가 따로 판정한다(승인 카드).
"""

import asyncio
import base64
import io
import json
import os
import random
import re
import shutil
import sys
import time
import urllib.error
import urllib.request
from urllib.parse import urlparse

import websockets

from guard import find_cdp_port
from upload import _Session, _visible

# 키패드가 있는 프레임의 출처(host 와 그 아래) → 그곳의 비밀번호(환경변수, 맥미니 .env 의 이름).
# text 는 키패드가 뜬 화면에 있어야 하는 글자다 - 네이버페이 화면에는 결제 비밀번호 말고도 숫자
# 0~9 가 늘어선 것(달력 등)이 있을 수 있다. 쿠페이 키패드에는 쓰지 않는다(이미 쓰는 그대로).
SITES = (
    {"host": "coupang.com", "env": "COUPANG_PAY_PIN", "setting": "JARVIS_COUPANG_PAY_PIN", "name": "쿠팡 결제 비밀번호(쿠페이)", "text": ""},
    {"host": "pay.naver.com", "env": "NAVER_PAY_PIN", "setting": "JARVIS_NAVER_PAY_PIN", "name": "네이버페이 결제 비밀번호", "text": "비밀번호"},
)

# 보관함: 이 컨테이너의 토큰은 꺼내기만 연다(login.py 와 같은 것).
VAULT_URL = os.environ.get("VAULT_URL", "http://vault:8105").rstrip("/")
VAULT_TOKEN = os.environ.get("VAULT_RELEASE_TOKEN", "")

# 읽지 못한 키패드의 모양을 남기는 곳(최근 것 몇 개만). 화면 사진에는 결제 화면이 담기므로
# 이 컨테이너 밖으로 보내지 않는다.
CAPTURE_DIR = os.environ.get("PIN_CAPTURE_DIR", "/tmp/jarvis-pin")
CAPTURES_KEPT = 5

# 넣다가 멈추면(키패드가 바뀌거나 사라져서) 키패드에 숫자 몇 자리가 남는다. 그 위에 다시 처음부터
# 넣으면 틀린 비밀번호가 되므로, 그 뒤 얼마 동안은 다시 넣지 않는다.
HALT_SECONDS = 600
_halted_until = 0.0

TOOL = {
    "name": "browser_enter_payment_pin",
    "description": (
        "결제 비밀번호 키패드(쿠팡 쿠페이, 네이버페이)가 화면에 떠 있을 때 비밀번호를 넣는다. 인자는 없다. "
        "비밀번호는 운영자가 결제 비밀번호 저장 카드로 보관함에 넣었거나 맥미니 설정에 있을 때만 넣을 수 있고, 당신에게는 보이지 않는다 - "
        "묻거나 추측하거나 키패드를 직접 누르지 말고 이 도구만 부른다. 저장돼 있지 않으면 넣지 않고 그렇다고 답한다 "
        "(그때 작업은 request_payment_pin, 대화는 save_payment_pin 으로 저장 카드를 올린다). "
        "쿠팡·네이버페이의 결제 화면(그 결제 호스트의 키패드)에서만 동작한다. 넣은 뒤에는 그 창의 다음 화면을 본다 - "
        "카드사 창 안의 네이버페이는 인증 결과 화면의 확인을 눌러야 카드사 창으로 넘어간다. "
        "넣지 못했다고 하면 키패드를 직접 누르지 말고, 다시 부르지도 말고 알린다."
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
  const pr = pad.getBoundingClientRect();
  if (pr.width > 800 || pr.height > 800) return null;
  const best = {};
  for (const [d, el] of keys) {
    if (!pad.contains(el)) continue;
    const r = el.getBoundingClientRect(), area = r.width * r.height;
    if (!best[d] || area < best[d][1]) best[d] = [el, area];
  }
  return [0,1,2,3,4,5,6,7,8,9].map(d => best[String(d)][0]);
})()
"""

# 키의 한가운데를 누르면 그 키가 눌리는가: 그 자리의 맨 위 요소가 키 자신이거나 키 안의 것(또는 키를
# 품은 것)이어야 한다. 키 위를 다른 것이 덮고 있으면 누른 숫자가 그 키로 가지 않는다.
HIT_JS = r"""
function () {
  const r = this.getBoundingClientRect();
  const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
  return !!hit && (hit === this || this.contains(hit) || hit.contains(this));
}
"""

# 읽지 못한 키패드의 모양: 키가 될 만한 작은 요소들의 태그·이름·글자·속성·그림. 값을 넣을 칸은 없다.
OUTLINE_JS = r"""
(() => {
  const cut = (v, n) => (v === null || v === undefined ? null : String(v).slice(0, n));
  const out = {
    title: document.title,
    pinText: !!document.body && document.body.innerText.includes('비밀번호'),
    canvases: document.querySelectorAll('canvas').length,
    keys: [], images: {},
  };
  const sel = 'button, a, [role=button], td, li, span, div, i, em, strong, img, input, canvas';
  for (const el of document.querySelectorAll(sel)) {
    const r = el.getBoundingClientRect();
    if (r.width < 8 || r.height < 8 || r.width > 200 || r.height > 200) continue;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') continue;
    const data = {};
    for (const a of el.attributes) if (a.name.startsWith('data-')) data[a.name] = cut(a.value, 40);
    const bg = cs.backgroundImage && cs.backgroundImage !== 'none' ? cs.backgroundImage : null;
    const src = el.tagName === 'IMG' ? (el.currentSrc || el.src) : null;
    for (const u of [bg, src]) {
      const m = u && /(data:image\/[a-z+]+;base64,[^")]+)/.exec(u);
      if (m && Object.keys(out.images).length < 20) out.images['i' + Object.keys(out.images).length] = m[1];
    }
    out.keys.push({
      tag: el.tagName, id: cut(el.id, 40), cls: cut(typeof el.className === 'string' ? el.className : '', 80),
      role: el.getAttribute('role'), aria: cut(el.getAttribute('aria-label'), 40), alt: cut(el.getAttribute('alt'), 40),
      title: cut(el.getAttribute('title'), 40), text: el.children.length ? null : cut((el.innerText || el.textContent || '').trim(), 6),
      data, bg: bg ? cut(bg, 120) : null, bgpos: bg ? cs.backgroundPosition : null, src: cut(src, 120),
      rect: [r.x, r.y, r.width, r.height].map(Math.round),
    });
    if (out.keys.length >= 300) break;
  }
  return JSON.stringify(out);
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

# 네이버페이 그림 키패드: 키마다 스프라이트 주소, 그 키가 보여 주는 칸(배경 위치·크기), 그림을 그린 크기
# (background-size - 그림을 반으로 줄여 그린다). key 는 키의 차례(querySelectorAll 순서).
NAVER_SPRITE_JS = r"""
(() => {
  const keys = Array.from(document.querySelectorAll('[class*="SecureKeyboard_number"]'));
  if (keys.length !== 10) return null;
  const out = [];
  for (let i = 0; i < keys.length; i++) {
    const cs = getComputedStyle(keys[i]);
    const m = /url\("?(data:image\/png;base64,[^")]+)"?\)/.exec(cs.backgroundImage);
    if (!m) return null;
    const [x, y] = cs.backgroundPosition.split(' ').map(parseFloat);
    const [bw, bh] = cs.backgroundSize.split(' ').map(parseFloat);
    out.push({key: String(i), src: m[1], x: -x, y: -y, w: keys[i].offsetWidth, h: keys[i].offsetHeight, bw, bh});
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

# 네이버페이 키패드의 숫자 모양(2026-10-30 스프라이트에서 뽑음). 글씨가 가늘어 10×14 로는 5 와 6 이
# 19칸 차이라 12×16 으로 본다 - 서로 192칸 중 최소 24칸(3·8) 다르고, 구멍 수가 한 번 더 가른다.
NAVER_TEMPLATES = {
    0: "000111111000001111111100011100001110011100000111111000000111111000000111111000000011111000000011111000000011111000000011111000000111011000000111011100000110001110011110001111111100000011111000",
    1: "000000011111000011111111001111111111111111001111111000001111000000001111000000001111000000001111000000001111000000001111000000001111000000001111000000001111000000001111000000001111000000001111",
    2: "001111111000011111111100111100001110111000000111110000000111000000000110000000001110000000011100000000111000000001110000000011100000000111000000001110000000011100000000111111111111111111111111",
    3: "000111111000011111111100011100001110111000000110000000000110000000001110000000011100000011111000000011111110000000000111000000000111110000000111111000000111111100001110011111111100000111111000",
    4: "000000111100000000111100000001111100000011101100000011001100000111001100001110001100001100001100011100001100011000001100111000001100111111111111111111111111000000001100000000001100000000001100",
    5: "011111111110011111111110011000000000011000000000011000000000111000100000111111111100111110111110111000000111000000000111000000000111110000000111111000000111011100011110001111111100000111111000",
    6: "000111111000001111111100011100001110011100000110111000000000111000110000111011111100111111111110111100000111111000000111111000000111011000000111011100000111001110001110001111111100000011111000",
    7: "111111111111111111111111000000000111000000000111000000001110000000001100000000011100000000011000000000111000000001110000000001110000000011100000000011100000000111000000000110000000001110000000",
    8: "000111111000001111111100011100001110011000000110011000000110011100001110001111111100000111111000001111111110011000000110111000000111111000000111111000000111011100001110001111111100000111111000",
    9: "000011110000001111111000011110011100111000001110111000000110111000000111111000000111111000001111011111111111001111110111000000000111000000000110011000001110011100011100001111111100000111110000",
}
NAVER_HOLES = {0: 1, 1: 0, 2: 0, 3: 0, 4: 1, 5: 0, 6: 1, 7: 0, 8: 2, 9: 1}

# 그림 키패드마다의 글꼴: 이름, 견본 크기, 견본, 구멍 수, 허용 차이·간격.
COUPAY = {"name": "쿠페이", "w": GLYPH_W, "h": GLYPH_H, "templates": TEMPLATES, "holes": HOLES, "max_miss": MAX_MISS, "min_margin": MIN_MARGIN}
NAVERPAY = {"name": "네이버페이", "w": 12, "h": 16, "templates": NAVER_TEMPLATES, "holes": NAVER_HOLES, "max_miss": 32, "min_margin": 12}


def _bits(cell, w: int = GLYPH_W, h: int = GLYPH_H) -> str | None:
    """칸(알파 채널 그림)의 글자 모양(w×h). 글자가 없으면 None."""
    from PIL import Image

    box = cell.point(lambda v: 255 if v >= 128 else 0).getbbox()
    if box is None:
        return None
    g = cell.crop(box).resize((w, h), Image.BILINEAR).point(lambda v: 1 if v >= 128 else 0)
    return "".join(str(g.getpixel((x, y))) for y in range(h) for x in range(w))


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


def read_sprite(cells: list[dict], glyphs: dict = COUPAY) -> dict[str, int]:
    """그림 키패드의 키 → 그 키에 그려진 숫자. 열 칸이 모두 확실하고 0~9 가 한 번씩 나와야 돌려준다.
    아니면 RuntimeError - 이때는 아무것도 누르지 않는다. 칸의 위치·크기는 화면에 그린 크기 기준이고,
    그림을 줄여 그렸으면(bw·bh, background-size) 그림의 실제 크기로 늘려 자른다."""
    from PIL import Image

    unsure = RuntimeError(f"{glyphs['name']} 키패드의 숫자를 확실히 읽지 못해 비밀번호를 넣지 않았습니다"
                          f"(틀리면 {glyphs['name']} 결제가 잠길 수 있습니다)")
    sheets = {}
    out = {}
    for c in cells:
        src = c["src"]
        if src not in sheets:
            png = base64.b64decode(src.split(",", 1)[1])
            sheets[src] = Image.open(io.BytesIO(png)).convert("RGBA").split()[3]
        sheet = sheets[src]
        bw, bh = c.get("bw"), c.get("bh")
        sx = sheet.width / bw if isinstance(bw, (int, float)) and bw > 0 else 1.0
        sy = sheet.height / bh if isinstance(bh, (int, float)) and bh > 0 else 1.0
        x, y = round(c["x"] * sx), round(c["y"] * sy)
        cell = sheet.crop((x, y, x + round(c["w"] * sx), y + round(c["h"] * sy)))
        bits = _bits(cell, glyphs["w"], glyphs["h"])
        if bits is None:
            raise unsure
        scored = sorted((sum(a != b for a, b in zip(bits, t)), d) for d, t in glyphs["templates"].items())
        (miss, digit), (next_miss, _) = scored[0], scored[1]
        if miss > glyphs["max_miss"] or next_miss - miss < glyphs["min_margin"] or _holes(cell) != glyphs["holes"][digit]:
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


def site_of(origin: str) -> dict | None:
    """프레임의 출처(https://rocketpay.coupang.com - 주소여도 된다)가 비밀번호를 넣을 수 있는 곳이면 그곳."""
    host = (urlparse(origin or "").hostname or "").lower()
    for site in SITES:
        if host == site["host"] or host.endswith("." + site["host"]):
            return site
    return None


async def _frames(s: _Session) -> list[dict]:
    """탭의 프레임들(맨 위 프레임부터): id, 주소, 출처. 다른 사이트의 프레임은 다른 프로세스라서
    여기서 평가되지 않는다 - 그런 키패드는 찾지 못하고 모양만 남긴다."""
    tree = (await s.call("Page.getFrameTree"))["frameTree"]
    out, queue = [], [tree]
    while queue:
        node = queue.pop(0)
        frame = node["frame"]
        out.append({"id": frame["id"], "url": frame.get("url", ""), "origin": frame.get("securityOrigin", "")})
        queue.extend(node.get("childFrames", []))
    return out


async def _world(s: _Session, frame_id: str) -> int | None:
    try:
        return (await s.call("Page.createIsolatedWorld", {"frameId": frame_id, "worldName": "jarvis-pin"}))["executionContextId"]
    except RuntimeError:
        return None


async def _says(s: _Session, ctx: int, text: str) -> bool:
    expr = f"!!document.body && document.body.innerText.includes({json.dumps(text)})"
    res = await s.call("Runtime.evaluate", {"expression": expr, "contextId": ctx, "returnByValue": True})
    return res.get("result", {}).get("value") is True


async def _keypad(s: _Session) -> tuple[dict, list[str], str] | None:
    """이 탭에서 비밀번호를 넣을 수 있는 키패드: (그곳, 숫자 요소 열 개의 objectId - 0~9 순서, 그 프레임의 호스트).
    넣을 수 있는 출처(SITES)의 프레임에서만 찾는다 - 쿠팡이면 쿠페이 그림 키패드를 먼저, 그다음 숫자가
    글자로 적힌 키패드. 그곳의 text 가 그 프레임에도 탭의 맨 위 프레임에도 없으면 키패드로 보지 않는다.
    쿠페이 그림을 확실히 읽지 못하면 RuntimeError."""
    frames = await _frames(s)
    for frame in frames:
        site = site_of(frame["origin"] or frame["url"])
        if site is None:
            continue
        ctx = await _world(s, frame["id"])
        if ctx is None:
            continue
        ids = None
        # 그림 키패드: 쿠페이는 a.pad-key[data-key] 로, 네이버페이는 키의 차례로 그 키를 다시 찾는다.
        sprite_js, glyphs, pick = {
            "coupang.com": (SPRITE_JS, COUPAY, """document.querySelector('a.pad-key[data-key="' + k + '"]')"""),
            "pay.naver.com": (NAVER_SPRITE_JS, NAVERPAY, """document.querySelectorAll('[class*="SecureKeyboard_number"]')[Number(k)]"""),
        }[site["host"]]
        sprite = await s.call("Runtime.evaluate", {"expression": sprite_js, "contextId": ctx, "returnByValue": True})
        cells = sprite.get("result", {}).get("value")
        if cells:
            digits = read_sprite(json.loads(cells), glyphs)
            by_digit = json.dumps([k for k, _ in sorted(digits.items(), key=lambda kv: kv[1])])
            res = await s.call("Runtime.evaluate", {"expression": f"{by_digit}.map(k => {pick})", "contextId": ctx})
            ids = await _objects(s, res)
            if not ids:
                continue
        if ids is None:
            ids = await _objects(s, await s.call("Runtime.evaluate", {"expression": KEYPAD_JS, "contextId": ctx}))
        if not ids:
            continue
        if site["text"] and not await _says(s, ctx, site["text"]):
            top = await _world(s, frames[0]["id"])
            if top is None or not await _says(s, top, site["text"]):
                continue
        return site, ids, (urlparse(frame["origin"] or frame["url"]).hostname or "").lower()
    return None


def _vault_pin(site: dict, host: str) -> str:
    """보관함에 저장된 그 결제의 비밀번호. host 는 키패드가 있는 프레임의 호스트 - 보관함이 그 결제의
    호스트(와 그 아래)인지 다시 보고 내준다. 저장된 것이 없으면 ""."""
    if not VAULT_TOKEN:
        return ""
    req = urllib.request.Request(
        f"{VAULT_URL}/release-pin",
        data=json.dumps({"site": site["host"], "host": host, "purpose": "결제 비밀번호 키패드"}).encode(),
        method="POST",
        headers={"Authorization": f"Bearer {VAULT_TOKEN}", "Content-Type": "application/json"},
    )
    try:
        return str(json.load(urllib.request.urlopen(req, timeout=10)).get("pin", ""))
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return ""
        try:
            msg = json.load(e).get("error", "")
        except Exception:  # noqa: BLE001
            msg = ""
        raise RuntimeError(msg or f"보관함이 {e.code} 로 답했습니다") from None
    except OSError as e:
        raise RuntimeError(f"보관함에 닿지 못했습니다: {e}") from None


async def _hits(s: _Session, object_id: str) -> bool:
    res = await s.call("Runtime.callFunctionOn", {"objectId": object_id, "functionDeclaration": HIT_JS, "returnByValue": True})
    return res.get("result", {}).get("value") is True


def _bare(url: str) -> str:
    """기록에 남길 주소 - ? 와 # 뒤(주문·인증 값이 실리는 곳)는 뺀다."""
    return re.split(r"[?#]", url or "", maxsplit=1)[0]


async def _capture(targets: list[dict], why: str) -> str | None:
    """키패드를 넣지 못한 때의 화면을 CAPTURE_DIR 아래 새 폴더에 남긴다: 탭·프레임 목록, 결제 출처
    프레임(없으면 맨 위 프레임)의 키가 될 만한 요소들과 그 그림, 화면 사진. 남긴 폴더를 돌려주고,
    남기다 실패해도 원래 오류를 가리지 않게 조용히 None."""
    try:
        where = os.path.join(CAPTURE_DIR, time.strftime("%Y%m%d-%H%M%S"))
        os.makedirs(where, exist_ok=True)
        with open(os.path.join(where, "why.txt"), "w", encoding="utf-8") as f:
            f.write(why + "\n")
        listing = [{"type": t.get("type"), "url": _bare(t.get("url", ""))} for t in targets]
        # 탭들과, 다른 사이트 화면 속에 따로 떠 있는 결제 출처의 프레임(그 키패드는 아직 넣지 못한다).
        pages = [t for t in targets if t.get("webSocketDebuggerUrl") and
                 (t.get("type") == "page" or (t.get("type") == "iframe" and site_of(t.get("url", ""))))][:6]
        for i, t in enumerate(pages):
            try:
                await asyncio.wait_for(_capture_page(t, i, where, listing), 15)
            except Exception as e:  # noqa: BLE001 - 한 탭이 안 되면 다음 탭
                listing.append({"page": i, "error": str(e)[:200]})
        with open(os.path.join(where, "targets.json"), "w", encoding="utf-8") as f:
            json.dump(listing, f, ensure_ascii=False, indent=1)
        for old in sorted(os.listdir(CAPTURE_DIR))[:-CAPTURES_KEPT]:
            shutil.rmtree(os.path.join(CAPTURE_DIR, old), ignore_errors=True)
        print(f"pin: 키패드를 넣지 못해 화면을 남겼습니다({why}): {where}", file=sys.stderr, flush=True)
        return where
    except Exception as e:  # noqa: BLE001 - 남기는 것은 덤이다
        print(f"pin: 화면을 남기지 못했습니다: {e}", file=sys.stderr, flush=True)
        return None


async def _capture_page(t: dict, i: int, where: str, listing: list):
    """탭(또는 따로 뜬 결제 프레임) 하나의 프레임 목록과 요소 모양, 앞에 보이는 탭이면 화면 사진(뒤에 가린 탭은
    그리지 않아 기다리기만 한다)."""
    async with websockets.connect(t["webSocketDebuggerUrl"], max_size=None) as ws:
        s = _Session(ws)
        await s.call("Page.enable")
        frames = await _frames(s)
        listing.append({"page": i, "frames": [{"url": _bare(fr["url"]), "origin": fr["origin"]} for fr in frames]})
        looked = [fr for fr in frames if site_of(fr["origin"] or fr["url"])] or frames[:1]
        for j, fr in enumerate(looked):
            ctx = await _world(s, fr["id"])
            if ctx is None:
                continue
            res = await s.call("Runtime.evaluate", {"expression": OUTLINE_JS, "contextId": ctx, "returnByValue": True})
            outline = json.loads(res.get("result", {}).get("value") or "{}")
            for name, uri in (outline.pop("images", None) or {}).items():
                with open(os.path.join(where, f"page{i}-frame{j}-{name}.png"), "wb") as f:
                    f.write(base64.b64decode(uri.split(",", 1)[1]))
            outline["url"], outline["origin"] = _bare(fr["url"]), fr["origin"]
            with open(os.path.join(where, f"page{i}-frame{j}.json"), "w", encoding="utf-8") as f:
                json.dump(outline, f, ensure_ascii=False, indent=1)
        shown = await s.call("Runtime.evaluate", {"expression": "document.visibilityState", "returnByValue": True})
        if t.get("type") == "page" and shown.get("result", {}).get("value") == "visible":
            shot = await s.call("Page.captureScreenshot", {"format": "png"})
            with open(os.path.join(where, f"page{i}.png"), "wb") as f:
                f.write(base64.b64decode(shot["data"]))


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


async def enter(pin: str | None = None, port: int | None = None) -> str:
    """보이는 탭부터 넣을 수 있는 키패드를 찾아 그곳의 비밀번호를 넣는다. pin·port 는 시험용(없으면
    그곳의 환경변수, 이 컨테이너의 브라우저)."""
    global _halted_until
    if time.time() < _halted_until:
        raise RuntimeError("조금 전에 비밀번호를 넣다가 멈춘 키패드가 있어 넣지 않았습니다 - 키패드에 숫자가 남아 있을 수 있습니다. "
                           "키패드를 누르지 말고 알리세요")
    port = port or find_cdp_port()
    if port is None:
        raise RuntimeError("브라우저가 떠 있지 않습니다")
    targets = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/list", timeout=5))
    pages = [t for t in targets if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]
    if not pages:
        raise RuntimeError("열린 탭이 없습니다")
    # 사람이 보는(앞에 나온) 탭부터, 그다음 나머지 - 결제 창은 새 창으로 뜨기도 한다.
    shown = [t for t in pages if await _visible(t["webSocketDebuggerUrl"])]
    # 다른 사이트 화면 안에 떠 있는 결제 출처의 프레임(쇼핑몰 위 결제창 안의 결제 화면)은 다른 프로세스라
    # 탭에서는 보이지 않는다. 그 프레임에 직접 붙어 읽고 누른다 - 누르는 좌표도 그 프레임 안의 것으로,
    # 그 프레임에 보낸다. 앞에 보이는 탭에 속한 것부터.
    by_id = {t.get("id"): t for t in targets}

    def tab_of(t: dict) -> dict | None:
        for _ in range(10):
            if t is None or t.get("type") != "iframe":
                return t
            t = by_id.get(t.get("parentId"))
        return None

    frames = [t for t in targets if t.get("type") == "iframe" and t.get("webSocketDebuggerUrl") and site_of(t.get("url", ""))]
    frames.sort(key=lambda t: tab_of(t) not in shown)
    order = shown + frames + [t for t in pages if t not in shown]

    for t in order:
        async with websockets.connect(t["webSocketDebuggerUrl"], max_size=None) as ws:
            s = _Session(ws)
            await s.call("Page.enable")
            await s.call("DOM.enable")
            try:
                found = await _keypad(s)
            except RuntimeError as e:
                await _capture(targets, str(e))
                raise
            if found is None:
                continue
            site, keys, host = found
            code = pin if pin is not None else (os.environ.get(site["env"], "") or _vault_pin(site, host))
            if not re.fullmatch(r"[0-9]{6}", code or ""):
                raise RuntimeError(f"{site['name']}가 저장되어 있지 않아 넣지 않았습니다. 키패드를 누르지 말고 저장 카드를 올리세요 - "
                                   "작업은 request_payment_pin, 대화는 save_payment_pin(운영자가 카드의 비밀 칸에 넣습니다)")
            entered = 0
            try:
                for ch in code:
                    if entered:
                        # 누를 때마다 숫자를 다시 섞는 키패드가 있다: 다음 숫자는 지금 배치에서 찾는다. 누른 직후
                        # 잠깐 다시 그리는 키패드도 있어 몇 번 더 본다.
                        again = None
                        for wait in (0, 0.3, 0.6):
                            await asyncio.sleep(wait)
                            try:
                                again = await _keypad(s)
                            except RuntimeError:
                                again = None
                            if again is not None:
                                break
                        if again is None or again[0] is not site:
                            raise RuntimeError(f"넣는 중에 키패드가 바뀌거나 사라져 {entered}자리에서 멈췄습니다. "
                                               "키패드에 숫자가 남아 있을 수 있으니 누르지 말고 알리세요")
                        keys = again[1]
                    key = keys[int(ch)]
                    if not await _hits(s, key):
                        raise RuntimeError("키패드 위를 다른 것이 덮고 있어 " +
                                           (f"{entered}자리에서 멈췄습니다. 키패드를 누르지 말고 알리세요" if entered else "넣지 않았습니다"))
                    x, y = await _center(s, key)
                    await _press(s, x, y)
                    entered += 1
                    await asyncio.sleep(random.uniform(0.12, 0.25))
            except Exception:
                if entered:
                    _halted_until = time.time() + HALT_SECONDS
                raise
            return f"{site['name']}를 넣었습니다"

    await _capture(targets, "넣을 수 있는 키패드를 찾지 못함")
    raise RuntimeError("화면에서 결제 비밀번호 키패드(0~9)를 찾지 못했거나 숫자를 확실히 읽지 못해 넣지 않았습니다(쿠팡·네이버페이 결제 화면에서만 넣습니다). "
                       "키패드를 직접 누르지 말고 알리세요")
