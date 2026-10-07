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
"""

import asyncio
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

log = logging.getLogger("guard")

AGENT_URL = os.environ.get("JARVIS_AGENT_URL", "http://agent:8080/intercept")
TOKEN = os.environ.get("MCP_BROWSER_TOKEN", "")
HOSTS_FILE = os.environ.get("PAYMENT_HOSTS_FILE", "/app/payment-hosts.json")
# 사람이 카드를 보고 결정할 때까지. 에이전트 쪽 대기와 맞춰 넉넉히 잡는다.
DECISION_TIMEOUT = int(os.environ.get("JARVIS_DECISION_TIMEOUT", "2400"))

# 결제 페이지가 스스로 불러오는 이미지·폰트까지 물으면 카드가 쏟아진다.
# 판단이 필요한 것은 문서 이동과 데이터 요청뿐이다.
SKIP_TYPES = {"Image", "Stylesheet", "Font", "Media", "Script", "Manifest"}

# 열람 전용 예외는 값을 보내지 않는 요청에만 건다.
READ_METHODS = {"GET", "HEAD"}

# 차단은 값을 보내는 요청에만 건다. 주문내역을 읽는 GET까지 막으면 구경조차
# 못 하게 되고, 그것은 이 가드가 지키려는 것과 상관이 없다.
WRITE_METHODS = {"POST", "PUT", "PATCH"}


def load_rules() -> tuple[list[str], list[str], set[tuple[str, str]]]:
    """묻는 호스트와 막는 URL 패턴. 예전 형식(hosts)도 읽어서, 규칙 파일을
    갈아끼우는 중에 가드가 빈손으로 뜨는 일이 없게 한다."""
    with open(HOSTS_FILE, encoding="utf-8") as f:
        data = json.load(f)
    ask = [h.strip().lower() for h in data.get("ask", data.get("hosts", [])) if h.strip()]
    block = [p.strip().lower() for p in data.get("block", []) if p.strip()]
    return ask, block, parse_views(data.get("view", []))


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


def parse_views(entries: list[str]) -> set[tuple[str, str]]:
    """"host/path" 를 (host, path) 로. 비교할 때와 같은 모양으로 미리 맞춰 둔다.

    "host/path/*" 는 그 경로 **아래**(path/...)를 뜻한다. path 자체나 이름이
    비슷한 형제(pathX)는 포함하지 않는다."""
    out = set()
    for e in entries:
        e = e.strip().lower()
        if not e or "/" not in e:
            continue
        host, path = e.split("/", 1)
        if path.endswith("/*"):
            out.add((host, "/" + path[:-2].rstrip("/") + "/*"))
        else:
            out.add((host, "/" + path.rstrip("/")))
    return out


def viewable(views: set[tuple[str, str]], url: str, method: str) -> bool:
    """열람 전용인가. GET·HEAD 이고 호스트가 같고, 경로가 정확히 같거나 "/*" 항목의 아래여야 한다."""
    if method.upper() not in READ_METHODS:
        return False
    u = urlparse(url)
    host = u.netloc.lower()
    path = (u.path.rstrip("/") or "/").lower()
    if (host, path) in views:
        return True
    for h, p in views:
        if h == host and p.endswith("/*") and path.startswith(p[:-1]):
            return True
    return False


class Guard:
    def __init__(self, ask_hosts: list[str], block_patterns: list[str], views: set[tuple[str, str]] | None = None):
        self.hosts = ask_hosts
        self.blocks = block_patterns
        self.views = views or set()
        # 멈춰 세울 대상은 두 목록의 합집합이다. 패턴에 없는 요청은 Chrome이
        # 아예 멈추지 않으므로, 여기 빠진 것은 판단 대상에도 오르지 못한다.
        self.patterns = [
            {"urlPattern": f"*{h}*", "requestStage": "Request"} for h in ask_hosts
        ] + [{"urlPattern": p, "requestStage": "Request"} for p in block_patterns]
        self._next_id = 0
        self._send_lock = asyncio.Lock()
        # 한 번 승인한 결제 흐름은 같은 호스트의 후속 요청마다 다시 묻지 않는다.
        # 결제 한 건이 카드 한 장이어야 쓸 수 있다.
        self._approved: dict[str, float] = {}

    def matches(self, host: str) -> bool:
        host = host.lower()
        return any(host == h or host.endswith("." + h) for h in self.hosts)

    def blocked(self, url: str, method: str) -> bool:
        if method.upper() not in WRITE_METHODS:
            return False
        url = url.lower()
        return any(fnmatch(url, p) for p in self.blocks)

    async def send(self, ws, method, params=None, session=None):
        async with self._send_lock:
            self._next_id += 1
            msg = {"id": self._next_id, "method": method, "params": params or {}}
            if session:
                msg["sessionId"] = session
            await ws.send(json.dumps(msg))

    async def ask(self, url: str, method: str, host: str) -> bool:
        headers = {"Authorization": f"Bearer {TOKEN}"}
        timeout = aiohttp.ClientTimeout(total=DECISION_TIMEOUT)
        try:
            async with aiohttp.ClientSession(timeout=timeout) as s:
                async with s.post(
                    AGENT_URL,
                    json={"url": url, "method": method, "host": host},
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

        if p.get("resourceType") in SKIP_TYPES:
            await go()
            return

        # 막는 쪽을 먼저 본다. 정해진 결제 수단이 아닌 길은 물어볼 것이 없다.
        if self.blocked(url, method):
            log.warning("네이버페이가 아닌 결제를 막았습니다: %s %s", method, url[:120])
            await self.send(
                ws,
                "Fetch.failRequest",
                {"requestId": rid, "errorReason": "BlockedByClient"},
                session,
            )
            return

        if not self.matches(host):
            await go()
            return

        # 결제 도메인 안의 주문내역처럼, 보여 주기만 하는 페이지는 묻지 않는다.
        if viewable(self.views, url, method):
            log.info("열람 전용 페이지라 묻지 않고 보냈습니다: %s %s", method, url[:120])
            await go()
            return

        loop = asyncio.get_running_loop()
        if loop.time() < self._approved.get(host, 0):
            await go()
            return

        log.info("결제 요청을 멈췄습니다: %s %s", method, url[:120])
        if await self.ask(url, method, host):
            # 결제 한 건은 요청 여러 개로 이뤄진다. 승인 뒤 잠깐은 같은
            # 호스트를 다시 묻지 않는다.
            self._approved[host] = loop.time() + 300
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
                "CDP에 붙었습니다 (port %s), 묻는 호스트 %d개 · 막는 패턴 %d개 · 열람 전용 %d개",
                port,
                len(self.hosts),
                len(self.blocks),
                len(self.views),
            )
            await self.send(
                ws,
                "Target.setAutoAttach",
                {"autoAttach": True, "waitForDebuggerOnStart": False, "flatten": True},
            )
            async for raw in ws:
                msg = json.loads(raw)
                method = msg.get("method")
                if method == "Target.attachedToTarget":
                    t = msg["params"]["targetInfo"]
                    if t.get("type") in ("page", "iframe"):
                        # 패턴에 걸리는 요청만 멈춘다. guard가 죽어도 일반
                        # 탐색은 영향을 받지 않는다는 뜻이다.
                        await self.send(
                            ws,
                            "Fetch.enable",
                            {"patterns": self.patterns},
                            msg["params"]["sessionId"],
                        )
                elif method == "Fetch.requestPaused":
                    asyncio.create_task(self.on_paused(ws, msg))


async def main():
    logging.basicConfig(level=logging.INFO, format="guard: %(message)s")
    ask, block, views = load_rules()
    guard = Guard(ask, block, views)
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
