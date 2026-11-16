"""login 의 탭 고르기, 아이디 칸 알아보기, 두 단계 로그인 화면 채우기와 브리지가 browser-use 에 묻는 탭 주소.

    python -m unittest test_login   (websockets 가 있는 곳 - 브라우저 이미지. 브라우저 시험은 Chromium/Chrome 과
                                     openssl 이 있어야, 브리지 시험은 mcp·starlette 가 있어야 돈다)

아이디·비밀번호·이름은 모두 가짜다. 실제 사이트에는 아무것도 넣지 않는다 - 브라우저 시험은 이 컴퓨터에 띄운
가짜 화면(https, 시험만을 위한 인증서)과 가짜 보관함으로 한다.
"""

import asyncio
import http.server
import json
import os
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import unittest
import urllib.request
from urllib.parse import parse_qs

os.environ.setdefault("PAYMENT_HOSTS_FILE", os.path.join(os.path.dirname(__file__), "payment-hosts.json"))

import jobsites  # noqa: E402
import login  # noqa: E402
import upload  # noqa: E402

USERNAME, PASSWORD, HINT = "hong@example.com", "fake-pass-1234", "ho***@example.com"


class Tabs:
    """가짜 /json/list. 읽을 때마다 lists 의 다음 목록을 주고, 마지막 것은 계속 준다."""

    def __init__(self):
        self.lists: list[list[dict]] = [[]]
        self.reads = 0
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                tabs = owner.lists[min(owner.reads, len(owner.lists) - 1)]
                owner.reads += 1
                body = json.dumps(tabs).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_):
                pass

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.port = self.server.server_address[1]


def tab(name: str, url: str) -> dict:
    return {"id": name, "type": "page", "url": url, "title": name, "webSocketDebuggerUrl": f"ws://fake/{name}"}


class PickTab(unittest.IsolatedAsyncioTestCase):
    """에이전트가 보고 있는 탭(browser-use 의 주소)을 고른다 - 보이는 탭이 여럿이어도."""

    @classmethod
    def setUpClass(cls):
        cls.tabs = Tabs()

    @classmethod
    def tearDownClass(cls):
        cls.tabs.server.shutdown()

    def setUp(self):
        self.shown: set[str] = set()
        self._visible, self._pause = upload._visible, upload.PICK_PAUSE

        async def visible(ws_url):
            return ws_url.rsplit("/", 1)[-1] in self.shown

        upload._visible, upload.PICK_PAUSE = visible, 0.01
        self.tabs.reads = 0

    def tearDown(self):
        upload._visible, upload.PICK_PAUSE = self._visible, self._pause

    async def pick(self, url, *lists):
        self.tabs.lists = list(lists)
        self.tabs.reads = 0
        got = await upload.page_for(self.tabs.port, url)
        return got and got["id"]

    async def test_the_popup_the_agent_is_on(self):
        # 창 관리자 없는 브라우저: 로그인 팝업과 그 팝업을 연 탭이 둘 다 보인다. 보이는 첫 탭이 아니라 그 주소의 탭.
        claude, google = tab("claude", "https://claude.ai/login"), tab("google", "https://accounts.google.com/v3/signin/identifier?dsh=1")
        self.shown = {"claude", "google"}
        self.assertEqual(await self.pick("https://accounts.google.com/v3/signin/identifier?dsh=1", [claude, google]), "google")
        self.assertEqual(await self.pick("https://claude.ai/login", [google, claude]), "claude")

    async def test_the_fragment_does_not_matter(self):
        self.shown = {"a"}
        self.assertEqual(await self.pick("https://accounts.example.com/signin#step2",
                                         [tab("a", "https://www.example.com/"), tab("b", "https://accounts.example.com/signin")]), "b")

    async def test_the_shown_one_of_the_same_page(self):
        same = "https://accounts.example.com/signin"
        self.shown = {"b"}
        self.assertEqual(await self.pick(same, [tab("a", same), tab("b", same)]), "b")
        self.shown = set()
        self.assertEqual(await self.pick(same, [tab("a", same), tab("b", same)]), "a")

    async def test_a_page_that_just_moved_on(self):
        # 넘어가는 중이라 목록이 아직 옛 주소면 다시 본다.
        old, new = tab("p", "https://accounts.example.com/signin"), tab("p", "https://accounts.example.com/challenge/pwd")
        self.assertEqual(await self.pick("https://accounts.example.com/challenge/pwd", [old], [old], [new]), "p")

    async def test_only_the_query_changed(self):
        # 같은 화면이 주소 끝의 값만 바꿨으면 그 탭. 다른 화면(경로)은 고르지 않는다.
        self.shown = {"o", "p"}
        lists = [tab("o", "https://www.example.com/"), tab("p", "https://accounts.example.com/signin?hl=ko&dsh=2")]
        self.assertEqual(await self.pick("https://accounts.example.com/signin?hl=ko", lists), "p")
        with self.assertRaisesRegex(RuntimeError, "찾지 못해"):
            await self.pick("https://accounts.example.com/other", lists)

    async def test_no_such_tab_means_no_tab(self):
        # 에이전트가 보는 탭이 목록에 없으면 다른 탭(보이는 탭)을 고르지 않는다.
        self.shown = {"o"}
        with self.assertRaisesRegex(RuntimeError, r"accounts\.example\.com.*찾지 못해"):
            await self.pick("https://accounts.example.com/signin", [tab("o", "https://claude.ai/login")])
        self.assertEqual(self.tabs.reads, upload.PICK_TRIES)

    async def test_without_the_agents_tab_the_first_shown(self):
        self.shown = {"b"}
        lists = [tab("a", "https://a.example.com/"), tab("b", "https://b.example.com/")]
        self.assertEqual(await self.pick(None, lists), "b")
        self.shown = set()
        self.assertIsNone(await self.pick(None, lists))


class SignInStep(unittest.TestCase):
    """아이디만 묻는 화면에서 다음으로 넘겨도 되는가 - 주소·제목·제목줄·폼 글자로."""

    def test_sign_in_pages(self):
        for urls, titles, heads, text in [
            (["https://accounts.google.com/v3/signin/identifier?hl=ko&flowName=WebLiteSignIn"], ["로그인 - Google 계정"], "로그인",
             "이메일 또는 휴대전화 이메일을 잊으셨나요? 다음 계정 만들기"),
            (["https://login.microsoftonline.com/common/oauth2/v2.0/authorize"], ["Sign in to your account"], "Sign in", ""),
            (["https://www.example.com/ap/signin"], ["Amazon Sign-In"], "Sign in or create account", ""),
            (["https://www.example.com/"], ["Example"], "", "아이디 로그인 다음"),
        ]:
            self.assertTrue(login.sign_in_step(urls, titles, heads, text), (urls, titles, heads))

    def test_not_sign_in_pages(self):
        for urls, titles, heads, text in [
            (["https://accounts.google.com/signin/v2/usernamerecovery"], ["Google 계정"], "이메일 찾기", ""),
            (["https://nid.naver.com/user2/help/idInquiry"], ["네이버 : 아이디 찾기"], "", "로그인"),
            (["https://www.example.com/login/forgot-password"], ["비밀번호 재설정"], "", "로그인"),
            (["https://accounts.example.com/lifecycle/steps/signup/name"], ["Example 계정"], "", "로그인"),
            (["https://www.example.com/account"], ["Example"], "계정 만들기", "이메일"),
            (["https://www.example.com/blog"], ["뉴스레터 구독"], "", "이메일 로그인"),
            (["https://www.example.com/blog/post"], ["Example"], "", "이메일"),
        ]:
            self.assertFalse(login.sign_in_step(urls, titles, heads, text), (urls, titles, heads))

    def test_the_query_is_not_a_reason_to_refuse(self):
        # ? 뒤(돌아갈 주소 등)에 가입·찾기 낱말이 있어도 로그인 화면이다.
        self.assertTrue(login.sign_in_step(["https://accounts.example.com/signin?continue=https://www.example.com/signup&reset=1"],
                                           ["Sign in"], "", ""))


# 실제 브라우저(Chromium·Chrome, 시험만을 위한 임시 프로필)로 가짜 로그인 화면을 띄워 넣는 것까지 본다.
# 모든 호스트를 이 컴퓨터로 돌려(host-resolver-rules) 주소만 accounts.example.com 처럼 만들고, https 는 시험 때
# 만든 자체 서명 인증서로 연다(--ignore-certificate-errors).
CHROMES = [os.environ.get("CHROMIUM", ""), "/usr/bin/chromium", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"]
CHROME = next((c for c in CHROMES if c and os.path.exists(c)), None)
OPENSSL = shutil.which("openssl")

# 구글처럼 같은 문서 안에서 아이디 단계를 비밀번호 단계로 바꾸는 화면. 비밀번호 단계에는 화면 밖에 숨긴 아이디
# 칸과 '비밀번호 표시' 칸이 있다(둘 다 건드리면 안 된다). ?slow 면 계정을 묻는 요청(2.6초)이 끝난 뒤에 바꾼다.
SWAP_PAGE = """<!doctype html><meta charset="utf-8"><title>로그인 - Example 계정</title>
<style>body{font:16px sans-serif} .gone{display:none}</style>
<h1>로그인</h1>
<form id="f" method="post" novalidate>
  <section id="step1">
    <label>이메일 또는 휴대전화 <input type="email" name="identifier" id="identifierId" autocomplete="username webauthn"></label>
    <input type="password" name="hiddenPassword" aria-hidden="true" style="display:none">
    <a href="/forgot">이메일을 잊으셨나요?</a>
  </section>
  <section id="step2" class="gone">
    <input type="email" name="identifier2" autocomplete="username" tabindex="-1" aria-hidden="true"
           style="position:absolute;left:-10000px;top:0;width:300px;height:30px">
    <label>비밀번호 입력 <input type="password" name="Passwd" autocomplete="current-password"></label>
    <label><input type="checkbox" id="show"> 비밀번호 표시</label>
  </section>
  <button id="next">다음</button>
  <a href="/signup">계정 만들기</a>
</form>
<script>
window.got = {submits: 0, final: 0, shown: 0};
document.getElementById('f').addEventListener('submit', e => {
  e.preventDefault();
  got.submits++;
  if (!document.getElementById('step2').classList.contains('gone')) { got.final++; return; }
  got.identifier = document.getElementById('identifierId').value;
  const swap = () => {
    if (!document.getElementById('step1')) return;
    document.getElementById('step1').remove();
    document.getElementById('step2').classList.remove('gone');
    history.pushState(null, '', '/v3/signin/challenge/pwd');
  };
  if (new URLSearchParams(location.search).has('slow')) fetch('/lookup').then(swap);
  else setTimeout(swap, 700);
});
document.getElementById('show').addEventListener('change', () => got.shown++);
</script>"""

# 아이디를 보내면 새 주소의 비밀번호 화면으로 넘어가는 화면(서버가 0.4초 걸린다).
NAV_ID_PAGE = """<!doctype html><meta charset="utf-8"><title>로그인</title><h1>로그인</h1>
<form method="post" action="/login/pw"><input type="text" name="userid" autocomplete="username"><button>다음</button></form>"""
NAV_PW_PAGE = """<!doctype html><meta charset="utf-8"><title>로그인</title><h1>로그인</h1>
<form method="post" action="/login/done"><input type="password" name="pw" autocomplete="current-password">
<label><input type="checkbox" name="keep"> 로그인 상태 유지</label><button>로그인</button></form>"""

# 폼이 없고 Enter 로는 넘어가지 않는 화면 - 'Next' 버튼을 눌러야 비밀번호 칸이 나온다. 구글의 데스크톱 화면처럼
# 아이디 칸은 type=text 에 autocomplete "username webauthn" 이고, 버튼은 칸의 구역(section) 밖에 있다.
NOFORM_PAGE = """<!doctype html><meta charset="utf-8"><title>Sign in</title>
<main id="box"><section><h2>Sign in</h2><input type="text" id="user" name="identifier" autocomplete="username webauthn">
<div role="button" tabindex="0">Forgot email?</div></section>
<div id="identifierNext"><div role="button" id="go" tabindex="0" style="display:inline-block;padding:8px 16px;background:#36c;color:#fff">Next</div></div>
<div role="button" tabindex="0">Create account</div></main>
<script>
window.got = {clicks: 0};
document.getElementById('go').addEventListener('click', () => {
  got.clicks++;
  got.identifier = document.getElementById('user').value;
  document.getElementById('box').innerHTML = '<h2>Welcome</h2><input type="password" id="pw" autocomplete="current-password">';
});
</script>"""

# 다음으로 넘기면 비밀번호 대신 보안문자를 묻는 화면.
CAPTCHA_PAGE = """<!doctype html><meta charset="utf-8"><title>로그인 - Example 계정</title><h1>로그인</h1>
<form id="f" novalidate><div id="s"><input type="email" name="identifier" autocomplete="username"></div><button>다음</button></form>
<script>
document.getElementById('f').addEventListener('submit', e => {
  e.preventDefault();
  document.getElementById('s').innerHTML = '<p>보안문자를 입력하세요</p><input type="text" name="captcha">';
});
</script>"""

# 아이디를 보내면 다른 사이트(idp.example.net)의 비밀번호 화면으로 넘어가는 화면.
SSO_PAGE = """<!doctype html><meta charset="utf-8"><title>Sign in</title><h1>Sign in</h1>
<form method="post" action="/sso/next"><input type="email" name="email" autocomplete="username"><button>Next</button></form>"""
IDP_PAGE = """<!doctype html><meta charset="utf-8"><title>Sign in - IdP</title>
<form><input type="password" name="pw" autocomplete="current-password"><button>Sign in</button></form>"""

# 아이디를 치면 같은 폼에 비밀번호 칸을 펼치는 화면 - 다음으로 넘기지 않고 바로 넣는다.
REVEAL_PAGE = """<!doctype html><meta charset="utf-8"><title>로그인</title><h1>로그인</h1>
<form id="f"><input type="email" name="email" autocomplete="username"><input type="password" name="pw" style="display:none">
<button>로그인</button></form>
<script>
window.got = {submits: 0};
const f = document.getElementById('f');
f.email.addEventListener('input', () => { f.pw.style.display = f.email.value ? '' : 'none'; });
f.addEventListener('submit', e => { e.preventDefault(); got.submits++; });
</script>"""

# 로그인 단계가 아닌 화면 둘: 이메일 하나뿐인 구독 화면, 아이디 말고 칸이 더 있는 화면.
NEWSLETTER_PAGE = """<!doctype html><meta charset="utf-8"><title>뉴스레터 구독</title><h1>뉴스레터 구독</h1>
<form id="f"><input type="email" name="email"><button>구독</button></form>
<script>window.got = {submits: 0}; document.getElementById('f').addEventListener('submit', e => { e.preventDefault(); got.submits++; });</script>"""
TWO_FIELD_PAGE = """<!doctype html><meta charset="utf-8"><title>로그인</title><h1>로그인</h1>
<form id="f"><input type="email" name="email" autocomplete="username"><input type="text" name="nickname"><button>다음</button></form>
<script>window.got = {submits: 0}; document.getElementById('f').addEventListener('submit', e => { e.preventDefault(); got.submits++; });</script>"""

# 로그인 팝업을 여는 화면(그 사이트의 저장된 로그인은 없다): 이메일 칸과 '구글로 계속' 버튼.
OPENER_PAGE = """<!doctype html><meta charset="utf-8"><title>Opener</title><h1>Log in</h1>
<form><input type="email" name="email" autocomplete="email"><button type="button">Continue with email</button></form>
<button id="g" onclick="window.open('{popup}', 'g', 'popup,width=480,height=640')">Continue with Example</button>"""

# 칸 알아보기: 각 화면에서 FIELDS_JS 가 고른 칸의 name.
FIELD_PAGES = {
    # autocomplete 가 "username webauthn" 인 칸 하나 - 이름·형식으로는 아이디로 보이지 않는다.
    "webauthn": '<input type="text" name="q1" autocomplete="username webauthn">',
    # 구글 데스크톱 로그인의 이메일 칸(폼 없음).
    "glif": '<main><section><input type="text" name="identifier" id="identifierId" autocomplete="username webauthn"></section></main>',
    # 비밀번호 앞의 마지막 글자 칸(b)이 아니라 username 낱말이 든 칸(a).
    "token": '<form><input name="a" autocomplete="username webauthn"><input name="b" type="text"><input type="password" name="p"></form>',
    # 비밀번호 단계에 화면 밖으로 밀어 둔 아이디 칸, 막아 둔(inert) 아이디 칸 - 둘 다 고르지 않는다.
    "hidden": '<form><input name="off" autocomplete="username" style="position:absolute;left:-10000px;width:300px">'
              '<div inert><input name="inert" autocomplete="username"></div><input type="password" name="p"></form>',
    # name 이 id 인 칸.
    "id": '<input type="text" name="id">',
}


class _TLS(http.server.ThreadingHTTPServer):
    """https 시험 서버 - 연결마다 그 연결의 스레드에서 TLS 를 연다."""

    daemon_threads = True

    def __init__(self, addr, handler, context):
        super().__init__(addr, handler)
        self.context = context

    def finish_request(self, request, client_address):
        try:
            tls = self.context.wrap_socket(request, server_side=True)
        except (ssl.SSLError, OSError):
            return
        try:
            super().finish_request(tls, client_address)
        finally:
            tls.close()


@unittest.skipUnless(CHROME and OPENSSL, "Chromium/Chrome 이나 openssl 이 없다")
class TwoStepInABrowser(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        cls.dir = tempfile.TemporaryDirectory()
        cert, key = os.path.join(cls.dir.name, "cert.pem"), os.path.join(cls.dir.name, "key.pem")
        made = subprocess.run([OPENSSL, "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", cert,
                               "-days", "2", "-subj", "/CN=example.com"], capture_output=True)
        if made.returncode != 0:
            cls.dir.cleanup()
            raise unittest.SkipTest(f"시험 인증서를 만들지 못했습니다: {made.stderr.decode(errors='replace')[:200]}")
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)

        cls.posted, cls.released = [], []

        class Pages(http.server.BaseHTTPRequestHandler):
            def page(self, body, code=200):
                self.send_response(code)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.end_headers()
                self.wfile.write(body.encode())

            def do_GET(self):
                host = (self.headers.get("Host") or "").split(":")[0]
                path = self.path.split("?", 1)[0]
                if host == "idp.example.net" and path == "/pw":
                    return self.page(IDP_PAGE)
                if path.startswith("/fields/"):
                    return self.page('<!doctype html><meta charset="utf-8"><title>칸</title>' + FIELD_PAGES[path.split("/")[-1]])
                if path == "/lookup":
                    time.sleep(2.6)
                    return self.page("ok")
                pages = {"/v3/signin/identifier": SWAP_PAGE, "/login/id": NAV_ID_PAGE, "/nf/signin": NOFORM_PAGE,
                         "/cap/signin": CAPTCHA_PAGE, "/sso/signin": SSO_PAGE, "/newsletter": NEWSLETTER_PAGE,
                         "/twofield/login": TWO_FIELD_PAGE, "/reveal/login": REVEAL_PAGE,
                         "/opener": OPENER_PAGE.replace("{popup}", f"https://accounts.example.com:{cls.web}/v3/signin/identifier")}
                if path in pages:
                    return self.page(pages[path])
                self.page("없음", 404)

            def do_POST(self):
                form = parse_qs(self.rfile.read(int(self.headers.get("Content-Length") or 0)).decode())
                cls.posted.append((self.path, {k: v[0] for k, v in form.items()}))
                if self.path == "/login/pw":
                    time.sleep(0.4)
                    return self.page(NAV_PW_PAGE)
                if self.path == "/sso/next":
                    self.send_response(302)
                    self.send_header("Location", f"https://idp.example.net:{cls.web}/pw")
                    self.end_headers()
                    return
                self.page("ok")

            def log_message(self, *_):
                pass

        class Vault(http.server.BaseHTTPRequestHandler):
            # 보관함 /release 흉내: 브라우저 토큰이어야 하고, example.com 의 로그인만 있다.
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                cls.released.append((self.path, self.headers.get("Authorization"), body))
                host = body.get("host", "")
                ok = self.path == "/release" and self.headers.get("Authorization") == "Bearer browser" and \
                    (host == "example.com" or host.endswith(".example.com"))
                self.send_response(200 if ok else 404)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                answer = {"domain": "example.com", "name": "Example 계정", "username": USERNAME, "password": PASSWORD, "hint": HINT}
                self.wfile.write(json.dumps(answer if ok else {"error": f"이 사이트({host})에 저장된 로그인 정보가 없습니다."}).encode())

            def log_message(self, *_):
                pass

        cls.server = _TLS(("127.0.0.1", 0), Pages, context)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.web = cls.server.server_address[1]
        cls.vault = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Vault)
        threading.Thread(target=cls.vault.serve_forever, daemon=True).start()
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            cls.cdp = sock.getsockname()[1]
        cls.profile = tempfile.TemporaryDirectory()
        cls.chrome = subprocess.Popen([
            CHROME, "--headless=new", f"--remote-debugging-port={cls.cdp}", f"--user-data-dir={cls.profile.name}",
            "--no-first-run", "--no-default-browser-check", "--disable-gpu", "--window-size=1000,800",
            "--ignore-certificate-errors", "--disable-popup-blocking",
            "--host-resolver-rules=MAP * 127.0.0.1, EXCLUDE localhost", "about:blank",
        ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(100):
            try:
                urllib.request.urlopen(f"http://127.0.0.1:{cls.cdp}/json/version", timeout=1)
                break
            except OSError:
                time.sleep(0.1)

    @classmethod
    def tearDownClass(cls):
        cls.chrome.terminate()
        cls.chrome.wait(10)
        cls.server.shutdown()
        cls.vault.shutdown()
        cls.profile.cleanup()
        cls.dir.cleanup()

    def setUp(self):
        self.saved = (login.VAULT_URL, login.TOKEN, jobsites.FILE)
        login.VAULT_URL, login.TOKEN = f"http://127.0.0.1:{self.vault.server_address[1]}", "browser"
        jobsites.FILE = os.path.join(self.dir.name, "no-job.json")  # 작업이 없다 - 대화가 부른 것
        jobsites._cache.update(mtime=None, value=None)
        self.posted.clear()
        self.released.clear()

    def tearDown(self):
        login.VAULT_URL, login.TOKEN, jobsites.FILE = self.saved
        for t in self.list():
            if t.get("type") == "page":
                urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/close/{t['id']}")

    def list(self) -> list[dict]:
        return json.load(urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/list"))

    def url(self, host, path):
        return f"https://{host}:{self.web}{path}"

    async def open(self, url):
        req = urllib.request.Request(f"http://127.0.0.1:{self.cdp}/json/new?{url}", method="PUT")
        target = json.load(urllib.request.urlopen(req))
        for _ in range(50):
            await asyncio.sleep(0.1)
            if await self.eval(target, "document.readyState") == "complete" and self.now(target).startswith("https://"):
                break
        return target

    def now(self, target) -> str:
        """그 탭의 지금 주소(/json/list - browser-use 가 아는 주소와 같은 곳에서 온다)."""
        return next((t["url"] for t in self.list() if t["id"] == target["id"]), "")

    async def eval(self, target, expr, gesture=False):
        import websockets

        async with websockets.connect(target["webSocketDebuggerUrl"], max_size=None) as ws:
            s = upload._Session(ws)
            r = await s.call("Runtime.evaluate", {"expression": expr, "returnByValue": True, "userGesture": gesture})
            return r.get("result", {}).get("value")

    def asked(self):
        return [(b["host"], b["purpose"]) for _, _, b in self.released]

    async def test_a_google_like_page_in_one_call(self):
        # 이메일 → 다음(같은 문서 안에서 비밀번호 단계로 바뀜) → 비밀번호. 마지막 '다음'은 누르지 않는다.
        t = await self.open(self.url("accounts.example.com", "/v3/signin/identifier"))
        said = await login.fill(self.now(t), port=self.cdp, wait=6)
        self.assertIn("비밀번호까지 넣었습니다", said)
        self.assertIn(HINT, said)
        self.assertNotIn(PASSWORD, said)
        got = await self.eval(t, "({...got, pw: document.querySelector('[name=Passwd]').value, "
                                 "hidden: document.querySelector('[name=identifier2]').value, "
                                 "showing: document.getElementById('show').checked, at: location.pathname})")
        self.assertEqual(got, {"submits": 1, "final": 0, "shown": 0, "identifier": USERNAME, "pw": PASSWORD,
                               "hidden": "", "showing": False, "at": "/v3/signin/challenge/pwd"})
        self.assertEqual(self.asked(), [("accounts.example.com", "대화")], "보관함에서는 한 번만 꺼낸다")

    async def test_a_slow_next_step_is_not_pressed_twice(self):
        # Enter 뒤 계정을 묻는 요청이 2초 넘게 걸려도 '다음'을 또 누르지 않는다 - 탭이 요청을 보냈으면 일하는 중이다.
        t = await self.open(self.url("accounts.example.com", "/v3/signin/identifier?slow=1"))
        said = await login.fill(self.now(t), port=self.cdp, wait=8)
        self.assertIn("비밀번호까지 넣었습니다", said)
        self.assertEqual(await self.eval(t, "[got.submits, document.querySelector('[name=Passwd]').value]"), [1, PASSWORD])

    async def test_a_page_that_moves_to_a_new_address(self):
        # 아이디를 보내면 새 주소의 비밀번호 화면이 온다(실행 맥락·프레임이 모두 바뀐다). 자동 로그인 칸도 켠다.
        t = await self.open(self.url("www.example.com", "/login/id"))
        said = await login.fill(self.now(t), port=self.cdp, wait=6)
        self.assertIn("비밀번호까지 넣었습니다", said)
        self.assertIn("자동 로그인을 켰습니다", said)
        self.assertEqual(self.posted, [("/login/pw", {"userid": USERNAME})], "로그인 버튼(/login/done)은 누르지 않는다")
        got = await self.eval(t, "[document.querySelector('[name=pw]').value, document.querySelector('[name=keep]').checked]")
        self.assertEqual(got, [PASSWORD, True])
        self.assertEqual(len(self.released), 1)

    async def test_when_enter_does_nothing_it_presses_next(self):
        t = await self.open(self.url("www.example.com", "/nf/signin"))
        said = await login.fill(self.now(t), port=self.cdp, wait=6)
        self.assertIn("비밀번호까지 넣었습니다", said)
        got = await self.eval(t, "[got.clicks, got.identifier, document.getElementById('pw').value]")
        self.assertEqual(got, [1, USERNAME, PASSWORD])

    async def test_a_password_field_that_opens_as_the_id_is_typed(self):
        t = await self.open(self.url("www.example.com", "/reveal/login"))
        said = await login.fill(self.now(t), port=self.cdp, wait=4)
        self.assertIn("아이디(ho***@example.com)와 비밀번호를 넣었습니다", said)
        got = await self.eval(t, "[got.submits, document.querySelector('[name=email]').value, document.querySelector('[name=pw]').value]")
        self.assertEqual(got, [0, USERNAME, PASSWORD], "Enter 를 누르지 않는다")

    async def test_no_password_field_comes(self):
        # 보안문자를 묻는 화면 - 기다리다 그렇다고 말한다. 비밀번호는 어디에도 넣지 않는다.
        t = await self.open(self.url("accounts.example.com", "/cap/signin"))
        said = await login.fill(self.now(t), port=self.cdp, wait=2)
        self.assertIn("2초 안에 비밀번호 칸이 나오지 않았습니다", said)
        self.assertIn("browser_get_state", said)
        self.assertIn("운영자", said)
        values = await self.eval(t, "[...document.querySelectorAll('input')].map(i => i.value)")
        self.assertNotIn(PASSWORD, values)
        self.assertEqual(len(self.released), 1)

    async def test_the_next_step_on_another_site_gets_no_password(self):
        t = await self.open(self.url("www.example.com", "/sso/signin"))
        said = await login.fill(self.now(t), port=self.cdp, wait=6)
        self.assertIn("다른 사이트(idp.example.net)", said)
        self.assertEqual(self.posted, [("/sso/next", {"email": USERNAME})])
        await asyncio.sleep(0.5)
        self.assertEqual(await self.eval(t, "document.querySelector('[name=pw]').value"), "")

    async def test_not_a_sign_in_step_only_the_id(self):
        # 구독 화면, 칸이 더 있는 화면: 아이디만 넣고 넘기지 않는다(다음을 누르고 다시 부르라고 한다).
        for path, field in [("/newsletter", "email"), ("/twofield/login", "email")]:
            t = await self.open(self.url("www.example.com", path))
            said = await login.fill(self.now(t), port=self.cdp, wait=2)
            self.assertIn("다음 버튼을 누르고", said, path)
            await asyncio.sleep(0.4)
            self.assertEqual(await self.eval(t, f"[got.submits, document.querySelector('[name={field}]').value]"), [0, USERNAME], path)
            self.tearDown()
            self.setUp()

    async def test_the_popup_not_the_tab_that_opened_it(self):
        # 로그인 팝업과 그 팝업을 연 탭이 둘 다 '보이는' 탭이다 - 보이는지로는 고를 수 없다. 에이전트가 보는 탭의
        # 주소로 고르고, 그 탭에만 넣는다.
        opener = await self.open(self.url("app.example.org", "/opener"))
        await self.eval(opener, "document.getElementById('g').click()", gesture=True)
        popup = None
        for _ in range(50):
            await asyncio.sleep(0.1)
            popup = next((t for t in self.list() if t.get("type") == "page" and "/v3/signin/identifier" in t["url"]), None)
            if popup and await self.eval(popup, "document.readyState") == "complete":
                break
        self.assertIsNotNone(popup)
        self.assertTrue(await upload._visible(opener["webSocketDebuggerUrl"]))
        self.assertTrue(await upload._visible(popup["webSocketDebuggerUrl"]))

        # 에이전트가 팝업을 연 탭을 보고 있으면 그 사이트의 것을 묻는다(여기에는 저장된 것이 없다).
        with self.assertRaisesRegex(RuntimeError, "저장된 로그인 정보가 없습니다"):
            await login.fill(self.now(opener), port=self.cdp, wait=2)
        self.assertEqual(self.asked(), [("app.example.org", "대화")])
        self.released.clear()

        # 팝업을 보고 있으면 팝업에 넣는다.
        said = await login.fill(popup["url"], port=self.cdp, wait=6)
        self.assertIn("비밀번호까지 넣었습니다", said)
        self.assertEqual(self.asked(), [("accounts.example.com", "대화")])
        self.assertEqual(await self.eval(popup, "[got.identifier, document.querySelector('[name=Passwd]').value]"), [USERNAME, PASSWORD])
        self.assertEqual(await self.eval(opener, "document.querySelector('[name=email]').value"), "", "연 탭에는 넣지 않는다")

    async def test_a_tab_that_is_not_there_gets_nothing(self):
        t = await self.open(self.url("accounts.example.com", "/v3/signin/identifier"))
        with self.assertRaisesRegex(RuntimeError, "찾지 못해"):
            await login.fill(self.url("accounts.example.com", "/v3/signin/challenge/pwd"), port=self.cdp, wait=2)
        self.assertEqual(self.released, [])
        self.assertEqual(await self.eval(t, "document.getElementById('identifierId').value"), "")

    async def test_which_fields_are_the_login(self):
        # FIELDS_JS 가 고르는 칸: autocomplete 는 낱말로(username webauthn), 숨긴 칸은 빼고.
        want = {"webauthn": ["q1", None], "glif": ["identifier", None], "token": ["a", "p"], "hidden": [None, "p"], "id": ["id", None]}
        for name, fields in want.items():
            t = await self.open(self.url("www.example.com", f"/fields/{name}"))
            expr = f"(() => {{ const f = {login.FIELDS_JS}; return f ? f.map(e => e && e.name) : null; }})()"
            self.assertEqual(await self.eval(t, expr), fields, name)


class BridgeFocus(unittest.IsolatedAsyncioTestCase):
    """브리지가 browser-use 에 지금 보고 있는 탭의 주소를 물어 로그인·파일 도구에 넘긴다."""

    @classmethod
    def setUpClass(cls):
        os.environ.setdefault("MCP_BROWSER_HOST", "127.0.0.1")
        try:
            import bridge
            from mcp.types import CallToolRequestParams, CallToolResult, TextContent
        except ImportError as e:  # 브라우저 이미지 밖
            raise unittest.SkipTest(f"브리지를 불러오지 못했습니다: {e}")
        cls.bridge, cls.Params, cls.Result, cls.Text = bridge, CallToolRequestParams, CallToolResult, TextContent

    def setUp(self):
        self.calls = []
        self.answer = None
        test = self

        class Upstream:
            async def call_tool(self, name, arguments=None, read_timeout_seconds=None):
                test.calls.append((name, arguments))
                if isinstance(test.answer, Exception):
                    raise test.answer
                return test.answer

        self.saved = (self.bridge._upstream, self.bridge.chromium_alive, login.fill, upload.upload)
        self.bridge._upstream = Upstream()
        self.bridge.chromium_alive = lambda: True

    def tearDown(self):
        self.bridge._upstream, self.bridge.chromium_alive, login.fill, upload.upload = self.saved

    def state(self, text, is_error=False):
        return self.Result(content=[self.Text(type="text", text=text)], isError=is_error)

    async def test_reads_the_url_of_browser_get_state(self):
        self.answer = self.state(json.dumps({"url": "https://accounts.example.com/signin", "title": "로그인",
                                             "tabs": [{"url": "https://app.example.org/", "title": "앱"}], "interactive_elements": []}))
        self.assertEqual(await self.bridge.focused_url(), "https://accounts.example.com/signin")
        self.assertEqual(self.calls, [("browser_get_state", {"include_screenshot": False})])

    async def test_does_not_know_when_it_cannot_ask(self):
        for answer in [RuntimeError("끊김"), self.state("Error: No browser session active"),
                       self.state('{"url": "https://a.example.com/"}', is_error=True), self.state('{"title": "제목만"}')]:
            self.answer = answer
            self.assertIsNone(await self.bridge.focused_url(), answer)
        # 브라우저가 떠 있지 않으면 묻지 않는다 - 물으면 browser-use 가 브라우저를 띄운다.
        self.calls.clear()
        self.bridge.chromium_alive = lambda: False
        self.assertIsNone(await self.bridge.focused_url())
        self.assertEqual(self.calls, [])

    async def test_the_tools_get_that_url(self):
        self.answer = self.state(json.dumps({"url": "https://accounts.example.com/signin"}))
        seen = []

        async def fill(url=None, port=None, wait=None):
            seen.append(("login", url))
            return "넣었습니다"

        async def put(paths, selector=None, url=None):
            seen.append(("upload", url))
            return "올렸습니다"

        login.fill, upload.upload = fill, put
        r = await self.bridge.call_tool(self.Params(name="browser_fill_login", arguments={}))
        self.assertEqual([c.text for c in r.content], ["넣었습니다"])
        await self.bridge.call_tool(self.Params(name="browser_upload_files", arguments={"paths": ["/uploads/a.png"]}))
        self.assertEqual(seen, [("login", "https://accounts.example.com/signin"), ("upload", "https://accounts.example.com/signin")])


if __name__ == "__main__":
    unittest.main()
