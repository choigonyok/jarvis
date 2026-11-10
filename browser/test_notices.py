"""작업 실행에 전할 말(notices)과, 그 말을 도구 결과에 붙이는 브리지.

    python -m unittest test_notices   (브리지 시험은 mcp·starlette 가 있는 곳 - 브라우저 이미지)
"""

import json
import os
import tempfile
import time
import unittest

import jobsites
import notices


class Notices(unittest.TestCase):
    def setUp(self):
        notices.clear()

    def test_said_once_in_order(self):
        self.assertTrue(notices.add("첫째"))
        self.assertFalse(notices.add("첫째"), "같은 말은 한 번만")
        self.assertFalse(notices.add("   "), "빈 말은 넣지 않는다")
        notices.add("둘째\n줄바꿈")
        self.assertEqual(notices.take(), ["첫째", "둘째 줄바꿈"])
        self.assertEqual(notices.take(), [], "꺼낸 말은 다시 나오지 않는다")

    def test_old_words_and_too_many_are_dropped(self):
        notices.add("오래된 말", now=time.time() - notices.TTL - 1)
        notices.add("새 말")
        self.assertEqual(notices.take(), ["새 말"])
        for i in range(notices.KEEP + 3):
            notices.add(f"말 {i}")
        got = notices.take()
        self.assertEqual(len(got), notices.KEEP)
        self.assertEqual(got[-1], f"말 {notices.KEEP + 2}", "가장 최근 것이 남는다")

    def test_rendered_as_the_guards(self):
        self.assertEqual(notices.render(["가", "나"]), "[자비스 가드] 가\n[자비스 가드] 나")


class BridgeNotices(unittest.TestCase):
    """에이전트가 넣은 말은 작업이 도는 동안의 다음 도구 결과에 붙고, 작업이 끝나면 버린다."""

    @classmethod
    def setUpClass(cls):
        os.environ.setdefault("MCP_BROWSER_HOST", "127.0.0.1")
        try:
            import bridge
            from mcp.types import CallToolResult
            from starlette.testclient import TestClient
        except ImportError as e:  # 브라우저 이미지 밖
            raise unittest.SkipTest(f"브리지를 불러오지 못했습니다: {e}")
        cls.bridge, cls.Result, cls.TestClient = bridge, CallToolResult, TestClient

    def setUp(self):
        notices.clear()
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        jobsites.FILE = os.path.join(self.dir.name, "job-sites.json")
        jobsites._cache.update(mtime=None, value=None)
        self.client = self.TestClient(self.bridge.build_app())

    def result(self):
        return self.Result(content=[{"type": "text", "text": "탭 2개"}])

    def texts(self, r):
        return [c.text for c in r.content]

    def test_a_running_job_hears_it_with_its_next_tool(self):
        r = self.client.post("/job-sites", json={"sites": ["www.musinsa.com"], "seconds": 600})
        self.assertEqual(r.status_code, 200)
        r = self.client.post("/notices", json={"text": "wait_payment 를 부르세요"})
        self.assertEqual((r.status_code, r.json()["added"]), (200, True))
        self.assertEqual(self.texts(self.bridge.with_notices(self.result())), ["탭 2개", "[자비스 가드] wait_payment 를 부르세요"])
        self.assertEqual(self.texts(self.bridge.with_notices(self.result())), ["탭 2개"], "한 번만 붙는다")

    def test_the_words_go_with_the_job(self):
        self.client.post("/job-sites", json={"sites": ["www.musinsa.com"], "seconds": 600})
        self.client.post("/notices", json={"text": "남은 말"})
        self.client.delete("/job-sites")
        self.client.post("/job-sites", json={"sites": ["www.coupang.com"], "seconds": 600})
        self.assertEqual(self.texts(self.bridge.with_notices(self.result())), ["탭 2개"], "끝난 실행의 말은 다음 실행에 가지 않는다")
        # 작업이 없으면(대화) 붙이지 않고 버린다.
        self.client.post("/notices", json={"text": "대화에 갈 뻔한 말"})
        self.client.delete("/job-sites")
        notices.add("대화에 갈 뻔한 말")
        self.assertEqual(self.texts(self.bridge.with_notices(self.result())), ["탭 2개"])
        self.assertEqual(notices.take(), [])

    def test_bad_requests(self):
        self.assertEqual(self.client.post("/notices", json={"text": " "}).status_code, 400)
        self.assertEqual(self.client.post("/notices", content=b"not json").status_code, 400)
        self.client.post("/job-sites", json={"sites": ["a.com"], "seconds": 600})
        notices.add("지울 말")
        self.assertEqual(self.client.delete("/notices").status_code, 200)
        self.assertEqual(notices.take(), [])


if __name__ == "__main__":
    unittest.main()
