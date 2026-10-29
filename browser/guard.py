"""결제로 나가는 요청을 사람에게 묻거나, 묻지 않고 막는다.

이 집의 결제 수단은 네이버페이·신용카드·쿠팡 쿠페이이고, 어느 것이든 결제는 승인 카드를
거친다(2026-10-30). 그 결제로 가는 요청 - 네이버페이·쿠팡 결제 호스트, PG 결제창과 카드사 창,
큰 쇼핑몰의 결제 경로 - 은 카드를 띄워 사람이 결정하고, 허용하지 않은 간편결제(카카오페이·
페이코 등)와 휴대폰 결제는 묻지 않고 막는다. 쇼핑몰이 자기 도메인 안에서 끝내는 결제는 PG 를
거치지 않아 호스트 목록에 걸리지 않을 수 있어서(실제로 쿠팡 간편결제가 카드 한 장 없이 끝난
적이 있다) 아는 쇼핑몰의 결제 경로는 따로 적어 묻는다.

막는 쪽이 묻는 쪽보다 조용한 것은 일부러다. 결제 수단이 정해져 있는데 다른
길로 가려는 것은 사람이 판단할 문제가 아니라 애초에 일어나면 안 되는 일이고,
카드로 올리면 "승인"이 있는 이상 언젠가 눌리게 된다.

PG 결제창(이니시스·KCP 같은, 쇼핑몰의 '신용카드'가 여는 창)과 카드사 창은 창을 여는
요청에서 카드를 올린다. 결제창 안에서 어느 길로 돈이 나가는지(카드사 앱, 간편결제)는
주소로 가릴 수 없어서, 승인 하나가 그 금액의 결제창 전부를 연다(에이전트 /intercept).

도구 층에서는 이 판단을 할 수 없다. browser_click은 조회 버튼이든 결제
버튼이든 같은 호출이다. 그래서 여기서는 도구를 보지 않고 **브라우저가 실제로
보내려는 요청**을 본다. CDP의 Fetch가 요청을 내보내기 직전에 멈춰 세우므로,
반려는 취소가 아니라 애초에 일어나지 않음이 된다.

browser-use는 건드리지 않는다. 같은 Chromium에 CDP로 따로 붙은 감시자라,
어떤 도구가 눌렀든 상관없이 걸린다.

백그라운드 작업이 도는 동안에는 하나를 더 본다: 탭의 페이지 이동이 그 작업의
사이트(jobsites.py) 밖으로 가면 막는다. 작업은 아무도 보지 않는 채로 로그인된
브라우저를 쓰므로, 읽은 페이지가 어디로 가라고 하든 제 사이트 밖으로는 못 간다.

막은 페이지 이동(결제창, 작업 사이트 밖)은 Chrome 의 오류 화면 대신 자비스의 안내 화면으로
채운다(info_page): 승인을 기다리는지, 허용하지 않은 결제 수단인지, 작업 사이트 밖인지. 오류
화면은 사람에게 '404'로 보였고(2026-10-30) 작업은 '도메인이 막혔다'로 읽었다(10/29).

결제 흐름은 로그에 길을 남긴다: 결제 호스트·결제창으로 가는 페이지 이동(값은 빼고 주소의
모양만)과 그 창을 연 창. 로그는 /logs(볼륨)에도 남아 컨테이너를 다시 만들어도 사라지지 않는다 -
10/30 01:32 배포로 01:11·01:19 실패의 로그가 지워져 원인을 다시 재현해야 했다.
"""

import asyncio
import base64
import glob
import json
import logging
import logging.handlers
import os
import re
import urllib.request
from fnmatch import fnmatch
from urllib.parse import parse_qsl, urlparse

import aiohttp
import websockets

import jobsites

log = logging.getLogger("guard")

AGENT_URL = os.environ.get("JARVIS_AGENT_URL", "http://agent:8080/intercept")
TOKEN = os.environ.get("MCP_BROWSER_TOKEN", "")
HOSTS_FILE = os.environ.get("PAYMENT_HOSTS_FILE", "/app/payment-hosts.json")
# 에이전트는 카드를 올리고 그 결정을 2분까지 기다렸다 답한다(그 사이 승인되면 멈춘 요청이 그대로
# 나간다). 이것은 그 답의 제한 시간이라 2분보다 길어야 한다.
DECISION_TIMEOUT = int(os.environ.get("JARVIS_DECISION_TIMEOUT", "150"))
LOG_DIR = os.environ.get("JARVIS_GUARD_LOG_DIR", "/logs")

# 결제 페이지가 스스로 불러오는 이미지·폰트까지 물으면 카드가 쏟아진다.
# 판단이 필요한 것은 문서 이동과 데이터 요청뿐이다.
SKIP_TYPES = {"Image", "Stylesheet", "Font", "Media", "Script", "Manifest"}

# 묻는 것도 막는 것도 값을 보내는 요청에만 건다. 상품 페이지·주문내역을 읽는
# GET 까지 멈추면 구경조차 못 하게 되고(네이버쇼핑), 그것은 이 가드가 지키려는
# 것 - 돈이 나가는 요청 - 과 상관이 없다.
WRITE_METHODS = {"POST", "PUT", "PATCH", "DELETE"}


def load_rules() -> tuple[list[str], list[str], list[str], list[dict], list[str]]:
    """묻는 호스트, 묻는 URL 패턴, 막는 URL 패턴, 묻는 것 중 통과시킬 규칙, 결제창(PG) 호스트.
    예전 형식(hosts)도 읽어서, 규칙 파일을 갈아끼우는 중에 가드가 빈손으로 뜨는 일이 없게 한다."""
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
    windows = [h.strip().lower() for h in data.get("window", []) if h.strip()]
    return ask, ask_patterns, block, passes, windows


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


def url_shape(url: str) -> str:
    """로그에 남길 주소의 모양 - 값은 빼고: 호스트+경로(;세션 표시) + 파라미터 이름. resultCode 만 값까지."""
    u = urlparse(url)
    path = u.path
    if ";" in path:
        path = path.split(";", 1)[0] + ";…"
    elif u.params:  # urlparse 는 마지막 조각의 ;PAY_JSESSIONID=… 를 path 밖(params)에 둔다
        path += ";…"
    names = []
    for k, v in parse_qsl(u.query, keep_blank_values=True):
        names.append(f"{k}={v[:12]}" if k.lower() == "resultcode" else k)
    return u.netloc + path + ("?" + "&".join(names[:10]) if names else "")


# 막은 페이지 이동 자리에 채우는 안내 화면. 사람(원격 화면)과 작업(화면 읽기)이 같이 읽는다.
INFO_PAGES = {
    "pending": (
        "결제 승인 대기",
        "이 결제는 운영자의 승인을 기다리고 있어 아직 보내지 않았습니다.",
        "폰이나 웹의 자비스에 올라간 결제 승인 카드를 승인하면 이어집니다. "
        "작업이라면 결제하기를 다시 누르지 말고 wait_payment 를 부르세요.",
    ),
    "refused": (
        "결제를 보내지 않았습니다",
        "승인되지 않은 결제 요청이라 보내지 않았습니다.",
        "결제가 필요하면 결제하기를 한 번 눌러 결제 승인 카드를 올리세요.",
    ),
    "blocked": (
        "허용하지 않은 결제 수단",
        "자비스는 이 결제 수단(다른 간편결제, 휴대폰 결제, 카드사 앱 결제 같은 것)을 쓰지 않습니다.",
        "네이버페이·신용카드(카드사 창에서는 그 안의 네이버페이)·쿠페이로 결제하세요. 우회하지 마세요.",
    ),
    "site": (
        "작업의 사이트 밖",
        "이 작업이 쓰는 사이트가 아니라서 열지 않았습니다: {host}",
        "꼭 필요하면 request_sites 로 사이트를 더하세요.",
    ),
}


def info_page(kind: str, host: str = "") -> str:
    """막은 페이지 이동 자리에 보일 HTML. 바깥 자원 없이 이 한 장으로 그린다."""
    title, what, todo = INFO_PAGES[kind]
    what = what.format(host=_esc(host))
    return (
        "<!doctype html><html lang=\"ko\"><head><meta charset=\"utf-8\">"
        f"<title>자비스 · {title}</title>"
        "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"
        "<style>body{margin:0;font:15px/1.6 -apple-system,'Apple SD Gothic Neo','NanumGothic',sans-serif;"
        "background:#f6f6f4;color:#222;display:flex;min-height:100vh;align-items:center;justify-content:center}"
        "main{max-width:440px;margin:24px;padding:24px 22px;background:#fff;border-radius:14px;"
        "box-shadow:0 1px 3px rgba(0,0,0,.08)}h1{font-size:18px;margin:0 0 10px}p{margin:8px 0}"
        ".todo{color:#555}</style></head><body><main>"
        f"<h1>자비스 · {title}</h1><p>{what}</p><p class=\"todo\">{todo}</p>"
        "</main></body></html>"
    )


def _esc(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


def _json_object(body: str | None) -> dict | None:
    if not body:
        return None
    try:
        value = json.loads(body)
    except ValueError:
        return None
    return value if isinstance(value, dict) else None


# 결제창을 여는 요청이 PG 에 넘기는 금액과 상품 이름의 칸. PG 마다 이름이 다르다:
# 이니시스 price·goodname, KCP good_mny·good_name, 나이스 Amt·GoodsName, 토스페이먼츠·포트원 amount·orderName·name.
ORDER_AMOUNT = ("price", "good_mny", "amt", "amount", "totalamount")
ORDER_ITEM = ("goodname", "good_name", "goodsname", "ordername", "productname", "name")


def window_order(body: str | None) -> dict:
    """결제창으로 가는 쓰기의 본문에서 금액(숫자만)과 상품 이름 - 읽히는 것만. 카드에 '결제창이
    받은 금액'으로 보이기만 하고, 통과시킬지는 이것으로 정하지 않는다."""
    fields = _fields(body)
    out = {}
    for key in ORDER_AMOUNT:
        value = re.sub(r"[,\s원]", "", str(fields.get(key, "")))
        if value.isdigit() and int(value) > 0:
            out["amount"] = str(int(value))  # 000113000 도 113000 - 같은 금액은 같은 글자로
            break
    for key in ORDER_ITEM:
        value = " ".join(str(fields.get(key, "")).split())
        if value:
            out["item"] = value[:60]
            break
    return out


def _fields(body: str | None) -> dict:
    """본문의 맨 위 칸들(이름은 소문자) - JSON 객체거나 폼(a=1&b=2). 둘 다 아니면 비어 있다.
    폼이 charset=EUC-KR 이라고 하면(이니시스) 그 글자로 푼다."""
    obj = _json_object(body)
    if obj is not None:
        return {str(k).lower(): v for k, v in obj.items() if isinstance(v, (str, int, float)) and not isinstance(v, bool)}
    if not body or "=" not in body:
        return {}
    encoding = "cp949" if re.search(r"(?:^|&)charset=euc-kr(?:&|$)", body, re.I) else "utf-8"
    return {k.lower(): v for k, v in parse_qsl(body, encoding=encoding, errors="replace")}


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


class Answer:
    """에이전트의 답: 보낼지(참이면 보낸다)와, 막았다면 기다리는 결제 카드."""

    def __init__(self, approved: bool, pending: str = ""):
        self.approved, self.pending = approved, pending

    def __bool__(self) -> bool:
        return self.approved


class Guard:
    def __init__(
        self,
        ask_hosts: list[str],
        ask_patterns: list[str],
        block_patterns: list[str],
        pass_rules: list[dict] | None = None,
        window_hosts: list[str] | None = None,
    ):
        self.hosts = ask_hosts
        self.ask_patterns = ask_patterns
        self.blocks = block_patterns
        self.pass_rules = pass_rules or []
        self.windows = window_hosts or []
        # 멈춰 세울 대상은 네 목록의 합집합이다. 패턴에 없는 요청은 Chrome이
        # 아예 멈추지 않으므로, 여기 빠진 것은 판단 대상에도 오르지 못한다.
        self.patterns = (
            [{"urlPattern": f"*{h}*", "requestStage": "Request"} for h in ask_hosts]
            + [{"urlPattern": p, "requestStage": "Request"} for p in ask_patterns]
            + [{"urlPattern": f"*{h}*", "requestStage": "Request"} for h in self.windows]
            + [{"urlPattern": p, "requestStage": "Request"} for p in block_patterns]
            # 페이지 이동은 전부 멈춰 본다: 작업이 도는 동안 그 사이트 밖으로
            # 가는지(leaves_job). 작업이 없으면 바로 보낸다.
            + [{"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}]
        )
        # 붙은 탭(page)의 세션 → 그 탭의 id(= 최상위 프레임 id).
        self.pages: dict[str, str] = {}
        # 탭 id → 그 탭을 연 탭·프레임(openerId, openerFrameId), 프레임 id → 마지막으로 간 주소.
        # 결제창(카드사 창)이 연 팝업의 네이버페이만 그 결제창의 승인으로 지나가게 하려고 본다.
        self.openers: dict[str, tuple[str, str]] = {}
        self.frames: dict[str, str] = {}
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
    FRAMES_KEPT = 2048

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
        # 결제가 지나가는 곳(결제창, 네이버페이·쿠팡 결제 호스트)은 작업의 사이트가 아니어도 연다 -
        # 결제창을 새 창으로 띄우는 PG 가 있고, 거기서 돈이 나가는 쓰기는 이 가드가 따로 묻는다.
        if self.is_window(url) or self.matches(urlparse(url).netloc):
            return False
        return not jobsites.allowed(url, sites)

    def matches(self, host: str) -> bool:
        host = host.lower()
        return any(host == h or host.endswith("." + h) for h in self.hosts)

    def is_window(self, url: str) -> bool:
        """PG 결제창(이니시스·KCP…)으로 가는 요청인가 - payment-hosts.json 의 window."""
        host = (urlparse(url).hostname or "").lower()
        return any(host == h or host.endswith("." + h) for h in self.windows)

    def opened_by_window(self, session: str | None) -> bool:
        """이 세션의 탭을 결제창(카드사 창, PG 창)의 페이지나 프레임이 열었나 - 연 쪽을 거슬러 세 단계까지.
        카드사 창 안에서 고른 네이버페이는 그 창이 띄운 팝업이다. 모르면 아니다(카드가 따로 뜬다)."""
        page = self.pages.get(session or "")
        for _ in range(3):
            if not page or page not in self.openers:
                return False
            opener, frame = self.openers[page]
            if self.is_window(self.frames.get(frame or opener, "")):
                return True
            page = opener
        return False

    def blocked(self, url: str, method: str) -> bool:
        if method.upper() not in WRITE_METHODS:
            return False
        url = url.lower()
        return any(fnmatch(url, p) for p in self.blocks)

    def asks(self, url: str, method: str) -> bool:
        """사람에게 물을 결제 요청인가: 값을 보내는 요청이고, 묻는 호스트나 결제창으로 가거나
        묻는 패턴(허용한 자사 간편결제)에 맞는다. 보기만 하는 GET 은 묻지 않는다."""
        if method.upper() not in WRITE_METHODS:
            return False
        if self.matches(urlparse(url).netloc) or self.is_window(url):
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

    async def ask(
        self,
        url: str,
        method: str,
        host: str,
        event: str = "",
        window: bool = False,
        order: dict | None = None,
        from_window: bool = False,
    ) -> Answer:
        headers = {"Authorization": f"Bearer {TOKEN}"}
        timeout = aiohttp.ClientTimeout(total=DECISION_TIMEOUT)
        payload = {"url": url, "method": method, "host": host}
        if event:
            payload["event"] = event
        if window:
            # 에이전트는 결제창의 승인 하나로 그 결제 한 건의 결제창 전부와 그 안의 네이버페이를 연다.
            payload["window"] = True
            payload.update({k: v for k, v in (order or {}).items() if k in ("amount", "item")})
        if from_window:
            # 결제창이 연 팝업의 요청: 그 결제창의 승인이 덮는 네이버페이.
            payload["fromWindow"] = True
        try:
            async with aiohttp.ClientSession(timeout=timeout) as s:
                async with s.post(
                    AGENT_URL,
                    json=payload,
                    headers=headers,
                ) as r:
                    body = await r.json()
                    return Answer(body.get("decision") == "approved", str(body.get("pending") or ""))
        except Exception as e:
            # 물어볼 수 없으면 보내지 않는다. 판단이 불가능할 때 통과시키는
            # 것은 이 경로가 막으려던 바로 그 일이다.
            log.error("승인을 받지 못했습니다: %s", e)
            return Answer(False)

    async def refuse(self, ws, rid: str, session: str | None, resource_type: str | None, kind: str, host: str = ""):
        """보내지 않는다. 페이지 이동이면 그 자리를 자비스의 안내 화면으로 채운다(오류 화면 대신)."""
        if resource_type != "Document":
            await self.send(ws, "Fetch.failRequest", {"requestId": rid, "errorReason": "BlockedByClient"}, session)
            return
        page = info_page(kind, host).encode("utf-8")
        await self.send(
            ws,
            "Fetch.fulfillRequest",
            {
                "requestId": rid,
                "responseCode": 200,
                "responseHeaders": [
                    {"name": "Content-Type", "value": "text/html; charset=utf-8"},
                    {"name": "Cache-Control", "value": "no-store"},
                ],
                "body": base64.b64encode(page).decode("ascii"),
            },
            session,
        )

    async def on_paused(self, ws, msg):
        p = msg["params"]
        session = msg.get("sessionId")
        rid = p["requestId"]
        req = p.get("request", {})
        url = req.get("url", "")
        method = req.get("method", "GET")
        host = urlparse(url).netloc

        rtype = p.get("resourceType")

        async def go():
            await self.send(ws, "Fetch.continueRequest", {"requestId": rid}, session)

        if rtype == "Document":
            if fid := p.get("frameId"):
                self.frames.pop(fid, None)
                self.frames[fid] = url
                while len(self.frames) > self.FRAMES_KEPT:
                    self.frames.pop(next(iter(self.frames)))
            if self.is_window(url) or self.matches(host):
                # 결제 흐름의 길: 값은 빼고 모양만, 어느 탭에서인지와 함께.
                log.info("결제 흐름 이동: %s %s (탭 %s)", method, url_shape(url)[:160], (self.pages.get(session or "") or "?")[:6])

        if rtype == "Document" and self.leaves_job(session, p.get("frameId"), url):
            log.warning("작업의 사이트 밖으로 가는 이동을 막았습니다: %s", url[:120])
            await self.refuse(ws, rid, session, rtype, "site", host)
            return

        if p.get("resourceType") == "Script" and method.upper() == "GET":
            self.saw_script(url)
        if p.get("resourceType") in SKIP_TYPES:
            await go()
            return

        # 막는 쪽을 먼저 본다. 정해진 결제 수단이 아닌 길은 물어볼 것이 없다.
        if self.blocked(url, method):
            log.warning("허용하지 않은 결제를 막았습니다: %s %s", method, url[:120])
            await self.refuse(ws, rid, session, rtype, "blocked", host)
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
        window = self.is_window(url)
        order = window_order(body) if window else {}
        from_window = not window and self.opened_by_window(session)
        log.info(
            "결제 요청을 멈췄습니다: %s %s%s%s%s [%s]",
            method,
            url[:120],
            f" ({event})" if event else "",
            f" (결제창, 금액 {'읽음' if 'amount' in order else '못 읽음'})" if window else "",
            " (결제창이 연 창)" if from_window else "",
            shape_of(body),
        )
        # 테스트의 가짜 ask 는 from_window 를 모른다: 그 창의 요청일 때만 넘긴다.
        extra = {"from_window": True} if from_window else {}
        answer = await self.ask(url, method, host, event, window=window, order=order, **extra)
        if answer:
            await go()
        else:
            await self.refuse(ws, rid, session, rtype, "pending" if getattr(answer, "pending", "") else "refused", host)

    async def watch(self, port: int):
        info = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json/version", timeout=5))
        ws_url = info["webSocketDebuggerUrl"]

        async with websockets.connect(ws_url, max_size=None) as ws:
            log.info(
                "CDP에 붙었습니다 (port %s), 묻는 호스트 %d개 · 묻는 패턴 %d개 · 결제창 %d개 · 막는 패턴 %d개 · 통과 규칙 %d개",
                port,
                len(self.hosts),
                len(self.ask_patterns),
                len(self.windows),
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
                        tid = t.get("targetId", "")
                        self.pages[session] = tid
                        self.frames.setdefault(tid, t.get("url", ""))
                        if t.get("openerId"):
                            self.openers[tid] = (t["openerId"], t.get("openerFrameId", ""))
                            opener_url = self.frames.get(t.get("openerFrameId") or t["openerId"], "")
                            if self.is_window(opener_url) or self.matches(urlparse(opener_url).netloc):
                                log.info("결제 흐름의 새 창: 연 창 %s (탭 %s)", url_shape(opener_url)[:120], tid[:6])
                    if t.get("type") in ("page", "iframe"):
                        # 패턴에 걸리는 요청만 멈춘다. guard가 죽어도 일반
                        # 탐색은 영향을 받지 않는다는 뜻이다.
                        await self.send(ws, "Fetch.enable", {"patterns": self.patterns}, session)
                    if msg["params"].get("waitingForDebugger"):
                        # 감시를 켠 뒤에야 풀어 준다. 워커처럼 감시하지 않는
                        # 대상도 멈춰 있으므로 반드시 풀어야 한다.
                        await self.send(ws, "Runtime.runIfWaitingForDebugger", {}, session)
                elif method == "Target.detachedFromTarget":
                    tid = self.pages.pop(msg["params"].get("sessionId", ""), None)
                    if tid:
                        self.openers.pop(tid, None)
                        self.frames.pop(tid, None)
                elif method == "Fetch.requestPaused":
                    asyncio.create_task(self.on_paused(ws, msg))


def setup_logging():
    """표준 출력(도커 로그)과, 있으면 /logs 볼륨의 파일 둘 다. 파일은 5MB 다섯 개까지 돌려 쓴다 -
    컨테이너를 다시 만들어도 남아서, 지난 결제가 어떻게 흘렀는지 나중에 볼 수 있다."""
    logging.basicConfig(level=logging.INFO, format="guard: %(message)s")
    try:
        os.makedirs(LOG_DIR, exist_ok=True)
        handler = logging.handlers.RotatingFileHandler(
            os.path.join(LOG_DIR, "guard.log"), maxBytes=5 << 20, backupCount=5, encoding="utf-8"
        )
    except OSError as e:
        log.warning("가드 로그 파일을 열지 못했습니다(%s) - 도커 로그에만 남깁니다", e)
        return
    handler.setFormatter(logging.Formatter("%(asctime)s guard: %(message)s"))
    logging.getLogger().addHandler(handler)


async def main():
    setup_logging()
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
