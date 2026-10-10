"""The graph: Graphiti on FalkorDB, with Haiku 5.5 extracting and a local
embedding model (bge-m3 on Ollama) for search.

Two ways in:
- `episode()` - free text (conversation, KakaoTalk, iMessage). Graphiti's own
  pipeline: the LLM finds people, places, plans and how they relate.
- `fact()` - structured records (calendar, spending, assets, workouts, jobs,
  photos). Written straight to the graph with IDs derived from the source's
  own keys, so the same thing always lands on the same node and edge and no
  LLM is asked whether two of them are the same.
"""

import os
import uuid
from datetime import datetime, timezone
from zoneinfo import ZoneInfo

from graphiti_core import Graphiti
from graphiti_core.cross_encoder.client import CrossEncoderClient
from graphiti_core.driver.falkordb_driver import FalkorDriver
from graphiti_core.edges import EntityEdge
from graphiti_core.embedder.openai import OpenAIEmbedder, OpenAIEmbedderConfig
from graphiti_core.nodes import EntityNode, EpisodeType

from .llm import HaikuClient

KST = ZoneInfo("Asia/Seoul")
NS = uuid.UUID("6f1d3c52-8a3e-4c1b-9a63-6b9e3b8f2a11")

# Who "나" is in each partition.
OWNER = (os.environ.get("MEMORY_OWNER_NAME") or "운영자") + "(나)"
SELF_NAME = {"owner": OWNER, "guest": "손님(나)"}

EXTRACTION_NOTE = (
    f"이 기록은 한국어 개인 비서의 기억이다. '나', '운영자', '{OWNER}'는 모두 같은 사람(이 비서의 주인)이다. "
    "사람·장소·가게·물건 이름은 원문 그대로 쓰고 번역하지 않는다. 인사말·이모티콘·의미 없는 잡담에서는 사실을 만들지 않는다. "
    "약속·계획·선호·관계·구매·건강처럼 나중에 다시 물어볼 만한 사실만 뽑는다. "
    "사실(fact) 문장과 요약은 반드시 한국어로 쓴다."
)


class PassThrough(CrossEncoderClient):
    """Graphiti's default reranker needs OpenAI. The default search (RRF)
    does not rerank, so this only keeps the constructor from reaching for it."""

    async def rank(self, query: str, passages: list[str]) -> list[tuple[str, float]]:
        return [(p, 1.0 - i / max(len(passages), 1)) for i, p in enumerate(passages)]


def kst(t: datetime | None) -> str | None:
    """Seoul time; a stored time without a zone is UTC, as Graphiti writes it."""
    if t is None:
        return None
    return (t if t.tzinfo else t.replace(tzinfo=timezone.utc)).astimezone(KST).isoformat()


def stable_id(*parts: str) -> str:
    return str(uuid.uuid5(NS, "\x1f".join(parts)))


def now() -> datetime:
    return datetime.now(timezone.utc)


class Graph:
    def __init__(self, on_usage):
        driver = FalkorDriver(
            host=os.environ.get("FALKORDB_HOST", "falkordb"),
            port=int(os.environ.get("FALKORDB_PORT", "6379")),
        )
        embedder = OpenAIEmbedder(
            config=OpenAIEmbedderConfig(
                api_key="ollama",
                base_url=os.environ.get("EMBED_URL", "http://ollama:11434/v1"),
                embedding_model=os.environ.get("EMBED_MODEL", "bge-m3"),
                embedding_dim=1024,
            )
        )
        self.llm = HaikuClient(os.environ.get("ANTHROPIC_API_KEY", ""), on_usage=on_usage)
        self.g = Graphiti(
            graph_driver=driver,
            llm_client=self.llm,
            embedder=embedder,
            cross_encoder=PassThrough(),
        )
        self.embedder = embedder
        # Graphiti keeps each group in its own FalkorDB graph; direct saves and
        # searches have to go to that graph too, not the driver's default_db.
        self.drivers = {g: driver.clone(database=g) for g in SELF_NAME}
        self._nodes: set[str] = set()
        # Heard after every direct fact: the 6-hour diary (sources.diary) is
        # made of these lines.
        self.on_fact = None

    async def setup(self):
        for d in self.drivers.values():
            await d.build_indices_and_constraints()

    async def episode(self, group: str, key: str, name: str, body: str, source_description: str, when: datetime):
        await self.g.add_episode(
            name=name,
            episode_body=body,
            source_description=source_description,
            reference_time=when,
            source=EpisodeType.message,
            group_id=group,
            custom_extraction_instructions=EXTRACTION_NOTE,
        )

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

    async def search(self, query: str, group: str, limit: int = 10) -> list[dict]:
        edges = await self.g.search(query, group_ids=[group], num_results=limit, driver=self.drivers[group])
        return [
            {
                "fact": e.fact,
                "relation": e.name,
                # Seoul time: a fact valid from 10/9 00:00 KST is 10/8 in UTC,
                # and a date cut from the UTC form read as the day before.
                "validAt": kst(e.valid_at),
                "invalidAt": kst(e.invalid_at),
            }
            for e in edges
        ]
