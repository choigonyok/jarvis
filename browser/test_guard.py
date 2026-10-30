"""guard 의 판정 - 무엇을 묻고, 무엇을 막고, 무엇을 그냥 보내는가.

    python -m unittest test_guard   (aiohttp·websockets 가 있는 곳 - 브라우저 이미지)
"""

import asyncio
import base64
import json
import os
import unittest

os.environ.setdefault("PAYMENT_HOSTS_FILE", os.path.join(os.path.dirname(__file__), "payment-hosts.json"))

import guard  # noqa: E402


class GuardRules(unittest.TestCase):
    def setUp(self):
        self.g = guard.Guard(*guard.load_rules())

    def test_reads_are_never_stopped(self):
        # 네이버쇼핑 상품 페이지가 뒤에서 네이버페이를 읽는 것(2026-10-17).
        # 스마트스토어 '구매하기'가 여는 네이버페이 주문서도(2026-10-16 작업 11: 그때는 GET 까지 물었고, 에이전트가
        # 백그라운드의 결제 호스트를 카드 없이 거부해 ERR_BLOCKED_BY_CLIENT 로 열리지 않았다).
        for url in ["https://pay.naver.com/web-api/benefit?x=1", "https://new-m.pay.naver.com/pcpay",
                    "https://checkout.coupang.com/ssr/desktop/checkout", "https://nid.naver.com/user2/api/route",
                    "https://orders.pay.naver.com/order/sheet/123", "https://stdpay.inicis.com/stdjs/INIStdPay.js"]:
            self.assertFalse(self.g.asks(url, "GET"), url)
            self.assertFalse(self.g.blocked(url, "GET"), url)

    def test_naver_pay_writes_are_asked(self):
        self.assertTrue(self.g.asks("https://pay.naver.com/pcpay/order/confirm", "POST"))
        self.assertTrue(self.g.asks("https://order.pay.naver.com/api/pay", "POST"))

    def test_login_is_not_a_payment(self):
        self.assertFalse(self.g.asks("https://nid.naver.com/nidlogin.login", "POST"))

    def test_coupang_pay_is_asked_not_blocked(self):
        url = "https://checkout.coupang.com/ssr/desktop/checkout/payment/submit"
        self.assertTrue(self.g.asks(url, "POST"))
        self.assertFalse(self.g.blocked(url, "POST"))

    def test_any_coupang_checkout_write_is_asked(self):
        # Whatever the path of Coupang's real payment request, a write to its
        # order and pay hosts raises a card - a pattern that misses it must not
        # mean a purchase without asking.
        for url in ["https://checkout.coupang.com/ssr/api/v2/order", "https://pay.coupang.com/api/approve",
                    "https://rocketpay.coupang.com/keypad/verify", "https://payment.coupang.com/x"]:
            self.assertTrue(self.g.asks(url, "POST"), url)
        self.assertFalse(self.g.asks("https://www.coupang.com/vp/products/123", "POST"), "상품 페이지의 쓰기는 결제가 아님")

    def test_other_in_house_pay_is_still_blocked(self):
        # 쇼핑몰 자체 간편결제(SSG페이·11페이)는 그대로 막는다.
        self.assertTrue(self.g.blocked("https://pay.ssg.com/ssgpay/approve", "POST"))
        self.assertTrue(self.g.blocked("https://pay.11st.co.kr/11pay/approve", "POST"))

    def test_card_payments_at_big_malls_are_asked(self):
        # 2026-10-30 신용카드 결제를 허용: 큰 쇼핑몰의 결제 경로는 막지 않고 결제 카드로 묻는다 - 카드로 낼지
        # 자체 간편결제로 낼지 주소로 가릴 수 없어서, 어느 쪽이든 승인 카드를 거친다.
        for url in ["https://payment.11st.co.kr/payment/order", "https://www.musinsa.com/order/payment/request",
                    "https://www.amazon.com/gp/buy/spc/handlers/checkout"]:
            self.assertFalse(self.g.blocked(url, "POST"), url)
            self.assertTrue(self.g.asks(url, "POST"), url)

    def test_a_card_payment_window_is_asked_not_blocked(self):
        # 2026-10-29 장범준 LP: 공식몰의 결제수단은 '신용카드' 하나이고, 결제하기가 이니시스 결제창을 여는
        # POST 를 block 이 막아 창이 빈 화면으로 떴다. 그 창 안의 간편결제로 네이버페이를 고를 수 있다.
        for url in ["https://stdpay.inicis.com/payMain/pay", "https://mobile.inicis.com/smart/payment/",
                    "https://pay.kcp.co.kr/plugin/payplus_web.jsp", "https://web.nicepay.co.kr/v3/v3Payment.jsp",
                    "https://service.iamport.kr/inicis_payments/prepare",
                    # 결제창에 간편결제가 없으면 카드사 중 하나카드를 골라 그 안의 간편결제를 본다(2026-10-29 운영자 규칙).
                    "https://pg.hanacard.co.kr/auth/start"]:
            self.assertTrue(self.g.is_window(url), url)
            self.assertTrue(self.g.asks(url, "POST"), url)
            self.assertFalse(self.g.blocked(url, "POST"), url)
        self.assertFalse(self.g.is_window("https://notinicis.com/pay"), "이름 끝이 같을 뿐인 남의 호스트")
        self.assertFalse(self.g.is_window("https://pay.naver.com/x"))

    def test_other_ways_out_of_a_window_stay_blocked(self):
        # 결제창 안에서 고를 수 있어도 네이버페이가 아닌 간편결제·휴대폰 결제는 승인과 상관없이 막는다
        # (block 을 묻기보다 먼저 본다). 휴대폰 문자는 맥미니로 넘어와 자비스가 읽을 수 있다.
        # 신용카드는 허용한다(카드사 창은 결제창 - 아래 test_card_company_windows_are_windows).
        for url in ["https://online-pay.kakaopay.com/pay/x", "https://id.payco.com/oauth2.0/authorize",
                    "https://ui.danal.co.kr/webpay/x", "https://mup.mobilians.co.kr/MUP/goCashMain.mcash"]:
            self.assertTrue(self.g.blocked(url, "POST"), url)

    def test_card_company_windows_are_windows(self):
        # 결제창에서 카드를 고르면 이어지는 카드사 창: 같은 결제창 승인으로 지나가도록 결제창이다.
        for url in ["https://acs.hanacard.co.kr/payapp/C000000000VPQC.web", "https://pay.shinhancard.com/x", "https://ansim.kbcard.com/x",
                    "https://card.nonghyup.com/acs/x", "https://api.stripe.com/v1/payment_intents/pi_1/confirm"]:
            self.assertTrue(self.g.is_window(url), url)
            self.assertTrue(self.g.asks(url, "POST"), url)
            self.assertFalse(self.g.blocked(url, "POST"), url)
        self.assertFalse(self.g.is_window("https://banking.nonghyup.com/x"), "NH 은행은 카드사 창이 아니다")

    def test_every_rule_is_watched(self):
        watched = {p["urlPattern"] for p in self.g.patterns}
        rules = json.load(open(os.environ["PAYMENT_HOSTS_FILE"]))
        for p in rules["ask_patterns"] + rules["block"]:
            self.assertIn(p, watched)
        for h in rules["ask"] + rules["window"]:
            self.assertIn(f"*{h}*", watched)

    def test_coupang_order_sheet_writes_that_move_no_money_pass(self):
        # 작업 16(2026-10-17): 주문서가 결제하기 전에 보내는 쓰기 - 결제수단 고르기, 통계, 봇 감지
        # 센서 - 까지 물어서, 주문서가 '시스템 오류: 주문중 오류'로 멈췄다.
        for name in ["landing", "paybox", "changePayType", "deliveryAddress", "usePartialPoint"]:
            body = json.dumps({"checkoutId": 1, "eventName": name, "eventData": {}})
            self.assertTrue(self.g.asks(EVENTS, "POST"))
            self.assertTrue(self.g.passes(EVENTS, body), name)
        for url in ["https://checkout.coupang.com/api/node/checkout/metrics",
                    "https://checkout.coupang.com/api/node/checkout/piiToken",
                    "https://checkout.coupang.com/api/node/cart/intendedBuyWidget/add"]:
            self.assertTrue(self.g.passes(url, '{"x":1}'), url)
        sensor = "https://checkout.coupang.com/tRe0NJmkYwOUcZbd9ExHQgaVWsY/frimQraO/PiwcSw/IjEkF/306dHQW"
        self.assertTrue(self.g.passes(sensor, '{"sensor_data":"3;0;1;0;3360836;..."}'))
        self.assertFalse(self.g.passes(sensor, '{"x":1}'), "센서가 아닌 같은 경로의 쓰기는 묻는다")

    def test_coupang_pay_button_is_asked(self):
        order = json.dumps({"checkoutId": 1, "eventName": "order", "eventData": {"eventType": "createOrder"}})
        self.assertFalse(self.g.passes(EVENTS, order))
        self.assertEqual(self.g.event_of(EVENTS, order), "order")
        # 모르는 이벤트, 돈이 걸린 이벤트, 읽지 못한 본문은 묻는다.
        for body in [json.dumps({"eventName": "oneClickPurchaseLanding"}), json.dumps({"eventName": "repaymentPrepay"}),
                     None, "", "not json", "[1]", json.dumps({"eventName": 3})]:
            self.assertFalse(self.g.passes(EVENTS, body), body)
        self.assertEqual(self.g.event_of(EVENTS, None), "?")
        # 결제 호스트는 본문과 상관없이 묻는다.
        self.assertFalse(self.g.passes("https://rocketpay.coupang.com/keypad/verify", json.dumps({"eventName": "paybox"})))

    def test_body_of(self):
        import base64

        self.assertIsNone(guard.body_of({"hasPostData": True}), "있다는데 못 받은 본문")
        self.assertEqual(guard.body_of({}), "")
        self.assertEqual(guard.body_of({"postData": "a"}), "a")
        parts = [{"bytes": base64.b64encode(b'{"a":').decode()}, {"bytes": base64.b64encode(b"1}").decode()}]
        self.assertEqual(guard.body_of({"postDataEntries": parts}), '{"a":1}')

    def test_what_a_window_is_handed(self):
        from urllib.parse import quote

        form = "version=1.0&mid=x&goodname=" + quote("장범준 2집 LP") + "&price=64000&buyername=" + quote("홍길동") + "&charset=UTF-8"
        self.assertEqual(guard.window_order(form), {"amount": "64000", "item": "장범준 2집 LP"})
        euckr = "goodname=" + quote("장범준 3집 LP".encode("cp949")) + "&price=54%2C000&charset=EUC-KR"
        self.assertEqual(guard.window_order(euckr), {"amount": "54000", "item": "장범준 3집 LP"})
        self.assertEqual(guard.window_order("amt=000113000&goodsname=LP"), {"amount": "113000", "item": "LP"}, "0 이 앞에 붙은 금액")
        toss = json.dumps({"amount": 64000, "orderName": "장범준 2집 LP", "customerName": "x"})
        self.assertEqual(guard.window_order(toss), {"amount": "64000", "item": "장범준 2집 LP"})
        for body in [None, "", "not a form", json.dumps({"price": True}), json.dumps({"amount": "0"}), "[1]"]:
            self.assertEqual(guard.window_order(body), {}, body)


EVENTS = "https://checkout.coupang.com/api/node/checkout/events"


class Paused(unittest.IsolatedAsyncioTestCase):
    """멈춘 요청에 가드가 실제로 보내는 답: 그대로 보내기(continue), 묻기, 막기(fail)."""

    async def asyncSetUp(self):
        self.g = guard.Guard(*guard.load_rules())
        self.sent, self.asked = [], []

        async def ask(url, method, host, event="", **_):
            self.asked.append(event)
            return False

        self.g.ask = ask
        sent = self.sent
        self.params = params = []

        class WS:
            async def send(self, raw):
                msg = json.loads(raw)
                sent.append(msg["method"])
                params.append(msg.get("params", {}))

        self.ws = WS()

    def page_of(self, i=-1):
        """채운 안내 화면의 HTML."""
        return base64.b64decode(self.params[i]["body"]).decode("utf-8")

    def paused(self, url, body, method="POST"):
        return {"sessionId": "s", "params": {"requestId": "r", "resourceType": "XHR",
                                             "request": {"url": url, "method": method, "postData": body}}}

    async def test_choosing_a_card_goes_through(self):
        await self.g.on_paused(self.ws, self.paused(EVENTS, json.dumps({"eventName": "changePayType"})))
        self.assertEqual((self.sent, self.asked), (["Fetch.continueRequest"], []))

    async def test_the_pay_button_is_asked_and_held(self):
        await self.g.on_paused(self.ws, self.paused(EVENTS, json.dumps({"eventName": "order"})))
        self.assertEqual((self.sent, self.asked), (["Fetch.failRequest"], ["order"]))

    async def test_opening_a_payment_window_is_asked_with_what_it_was_handed(self):
        # 2026-10-29 18:31·18:35 가드 로그: '허용하지 않은 결제를 막았습니다: POST stdpay.inicis.com/payMain/pay'.
        # 이제는 막지 않고 묻는다 - 결제창이라고, 결제창이 받은 금액·상품과 함께. 결정 전에는 보내지 않는다.
        from urllib.parse import quote

        asked = []

        async def ask(url, method, host, event="", window=False, order=None):
            asked.append((host, window, order))
            return False

        self.g.ask = ask
        form = "version=1.0&mid=x&goodname=" + quote("장범준 2집 LP") + "&price=64000&charset=UTF-8"
        opening = {"sessionId": "s", "frameId": "F2", "params": {
            "requestId": "r", "resourceType": "Document", "frameId": "F2",
            "request": {"url": "https://stdpay.inicis.com/payMain/pay", "method": "POST", "postData": form}}}
        await self.g.on_paused(self.ws, opening)
        # 막은 결제창 자리는 Chrome 의 오류 화면이 아니라 자비스의 안내 화면이다(사람에게는 '404'로 보였다).
        self.assertEqual(self.sent, ["Fetch.fulfillRequest"])
        self.assertIn("자비스 · 결제를 보내지 않았습니다", self.page_of())
        self.assertEqual(asked, [("stdpay.inicis.com", True, {"amount": "64000", "item": "장범준 2집 LP"})])

        asked.clear()
        await self.g.on_paused(self.ws, self.paused("https://pay.naver.com/pcpay/order/confirm", "{}"))
        self.assertEqual(asked, [("pay.naver.com", False, {})], "네이버페이는 결제창이 아니다")

    async def test_an_approval_opens_no_window_here(self):
        # 승인 창은 에이전트에만 있다: 승인 받은 바로 뒤의 같은 호스트 요청도 다시 묻는다(예전에는 5분 동안
        # 묻지 않아, 에이전트가 결제자·결제 한 건을 따지기 전에 지나갔다).
        async def yes(url, method, host, event="", **_):
            self.asked.append(event)
            return True

        self.g.ask = yes
        order = json.dumps({"eventName": "order"})
        await self.g.on_paused(self.ws, self.paused(EVENTS, order))
        await self.g.on_paused(self.ws, self.paused(EVENTS, order))
        await self.g.on_paused(self.ws, self.paused("https://rocketpay.coupang.com/rocketpay/pay/authentication", "{}"))
        self.assertEqual(self.asked, ["order", "order", ""])
        self.assertEqual(self.sent, ["Fetch.continueRequest"] * 3)
        self.assertFalse(hasattr(self.g, "_approved"))

    async def test_the_bot_sensor_goes_to_the_path_its_script_came_from(self):
        # 2026-10-18 01:50: 센서 POST 의 본문을 Chrome 이 넘겨주지 않아 본문 규칙이 못 잡았고,
        # 결제하기보다 먼저 결제 카드가 떴다. 센서는 자기 스크립트를 받아 간 경로로 보낸다.
        sensor = "https://checkout.coupang.com/tRe0NJmkYwOUcZbd9ExHQgaVWsY/frimQraO/PiwcSw/PnZ1d/mATPxMX"
        unread = {"sessionId": "s", "params": {"requestId": "r", "resourceType": "XHR",
                                               "request": {"url": sensor, "method": "POST", "hasPostData": True}}}
        await self.g.on_paused(self.ws, unread)
        self.assertEqual(self.asked, [""], "스크립트를 보기 전에는 묻는다")
        self.sent.clear()
        self.asked.clear()
        script = {"sessionId": "s", "params": {"requestId": "r2", "resourceType": "Script",
                                               "request": {"url": sensor + "?v=aa8e79df-833d", "method": "GET"}}}
        await self.g.on_paused(self.ws, script)
        await self.g.on_paused(self.ws, unread)
        self.assertEqual((self.sent, self.asked), (["Fetch.continueRequest", "Fetch.continueRequest"], []))
        # 다른 무작위 경로, 다른 호스트의 같은 경로는 여전히 묻는다.
        other = json.loads(json.dumps(unread))
        other["params"]["request"]["url"] = "https://checkout.coupang.com/tRe0NJmkYwOUcZbd9ExHQgaVWsY/zzz/aaa"
        await self.g.on_paused(self.ws, other)
        self.assertEqual(self.asked, [""])
        self.assertFalse(self.g.passes("https://pay.coupang.com/tRe0NJmkYwOUcZbd9ExHQgaVWsY/frimQraO/PiwcSw/PnZ1d/mATPxMX", None))

    def test_scripts_kept_are_bounded(self):
        for i in range(self.g.SCRIPTS_KEPT + 10):
            self.g.saw_script(f"https://checkout.coupang.com/s/{i}?v=1")
        self.assertEqual(len(self.g.scripts), self.g.SCRIPTS_KEPT)
        self.assertNotIn("checkout.coupang.com/s/0", self.g.scripts)
        self.assertIn(f"checkout.coupang.com/s/{self.g.SCRIPTS_KEPT + 9}", self.g.scripts)

    def test_the_log_shows_a_bodys_shape_not_its_values(self):
        self.assertEqual(guard.shape_of(None), "본문 못 읽음")
        self.assertEqual(guard.shape_of(json.dumps({"eventName": "order", "checkoutId": 1})), "JSON eventName,checkoutId")
        self.assertEqual(guard.shape_of("a=1&b=2"), "본문 7자")


if __name__ == "__main__":
    unittest.main()


class Records(unittest.IsolatedAsyncioTestCase):
    """가드가 혼자 내린 결정과 결제의 길은 기록으로 남고, 값은 남지 않고, 판단을 기다리게 하지 않는다."""

    async def asyncSetUp(self):
        self.g = guard.Guard(*guard.load_rules())

        async def ask(*_, **__):
            return False

        self.g.ask = ask

        class WS:
            async def send(self, raw):
                pass

        self.ws = WS()

    def paused(self, url, body, method="POST", rtype="XHR"):
        return {"sessionId": "s", "params": {"requestId": "r", "resourceType": rtype, "frameId": "f",
                                             "request": {"url": url, "method": method, "postData": body}}}

    def noted(self):
        out = []
        while not self.g.records.empty():
            out.append(self.g.records.get_nowait())
        return out

    async def test_a_refused_payment_is_noted_without_its_values(self):
        await self.g.on_paused(self.ws, self.paused("https://pay.danal.co.kr/pay?token=secret-value&amount=1000", "{}"))
        got = self.noted()
        self.assertEqual([r["kind"] for r in got], ["guard.refused"])
        self.assertEqual(got[0]["payload"]["why"], "blocked")
        self.assertNotIn("secret-value", json.dumps(got))
        self.assertIn("token", got[0]["payload"]["url"])

    async def test_a_write_that_charges_nothing_is_noted(self):
        await self.g.on_paused(self.ws, self.paused(EVENTS, json.dumps({"eventName": "changePayType", "card": "1234"})))
        got = self.noted()
        self.assertEqual([r["kind"] for r in got], ["guard.passed"])
        self.assertNotIn("1234", json.dumps(got))

    async def test_the_way_a_payment_goes_is_noted(self):
        await self.g.on_paused(self.ws, self.paused("https://stdpay.inicis.com/payMain/pay?oid=abc", "", "GET", "Document"))
        got = self.noted()
        self.assertEqual(got[0]["kind"], "guard.flow")
        self.assertNotIn("abc", json.dumps(got))

    async def test_keys_differ_and_a_full_queue_does_not_wait(self):
        self.g.records = asyncio.Queue(maxsize=2)
        for i in range(5):
            self.g.note("guard.flow", "h", n=i)  # 기다리면 여기서 멈춘다
        got = self.noted()
        self.assertEqual(len(got), 2)
        self.assertNotEqual(got[0]["key"], got[1]["key"])

    async def test_records_go_out_in_a_batch(self):
        posted = []

        async def post(batch):
            posted.append(batch)
            return True

        self.g.post_records = post
        self.g.note("guard.flow", "a")
        self.g.note("guard.flow", "b")
        task = asyncio.create_task(self.g.send_records())
        await asyncio.sleep(0.01)
        task.cancel()
        self.assertEqual([[r["subject"] for r in b] for b in posted], [["a", "b"]])


class JobSites(unittest.TestCase):
    """작업이 도는 동안: 탭의 최상위 이동은 그 작업의 사이트 안에서만."""

    def setUp(self):
        import tempfile
        import time

        import jobsites

        self.jobsites = jobsites
        self.dir = tempfile.TemporaryDirectory()
        jobsites.FILE = os.path.join(self.dir.name, "job-sites.json")
        jobsites._cache.update(mtime=None, value=None)
        self.g = guard.Guard(*guard.load_rules())
        self.g.pages["s-page"] = "T1"
        self.hold = lambda sites, seconds=600: json.dump(
            {"sites": sites, "until": time.time() + seconds}, open(jobsites.FILE, "w")
        )

    def tearDown(self):
        self.dir.cleanup()

    def test_nothing_is_held_without_a_job(self):
        self.assertFalse(self.g.leaves_job("s-page", "T1", "https://evil.example/"))

    def test_a_job_stays_on_its_sites(self):
        self.hold(["www.coupang.com"])
        self.assertTrue(self.g.leaves_job("s-page", "T1", "https://evil.example/?d=1"))
        self.assertFalse(self.g.leaves_job("s-page", "T1", "https://checkout.coupang.com/pay"))
        self.assertFalse(self.g.leaves_job("s-page", "T1", "https://nid.naver.com/nidlogin.login"))

    def test_frames_inside_a_tab_are_not_its_location(self):
        self.hold(["www.coupang.com"])
        self.assertFalse(self.g.leaves_job("s-page", "F9", "https://ads.example/"))
        self.assertFalse(self.g.leaves_job("s-iframe", None, "https://ads.example/"))

    def test_payment_windows_open_for_any_job(self):
        # 결제창을 새 창으로 띄우는 PG 가 있다. 작업 1(2026-10-29)은 결제창 호스트를 짐작해 사이트에 더했지만
        # (fcmobile.inicis.com) 실제 창은 stdpay.inicis.com 이었다 - 결제창은 작업의 사이트가 아니어도 열린다.
        self.hold(["shampoomusicent.com"])
        for url in ["https://stdpay.inicis.com/payMain/pay", "https://pay.kcp.co.kr/x", "https://pg.hanacard.co.kr/auth",
                    "https://pay.naver.com/payments/abc", "https://orders.pay.naver.com/order/sheet/1"]:
            self.assertFalse(self.g.leaves_job("s-page", "T1", url), url)
        self.assertTrue(self.g.leaves_job("s-page", "T1", "https://evil.example/pay"))

    def test_a_job_without_sites_opens_none(self):
        self.hold([])
        self.assertTrue(self.g.leaves_job("s-page", "T1", "https://www.google.com/"))
        self.assertFalse(self.g.leaves_job("s-page", "T1", "about:blank"))

    def test_an_expired_hold_lets_go(self):
        self.hold(["www.coupang.com"], seconds=-1)
        self.assertFalse(self.g.leaves_job("s-page", "T1", "https://evil.example/"))

    def test_every_navigation_is_watched(self):
        self.assertIn({"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}, self.g.patterns)


class InfoPagesAndWindows(unittest.IsolatedAsyncioTestCase):
    """막은 페이지 이동의 안내 화면, 결제창이 연 창의 네이버페이, 결제 흐름의 길(로그)."""

    async def asyncSetUp(self):
        self.g = guard.Guard(*guard.load_rules())
        self.sent, self.params, self.asked = [], [], []
        sent, params = self.sent, self.params

        class WS:
            async def send(self, raw):
                msg = json.loads(raw)
                sent.append(msg["method"])
                params.append(msg.get("params", {}))

        self.ws = WS()

    def doc(self, url, session="s-pop", frame="P1", method="POST", body="a=1"):
        return {"sessionId": session, "params": {"requestId": "r", "resourceType": "Document", "frameId": frame,
                                                 "request": {"url": url, "method": method, "postData": body}}}

    async def test_a_pending_card_fills_the_page_with_its_wait(self):
        async def ask(url, method, host, event="", **_):
            return guard.Answer(False, "p-1")

        self.g.ask = ask
        await self.g.on_paused(self.ws, self.doc("https://stdpay.inicis.com/payMain/pay"))
        self.assertEqual(self.sent, ["Fetch.fulfillRequest"])
        page = base64.b64decode(self.params[0]["body"]).decode("utf-8")
        self.assertIn("<title>자비스 · 결제 승인 대기</title>", page)
        self.assertIn("wait_payment", page)
        headers = {h["name"]: h["value"] for h in self.params[0]["responseHeaders"]}
        self.assertEqual(headers["Content-Type"], "text/html; charset=utf-8")

    async def test_a_refused_xhr_is_still_failed(self):
        async def ask(url, method, host, event="", **_):
            return guard.Answer(False, "p-1")

        self.g.ask = ask
        xhr = self.doc("https://pay.naver.com/orderApi/payment/partners/register")
        xhr["params"]["resourceType"] = "XHR"
        await self.g.on_paused(self.ws, xhr)
        self.assertEqual(self.sent, ["Fetch.failRequest"])

    async def test_the_hana_app_card_is_blocked_and_said_so(self):
        # 2026-10-30 01:36: 결제창 승인 안에서 하나Pay 앱카드(M003121110MCAC)가 그대로 나가 폰 승인을 5분 기다렸다.
        # 운영자 '하나pay 같은 거 사용하지 말고' - 이제 가드가 막는다. 하나카드 창 안 네이버페이(NVP...)는 그대로.
        self.assertTrue(self.g.blocked("https://acs.hanacard.co.kr/payapp/M003121110MCAC.web;PAY_JSESSIONID=x", "POST"))
        self.assertTrue(self.g.blocked("https://acs.hanacard.co.kr/payapp/M003121510HCAAC.web", "POST"))
        for path in ["NVP001001WPC.web", "C000000000VPQC.web"]:
            self.assertFalse(self.g.blocked("https://acs.hanacard.co.kr/payapp/" + path, "POST"), path)
        await self.g.on_paused(self.ws, self.doc("https://acs.hanacard.co.kr/payapp/M003121110MCAC.web"))
        self.assertEqual(self.sent, ["Fetch.fulfillRequest"])
        self.assertIn("허용하지 않은 결제 수단", base64.b64decode(self.params[0]["body"]).decode("utf-8"))

    async def test_leaving_the_jobs_sites_is_said_on_the_page(self):
        import tempfile
        import time

        import jobsites

        d = tempfile.TemporaryDirectory()
        self.addCleanup(d.cleanup)
        jobsites.FILE = os.path.join(d.name, "job-sites.json")
        jobsites._cache.update(mtime=None, value=None)
        json.dump({"sites": ["shampoomusicent.com"], "until": time.time() + 600}, open(jobsites.FILE, "w"))
        self.g.pages["s-tab"] = "T1"
        await self.g.on_paused(self.ws, self.doc("https://evil.example/x", session="s-tab", frame="T1", method="GET", body=""))
        self.assertEqual(self.sent, ["Fetch.fulfillRequest"])
        page = base64.b64decode(self.params[0]["body"]).decode("utf-8")
        self.assertIn("작업의 사이트 밖", page)
        self.assertIn("evil.example", page)

    async def test_naver_pay_from_the_card_companys_popup_says_so(self):
        # 카드사 창(acs.hanacard.co.kr)이 연 네이버페이 팝업의 요청만 '결제창이 연 창'이다 - 에이전트는 그것만
        # 결제창의 승인으로 지나가게 한다. 따로 연 탭의 네이버페이는 아니다.
        got = []

        async def ask(url, method, host, event="", window=False, order=None, from_window=False):
            got.append((host, window, from_window))
            return guard.Answer(True)

        self.g.ask = ask
        # 쇼핑몰 탭(T0) 안의 이니시스 iframe(F-ini) 이 카드사 창(T-hana)을 열고, 그 창이 네이버페이 팝업(T-np)을 연다.
        self.g.pages.update({"s-shop": "T0", "s-hana": "T-hana", "s-np": "T-np", "s-tab": "T-tab"})
        self.g.frames.update({"T0": "https://shampoomusicent.com/x", "F-ini": "https://stdpay.inicis.com/payMain/pay",
                              "T-hana": "https://acs.hanacard.co.kr/payapp/NVP001001WPC.web"})
        self.g.openers.update({"T-hana": ("T0", "F-ini"), "T-np": ("T-hana", "T-hana")})
        self.assertTrue(self.g.opened_by_window("s-np"))
        self.assertTrue(self.g.opened_by_window("s-hana"))
        self.assertFalse(self.g.opened_by_window("s-tab"))
        self.assertFalse(self.g.opened_by_window("s-shop"))
        xhr = self.doc("https://pay.naver.com/orderApi/payment/partners/apply", session="s-np")
        xhr["params"]["resourceType"] = "XHR"
        await self.g.on_paused(self.ws, xhr)
        own = self.doc("https://orders.pay.naver.com/order/pay", session="s-tab")
        own["params"]["resourceType"] = "XHR"
        await self.g.on_paused(self.ws, own)
        self.assertEqual(got, [("pay.naver.com", False, True), ("orders.pay.naver.com", False, False)])

    def test_a_shape_keeps_no_value(self):
        shape = guard.url_shape("https://acs.hanacard.co.kr/payapp/NVP001002WPC.web;PAY_JSESSIONID=abc?resultCode=Success&reserveId=R1&paymentId=NP9")
        self.assertEqual(shape, "acs.hanacard.co.kr/payapp/NVP001002WPC.web;…?resultCode=Success&reserveId&paymentId")
        self.assertNotIn("abc", shape)
        self.assertNotIn("NP9", shape)

    def test_the_log_is_also_kept_in_the_volume(self):
        import logging
        import tempfile

        d = tempfile.TemporaryDirectory()
        self.addCleanup(d.cleanup)
        old = guard.LOG_DIR
        guard.LOG_DIR = d.name
        root = logging.getLogger()
        before = list(root.handlers)
        try:
            guard.setup_logging()
            guard.log.info("결제 흐름 이동: 시험")
            for h in root.handlers:
                h.flush()
            self.assertIn("결제 흐름 이동: 시험", open(os.path.join(d.name, "guard.log"), encoding="utf-8").read())
        finally:
            for h in root.handlers[:]:
                if h not in before:
                    root.removeHandler(h)
                    h.close()
            guard.LOG_DIR = old

