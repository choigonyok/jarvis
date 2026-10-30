"""POST /facts says which `later` is the service's own outage - the day's
budget, the graph or the model API away - so that mind counts no try for it,
and which is the fact's."""

import os
import unittest

os.environ.setdefault("API_TOKEN", "owner-token")
os.environ.setdefault("DATABASE_URL", "postgresql://unused/unused")  # never connected: no lifespan

from fastapi.testclient import TestClient  # noqa: E402

from app import main  # noqa: E402


class StubState:
    async def fact_result(self, ref):
        return None

    async def spent_today(self):
        return 0.0

    async def save_fact(self, ref, group, r):
        pass


class APIConnectionError(Exception):
    pass


class StubGraph:
    async def triplet(self, group, ref, fact):
        if ref == "away":
            raise APIConnectionError("model API unreachable")
        if ref == "slow":
            raise TimeoutError("graph took too long")
        raise ValueError("edge type not allowed")


FACT = {"source": {"self": True}, "target": {"name": "지수"}, "relation": "FRIEND_OF", "fact": "지수는 친구다"}


class FactsLaterTest(unittest.TestCase):
    def setUp(self):
        main.TOKEN = "owner-token"
        main.state, main.graph = StubState(), StubGraph()
        self.client = TestClient(main.app)

    def post(self, refs):
        body = {"group": "owner", "facts": [{**FACT, "ref": r} for r in refs]}
        r = self.client.post("/facts", json=body, headers={"Authorization": "Bearer owner-token"})
        self.assertEqual(r.status_code, 200, r.text)
        return {x["ref"]: x for x in r.json()["results"]}

    def test_outages_are_marked_and_a_fact_failure_is_not(self):
        main.HAS_KEY, main.DAILY_USD = True, 1.0
        got = self.post(["away", "slow", "bad"])
        self.assertTrue(got["away"]["outage"])
        self.assertTrue(got["slow"]["outage"])
        self.assertFalse(got["bad"]["outage"])
        self.assertIn("ValueError", got["bad"]["later"])

    def test_a_spent_budget_is_an_outage(self):
        main.HAS_KEY, main.DAILY_USD = True, 0.0
        got = self.post(["bad"])
        self.assertTrue(got["bad"]["outage"])


class RebuildGuardTest(unittest.TestCase):
    """Making the graph again empties it first: it asks to be named twice, and
    only the operator's has a table to come back from."""

    def setUp(self):
        main.TOKEN = "owner-token"
        main.GUEST_TOKEN = "guest-token"
        self.client = TestClient(main.app)

    def post(self, path, token="owner-token"):
        return self.client.post(path, headers={"Authorization": f"Bearer {token}"}).status_code

    def test_it_asks_twice(self):
        self.assertEqual(self.post("/rebuild?group=owner"), 422)
        self.assertEqual(self.post("/rebuild?group=owner&confirm=guest"), 422)

    def test_the_guest_has_no_table(self):
        self.assertEqual(self.post("/rebuild?group=guest&confirm=guest"), 422)

    def test_the_guest_token_is_not_for_it(self):
        self.assertEqual(self.post("/rebuild?group=owner&confirm=owner", "guest-token"), 403)


if __name__ == "__main__":
    unittest.main()
