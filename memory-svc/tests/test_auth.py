"""The guest's agent's token: it reads the guest's group and nothing else."""

import os
import unittest

os.environ.setdefault("API_TOKEN", "owner-token")
os.environ.setdefault("API_GUEST_TOKEN", "guest-token")
os.environ.setdefault("DATABASE_URL", "postgresql://unused/unused")  # never connected: no lifespan

from fastapi.testclient import TestClient  # noqa: E402

from app import main  # noqa: E402


class StubGraph:
    async def search(self, q, group, limit, center=None, mode="rerank"):
        return [{"group": group}]

    async def topics(self, group):
        return []


class GuestTokenTest(unittest.TestCase):
    def setUp(self):
        main.TOKEN, main.GUEST_TOKEN = "owner-token", "guest-token"
        main.graph = StubGraph()
        self.client = TestClient(main.app)  # not entered: no lifespan, no FalkorDB

    def get(self, path, token):
        return self.client.get(path, headers={"Authorization": f"Bearer {token}"} if token else {})

    def test_guest_reads_the_guest_group(self):
        r = self.get("/search?q=x&group=guest", "guest-token")
        self.assertEqual(r.status_code, 200)
        self.assertEqual(r.json()["facts"], [{"group": "guest"}])

    def test_guest_cannot_read_the_owner_group(self):
        for path in ("/search?q=x&group=owner", "/nodes?q=x&group=owner", "/nodes/u1?group=owner", "/topics?group=owner"):
            self.assertEqual(self.get(path, "guest-token").status_code, 403, path)

    def test_guest_cannot_write_or_run(self):
        h = {"Authorization": "Bearer guest-token"}
        self.assertEqual(self.client.post("/facts", json={"group": "guest", "facts": []}, headers=h).status_code, 403)
        self.assertEqual(self.client.post("/run", headers=h).status_code, 403)
        self.assertEqual(self.get("/status", "guest-token").status_code, 403)

    def test_owner_reads_any_group_and_strangers_none(self):
        self.assertEqual(self.get("/search?q=x&group=owner", "owner-token").status_code, 200)
        self.assertEqual(self.get("/search?q=x&group=guest", "nope").status_code, 401)
        self.assertEqual(self.get("/search?q=x&group=guest", "").status_code, 401)


if __name__ == "__main__":
    unittest.main()
