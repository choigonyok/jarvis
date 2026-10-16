"""G-D1 and G-D2 against a throwaway FalkorDB (scripts/dbtest memory-svc).

Nothing leaves the machine: the embedder is a stand-in, and the paths tested
(direct node and fact writes) never call the LLM.
"""

import os
import unittest

from graphiti_core.embedder.client import EmbedderClient
from graphiti_core.nodes import EntityNode

if not os.environ.get("FALKORDB_HOST"):
    raise unittest.SkipTest("FALKORDB_HOST 이 없어 건너뜁니다 (scripts/dbtest memory-svc)")

from app import graph as G  # noqa: E402


class FakeEmbedder(EmbedderClient):
    async def create(self, input_data):
        return [0.1] * 16

    async def create_batch(self, input_data_list):
        return [[0.1] * 16 for _ in input_data_list]


def fresh_graph() -> "G.Graph":
    g = G.Graph(on_usage=None, on_voyage=None)
    g.embedder = FakeEmbedder()
    return g


class GraphTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.g = fresh_graph()
        await self.g.drivers["guest"].execute_query("MATCH (n) DETACH DELETE n")

    async def test_restart_keeps_what_a_node_has_gathered(self):
        """G-D1: after a restart the cache is empty; writing to a node that is
        already there must not replace its summary with an empty one."""
        nid = await self.g.node("guest", "Person", "k1", "테스트 사람")
        await self.g.drivers["guest"].execute_query(
            "MATCH (n:Entity {uuid: $u}) SET n.summary = $s", u=nid, s="쌓인 요약"
        )
        restarted = fresh_graph()
        self.assertEqual(await restarted.node("guest", "Person", "k1", "테스트 사람"), nid)
        node = await EntityNode.get_by_uuid(restarted.drivers["guest"], nid)
        self.assertEqual(node.summary, "쌓인 요약")

    async def test_an_edge_that_moves_is_still_one_edge(self):
        """G-D2: the same fact key written again with another target moves the
        edge; it is not left beside the old one under the same uuid."""
        await self.g.fact("guest", "k-edge", "LIKES", ("Place", "p1", "카페 가"), "나는 카페 가를 좋아한다")
        await self.g.fact("guest", "k-edge", "LIKES", ("Place", "p2", "카페 나"), "나는 카페 나를 좋아한다")
        rows, _, _ = await self.g.drivers["guest"].execute_query(
            "MATCH ()-[e:RELATES_TO {uuid: $u}]->(t) RETURN t.name AS target",
            u=G.stable_id("guest", "edge", "k-edge"),
        )
        self.assertEqual([r["target"] for r in rows], ["카페 나"])


if __name__ == "__main__":
    unittest.main()
