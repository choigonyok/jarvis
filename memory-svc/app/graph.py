"""The graph: Graphiti on FalkorDB. Haiku 5.5 resolves, extracts and
summarises; Voyage embeds (voyage-4-large) and reranks (rerank-3).

Three ways in:
- `triplet()` - a fact mind has read and checked against its source. Graphiti's
  add_triplet puts its people and places on the nodes already known, merges it
  with the same fact said before, and retires the facts it contradicts.
- `fact()` - structured records (calendar, spending, assets, workouts, jobs,
  photos), which mind reads and sends (/records). Written straight to the
  graph with IDs derived from the source's own keys, so the same thing always
  lands on the same node and edge and no LLM is asked whether two of them are
  the same.
- `episode()` - free text, Graphiti's own extraction: the 6-hour diary (which
  keeps people's summaries current), and conversations until mind reads them.
"""

import asyncio
import os
import uuid
from datetime import datetime, timezone
from zoneinfo import ZoneInfo

from . import patches  # noqa: F401 - before Graphiti makes any driver
from graphiti_core import Graphiti
from graphiti_core.cross_encoder.client import CrossEncoderClient
from graphiti_core.driver.falkordb_driver import FalkorDriver
from graphiti_core.edges import EntityEdge, create_entity_edge_embeddings
from graphiti_core.embedder.client import EmbedderClient
from graphiti_core.errors import EdgeNotFoundError, NodeNotFoundError
from graphiti_core.nodes import EntityNode, EpisodeType, EpisodicNode, create_entity_node_embeddings
from graphiti_core.search.search import search as graph_search
from graphiti_core.search.search_config import (
    EdgeReranker,
    EdgeSearchConfig,
    EdgeSearchMethod,
    NodeReranker,
    NodeSearchConfig,
    NodeSearchMethod,
    SearchConfig,
)
from graphiti_core.search.search_config_recipes import EDGE_HYBRID_SEARCH_RRF
from graphiti_core.search.search_filters import SearchFilters
from graphiti_core.utils.bulk_utils import add_nodes_and_edges_bulk
from graphiti_core.utils.maintenance.edge_operations import resolve_extracted_edge
from graphiti_core.utils.maintenance.node_operations import resolve_extracted_nodes

from .llm import HaikuClient
from .voyage import Voyage

KST = ZoneInfo("Asia/Seoul")
NS = uuid.UUID("6f1d3c52-8a3e-4c1b-9a63-6b9e3b8f2a11")

# Who "나" is in each partition.
OWNER = (os.environ.get("MEMORY_OWNER_NAME") or "운영자") + "(나)"
SELF_NAME = {"owner": OWNER, "guest": "손님(나)"}

# Measured on the owner graph (2026-10-11, 99 questions): voyage-4-large alone
# puts the answer first 93% of the time against bge-m3's 78%; rerank-3 on top
# of either takes it to 96%. Changing EMBED_MODEL re-embeds the whole graph
# once at the next start (see reembed).
EMBED_MODEL = os.environ.get("EMBED_MODEL", "voyage-4-large")
RERANK_MODEL = os.environ.get("RERANK_MODEL", "rerank-3")

EXTRACTION_NOTE = (
    f"이 기록은 한국어 개인 비서의 기억이다. '나', '운영자', '{OWNER}'는 모두 같은 사람(이 비서의 주인)이다. "
    "사람·장소·가게·물건 이름은 원문 그대로 쓰고 번역하지 않는다. 인사말·이모티콘·의미 없는 잡담에서는 사실을 만들지 않는다. "
    "약속·계획·선호·관계·구매·건강처럼 나중에 다시 물어볼 만한 사실만 뽑는다. "
    "사실(fact) 문장과 요약은 반드시 한국어로 쓴다."
)

# Edge range indices that hide facts on FalkorDB 4.22 (see patches): no longer
# created, and dropped where an older start left them.
EDGE_RANGE_INDICES = {
    "RELATES_TO": ("uuid", "group_id", "name", "created_at", "expired_at", "valid_at", "invalid_at"),
    "MENTIONS": ("uuid", "group_id"),
    "HAS_MEMBER": ("uuid",),
    "HAS_EPISODE": ("uuid", "group_id"),
    "NEXT_EPISODE": ("uuid", "group_id"),
}

RERANKERS = {"rerank": EdgeReranker.cross_encoder, "mmr": EdgeReranker.mmr, "rrf": EdgeReranker.rrf}


class VoyageEmbedder(EmbedderClient):
    """What Graphiti stores - names, facts, topics - is embedded as a document;
    a search query is embedded as a query, by Graph.query_vector."""

    def __init__(self, voyage: Voyage):
        self.voyage = voyage

    async def create(self, input_data) -> list[float]:
        text = input_data if isinstance(input_data, str) else next(iter(input_data), "")
        return (await self.voyage.embed([text], model=EMBED_MODEL, input_type="document"))[0]

    async def create_batch(self, input_data_list: list[str]) -> list[list[float]]:
        out: list[list[float]] = []
        for i in range(0, len(input_data_list), 128):
            out += await self.voyage.embed(input_data_list[i : i + 128], model=EMBED_MODEL, input_type="document")
        return out


class VoyageReranker(CrossEncoderClient):
    def __init__(self, voyage: Voyage):
        self.voyage = voyage

    async def rank(self, query: str, passages: list[str]) -> list[tuple[str, float]]:
        if not passages:
            return []
        ranked = await self.voyage.rerank(query, passages, model=RERANK_MODEL)
        return [(passages[i], score) for i, score in ranked]


def kst(t: datetime | str | None) -> str | None:
    """Seoul time; a stored time without a zone is UTC, as Graphiti writes it.
    Read raw from FalkorDB, a time comes back as its ISO string."""
    if not t:
        return None
    if isinstance(t, str):
        t = datetime.fromisoformat(t)
    return (t if t.tzinfo else t.replace(tzinfo=timezone.utc)).astimezone(KST).isoformat()


def stable_id(*parts: str) -> str:
    return str(uuid.uuid5(NS, "\x1f".join(parts)))


def now() -> datetime:
    return datetime.now(timezone.utc)


def when(v: str | None) -> datetime | None:
    """An ISO time from a caller; a bare date is midnight in Seoul."""
    if not v:
        return None
    t = datetime.fromisoformat(v)
    return t if t.tzinfo else t.replace(tzinfo=KST)


class Graph:
    def __init__(self, on_usage, on_voyage):
        key = os.environ.get("VOYAGE_API_KEY", "")
        if not key:
            raise RuntimeError("VOYAGE_API_KEY 가 없습니다 - 기억의 임베딩과 재정렬은 Voyage 로 합니다")
        driver = FalkorDriver(
            host=os.environ.get("FALKORDB_HOST", "falkordb"),
            port=int(os.environ.get("FALKORDB_PORT", "6379")),
        )
        self.voyage = Voyage(key, on_usage=on_voyage)
        self.embedder = VoyageEmbedder(self.voyage)
        reranker = VoyageReranker(self.voyage)
        self.llm = HaikuClient(os.environ.get("ANTHROPIC_API_KEY", ""), on_usage=on_usage)
        self.g = Graphiti(graph_driver=driver, llm_client=self.llm, embedder=self.embedder, cross_encoder=reranker)
        # Graphiti keeps each group in its own FalkorDB graph; direct saves and
        # searches have to go to that graph too, not the driver's default_db.
        self.drivers = {g: driver.clone(database=g) for g in SELF_NAME}
        # add_episode picks the group's graph per call; Graphiti's resolving
        # helpers read through a clients bundle bound to one graph - so each
        # group gets an instance bound to its graph, for its clients.
        self.groups = {
            g: Graphiti(graph_driver=d, llm_client=self.llm, embedder=self.embedder, cross_encoder=reranker)
            for g, d in self.drivers.items()
        }
        # LLM-resolved writes one at a time: two facts about a new person
        # arriving together must not both create that person.
        self.writing = asyncio.Lock()
        self._nodes: set[str] = set()
        # Heard after every direct fact: the 6-hour diary (sources.diary) is
        # made of these lines.
        self.on_fact = None
        self.on_extract = None

    async def setup(self):
        for d in self.drivers.values():
            await d.build_indices_and_constraints()
            rows, _, _ = await d.execute_query(
                "CALL db.indexes() YIELD label, properties, types, entitytype "
                "WHERE entitytype = 'RELATIONSHIP' RETURN label, properties, types"
            )
            for r in rows:
                for p in r["properties"]:
                    if r["label"] in EDGE_RANGE_INDICES and "RANGE" in (r["types"].get(p) or []):
                        await d.execute_query(f"DROP INDEX FOR ()-[e:{r['label']}]-() ON (e.{p})")

    # --- writing -------------------------------------------------------------

    async def episode(self, group: str, key: str, name: str, body: str, source_description: str, when: datetime):
        async with self.writing:
            result = await self.g.add_episode(
                name=name,
                episode_body=body,
                source_description=source_description,
                reference_time=when,
                source=EpisodeType.message,
                group_id=group,
                custom_extraction_instructions=EXTRACTION_NOTE,
            )
        if self.on_extract:
            await self.on_extract(group, source_description, getattr(result, "edges", None) or [])

    async def node(self, group: str, kind: str, key: str, name: str, summary: str = "") -> str:
        nid = stable_id(group, "node", kind, key)
        if nid in self._nodes:
            return nid
        n = EntityNode(uuid=nid, name=name, group_id=group, labels=["Entity", kind], summary=summary, created_at=now())
        await n.generate_name_embedding(self.embedder)
        await n.save(self.drivers[group])
        self._nodes.add(nid)
        return nid

    async def me(self, group: str) -> str:
        return await self.node(group, "Person", "self", SELF_NAME.get(group, "나"))

    async def fact(
        self,
        group: str,
        key: str,
        relation: str,
        target: tuple[str, str, str],
        fact: str,
        valid_at: datetime | None = None,
        invalid_at: datetime | None = None,
        source: tuple[str, str, str] | None = None,
    ):
        """One structured fact: (source or me) -[relation]-> target.
        target/source are (kind, key, display name)."""
        src = await (self.node(group, *source) if source else self.me(group))
        dst = await self.node(group, *target)
        edge = EntityEdge(
            uuid=stable_id(group, "edge", key),
            group_id=group,
            source_node_uuid=src,
            target_node_uuid=dst,
            name=relation,
            fact=fact,
            created_at=now(),
            valid_at=valid_at,
            invalid_at=invalid_at,
            episodes=[],
        )
        await edge.generate_embedding(self.embedder)
        await edge.save(self.drivers[group])
        if self.on_fact:
            await self.on_fact(group, fact, valid_at)

    async def has_fact(self, group: str, key: str, fact: str) -> bool:
        """Whether the edge a structured record lands on already says this,
        word for word: then writing it again would only re-embed it."""
        try:
            e = await EntityEdge.get_by_uuid(self.drivers[group], stable_id(group, "edge", key))
        except EdgeNotFoundError:
            return False
        return (e.fact or "") == fact

    async def _endpoint(self, group: str, spec: dict) -> EntityNode:
        """A node mind names: by id when it knows one (and the node is still
        there), "나" for the operator, otherwise a new node by name that
        add_triplet resolves against the people and places already known."""
        if spec.get("id"):
            try:
                return await EntityNode.get_by_uuid(self.drivers[group], spec["id"])
            except NodeNotFoundError:
                pass
        if spec.get("self"):
            return await EntityNode.get_by_uuid(self.drivers[group], await self.me(group))
        kind = spec.get("kind") or "Thing"
        return EntityNode(uuid=str(uuid.uuid4()), name=spec["name"], group_id=group, labels=["Entity", kind], created_at=now())

    async def triplet(self, group: str, ref: str, f: dict) -> dict:
        """One checked fact through add_triplet. Its edge id comes from mind's
        ref, so a retry lands on the same edge. Returns what Graphiti made of
        it: the nodes it was put on, whether it repeats a fact already known,
        and the facts it ended."""
        src = await self._endpoint(group, f["source"])
        dst = await self._endpoint(group, f["target"])
        edge = EntityEdge(
            uuid=stable_id(group, "fact", ref),
            group_id=group,
            source_node_uuid=src.uuid,
            target_node_uuid=dst.uuid,
            name=f["relation"],
            fact=f["fact"],
            created_at=now(),
            valid_at=when(f.get("validAt")),
            invalid_at=when(f.get("invalidAt")),
            episodes=[],
        )
        async with self.writing:
            resolved, ended, source, target = await self._add_triplet(group, src, edge, dst)
            ended += await self._end(group, f.get("ends") or [], resolved, source, target)
        # The same retired fact can come back twice.
        ended = list({e.uuid: e for e in ended if e.uuid != resolved.uuid}.values())
        if self.on_fact and resolved.uuid == edge.uuid:
            await self.on_fact(group, resolved.fact, resolved.valid_at)
        return {
            "ref": ref,
            "edge": {"id": resolved.uuid, "fact": resolved.fact},
            "duplicate": resolved.uuid != edge.uuid,
            # Graphiti read the new fact as already superseded by a later one.
            "expired": resolved.expired_at is not None,
            "source": {"id": source.uuid, "name": source.name},
            "target": {"id": target.uuid, "name": target.name},
            "ended": [{"id": e.uuid, "fact": e.fact, "invalidAt": kst(e.invalid_at)} for e in ended],
        }

    async def _add_triplet(self, group: str, source: EntityNode, edge: EntityEdge, target: EntityNode):
        """Graphiti's add_triplet (0.30.2), except that a new fact is weighed
        only against facts about its own subject, and only ends facts about the
        same matter. Graphiti's looks for what it contradicts across the whole
        graph, and "지수는 매운 음식을 못 먹는다" ended "나는 요즘 매운 음식도
        잘 먹는다" - someone else - because both are about spicy food. A repeat
        is still looked for between the same two nodes."""
        driver, clients = self.drivers[group], self.groups[group].clients
        ends = []
        for node in (source, target):
            try:
                ends.append(await EntityNode.get_by_uuid(driver, node.uuid))
            except NodeNotFoundError:
                if node.name_embedding is None:
                    await node.generate_name_embedding(self.embedder)
                found, _, _ = await resolve_extracted_nodes(clients, [node])
                ends.append(found[0])
        src, dst = ends
        edge.source_node_uuid, edge.target_node_uuid = src.uuid, dst.uuid
        try:
            known = await EntityEdge.get_by_uuid(driver, edge.uuid)
            if (known.source_node_uuid, known.target_node_uuid) != (src.uuid, dst.uuid):
                edge.uuid = str(uuid.uuid4())  # an id taken by another pair: a new edge
        except EdgeNotFoundError:
            pass
        if edge.fact_embedding is None:
            await edge.generate_embedding(self.embedder)

        async def among(edges: list[EntityEdge]) -> list[EntityEdge]:
            if not edges:
                return []
            found = await graph_search(
                clients, edge.fact, [group], EDGE_HYBRID_SEARCH_RRF,
                SearchFilters(edge_uuids=[e.uuid for e in edges]), driver=driver,
            )
            return found.edges

        repeats = await among(await EntityEdge.get_between_nodes(driver, src.uuid, dst.uuid))
        about = await among(await EntityEdge.get_by_node_uuid(driver, src.uuid))
        episode = EpisodicNode(
            name="", source=EpisodeType.text, source_description="", content="",
            valid_at=edge.valid_at or now(), entity_edges=[], group_id=group,
        )
        resolved, ended, _ = await resolve_extracted_edge(self.llm, edge, repeats, about, episode, None)
        # Ended only if it is about the same thing (the same far node) or the
        # same kind of fact (the same relation): a fact the model called a
        # contradiction otherwise is left as it was - a true fact wrongly
        # ended is lost, two facts side by side are not.
        def same_matter(e: EntityEdge) -> bool:
            other = e.target_node_uuid if e.source_node_uuid == src.uuid else e.source_node_uuid
            return other == dst.uuid or e.name == edge.name
        ended = [e for e in ended if same_matter(e)]
        edges = [resolved] + ended
        await create_entity_edge_embeddings(self.embedder, edges)
        await create_entity_node_embeddings(self.embedder, [src, dst])
        await add_nodes_and_edges_bulk(driver, [], [], [src, dst], edges, self.embedder)
        return resolved, ended, src, dst

    async def _end(self, group: str, ids: list[str], new: EntityEdge, source: EntityNode, target: EntityNode) -> list[EntityEdge]:
        """End the facts mind named: it read them as no longer so, with the
        words that say it quoted. Graphiti may already have. Only a fact about
        the same subject, or between the same two, is ended - one that only
        shares the object ("매운 음식") would be someone else's."""
        driver, ended = self.drivers[group], []
        for eid in ids:
            try:
                e = await EntityEdge.get_by_uuid(driver, eid)
            except EdgeNotFoundError:
                continue
            nodes = {e.source_node_uuid, e.target_node_uuid}
            if e.uuid == new.uuid or e.invalid_at is not None or not (source.uuid in nodes and (e.source_node_uuid == source.uuid or target.uuid in nodes)):
                continue
            e.invalid_at = new.valid_at or now()
            e.expired_at = now()
            await e.save(driver)
            ended.append(e)
        return ended

    async def communities(self, group: str) -> int:
        """Topics: clusters of related people, places and things, each with an
        LLM summary. Rebuilt whole each time."""
        async with self.writing:
            nodes, _ = await self.g.build_communities(group_ids=[group])
        return len(nodes)

    async def reembed(self) -> int:
        """Every name, fact and topic embedded again with EMBED_MODEL. Vectors
        from two models cannot be compared, so a model change has to reach the
        whole graph at once."""
        n = 0
        for d in self.drivers.values():
            for match, field, text in (
                ("(x:Entity)", "name_embedding", "name"),
                ("(x:Community)", "name_embedding", "name"),
                ("()-[x:RELATES_TO]->()", "fact_embedding", "fact"),
            ):
                rows, _, _ = await d.execute_query(f"MATCH {match} RETURN x.uuid AS uuid, x.{text} AS text")
                for i in range(0, len(rows), 128):
                    chunk = rows[i : i + 128]
                    vecs = await self.embedder.create_batch([r["text"] or "" for r in chunk])
                    await d.execute_query(
                        f"UNWIND $rows AS r MATCH {match} WHERE x.uuid = r.uuid SET x.{field} = vecf32(r.v)",
                        rows=[{"uuid": r["uuid"], "v": v} for r, v in zip(chunk, vecs)],
                    )
                    n += len(chunk)
        return n

    # --- reading -------------------------------------------------------------

    async def query_vector(self, query: str) -> list[float]:
        return (await self.voyage.embed([query.replace("\n", " ")], model=EMBED_MODEL, input_type="query"))[0]

    async def names(self, group: str, uuids: set[str]) -> dict[str, str]:
        if not uuids:
            return {}
        rows, _, _ = await self.drivers[group].execute_query(
            "MATCH (n:Entity) WHERE n.uuid IN $ids RETURN n.uuid AS uuid, n.name AS name", ids=list(uuids)
        )
        return {r["uuid"]: r["name"] for r in rows}

    async def search(self, query: str, group: str, limit: int = 10, center: str | None = None, mode: str = "rerank") -> list[dict]:
        """Facts for a question: keyword and vector candidates (and, around a
        person, the facts within two steps of them), ordered by the reranker -
        or by MMR when the answer should cover ground rather than repeat."""
        methods = [EdgeSearchMethod.bm25, EdgeSearchMethod.cosine_similarity]
        if center:
            methods.append(EdgeSearchMethod.bfs)
        config = SearchConfig(
            edge_config=EdgeSearchConfig(search_methods=methods, reranker=RERANKERS[mode], sim_min_score=0.0, bfs_max_depth=2),
            limit=limit,
        )
        results = await graph_search(
            self.g.clients,
            query,
            [group],
            config,
            SearchFilters(),
            center_node_uuid=center,
            bfs_origin_node_uuids=[center] if center else None,
            query_vector=await self.query_vector(query),
            driver=self.drivers[group],
        )
        edges = results.edges
        names = await self.names(group, {u for e in edges for u in (e.source_node_uuid, e.target_node_uuid)})
        return [
            {
                "id": e.uuid,
                "fact": e.fact,
                "relation": e.name,
                # Seoul time: a fact valid from 10/9 00:00 KST is 10/8 in UTC,
                # and a date cut from the UTC form read as the day before.
                "validAt": kst(e.valid_at),
                "invalidAt": kst(e.invalid_at),
                "source": {"id": e.source_node_uuid, "name": names.get(e.source_node_uuid, "")},
                "target": {"id": e.target_node_uuid, "name": names.get(e.target_node_uuid, "")},
            }
            for e in edges
        ]

    async def find(self, query: str, group: str, limit: int = 5) -> list[dict]:
        """People, places and things by name - how mind ties "지수" in a
        conversation to the node it already has."""
        config = SearchConfig(
            node_config=NodeSearchConfig(
                search_methods=[NodeSearchMethod.bm25, NodeSearchMethod.cosine_similarity],
                reranker=NodeReranker.rrf,
                sim_min_score=0.0,
            ),
            limit=limit,
        )
        results = await graph_search(
            self.g.clients, query, [group], config, SearchFilters(),
            query_vector=await self.query_vector(query), driver=self.drivers[group],
        )
        return [
            {"id": n.uuid, "name": n.name, "kinds": [l for l in n.labels if l != "Entity"], "summary": n.summary}
            for n in results.nodes
        ]

    async def profile(self, group: str, uid: str, limit: int = 30) -> dict | None:
        """One node with its summary and latest facts, the ones still true first."""
        try:
            n = await EntityNode.get_by_uuid(self.drivers[group], uid)
        except NodeNotFoundError:
            return None
        rows, _, _ = await self.drivers[group].execute_query(
            """MATCH (n:Entity {uuid: $id})-[e:RELATES_TO]-(m:Entity)
               RETURN e.uuid AS id, e.fact AS fact, e.name AS relation, e.valid_at AS valid_at,
                      e.invalid_at AS invalid_at, e.expired_at AS expired_at, m.name AS other
               ORDER BY e.expired_at IS NOT NULL, coalesce(e.valid_at, e.created_at) DESC LIMIT $limit""",
            id=uid,
            limit=limit,
        )
        return {
            "id": n.uuid,
            "name": n.name,
            "kinds": [l for l in n.labels if l != "Entity"],
            "summary": n.summary,
            "facts": [
                {
                    "id": r["id"],
                    "fact": r["fact"],
                    "relation": r["relation"],
                    "other": r["other"],
                    "validAt": kst(r["valid_at"]),
                    "ended": r["expired_at"] is not None or r["invalid_at"] is not None,
                }
                for r in rows
            ],
        }

    async def topics(self, group: str) -> list[dict]:
        rows, _, _ = await self.drivers[group].execute_query(
            """MATCH (c:Community) OPTIONAL MATCH (c)-[:HAS_MEMBER]->(m)
               RETURN c.uuid AS id, c.name AS name, c.summary AS summary, count(m) AS members
               ORDER BY members DESC"""
        )
        return [{"id": r["id"], "name": r["name"], "summary": r["summary"], "members": r["members"]} for r in rows]
