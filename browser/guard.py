"""결제로 나가는 요청을 사람에게 묻거나, 묻지 않고 막는다.

이 집의 결제 수단은 네이버페이 하나다. 네이버페이로 가는 요청은 카드를 띄워
사람이 결정하고, 쇼핑몰이 자기 도메인 안에서 끝내는 자사 간편결제는 묻지 않고
막는다 - 그런 결제는 PG를 거치지 않아 호스트 목록에 걸리지 않고, 실제로 쿠팡
간편결제가 카드 한 장 없이 끝난 적이 있다.

막는 쪽이 묻는 쪽보다 조용한 것은 일부러다. 결제 수단이 정해져 있는데 다른
길로 가려는 것은 사람이 판단할 문제가 아니라 애초에 일어나면 안 되는 일이고,
카드로 올리면 "승인"이 있는 이상 언젠가 눌리게 된다.

도구 층에서는 이 판단을 할 수 없다. browser_click은 조회 버튼이든 결제
버튼이든 같은 호출이다. 그래서 여기서는 도구를 보지 않고 **브라우저가 실제로
보내려는 요청**을 본다. CDP의 Fetch가 요청을 내보내기 직전에 멈춰 세우므로,
반려는 취소가 아니라 애초에 일어나지 않음이 된다.

browser-use는 건드리지 않는다. 같은 Chromium에 CDP로 따로 붙은 감시자라,
어떤 도구가 눌렀든 상관없이 걸린다.

백그라운드 작업이 도는 동안에는 하나를 더 본다: 탭의 페이지 이동이 그 작업의
사이트(jobsites.py) 밖으로 가면 막는다. 작업은 아무도 보지 않는 채로 로그인된
브라우저를 쓰므로, 읽은 페이지가 어디로 가라고 하든 제 사이트 밖으로는 못 간다.
"""

import asyncio
import base64
import glob
import json
import logging
import os
import re
import urllib.request
from fnmatch import fnmatch
from urllib.parse import urlparse

import aiohttp
import websockets

import jobsites

log = logging.getLogger("guard")

AGENT_URL = os.environ.get("JARVIS_AGENT_URL", "http://agent:8080/intercept")
TOKEN = os.environ.get("MCP_BROWSER_TOKEN", "")
HOSTS_FILE = os.environ.get("PAYMENT_HOSTS_FILE", "/app/payment-hosts.json")
# 에이전트는 카드를 올리고 바로 답한다(결정을 기다리지 않는다). 이것은 그 답의 제한 시간이다.
DECISION_TIMEOUT = int(os.environ.get("JARVIS_DECISION_TIMEOUT", "30"))

# 결제 페이지가 스스로 불러오는 이미지·폰트까지 물으면 카드가 쏟아진다.
# 판단이 필요한 것은 문서 이동과 데이터 요청뿐이다.
SKIP_TYPES = {"Image", "Stylesheet", "Font", "Media", "Script", "Manifest"}

# 묻는 것도 막는 것도 값을 보내는 요청에만 건다. 상품 페이지·주문내역을 읽는
# GET 까지 멈추면 구경조차 못 하게 되고(네이버쇼핑), 그것은 이 가드가 지키려는
# 것 - 돈이 나가는 요청 - 과 상관이 없다.
WRITE_METHODS = {"POST", "PUT", "PATCH", "DELETE"}


def load_rules() -> tuple[list[str], list[str], list[str], list[dict]]:
    """묻는 호스트, 묻는 URL 패턴, 막는 URL 패턴, 묻는 것 중 통과시킬 규칙. 예전 형식(hosts)도
    읽어서, 규칙 파일을 갈아끼우는 중에 가드가 빈손으로 뜨는 일이 없게 한다."""
    with open(HOSTS_FILE, encoding="utf-8") as f:
        data = json.load(f)
    ask = [h.strip().lower() for h in data.get("ask", data.get("hosts", [])) if h.strip()]
    ask_patterns = [p.strip().lower() for p in data.get("ask_patterns", []) if p.strip()]
    block = [p.strip().lower() for p in data.get("block", []) if p.strip()]
    passes = []
    for r in data.get("pass", []):
        if not isinstance(r, dict) or not str(r.get("url", "")).strip():
            continue
        rule = {"url": r["url"].strip().lower()}
        if r.get("json_field"):
            rule["json_field"] = r["json_field"]
            rule["values"] = {str(v).lower() for v in r.get("values", [])}
        if r.get("json_key"):
            rule["json_key"] = r["json_key"]
        if r.get("script_path"):
            rule["script_path"] = True
        passes.append(rule)
    return ask, ask_patterns, block, passes


def body_of(request: dict) -> str | None:
    """멈춘 요청의 본문. 본문이 없으면 "", 있다는데 Chrome 이 넘겨주지 않았으면(너무 길 때)
    None - 읽지 못한 본문으로는 아무것도 통과시키지 않는다."""
    if "postData" in request:
        return request["postData"]
    entries = request.get("postDataEntries")
    if entries:
        try:
            return b"".join(base64.b64decode(e.get("bytes", "")) for e in entries).decode("utf-8", "replace")
        except (ValueError, TypeError, AttributeError):
            return None
    return None if request.get("hasPostData") else ""


def shape_of(body: str | None) -> str:
    """로그에 남길 본문의 모양 - 값은 빼고: 읽지 못함, 길이, JSON 이면 맨 위 키들."""
    if body is None:
        return "본문 못 읽음"
    obj = _json_object(body)
    if obj is not None:
        return "JSON " + ",".join(list(obj)[:8])
    return f"본문 {len(body)}자"


def _json_object(body: str | None) -> dict | None:
    if not body:
        return None
    try:
        value = json.loads(body)
    except ValueError:
        return None
    return value if isinstance(value, dict) else None


def find_cdp_port() -> int | None:
    """Chromium은 실행마다 포트가 바뀌고 DevToolsActivePort를 남기지 않는다.
    같은 컨테이너 안이므로 프로세스의 인자에서 직접 읽는다."""
    for path in glob.glob("/proc/[0-9]*/cmdline"):
        try:
            cmd = open(path, "rb").read().decode("utf8", "replace")
        except OSError:
            continue
        if "chromium" not in cmd or "--type=" in cmd:
            continue
        m = re.search(r"--remote-debugging-port=(\d+)", cmd)
        if m:
            return int(m.group(1))
    return None


class Guard:
    def __init__(
        self,
        ask_hosts: list[str],
        ask_patterns: list[str],
        block_patterns: list[str],
        pass_rules: list[dict] | None = None,
    ):
        self.hosts = ask_hosts
        self.ask_patterns = ask_patterns
        self.blocks = block_patterns
        self.pass_rules = pass_rules or []
        # 멈춰 세울 대상은 세 목록의 합집합이다. 패턴에 없는 요청은 Chrome이
        # 아예 멈추지 않으므로, 여기 빠진 것은 판단 대상에도 오르지 못한다.
        self.patterns = (
            [{"urlPattern": f"*{h}*", "requestStage": "Request"} for h in ask_hosts]
            + [{"urlPattern": p, "requestStage": "Request"} for p in ask_patterns]
            + [{"urlPattern": p, "requestStage": "Request"} for p in block_patterns]
            # 페이지 이동은 전부 멈춰 본다: 작업이 도는 동안 그 사이트 밖으로
            # 가는지(leaves_job). 작업이 없으면 바로 보낸다.
            + [{"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}]
        )
        # 붙은 탭(page)의 세션 → 그 탭의 id(= 최상위 프레임 id).
        self.pages: dict[str, str] = {}
        self._next_id = 0
        self._send_lock = asyncio.Lock()
        # 승인은 여기 두지 않는다. 멈춘 요청은 매번 에이전트에 묻고, 에이전트가 승인 한 건(그 결제자·그
        # 사이트의 결제 한 건, 결제자가 돌아온 때부터 10분, 결제하기 한 번)으로 답한다 - 승인 창이 한
        # 곳에만 있어야 가장 느슨한 창이 실제 기준이 되는 일이 없다. 결제 한 건의 요청 여럿은 그 승인으로
        # 지나가므로 카드는 한 장이다.
        # 페이지가 스크립트로 받아 간 주소(호스트+경로), 최근 것부터 SCRIPTS_KEPT 개.
        # Akamai 센서는 자기 스크립트를 받아 간 바로 그 경로로 측정값을 보낸다.
        self.scripts: dict[str, None] = {}

    SCRIPTS_KEPT = 256

    def saw_script(self, url: str):
        u = urlparse(url)
        key = u.netloc.lower() + u.path
        self.scripts.pop(key, None)
        self.scripts[key] = None
        while len(self.scripts) > self.SCRIPTS_KEPT:
            self.scripts.pop(next(iter(self.scripts)))

    def leaves_job(self, session: str | None, frame_id: str | None, url: str) -> bool:
        """작업이 도는 중에, 탭의 최상위 페이지가 그 작업의 사이트 밖으로 가려는가.
        탭 안의 iframe(광고, 보안문자)은 보지 않는다 - 탭이 어디에 있는지가 문제다."""
        sites = jobsites.current()
        if sites is None:
            return False
        page = self.pages.get(session or "")
        if page is None or (frame_id and frame_id != page):
            return False
        return not jobsites.allowed(url, sites)

    def matches(self, host: str) -> bool:
        host = host.lower()
        return any(host == h or host.endswith("." + h) for h in self.hosts)

    def blocked(self, url: str, method: str) -> bool:
        if method.upper() not in WRITE_METHODS:
            return False
        url = url.lower()
        return any(fnmatch(url, p) for p in self.blocks)

    def asks(self, url: str, method: str) -> bool:
        """사람에게 물을 결제 요청인가: 값을 보내는 요청이고, 묻는 호스트로 가거나
        묻는 패턴(허용한 자사 간편결제)에 맞는다. 보기만 하는 GET 은 묻지 않는다."""
        if method.upper() not in WRITE_METHODS:
            return False
        if self.matches(urlparse(url).netloc):
            return True
        low = url.lower()
        return any(fnmatch(low, p) for p in self.ask_patterns)

    def passes(self, url: str, body: str | None) -> bool:
        """묻는 쓰기 요청 중에, 돈이 나가지 않는다고 확인해 둔 것인가(payment-hosts.json 의 pass).
        쿠팡 주문서는 배송지·결제수단을 고를 때도 결제하기와 같은 주소로 쓰기를 보내서, 이것을
        묻거나 막으면 주문서가 결제 전에 '주문중 오류'로 멈춘다. 본문을 봐야 하는 규칙은 본문을
        읽지 못하면 통과시키지 않는다 - 모르는 것은 묻는다."""
        low = url.lower()
        for rule in self.pass_rules:
            if not fnmatch(low, rule["url"]):
                continue
            if "json_field" in rule:
                value = (_json_object(body) or {}).get(rule["json_field"])
                if isinstance(value, str) and value.lower() in rule["values"]:
                    return True
                continue
            if "json_key" in rule:
                if rule["json_key"] in (_json_object(body) or {}):
                    return True
                continue
            if rule.get("script_path"):
                u = urlparse(url)
                if u.netloc.lower() + u.path in self.scripts:
                    return True
                continue
            return True
        return False

    def event_of(self, url: str, body: str | None) -> str:
        """카드에 보일 요청의 이름: 본문으로 판단하는 주소(쿠팡 주문서 이벤트)면 그 이벤트.
        결제하기는 'order' 다."""
        low = url.lower()
        for rule in self.pass_rules:
            if "json_field" in rule and fnmatch(low, rule["url"]):
                value = (_json_object(body) or {}).get(rule["json_field"])
                return value if isinstance(value, str) else "?"
        return ""

    async def send(self, ws, method, params=None, session=None):
        async with self._send_lock:
            self._next_id += 1
            msg = {"id": self._next_id, "method": method, "params": params or {}}
            if session:
                msg["sessionId"] = session
            await ws.send(json.dumps(msg))

    async def ask(self, url: str, method: str, host: str, event: str = "") -> bool:
        headers = {"Authorization": f"Bearer {TOKEN}"}
        timeout = aiohttp.ClientTimeout(total=DECISION_TIMEOUT)
        payload = {"url": url, "method": method, "host": host}
        if event:
            payload["event"] = event
        try:
            async with aiohttp.ClientSession(timeout=timeout) as s:
                async with s.post(
                    AGENT_URL,
                    json=payload,
                    headers=headers,
                ) as r:
                    body = await r.json()
                    return body.get("decision") == "approved"
        except Exception as e:
            # 물어볼 수 없으면 보내지 않는다. 판단이 불가능할 때 통과시키는
            # 것은 이 경로가 막으려던 바로 그 일이다.
            log.error("승인을 받지 못했습니다: %s", e)
            return False

    async def on_paused(self, ws, msg):
        p = msg["params"]
        session = msg.get("sessionId")
        rid = p["requestId"]
        req = p.get("request", {})
        url = req.get("url", "")
        method = req.get("method", "GET")
        host = urlparse(url).netloc

        async def go():
            await self.send(ws, "Fetch.continueRequest", {"requestId": rid}, session)

        if p.get("resourceType") == "Document" and self.leaves_job(session, p.get("frameId"), url):
            log.warning("작업의 사이트 밖으로 가는 이동을 막았습니다: %s", url[:120])
            await self.send(
                ws,
                "Fetch.failRequest",
                {"requestId": rid, "errorReason": "BlockedByClient"},
                session,
            )
            return

        if p.get("resourceType") == "Script" and method.upper() == "GET":
            self.saw_script(url)
        if p.get("resourceType") in SKIP_TYPES:
            await go()
            return

        # 막는 쪽을 먼저 본다. 정해진 결제 수단이 아닌 길은 물어볼 것이 없다.
        if self.blocked(url, method):
            log.warning("허용하지 않은 결제를 막았습니다: %s %s", method, url[:120])
            await self.send(
                ws,
                "Fetch.failRequest",
                {"requestId": rid, "errorReason": "BlockedByClient"},
                session,
            )
            return

        # 보기만 하는 요청(GET·HEAD)과 결제와 상관없는 요청은 그대로 보낸다.
        if not self.asks(url, method):
            await go()
            return

        # 결제 호스트로 가지만 돈이 나가지 않는 쓰기(주문서의 결제수단 선택, 통계, 봇 감지 센서).
        body = body_of(req)
        if self.passes(url, body):
            await go()
            return

        event = self.event_of(url, body)
        log.info("결제 요청을 멈췄습니다: %s %s%s [%s]", method, url[:120], f" ({event})" if event else "", shape_of(body))
        if await self.ask(url, method, host, event):
            await go()
        else:
            await self.send(
                ws,
                "Fetch.failRequest",
                {"requestId": rid, "errorReason": "BlockedByClient"},
                session,
            )

    async def watch(self, port: int):
        info = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/version", timeout=5))
        ws_url = info["webSocketDebuggerUrl"]

        async with websockets.connect(ws_url, max_size=None) as ws:
            log.info(
                "CDP에 붙었습니다 (port %s), 묻는 호스트 %d개 · 묻는 패턴 %d개 · 막는 패턴 %d개 · 통과 규칙 %d개",
                port,
                len(self.hosts),
                len(self.ask_patterns),
                len(self.blocks),
                len(self.pass_rules),
            )
            await self.send(
                ws,
                "Target.setAutoAttach",
                # 새 탭은 가드가 Fetch 를 켤 때까지 멈춰 있다. False 면 탭이 뜨자마자
                # 보내는 첫 요청이 감시가 붙기 전에 나갈 수 있다.
                {"autoAttach": True, "waitForDebuggerOnStart": True, "flatten": True},
            )
            async for raw in ws:
                msg = json.loads(raw)
                method = msg.get("method")
                if method == "Target.attachedToTarget":
                    t = msg["params"]["targetInfo"]
                    session = msg["params"]["sessionId"]
                    if t.get("type") == "page":
                        self.pages[session] = t.get("targetId", "")
                    if t.get("type") in ("page", "iframe"):
                        # 패턴에 걸리는 요청만 멈춘다. guard가 죽어도 일반
                        # 탐색은 영향을 받지 않는다는 뜻이다.
                        await self.send(ws, "Fetch.enable", {"patterns": self.patterns}, session)
                    if msg["params"].get("waitingForDebugger"):
                        # 감시를 켠 뒤에야 풀어 준다. 워커처럼 감시하지 않는
                        # 대상도 멈춰 있으므로 반드시 풀어야 한다.
                        await self.send(ws, "Runtime.runIfWaitingForDebugger", {}, session)
                elif method == "Target.detachedFromTarget":
                    self.pages.pop(msg["params"].get("sessionId", ""), None)
                elif method == "Fetch.requestPaused":
                    asyncio.create_task(self.on_paused(ws, msg))


async def main():
    logging.basicConfig(level=logging.INFO, format="guard: %(message)s")
    guard = Guard(*load_rules())
    while True:
        port = find_cdp_port()
        if port is None:
            await asyncio.sleep(3)
            continue
        try:
            await guard.watch(port)
        except Exception as e:
            log.info("CDP 연결이 끊겼습니다 (%s). 다시 찾습니다.", type(e).__name__)
        await asyncio.sleep(3)


if __name__ == "__main__":
    asyncio.run(main())
