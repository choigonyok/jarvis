"""저장된 로그인 정보를 에이전트가 보고 있는 탭의 로그인 칸에 넣는다.

아이디·비밀번호는 보관함(vault-svc)에 봉인돼 있다. 이 도구가 그 탭의 주소를 직접 읽어(모델이 말한 주소가
아니라) 보관함에 그 사이트의 것을 달라고 하고, 받은 값을 바로 칸에 넣고 버린다. 모델은 "넣었다"는 말만
받는다 - 아이디도 비밀번호도 보지 못한다.

어느 탭인가: 브리지가 browser-use 에 지금 보고 있는 탭의 주소(browser_get_state 의 url - 모델이 보는 화면)를
물어 넘기고, 그 주소의 탭에만 넣는다(upload.page_for). 그런 탭이 없으면 넣지 않는다. 이 브라우저는 창 관리자
없이 떠서, 로그인 팝업 창과 그 팝업을 연 탭이 둘 다 '보이는' 탭이다 - 보이는지로는 고를 수 없다. 주소를
묻지 못했을 때만 앞에 보이는 첫 탭에 넣는다.

다른 사이트의 화면이면 보관함이 내주지 않는다(가짜 로그인 화면에 넣는 통로가 되지 않게). 탭 안의 iframe 칸은
같은 사이트의 것일 때만 채운다.

아이디만 먼저 묻는 화면(구글: 이메일 → 다음 → 비밀번호)은 그 화면이 로그인 단계로 분명할 때 - 아이디 칸이
그 폼(폼이 없으면 화면)의 유일한 입력 칸이고, 주소·제목·제목줄·폼 글자가 로그인을 말하고, 비밀번호 찾기·가입·
구독 화면으로 보이지 않을 때 - 한 번에 넣는다. 아이디를 넣고 사람처럼 다음 단계로 넘긴다: 그 칸에서 Enter,
그것이 먹지 않으면(주소도 그대로고 탭이 아무 요청도 보내지 않으면) 그 칸 가까이의 '다음' 버튼 하나. 그리고
같은 탭에 같은 사이트의 비밀번호 칸이 나오기를 NEXT_WAIT 초까지 기다려 비밀번호를 넣는다. 다른 사이트로
넘어가면 넣지 않는다. 보관함에서는 부를 때마다 한 번만 꺼내고, 값은 그 기다림 동안만 메모리에 둔다. 마지막
로그인 버튼은 누르지 않는다 - 모델이 누르고 browser_get_state 로 확인한다. 로그인 단계로 분명하지 않으면
아이디만 넣고, 다음을 눌러 비밀번호 칸이 나오면 다시 부르라고 한다.

넣은 뒤 그 폼의 '자동 로그인'·'로그인 상태 유지' 칸을 켠다. 이 브라우저는 쓰고 나서 10분쯤 지나면 꺼졌다 다시
뜨고, 쿠팡은 자동 로그인 없이 들어오면 두 시간쯤 가는 세션만 준다 - 켜지 않은 로그인은 그날 안에 풀려 작업
(가계부 주문 조회 등)이 멈춘다. 켜고 들어오면 로그인 쿠키가 1년짜리다.
"""

import asyncio
import json
import os
import re
import time
import urllib.error
import urllib.request
from urllib.parse import urlparse

import websockets

import jobsites
from guard import find_cdp_port
from upload import _Session, _same_page, page_for

VAULT_URL = os.environ.get("VAULT_URL", "http://vault:8105").rstrip("/")
TOKEN = os.environ.get("VAULT_RELEASE_TOKEN", "")

# 아이디만 묻는 화면에서 다음으로 넘긴 뒤 비밀번호 칸을 기다리는 시간(초), 그동안 화면을 보는 간격, Enter 로
# 넘어가지 않을 때 '다음' 버튼을 누르기까지 기다리는 시간.
NEXT_WAIT, NEXT_POLL, CLICK_AFTER = 10.0, 0.25, 2.0

TOOL = {
    "name": "browser_fill_login",
    "description": (
        "지금 보고 있는 탭(browser_get_state 가 보여 주는 화면)이 로그인 화면일 때, 그 사이트에 저장된 아이디와 비밀번호를 칸에 넣는다. "
        "값은 이 도구가 보관함에서 직접 받아 넣으며 당신에게는 보이지 않는다 - 묻거나 추측하지 말고 이 도구만 부른다. 인자는 없다. "
        "아이디만 먼저 묻는 화면(구글처럼 이메일 → 다음 → 비밀번호)이면 아이디를 넣고 다음 단계로 넘겨 비밀번호까지 넣는다. "
        "넣은 뒤 로그인 버튼(화면에 따라 '다음')을 누르고 browser_get_state 로 로그인됐는지 확인한다. "
        "비밀번호 칸이 나오지 않았다고 하면 화면을 보고, 보안문자·휴대폰 승인·2단계 인증은 추측하지 말고 운영자에게 부탁한다. "
        "저장된 정보가 없다고 하면, 대화에서는 운영자에게 저장(save_login)을 부탁하고 작업에서는 request_login 을 부른다."
    ),
    "inputSchema": {"type": "object", "properties": {}},
}

# 지금 프레임의 로그인 칸: [아이디 칸 또는 null, 비밀번호 칸 또는 null].
# 비밀번호 칸이 있으면 같은 폼에서 autocomplete 에 username 낱말이 든 칸, 없으면 그보다 앞의 마지막 글자 칸이
# 아이디 칸이다. 아이디만 먼저 받는 화면(다음 단계에 비밀번호)은 아이디로 보이는 칸 하나만.
# autocomplete 는 낱말로 본다 - 구글은 "username webauthn" 이다. 보이는 칸만 본다: 크기가 있고, 숨기거나 막지
# 않았고(inert), 화면 왼쪽·위 밖으로 밀어 두지 않은 칸 - 비밀번호 단계에 숨겨 둔 아이디 칸에 넣지 않게.
FIELDS_JS = r"""
(() => {
  const shown = el => {
    const r = el.getBoundingClientRect(), s = getComputedStyle(el);
    return r.width > 2 && r.height > 2 && r.right + scrollX > 0 && r.bottom + scrollY > 0 &&
      s.visibility !== 'hidden' && s.display !== 'none' && !el.disabled && !el.readOnly && !el.closest('[inert]');
  };
  const inputs = [...document.querySelectorAll('input')].filter(shown);
  const isUser = i => /\busername\b/i.test(i.getAttribute('autocomplete') || '');
  const textLike = i => ['text', 'email', 'tel', ''].includes((i.getAttribute('type') || '').toLowerCase()) || isUser(i);
  const named = i => isUser(i) || (i.getAttribute('type') || '').toLowerCase() === 'email' ||
    [i.name, i.id].some(v => /(user|login|email|account|identifier|^id$|_id$|userid)/i.test(v || ''));
  const pw = inputs.find(i => i.type === 'password') || null;
  if (pw) {
    const scope = pw.form ? [...pw.form.querySelectorAll('input')].filter(shown) : inputs;
    const before = scope.filter(i => textLike(i) && (i.compareDocumentPosition(pw) & Node.DOCUMENT_POSITION_FOLLOWING));
    const user = scope.find(i => i !== pw && isUser(i)) || before[before.length - 1] || null;
    return [user, pw];
  }
  const user = inputs.find(i => textLike(i) && named(i)) || null;
  return user ? [user, null] : null;
})()
"""

# 아이디 칸(this)에서 본 화면: 그 폼(없으면 화면)에 다른 입력 칸이 보이는가, 그 프레임의 주소·제목, 화면의
# 제목줄(h1·h2), 그 폼(없으면 칸을 품은 구역)의 글자. 로그인 단계인지는 sign_in_step 이 가린다.
STEP_FN = r"""
function() {
  const looks = el => {
    const r = el.getBoundingClientRect(), s = getComputedStyle(el);
    return r.width > 2 && r.height > 2 && r.right + scrollX > 0 && r.bottom + scrollY > 0 && s.visibility !== 'hidden' && s.display !== 'none';
  };
  const skip = ['checkbox', 'radio', 'submit', 'button', 'reset', 'image', 'file', 'hidden', 'range', 'color'];
  const all = this.form ? [...this.form.elements] : [...this.ownerDocument.querySelectorAll('input, textarea')];
  const others = all.filter(el => el !== this && ['INPUT', 'TEXTAREA'].includes(el.tagName) && !skip.includes((el.type || '').toLowerCase()) && looks(el));
  const box = this.form || this.closest('section, main, [role=main], [role=dialog], dialog') || this.ownerDocument.body;
  const heads = [...this.ownerDocument.querySelectorAll('h1, h2, [role=heading]')].filter(looks).map(h => h.innerText || '').join(' ');
  return {alone: others.length === 0, url: location.href, title: this.ownerDocument.title || '',
          heads: heads.slice(0, 300), text: ((box && box.innerText) || '').slice(0, 600)};
}
"""

# 로그인 단계로 보이는 말 - 주소·제목·제목줄·폼 글자에서 찾는다. LOG_IN 은 그중 로그인이라고 바로 말하는 것.
LOG_IN = re.compile(r"sign[\s_-]?in|log[\s_-]?in|log[\s_-]?on|로그인", re.I)
SIGN_IN = re.compile(LOG_IN.pattern + r"|identifier|oauth|auth|account|계정", re.I)
# 로그인이 아닌 화면으로 보이는 말 - 주소(? 앞)·제목·제목줄에서만 찾는다. 폼 글자는 보지 않는다: 로그인 폼에도
# '이메일을 잊으셨나요?', '계정 만들기' 같은 링크가 있다. 찾기·재설정·구독 화면은 어디에 나와도 아니다.
NOT_SIGN_IN = re.compile(r"forgot|reset|recover|subscri|newsletter|찾기|재설정|구독|탈퇴", re.I)
# 가입 화면으로 보이는 말 - 주소에 있으면 아니고, 제목·제목줄은 로그인(LOG_IN)을 함께 말하지 않을 때만 아니다
# ('Sign in or create account', '로그인 또는 회원가입'은 로그인 화면, '계정 만들기'는 아니다).
SIGN_UP = re.compile(r"sign[\s_-]?up|register|join|create|가입|만들기", re.I)

# 그 칸에 커서를 두고 남은 값(지난 아이디)을 지운다. 커서가 그 칸에 있어야 true - 아니면 넣지 않는다(다른 칸에
# 들어가지 않게).
CLEAR_FN = r"""
function() {
  this.focus();
  this.value = '';
  this.dispatchEvent(new Event('input', {bubbles: true}));
  return this.isConnected && this.getRootNode().activeElement === this;
}
"""

# 커서가 그 칸에 있는가 - Enter 를 그 칸에만 보낸다.
FOCUSED_FN = "function() { return this.isConnected && this.getRootNode().activeElement === this; }"

# 아이디 칸이 아직 보이는가 - 다음 단계로 넘어갔으면 '다음' 버튼을 또 누르지 않는다.
SHOWN_FN = r"""
function() {
  if (!this.isConnected) return false;
  const r = this.getBoundingClientRect(), s = getComputedStyle(this);
  return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none';
}
"""

# 아이디 칸(this)의 '다음' 버튼: 이름이 정확히 다음·계속·확인·로그인·next·continue·sign in·log in 인 보이는
# 버튼. 그 폼에서 찾고, 폼이 없거나 폼에 없으면 칸에서 바깥으로 한 겹씩 넓혀 그런 버튼이 처음 나오는 곳에서
# 찾는다(구글은 폼이 없고 버튼이 칸의 구역 밖에 있다). 그곳에 하나일 때만 고른다. 'Google 계정으로 계속'처럼
# 다른 곳으로 가는 버튼은 이름이 달라 고르지 않는다.
NEXT_FN = r"""
function() {
  const words = /^(다음|계속|계속하기|확인|로그인|next|continue|sign\s*in|log\s*in)$/i;
  const looks = el => {
    const r = el.getBoundingClientRect(), s = getComputedStyle(el);
    return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none' && !el.disabled && el.getAttribute('aria-disabled') !== 'true';
  };
  const label = el => ((el.tagName === 'INPUT' ? el.value : (el.innerText || el.textContent)) || '').trim() || (el.getAttribute('aria-label') || '').trim();
  const within = box => [...box.querySelectorAll('button, input[type=submit], input[type=button], [role=button]')].filter(b => looks(b) && words.test(label(b)));
  for (const box of [this.form, ...(function* (a) { for (; a; a = a.parentElement) yield a; })(this.parentElement)]) {
    if (!box) continue;
    const found = within(box);
    if (found.length) return found.length === 1 ? found[0] : null;
  }
  return null;
}
"""

# 버튼의 한가운데를 누르면 그 버튼이 눌리는가 - 다른 것(로딩 막)이 덮고 있으면 누르지 않는다.
HIT_FN = r"""
function() {
  const r = this.getBoundingClientRect();
  const hit = this.ownerDocument.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
  return !!hit && (hit === this || this.contains(hit));
}
"""

# 로그인 칸(this)과 같은 폼에서 '로그인을 유지하는' 체크 칸을 찾아 켠다. 켰거나 이미 켜져 있으면
# true, 그런 칸이 없으면 false. 레이블 글자로 알아본다: 쿠팡 '자동 로그인', 네이버 '로그인 상태 유지'.
REMEMBER_FN = r"""
function() {
  const pat = /자동\s*로그인|로그인\s*(상태\s*)?유지|remember\s*me|keep\s*me\s*(signed|logged)\s*in|stay\s*signed\s*in/i;
  const scope = this.form || this.closest('section, form, main') || document;
  for (const box of scope.querySelectorAll('input[type=checkbox]')) {
    const said = [box.getAttribute('aria-label') || '', box.name || '', box.id || ''];
    for (const l of box.labels || []) said.push(l.innerText || '');
    if (box.closest('label')) said.push(box.closest('label').innerText || '');
    if (box.parentElement) said.push(box.parentElement.innerText || '');
    if (!said.some(t => pat.test(t))) continue;
    if (!box.checked) box.click();
    return box.checked;
  }
  return false;
}
"""


def _domain_of(host: str) -> str:
    return jobsites._domain(host)


def _on_site(host: str, domain: str) -> bool:
    return host == domain or host.endswith("." + domain)


def sign_in_step(urls: list[str], titles: list[str], heads: str, text: str) -> bool:
    """아이디만 묻는 화면이 로그인 단계로 분명한가 - 그 칸 하나뿐인지는 부르는 쪽이 본다. urls 는 탭과 그 칸의
    프레임 주소, titles 는 그 제목, heads 는 화면의 제목줄, text 는 그 폼의 글자."""
    places = [f"{u.hostname or ''}{u.path}" for u in map(urlparse, urls) if u.hostname]
    named = [x or "" for x in [*titles, heads]]
    if any(NOT_SIGN_IN.search(x) for x in [*places, *named]):
        return False
    if any(SIGN_UP.search(x) for x in places) or any(SIGN_UP.search(x) and not LOG_IN.search(x) for x in named):
        return False
    return any(SIGN_IN.search(x or "") for x in [*urls, *named, text])


def _release(host: str, purpose: str) -> dict:
    if not TOKEN:
        raise RuntimeError("보관함 토큰(VAULT_RELEASE_TOKEN)이 없어 저장된 로그인 정보를 쓸 수 없습니다")
    req = urllib.request.Request(
        f"{VAULT_URL}/release",
        data=json.dumps({"host": host, "purpose": purpose}).encode(),
        method="POST",
        headers={"Authorization": f"Bearer {TOKEN}", "Content-Type": "application/json"},
    )
    try:
        return json.load(urllib.request.urlopen(req, timeout=10))
    except urllib.error.HTTPError as e:
        try:
            msg = json.load(e).get("error", "")
        except Exception:
            msg = ""
        raise RuntimeError(msg or f"보관함이 {e.code} 로 답했습니다") from None


async def _frames(s: _Session) -> list[tuple[str, str]]:
    tree = (await s.call("Page.getFrameTree"))["frameTree"]
    out, stack = [], [tree]
    while stack:
        node = stack.pop()
        out.append((node["frame"]["id"], node["frame"].get("url", "")))
        stack.extend(node.get("childFrames", []))
    return out


async def _top_url(s: _Session) -> str:
    return (await s.call("Page.getFrameTree"))["frameTree"]["frame"].get("url", "")


async def _fields(s: _Session, domain: str) -> tuple[str | None, str | None]:
    """같은 사이트의 프레임에서 찾은 (아이디 칸, 비밀번호 칸)의 objectId."""
    for frame, url in await _frames(s):
        host = (urlparse(url).hostname or "").lower()
        if not _on_site(host, domain):
            continue
        try:
            ctx = (await s.call("Page.createIsolatedWorld", {"frameId": frame, "worldName": "jarvis-login"}))["executionContextId"]
        except RuntimeError:
            continue
        res = (await s.call("Runtime.evaluate", {"expression": FIELDS_JS, "contextId": ctx})).get("result", {})
        if res.get("subtype") == "null" or not res.get("objectId"):
            continue
        props = (await s.call("Runtime.getProperties", {"objectId": res["objectId"], "ownProperties": True}))["result"]
        ids = {p["name"]: p.get("value", {}).get("objectId") for p in props if p["name"] in ("0", "1")}
        if ids.get("0") or ids.get("1"):
            return ids.get("0"), ids.get("1")
    return None, None


async def _on(s: _Session, object_id: str, fn: str) -> bool:
    """칸·버튼(object_id)에 fn 을 불러 그 답이 true 인가. 그 요소가 사라졌으면(화면이 바뀜) False."""
    try:
        res = await s.call("Runtime.callFunctionOn", {"objectId": object_id, "functionDeclaration": fn, "returnByValue": True})
    except RuntimeError:
        return False
    return res.get("result", {}).get("value") is True


async def _type(s: _Session, object_id: str, text: str):
    await s.call("DOM.focus", {"objectId": object_id})
    # 미리 채워진 값(지난 아이디)이 있으면 지우고 넣는다. 커서가 그 칸에 없으면 넣지 않는다.
    if not await _on(s, object_id, CLEAR_FN):
        raise RuntimeError("로그인 칸에 커서를 두지 못해 넣지 않았습니다. browser_get_state 로 화면을 보고 다시 부르세요")
    await s.call("Input.insertText", {"text": text})


async def _sign_in_step(s: _Session, user: str, page: dict) -> bool:
    """아이디만 묻는 이 화면에서 아이디를 넣고 다음 단계로 넘겨도 되는가(STEP_FN, sign_in_step)."""
    try:
        res = await s.call("Runtime.callFunctionOn", {"objectId": user, "functionDeclaration": STEP_FN, "returnByValue": True})
    except RuntimeError:
        return False
    seen = res.get("result", {}).get("value") or {}
    if not seen.get("alone"):
        return False
    return sign_in_step([page.get("url", ""), seen.get("url", "")], [page.get("title", ""), seen.get("title", "")],
                        seen.get("heads", ""), seen.get("text", ""))


def _requests(s: _Session) -> int:
    """이 탭이 지금까지 보낸 요청 수(Network.enable 뒤부터, 응답을 기다리는 사이 읽은 알림으로 센다)."""
    return s.seen.get("Network.requestWillBeSent", 0)


async def _press_enter(s: _Session, user: str) -> bool:
    """아이디 칸에서 Enter 를 누른다 - 커서가 그 칸에 있을 때만(다른 곳에 Enter 가 가지 않게)."""
    if not await _on(s, user, FOCUSED_FN):
        return False
    for kind in ("keyDown", "keyUp"):
        key = {"type": kind, "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13, "nativeVirtualKeyCode": 13}
        if kind == "keyDown":
            key.update(text="\r", unmodifiedText="\r")
        await s.call("Input.dispatchKeyEvent", key)
    return True


async def _click_next(s: _Session, user: str) -> bool:
    """아이디 칸이 그대로 보이면 그 칸의 '다음' 버튼(NEXT_FN)을 마우스로 누른다. 누르면 True."""
    if not await _on(s, user, SHOWN_FN):
        return False
    try:
        button = (await s.call("Runtime.callFunctionOn", {"objectId": user, "functionDeclaration": NEXT_FN})).get("result", {})
        if button.get("subtype") == "null" or not button.get("objectId"):
            return False
        await s.call("DOM.scrollIntoViewIfNeeded", {"objectId": button["objectId"]})
        if not await _on(s, button["objectId"], HIT_FN):
            return False
        quads = (await s.call("DOM.getContentQuads", {"objectId": button["objectId"]})).get("quads") or []
        if not quads:
            return False
        x, y = sum(quads[0][0::2]) / 4, sum(quads[0][1::2]) / 4
        await s.call("Input.dispatchMouseEvent", {"type": "mouseMoved", "x": x, "y": y})
        await s.call("Input.dispatchMouseEvent", {"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1})
        await asyncio.sleep(0.06)
        await s.call("Input.dispatchMouseEvent", {"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1})
    except RuntimeError:
        return False
    return True


async def _next_step(s: _Session, user: str, domain: str, password: str, wait: float) -> tuple[str | None, str, bool]:
    """아이디를 넣은 칸(user)에서 다음 단계로 넘기고, 같은 탭에 같은 사이트의 비밀번호 칸이 보이면 password 를
    넣는다. 화면은 같은 문서 안에서 바뀌기도(구글) 새 주소로 넘어가기도 해서, 볼 때마다 프레임과 칸을 처음부터
    다시 찾는다. 돌려주는 것: (넣은 비밀번호 칸 또는 None, 넘어간 다른 사이트 또는 '', 다음으로 넘겼는가) -
    둘 다 없으면 wait 초 안에 비밀번호 칸이 나오지 않은 것이다."""
    await asyncio.sleep(0.3)
    # 아이디를 넣자마자 비밀번호 칸을 펼치는 화면이면 넘기지 않고 바로 넣는다.
    try:
        _, pw = await _fields(s, domain)
    except RuntimeError:
        pw = None
    if pw:
        await _type(s, pw, password)
        return pw, "", False
    # Enter 뒤에 이 탭이 요청을 보냈는지 본다 - 보냈으면 화면이 다음 단계를 받아 일하는 중이다(구글은 계정을
    # 묻는 요청을 보내고 1~3초 뒤에 바뀐다). 그때는 '다음' 버튼을 또 누르지 않는다.
    try:
        await s.call("Network.enable")
    except RuntimeError:
        pass  # 세지 못하면 Enter 가 먹지 않은 것으로 보고 '다음'을 누른다
    start = await _top_url(s)
    sent = _requests(s)
    began = time.monotonic()
    click_at = CLICK_AFTER if await _press_enter(s, user) else 0.0
    clicked = False
    while time.monotonic() - began < wait:
        await asyncio.sleep(NEXT_POLL)
        try:
            top = await _top_url(s)
        except RuntimeError:
            continue
        u = urlparse(top)
        host = (u.hostname or "").lower()
        if u.scheme in ("http", "https") and (u.scheme != "https" or not _on_site(host, domain)):
            return None, host or top, True
        try:
            _, pw = await _fields(s, domain)
        except RuntimeError:
            continue  # 넘어가는 중이라 프레임이나 실행 맥락이 바뀌었다
        if pw:
            try:
                await _type(s, pw, password)
            except RuntimeError:
                continue  # 그사이 다시 그려졌다 - 다음 차례에 다시 찾는다
            return pw, "", True
        if not clicked and time.monotonic() - began >= click_at and _same_page(top) == _same_page(start):
            # 주소도 그대로고 탭이 아무 요청도 보내지 않았으면 Enter 가 먹지 않은 것이다.
            clicked = True
            if _requests(s) == sent:
                await _click_next(s, user)
    return None, "", True


async def fill(url: str | None = None, port: int | None = None, wait: float = NEXT_WAIT) -> str:
    """url 은 에이전트가 보고 있는 탭의 주소(브리지가 browser-use 에 물은 것 - upload.page_for). port·wait 는
    시험용(없으면 이 컨테이너의 브라우저, NEXT_WAIT)."""
    port = port or find_cdp_port()
    if port is None:
        raise RuntimeError("브라우저가 떠 있지 않습니다")
    page = await page_for(port, url)
    if page is None:
        raise RuntimeError("열린 탭이 없습니다")
    host = (urlparse(page.get("url", "")).hostname or "").lower()
    if urlparse(page.get("url", "")).scheme != "https" or not host:
        raise RuntimeError("https 로그인 화면이 아니라서 넣지 않았습니다")
    domain = _domain_of(host)

    async with websockets.connect(page["webSocketDebuggerUrl"], max_size=None) as ws:
        s = _Session(ws)
        await s.call("Page.enable")
        await s.call("DOM.enable")
        user, pw = await _fields(s, domain)
        if not user and not pw:
            raise RuntimeError("이 화면에서 로그인 칸을 찾지 못했습니다. 로그인 화면으로 가서 다시 부르세요")
        two_step = not pw and await _sign_in_step(s, user, page)
        # 칸이 있을 때만, 부를 때마다 한 번 꺼낸다: 꺼낼 때마다 보관함에 기록이 남는다.
        purpose = "작업" if jobsites.current() is not None else "대화"
        got = _release(host, purpose)
        left, moved = "", False
        try:
            name, hint = got.get("name") or got.get("domain", host), got.get("hint", "")
            if user:
                await _type(s, user, got["username"])
            if pw:
                await _type(s, pw, got["password"])
            elif two_step:
                pw, left, moved = await _next_step(s, user, domain, got["password"], wait)
        finally:
            got.clear()
        kept = await _remember(s, pw or user)
    keep = " 자동 로그인을 켰습니다." if kept else ""
    if pw and moved:
        return f"{name} 아이디({hint})를 넣고 다음 단계로 넘겨 비밀번호까지 넣었습니다.{keep} 로그인 버튼(화면에 따라 '다음')을 누르세요."
    if pw and user:
        return f"{name} 아이디({hint})와 비밀번호를 넣었습니다.{keep} 로그인 버튼을 누르세요."
    if pw:
        return f"{name} 비밀번호를 넣었습니다(아이디 칸은 없었습니다).{keep} 로그인 버튼을 누르세요."
    if two_step and left:
        return (f"{name} 아이디({hint})를 넣고 다음 단계로 넘겼더니 다른 사이트({left})로 넘어가 비밀번호는 넣지 않았습니다. "
                "browser_get_state 로 화면을 보세요 - 그 사이트의 로그인 화면이면 이 도구를 다시 부르면 그 사이트에 저장된 정보로 넣습니다.")
    if two_step:
        return (f"{name} 아이디({hint})를 넣고 다음 단계로 넘겼지만 {wait:g}초 안에 비밀번호 칸이 나오지 않았습니다. "
                "browser_get_state 로 화면을 보세요 - 비밀번호 칸이 나와 있으면 이 도구를 다시 부르고, 보안문자·휴대폰 승인·"
                "2단계 인증을 묻는 화면이면 추측하지 말고 운영자에게 부탁하세요.")
    return f"{name} 아이디({hint})를 넣었습니다. 다음 버튼을 누르고, 비밀번호 칸이 나오면 이 도구를 다시 부르세요."


async def _remember(s: _Session, field: str) -> bool:
    """그 폼의 자동 로그인 칸을 켠다. 켜지 못해도 로그인은 이어 간다 - 오래 가지 않을 뿐이다."""
    try:
        res = await s.call("Runtime.callFunctionOn", {"objectId": field, "functionDeclaration": REMEMBER_FN, "returnByValue": True})
    except RuntimeError:
        return False
    return res.get("result", {}).get("value") is True
