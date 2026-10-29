"""pin 의 쿠페이 그림 키패드 읽기 - 숫자를 확실히 읽을 때만 돌려주는가.

    python -m unittest test_pin   (Pillow·websockets·aiohttp 가 있는 곳 - 브라우저 이미지)

SPRITE 는 2026-10-18 01:53 결제 화면(rocketpay.coupang.com)의 실제 스프라이트다. 키 배치만 담긴
그림이라 비밀번호와는 상관이 없다. 칸 순서(행 우선)로 5 9 8 4 / 1 2 6 7 / 0 3 이 그려져 있다.
"""

import asyncio
import base64
import io
import json
import os
import unittest
import urllib.request

os.environ.setdefault("PAYMENT_HOSTS_FILE", os.path.join(os.path.dirname(__file__), "payment-hosts.json"))

from PIL import Image  # noqa: E402

import pin  # noqa: E402

SPRITE = (
    "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"
)
# 키 N 이 보여 주는 칸: 쿠페이 키패드의 배경 위치(가로 25, 세로 26 간격)와 span 크기(17×26).
OFFSETS = [(0, 0), (25, 0), (50, 0), (75, 0), (0, 26), (25, 26), (50, 26), (75, 26), (0, 52), (25, 52)]
DRAWN = [5, 9, 8, 4, 1, 2, 6, 7, 0, 3]


def cells(src, offsets=OFFSETS, w=17, h=26):
    return [{"key": str(i), "src": src, "x": x, "y": y, "w": w, "h": h} for i, (x, y) in enumerate(offsets)]


def uri(img):
    buf = io.BytesIO()
    img.save(buf, "PNG")
    return "data:image/png;base64," + base64.b64encode(buf.getvalue()).decode()


class SpriteKeypad(unittest.TestCase):
    def setUp(self):
        self.src = "data:image/png;base64," + SPRITE
        self.sheet = Image.open(io.BytesIO(base64.b64decode(SPRITE))).convert("RGBA")

    def test_reads_the_real_keypad(self):
        self.assertEqual(pin.read_sprite(cells(self.src)), {str(i): d for i, d in enumerate(DRAWN)})

    def test_reads_a_shuffled_one(self):
        # 그림 속 칸을 다른 순서로 다시 붙여, 배치가 바뀐 키패드를 만든다.
        order = [3, 0, 9, 1, 8, 2, 7, 4, 6, 5]
        shuffled = Image.new("RGBA", self.sheet.size, (0, 0, 0, 0))
        for slot, src in enumerate(order):
            sx, sy = OFFSETS[src]
            dx, dy = OFFSETS[slot]
            shuffled.paste(self.sheet.crop((sx, sy, sx + 25, sy + 26)), (dx, dy))
        got = pin.read_sprite(cells(uri(shuffled)))
        self.assertEqual(got, {str(slot): DRAWN[src] for slot, src in enumerate(order)})

    def test_a_pixel_off_still_reads(self):
        shifted = [(x + 1, y) for x, y in OFFSETS[:5]] + [(x, y + 1) for x, y in OFFSETS[5:]]
        self.assertEqual(pin.read_sprite(cells(self.src, shifted)), {str(i): d for i, d in enumerate(DRAWN)})

    def test_refuses_what_it_cannot_read(self):
        # 빈 칸(오른쪽 아래), 같은 숫자 두 번, 반쯤 잘린 숫자 - 모두 아무것도 누르지 않는다.
        for offsets in [OFFSETS[:9] + [(75, 52)], OFFSETS[:9] + [OFFSETS[0]]]:
            with self.assertRaises(RuntimeError):
                pin.read_sprite(cells(self.src, offsets))
        with self.assertRaises(RuntimeError):
            pin.read_sprite(cells(self.src, w=8))



class Sites(unittest.TestCase):
    def test_where_a_pin_may_go(self):
        # 키패드가 있는 프레임의 출처로 가린다. 네이버는 결제 호스트만 - 블로그·카페는 누구나 글을 올린다.
        for origin, host in [("https://rocketpay.coupang.com", "coupang.com"), ("https://checkout.coupang.com/x?y", "coupang.com"),
                             ("https://pay.naver.com", "pay.naver.com"), ("https://new-m.pay.naver.com", "pay.naver.com"),
                             ("https://orders.pay.naver.com/order/1", "pay.naver.com")]:
            self.assertEqual((pin.site_of(origin) or {}).get("host"), host, origin)
        for origin in ["https://blog.naver.com", "https://cafe.naver.com", "https://nid.naver.com", "https://naver.com",
                       "https://stdpay.inicis.com", "https://notcoupang.com", "https://pay.naver.com.evil.example", "", "null", "://"]:
            self.assertIsNone(pin.site_of(origin), origin)


# 실제 브라우저(Chromium·Chrome, 시험만을 위한 임시 프로필)로 키패드 페이지를 띄워 누르는 것까지 본다.
# 모든 호스트를 이 컴퓨터로 돌려(host-resolver-rules) 출처만 pay.naver.com·blog.naver.com 처럼 만든다.
CHROMES = [os.environ.get("CHROMIUM", ""), "/usr/bin/chromium", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"]
CHROME = next((c for c in CHROMES if c and os.path.exists(c)), None)

KEYPAD_PAGE = """<!doctype html><meta charset="utf-8"><title>결제 비밀번호</title>
<style>body{font:16px sans-serif} #pad{display:grid;grid-template-columns:repeat(3,60px);gap:6px;width:200px}
#pad button{height:48px} #cover{position:absolute;inset:0;background:transparent;display:none}</style>
<p id="label">결제 비밀번호 6자리를 눌러 주세요</p><div id="pad"></div><div id="cover"></div>
<script>
const q = new URLSearchParams(location.search);
window.entered = [];
function draw() {
  const ds = [0,1,2,3,4,5,6,7,8,9].sort(() => Math.random() - 0.5);
  const pad = document.getElementById('pad');
  pad.innerHTML = '';
  for (const d of ds) {
    const b = document.createElement('button');
    if (q.get('kind') === 'image') { b.style.backgroundImage = 'url(data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==)'; b.dataset.k = 'x' + d; }
    else if (q.get('kind') === 'aria') { b.setAttribute('aria-label', String(d)); b.textContent = ''; }
    else b.textContent = String(d);
    b.addEventListener('click', () => {
      window.entered.push(d);
      document.body.dataset.entered = window.entered.join('');
      if (q.get('shuffle')) draw();
      if (q.get('vanish') && window.entered.length === Number(q.get('vanish'))) pad.remove();
    });
    pad.appendChild(b);
  }
}
draw();
if (q.get('cover')) document.getElementById('cover').style.display = 'block';
if (q.get('notext')) document.getElementById('label').textContent = '숫자를 고르세요';
</script>"""

HOST_PAGE = """<!doctype html><meta charset="utf-8"><title>결제</title><p>결제 비밀번호</p>
<iframe id="f" src="{src}" style="position:absolute;left:120px;top:90px;width:400px;height:420px;border:7px solid #888"></iframe>"""


@unittest.skipUnless(CHROME, "Chromium/Chrome 이 없다")
class KeypadInABrowser(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        import http.server
        import socket
        import subprocess
        import tempfile
        import threading
        import time

        class Pages(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                path, _, query = self.path.partition("?")
                if path == "/keypad":
                    body = KEYPAD_PAGE
                elif path == "/host":
                    from urllib.parse import parse_qs
                    body = HOST_PAGE.replace("{src}", parse_qs(query)["src"][0])
                else:
                    self.send_response(404)
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.end_headers()
                self.wfile.write(body.encode())

            def log_message(self, *_):
                pass

        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Pages)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.web = cls.server.server_address[1]
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            cls.cdp = sock.getsockname()[1]
        cls.profile = tempfile.TemporaryDirectory()
        cls.capture = tempfile.TemporaryDirectory()
        pin.CAPTURE_DIR = cls.capture.name
        cls.chrome = subprocess.Popen([
            CHROME, "--headless=new", f"--remote-debugging-port={cls.cdp}", f"--user-data-dir={cls.profile.name}",
            "--no-first-run", "--no-default-browser-check", "--disable-gpu", "--window-size=1000,800",
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
        cls.profile.cleanup()
        cls.capture.cleanup()

    def tearDown(self):
        pin._halted_until = 0.0
        for t in json.load(urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/list")):
            if t.get("type") == "page":
                urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/close/{t['id']}")

    async def open(self, url):
        req = urllib.request.Request(f"http://127.0.0.1:{self.cdp}/json/new?{url}", method="PUT")
        target = json.load(urllib.request.urlopen(req))
        await asyncio.sleep(1.2)
        return target

    async def entered(self, target, expr="window.entered.join('')"):
        import websockets

        async with websockets.connect(target["webSocketDebuggerUrl"], max_size=None) as ws:
            s = pin._Session(ws)
            r = await s.call("Runtime.evaluate", {"expression": expr, "returnByValue": True})
            return r.get("result", {}).get("value")

    async def frame_entered(self, target):
        """탭 안 첫 iframe 이 받은 숫자 - 출처가 달라 바깥 페이지에서는 못 읽으므로 그 프레임에서 읽는다."""
        import websockets

        async with websockets.connect(target["webSocketDebuggerUrl"], max_size=None) as ws:
            s = pin._Session(ws)
            frames = await pin._frames(s)
            ctx = await pin._world(s, frames[1]["id"])
            r = await s.call("Runtime.evaluate", {"expression": "document.body.dataset.entered || ''", "contextId": ctx, "returnByValue": True})
            return r.get("result", {}).get("value")

    def url(self, host, query=""):
        return f"http://{host}:{self.web}/keypad?{query}"

    async def test_enters_on_a_shuffled_keypad(self):
        for kind in ["text", "aria"]:
            t = await self.open(self.url("pay.naver.com", f"kind={kind}"))
            self.assertEqual(await pin.enter(pin="135790", port=self.cdp), "네이버페이 결제 비밀번호를 넣었습니다")
            self.assertEqual(await self.entered(t), "135790", kind)

    async def test_reads_again_when_the_keypad_reshuffles_after_each_press(self):
        t = await self.open(self.url("orders.pay.naver.com", "shuffle=1"))
        await pin.enter(pin="902468", port=self.cdp)
        self.assertEqual(await self.entered(t), "902468")

    async def test_a_keypad_in_a_frame_of_the_same_site(self):
        inner = self.url("pay.naver.com", "shuffle=1")
        t = await self.open(f"http://new-m.pay.naver.com:{self.web}/host?src={inner}")
        await pin.enter(pin="314159", port=self.cdp)
        self.assertEqual(await self.frame_entered(t), "314159")

    async def iframe_entered(self):
        """따로 떠 있는(다른 사이트 화면 속) 프레임이 받은 숫자 - 그 프레임에 직접 붙어 읽는다."""
        import websockets

        frame = next(t for t in json.load(urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/list")) if t.get("type") == "iframe")
        async with websockets.connect(frame["webSocketDebuggerUrl"], max_size=None) as ws:
            s = pin._Session(ws)
            r = await s.call("Runtime.evaluate", {"expression": "document.body.dataset.entered || ''", "returnByValue": True})
            return r.get("result", {}).get("value")

    async def test_a_keypad_in_a_payment_frame_of_another_site(self):
        # 쇼핑몰 화면 위 결제창 안의 결제 화면: 다른 사이트 프레임이라 탭에서는 평가되지 않는다 - 그 프레임에
        # 직접 붙어, 그 프레임 안의 좌표로 누른다.
        await self.open(f"http://shop.example:{self.web}/host?src={self.url('pay.naver.com', 'shuffle=1')}")
        self.assertEqual(await pin.enter(pin="271828", port=self.cdp), "네이버페이 결제 비밀번호를 넣었습니다")
        self.assertEqual(await self.iframe_entered(), "271828")

    async def test_does_not_enter_where_it_should_not(self):
        # 블로그(같은 naver.com 이라도 누구나 쓰는 곳), '비밀번호' 글자가 없는 화면, 덮인 키패드, 그림이라
        # 읽지 못하는 키패드, 다른 사이트 화면 속 블로그 프레임 - 어느 것도 누르지 않는다.
        cases = [self.url("blog.naver.com"), self.url("pay.naver.com", "notext=1"), self.url("pay.naver.com", "cover=1"),
                 f"http://shop.example:{self.web}/host?src={self.url('blog.naver.com')}",
                 self.url("pay.naver.com", "kind=image")]
        for url in cases:
            t = await self.open(url)
            with self.assertRaises(RuntimeError, msg=url):
                await pin.enter(pin="135790", port=self.cdp)
            got = await self.entered(t, "(window.entered || []).join('') + '|' + "
                                        "(document.getElementById('f') ? 'frame' : '')")
            self.assertTrue(got.startswith("|"), f"{url}: {got}")
            self.tearDown()
        # 읽지 못한 키패드는 모양을 남긴다.
        kept = os.listdir(self.capture.name)
        self.assertTrue(kept)
        last = os.path.join(self.capture.name, sorted(kept)[-1])
        self.assertTrue(os.path.exists(os.path.join(last, "targets.json")))
        # 마지막(그림 키패드)은 그 프레임의 키 모양과 그림까지 남긴다 - 다음에 읽는 법을 만들 재료.
        outlines = [json.load(open(os.path.join(last, f))) for f in os.listdir(last) if f.endswith(".json") and f != "targets.json"]
        frame = [o for o in outlines if "pay.naver.com" in o.get("origin", "")]
        self.assertTrue(frame and len(frame[0]["keys"]) >= 10, outlines)
        self.assertTrue(any(f.endswith(".png") and "-i" in f for f in os.listdir(last)), os.listdir(last))

    async def test_stops_and_holds_when_the_keypad_goes_away(self):
        # 세 자리를 누른 뒤 키패드가 사라지면 거기서 멈추고, 한동안은 다시 넣지 않는다(이어 넣으면 틀린다).
        t = await self.open(self.url("pay.naver.com", "vanish=3"))
        with self.assertRaisesRegex(RuntimeError, "3자리에서 멈췄습니다"):
            await pin.enter(pin="246802", port=self.cdp)
        self.assertEqual(await self.entered(t), "246")
        await self.open(self.url("pay.naver.com"))
        with self.assertRaisesRegex(RuntimeError, "멈춘 키패드"):
            await pin.enter(pin="246802", port=self.cdp)

    async def test_the_pin_must_be_set(self):
        await self.open(self.url("pay.naver.com"))
        os.environ.pop("NAVER_PAY_PIN", None)
        with self.assertRaisesRegex(RuntimeError, "JARVIS_NAVER_PAY_PIN"):
            await pin.enter(port=self.cdp)


if __name__ == "__main__":
    unittest.main()
