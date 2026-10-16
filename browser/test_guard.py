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
