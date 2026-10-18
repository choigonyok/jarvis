"""저장된 로그인 정보를 지금 보이는 탭의 로그인 칸에 넣는다.

아이디·비밀번호는 보관함(vault-svc)에 봉인돼 있다. 이 도구가 지금 탭의 주소를 직접
읽어(모델이 말한 주소가 아니라) 보관함에 그 사이트의 것을 달라고 하고, 받은 값을 바로
칸에 넣고 버린다. 모델은 "넣었다"는 말만 받는다 - 아이디도 비밀번호도 보지 못한다.

다른 사이트의 화면이면 보관함이 내주지 않는다(가짜 로그인 화면에 넣는 통로가 되지 않게).
탭 안의 iframe 칸은 같은 사이트의 것일 때만 채운다.

넣은 뒤 그 폼의 '자동 로그인'·'로그인 상태 유지' 칸을 켠다. 이 브라우저는 쓰고 나서 10분쯤
지나면 꺼졌다 다시 뜨고, 쿠팡은 자동 로그인 없이 들어오면 두 시간쯤 가는 세션만 준다 - 켜지
않은 로그인은 그날 안에 풀려, 가계부 주문 조회가 "로그인이 풀렸다"로 멈췄다(2026-10-18).
켜고 들어오면 로그인 쿠키가 1년짜리다.
"""

import json
import os
import urllib.error
import urllib.request
from urllib.parse import urlparse

import websockets

import jobsites
from guard import find_cdp_port
from upload import _Session, _visible

VAULT_URL = os.environ.get("VAULT_URL", "http://vault:8105").rstrip("/")
TOKEN = os.environ.get("VAULT_RELEASE_TOKEN", "")

TOOL = {
    "name": "browser_fill_login",
    "description": (
        "지금 보이는 탭이 로그인 화면일 때, 그 사이트에 저장된 아이디와 비밀번호를 칸에 넣는다. "
        "값은 이 도구가 보관함에서 직접 받아 넣으며 당신에게는 보이지 않는다 - 묻거나 추측하지 말고 이 도구만 부른다. "
        "인자는 없다. 넣은 뒤 로그인 버튼을 누르고 browser_get_state 로 로그인됐는지 확인한다. "
        "저장된 정보가 없다고 하면, 대화에서는 운영자에게 저장(save_login)을 부탁하고 작업에서는 request_login 을 부른다."
    ),
    "inputSchema": {"type": "object", "properties": {}},
}

# 지금 프레임의 로그인 칸: [아이디 칸 또는 null, 비밀번호 칸 또는 null].
# 비밀번호 칸이 있으면 같은 폼에서 그보다 앞의 마지막 글자 칸이 아이디 칸이다.
# 아이디만 먼저 받는 화면(다음 단계에 비밀번호)은 아이디로 보이는 칸 하나만.
FIELDS_JS = r"""
(() => {
  const shown = el => {
    const r = el.getBoundingClientRect(), s = getComputedStyle(el);
    return r.width > 2 && r.height > 2 && s.visibility !== 'hidden' && s.display !== 'none' && !el.disabled && !el.readOnly;
  };
  const inputs = [...document.querySelectorAll('input')].filter(shown);
  const textLike = i => ['text', 'email', 'tel', ''].includes((i.getAttribute('type') || '').toLowerCase()) || i.autocomplete === 'username';
  const named = i => i.autocomplete === 'username' || (i.getAttribute('type') || '') === 'email' || /(user|login|email|account|^id$|_id$|userid)/i.test((i.name || '') + ' ' + (i.id || ''));
  const pw = inputs.find(i => i.type === 'password') || null;
  if (pw) {
    const scope = pw.form ? [...pw.form.querySelectorAll('input')].filter(shown) : inputs;
    const before = scope.filter(i => textLike(i) && (i.compareDocumentPosition(pw) & Node.DOCUMENT_POSITION_FOLLOWING));
    const user = scope.find(i => i.autocomplete === 'username') || before[before.length - 1] || null;
    return [user, pw];
  }
  const user = inputs.find(i => textLike(i) && named(i)) || null;
  return user ? [user, null] : null;
})()
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


async def _fields(s: _Session, domain: str) -> tuple[str | None, str | None]:
    """같은 사이트의 프레임에서 찾은 (아이디 칸, 비밀번호 칸)의 objectId."""
    for frame, url in await _frames(s):
        host = (urlparse(url).hostname or "").lower()
        if host != domain and not host.endswith("." + domain):
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


async def _type(s: _Session, object_id: str, text: str):
    await s.call("DOM.focus", {"objectId": object_id})
    # 미리 채워진 값(지난 아이디)이 있으면 지우고 넣는다.
    await s.call(
        "Runtime.callFunctionOn",
        {
            "objectId": object_id,
            "functionDeclaration": "function(){ this.value=''; this.dispatchEvent(new Event('input',{bubbles:true})); }",
        },
    )
    await s.call("Input.insertText", {"text": text})


async def fill() -> str:
    port = find_cdp_port()
    if port is None:
        raise RuntimeError("브라우저가 떠 있지 않습니다")
    targets = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/list", timeout=5))
    page = None
    for t in (t for t in targets if t.get("type") == "page" and t.get("webSocketDebuggerUrl")):
        if await _visible(t["webSocketDebuggerUrl"]):
            page = t
            break
    if page is None:
        raise RuntimeError("열린 탭이 없습니다")
    host = (urlparse(page.get("url", "")).hostname or "").lower()
    if urlparse(page.get("url", "")).scheme != "https" or not host:
        raise RuntimeError("https 로그인 화면이 아니라서 넣지 않았습니다")

    async with websockets.connect(page["webSocketDebuggerUrl"], max_size=None) as ws:
        s = _Session(ws)
        await s.call("Page.enable")
        await s.call("DOM.enable")
        user, pw = await _fields(s, _domain_of(host))
        if not user and not pw:
            raise RuntimeError("이 화면에서 로그인 칸을 찾지 못했습니다. 로그인 화면으로 가서 다시 부르세요")
        # 칸이 있을 때만 꺼낸다: 꺼낼 때마다 보관함에 기록이 남는다.
        purpose = "작업" if jobsites.current() is not None else "대화"
        got = _release(host, purpose)
        name, hint = got.get("name") or got.get("domain", host), got.get("hint", "")
        if user:
            await _type(s, user, got["username"])
        if pw:
            await _type(s, pw, got["password"])
        got.clear()
        kept = await _remember(s, pw or user)
    keep = " 자동 로그인을 켰습니다." if kept else ""
    if pw and user:
        return f"{name} 아이디({hint})와 비밀번호를 넣었습니다.{keep} 로그인 버튼을 누르세요."
    if pw:
        return f"{name} 비밀번호를 넣었습니다(아이디 칸은 없었습니다).{keep} 로그인 버튼을 누르세요."
    return f"{name} 아이디({hint})를 넣었습니다. 다음 단계에서 비밀번호 칸이 나오면 이 도구를 다시 부르세요."


async def _remember(s: _Session, field: str) -> bool:
    """그 폼의 자동 로그인 칸을 켠다. 켜지 못해도 로그인은 이어 간다 - 오래 가지 않을 뿐이다."""
    try:
        res = await s.call("Runtime.callFunctionOn", {"objectId": field, "functionDeclaration": REMEMBER_FN, "returnByValue": True})
    except RuntimeError:
        return False
    return res.get("result", {}).get("value") is True
