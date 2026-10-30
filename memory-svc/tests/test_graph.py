"""G-D1 and G-D2 against a throwaway FalkorDB (scripts/dbtest memory-svc).

Nothing leaves the machine: the embedder is a stand-in, and the paths tested
(direct node and fact writes) never call the LLM.
"""

import os
import unittest

import httpx

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

    async def test_without_voyage_facts_are_found_by_their_words(self):
        """Voyage out of reach - the embedding and the reranker both - and the
        facts and names still come, by their words: not a 500 and a
        traceback (2026-10-27)."""
        await self.g.fact("guest", "k-words", "LIKES", ("Place", "p9", "망원 카페"), "나는 망원 카페를 좋아한다")

        async def unreachable(*args, **kwargs):
            raise httpx.ConnectTimeout("no route to Voyage")

        self.g.voyage.embed = unreachable
        self.g.voyage.rerank = unreachable
        facts = await self.g.search("망원", "guest", 5)
        self.assertTrue(any("망원" in f["fact"] for f in facts), facts)
        nodes = await self.g.find("망원", "guest", 5)
        self.assertTrue(any("망원" in n["name"] for n in nodes), nodes)

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

    async def test_the_graph_comes_back_from_its_rows(self):
        """4f: the graph is an index of mind's facts table. What is exported
        comes back on the same edges and nodes when the graph is made again
        from the rows, with no model asked; a fact that ended stays ended."""
        ended = "2026-10-01T00:00:00+09:00"
        await self.g.fact("guest", "k-a", "LIKES", ("Place", "p1", "망원 카페"), "나는 망원 카페를 좋아한다")
        await self.g.fact("guest", "k-b", "LIVES_IN", ("Place", "p2", "옛 동네"), "나는 옛 동네에 살았다",
                          invalid_at=G.when(ended))
        edges = await self.g.export("guest", "", 100)
        self.assertEqual(len(edges), 2)
        place = next(e for e in edges if e["relation"] == "LIKES")["target"]
        self.assertEqual((place["name"], place["kind"]), ("망원 카페", "Place"))

        rows = [{**e, "id": "edge:" + e["id"], "edge": e["id"], "origin": "legacy"} for e in edges]
        rows.append({"id": "record:spending:1", "origin": "record", "key": "spending:1", "relation": "결제",
                     "target": {"kind": "Place", "key": "m1", "name": "육전식당"}, "fact": "육전식당에서 결제"})
        rows.append({"id": "mf_1", "origin": "checked", "relation": "FRIEND_OF", "fact": "지수는 친구다",
                     "source": {"self": True}, "target": {"name": "지수", "kind": "Person"}})

        async def pages():
            yield rows[:2]
            yield rows[2:]

        self.assertEqual(await self.g.rebuild("guest", pages()), 4)
        back = await self.g.export("guest", "", 100)
        self.assertEqual({e["fact"] for e in back},
                         {"나는 망원 카페를 좋아한다", "나는 옛 동네에 살았다", "육전식당에서 결제", "지수는 친구다"})
        self.assertEqual({e["id"] for e in edges} - {e["id"] for e in back}, set())
        gone = next(e for e in back if e["relation"] == "LIVES_IN")
        self.assertTrue(gone["invalidAt"].startswith("2026-10-01"))
        keep = next(e for e in back if e["relation"] == "LIKES")
        self.assertEqual(keep["target"]["id"], place["id"])
        me = await self.g.me("guest")
        self.assertEqual(next(e for e in back if e["relation"] == "FRIEND_OF")["source"]["id"], me)

    async def test_export_comes_in_pages(self):
        for i in range(3):
            await self.g.fact("guest", f"k-{i}", "LIKES", ("Place", f"p{i}", f"곳 {i}"), f"사실 {i}")
        first = await self.g.export("guest", "", 2)
        rest = await self.g.export("guest", first[-1]["id"], 2)
        self.assertEqual(len(first), 2)
        self.assertEqual(len(rest), 1)
        self.assertEqual(len({e["id"] for e in first + rest}), 3)


if __name__ == "__main__":
    unittest.main()
