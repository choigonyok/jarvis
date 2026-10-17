"""guard 의 판정 - 무엇을 묻고, 무엇을 막고, 무엇을 그냥 보내는가.

    python -m unittest test_guard   (aiohttp·websockets 가 있는 곳 - 브라우저 이미지)
"""

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
        for url in ["https://pay.naver.com/web-api/benefit?x=1", "https://new-m.pay.naver.com/pcpay",
                    "https://checkout.coupang.com/ssr/desktop/checkout", "https://nid.naver.com/user2/api/route"]:
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
        self.assertTrue(self.g.blocked("https://pay.ssg.com/ssgpay/approve", "POST"))
        self.assertTrue(self.g.blocked("https://payment.11st.co.kr/payment/order", "POST"))

    def test_every_rule_is_watched(self):
        watched = {p["urlPattern"] for p in self.g.patterns}
        rules = json.load(open(os.environ["PAYMENT_HOSTS_FILE"]))
        for p in rules["ask_patterns"] + rules["block"]:
            self.assertIn(p, watched)

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


EVENTS = "https://checkout.coupang.com/api/node/checkout/events"


class Paused(unittest.IsolatedAsyncioTestCase):
    """멈춘 요청에 가드가 실제로 보내는 답: 그대로 보내기(continue), 묻기, 막기(fail)."""

    async def asyncSetUp(self):
        self.g = guard.Guard(*guard.load_rules())
        self.sent, self.asked = [], []

        async def ask(url, method, host, event=""):
            self.asked.append(event)
            return False

        self.g.ask = ask
        sent = self.sent

        class WS:
            async def send(self, raw):
                sent.append(json.loads(raw)["method"])

        self.ws = WS()

    def paused(self, url, body, method="POST"):
        return {"sessionId": "s", "params": {"requestId": "r", "resourceType": "XHR",
                                             "request": {"url": url, "method": method, "postData": body}}}

    async def test_choosing_a_card_goes_through(self):
        await self.g.on_paused(self.ws, self.paused(EVENTS, json.dumps({"eventName": "changePayType"})))
        self.assertEqual((self.sent, self.asked), (["Fetch.continueRequest"], []))

    async def test_the_pay_button_is_asked_and_held(self):
        await self.g.on_paused(self.ws, self.paused(EVENTS, json.dumps({"eventName": "order"})))
        self.assertEqual((self.sent, self.asked), (["Fetch.failRequest"], ["order"]))


if __name__ == "__main__":
    unittest.main()


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

    def test_a_job_without_sites_opens_none(self):
        self.hold([])
        self.assertTrue(self.g.leaves_job("s-page", "T1", "https://www.google.com/"))
        self.assertFalse(self.g.leaves_job("s-page", "T1", "about:blank"))

    def test_an_expired_hold_lets_go(self):
        self.hold(["www.coupang.com"], seconds=-1)
        self.assertFalse(self.g.leaves_job("s-page", "T1", "https://evil.example/"))

    def test_every_navigation_is_watched(self):
        self.assertIn({"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}, self.g.patterns)
