"""memory-svc: jarvis' long-term memory, a Graphiti knowledge graph.

Every MEMORY_INTERVAL it collects what is new across jarvis (sources.py) into
the graph, mind puts in the facts it has checked (/facts) and the structured
records it read (/records); agents and mind
ask it with /search, /nodes and /topics. The operator's data lives in group
"owner", the guest's conversation in group "guest" - and every call names
exactly one group, so neither can read the other's.
"""

import asyncio
import logging
import os
import secrets
from datetime import datetime

from fastapi import Depends, FastAPI, Header, HTTPException, Query
from pydantic import BaseModel, Field

from . import sources
from .graph import EMBED_MODEL, Graph, kst
from .llm import MODEL
from .state import State

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("memory")

TOKEN = os.environ.get("API_TOKEN", "")
# An empty token would leave the graph - everything jarvis knows about the
# operator - open to anything on the network. Only a local trial may run so.
if not TOKEN and os.environ.get("API_TOKEN_OPTIONAL") != "true":
    raise SystemExit("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
# The guest's agent's own token: it reads group "guest" and nothing else.
GUEST_TOKEN = os.environ.get("API_GUEST_TOKEN", "")
INTERVAL = int(os.environ.get("MEMORY_INTERVAL", "300"))
DAILY_USD = float(os.environ.get("MEMORY_DAILY_USD", "1.0"))
HAS_KEY = bool(os.environ.get("ANTHROPIC_API_KEY"))
VOYAGE_KEY = bool(os.environ.get("VOYAGE_API_KEY"))
# Which collectors run. mind reads KakaoTalk and iMessage, and reads the
# structured sources and sends them to /records; what is left here is the
# assistant's own conversation (chat) and the weekly topics.
DEFAULT_SOURCES = "chat,topics"
SOURCES = [s.strip() for s in (os.environ.get("MEMORY_SOURCES") or DEFAULT_SOURCES).split(",") if s.strip()]
if unknown := [s for s in SOURCES if s not in sources.ALL]:
    log.warning("MEMORY_SOURCES 에 모르는 수집기가 있어 뺍니다: %s", ", ".join(unknown))
    SOURCES = [s for s in SOURCES if s in sources.ALL]
GROUP = "^(owner|guest)$"

state = State()
graph: Graph | None = None
wake = asyncio.Event()
running = {"now": None}


def caller(authorization: str = Header(default="")) -> str:
    """Who is asking: "owner" for the services' token, "guest" for the guest's agent's."""
    if not TOKEN or secrets.compare_digest(authorization, f"Bearer {TOKEN}"):
        return "owner"
    if GUEST_TOKEN and secrets.compare_digest(authorization, f"Bearer {GUEST_TOKEN}"):
        return "guest"
    raise HTTPException(401, "인증이 필요합니다.")


def auth(who: str = Depends(caller)):
    """The services' token only: writing, the status, a run."""
    if who != "owner":
        raise HTTPException(403, "손님은 손님 기억을 읽기만 할 수 있습니다.")


def reader(group: str = Query(pattern=GROUP), who: str = Depends(caller)):
    """A read of one group: the guest's token may name only the guest's."""
    if who == "guest" and group != "guest":
        raise HTTPException(403, "손님은 손님 기억만 읽을 수 있습니다.")


async def llm_ok() -> bool:
    return HAS_KEY and await state.spent_today() < DAILY_USD


async def embeddings():
    """A new EMBED_MODEL re-embeds the whole graph once: vectors from two
    models cannot be compared, so search would rank by noise until it did.
    Checked every round, so a failed attempt is tried again."""
    if (await state.cursor("embeddings")).get("model") == EMBED_MODEL:
        return
    running["now"] = "embeddings"
    try:
        n = await graph.reembed()
        await state.save("embeddings", {"model": EMBED_MODEL}, n)
        log.info("그래프를 %s 로 다시 임베딩했습니다: %d개", EMBED_MODEL, n)
    except Exception as e:
        log.warning("다시 임베딩하지 못했습니다: %s", e)
        await state.fail("embeddings", f"{type(e).__name__}: {e}")


async def loop():
    ctx = sources.Ctx(graph, state, llm_ok)
    while True:
        await embeddings()
        for name in SOURCES:
            run = sources.ALL[name]
            running["now"] = name
            try:
                n = await run(ctx)
                if n:
                    log.info("%s: %d개를 기억에 넣었습니다", name, n)
            except Exception as e:  # one source failing must not stop the rest
                log.warning("%s 수집 실패: %s", name, e)
                await state.fail(name, f"{type(e).__name__}: {e}")
        running["now"] = None
        try:
            await asyncio.wait_for(wake.wait(), INTERVAL)
        except asyncio.TimeoutError:
            pass
        wake.clear()


async def lifespan(app: FastAPI):
    global graph
    await state.open()

    def on_usage(i: int, o: int):
        asyncio.get_running_loop().create_task(state.add_usage(i, o))

    graph = Graph(on_usage, state.add_voyage_usage)
    await graph.setup()

    task = asyncio.create_task(loop())
    yield
    task.cancel()


app = FastAPI(lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/search", dependencies=[Depends(reader)])
async def search(
    q: str = Query(min_length=1),
    group: str = Query(pattern=GROUP),
    limit: int = 10,
    center: str | None = Query(None, description="사람·장소 노드 id: 그 주변 두 단계의 사실도 후보로"),
    mode: str = Query("rerank", pattern="^(rerank|mmr|rrf)$"),
):
    return {"facts": await graph.search(q, group, max(1, min(limit, 30)), center=center, mode=mode)}


@app.get("/nodes", dependencies=[Depends(reader)])
async def find(q: str = Query(min_length=1), group: str = Query(pattern=GROUP), limit: int = 5):
    return {"nodes": await graph.find(q, group, max(1, min(limit, 20)))}


@app.get("/nodes/{uid}", dependencies=[Depends(reader)])
async def profile(uid: str, group: str = Query(pattern=GROUP), limit: int = 30):
    p = await graph.profile(group, uid, max(1, min(limit, 100)))
    if p is None:
        raise HTTPException(404, "그런 노드가 없습니다.")
    return p


class Endpoint(BaseModel):
    """One end of a fact: a node mind already knows (id), the operator
    ("self"), or a name for Graphiti to resolve against the nodes it has."""

    id: str | None = None
    name: str = ""
    kind: str | None = None
    self_: bool = Field(False, alias="self")


class FactIn(BaseModel):
    ref: str = Field(min_length=1, max_length=200)
    source: Endpoint
    target: Endpoint
    relation: str = Field(min_length=1, max_length=80)
    fact: str = Field(min_length=1, max_length=1000)
    validAt: str | None = None
    invalidAt: str | None = None
    # Facts mind read as no longer so by this one's words, by edge id.
    ends: list[str] = Field(default_factory=list, max_length=20)


class FactsIn(BaseModel):
    group: str = Field(pattern=GROUP)
    facts: list[FactIn] = Field(max_length=50)


# What a failure inside add_triplet is called when it is the services under
# it, not the fact: the graph database or the model API being away, slow or
# rationing. Such a `later` is marked `outage`, and mind counts no try for it
# (gocommon/health's rule).
OUTAGE_NAMES = ("Connection", "Timeout", "RateLimit", "Overloaded", "ServiceUnavailable", "InternalServer", "APIStatus")


def outage(e: BaseException) -> bool:
    if isinstance(e, (ConnectionError, TimeoutError, asyncio.TimeoutError)):
        return True
    return any(n in type(e).__name__ for n in OUTAGE_NAMES)


@app.post("/facts", dependencies=[Depends(auth)])
async def add_facts(body: FactsIn):
    """Facts mind has checked against their source, one add_triplet each. A
    ref seen before is answered from memory_facts without asking Graphiti
    again; one that cannot go in now comes back with `later`, for mind to send
    again - with `outage` when the reason is not the fact but this service
    (the day's LLM budget spent, the graph or the model API away)."""
    for f in body.facts:
        if not all(e.id or e.name.strip() or e.self_ for e in (f.source, f.target)):
            raise HTTPException(422, f"{f.ref}: 사실의 양 끝에는 id, 이름, self 중 하나가 있어야 합니다.")
    out = []
    for f in body.facts:
        if (done := await state.fact_result(f.ref)) is not None:
            out.append(done)
            continue
        if not await llm_ok():
            out.append({"ref": f.ref, "later": "오늘 LLM 예산을 다 써서 내일 넣습니다.", "outage": True})
            continue
        try:
            r = await graph.triplet(body.group, f.ref, f.model_dump(by_alias=True))
        except Exception as e:
            log.warning("사실을 넣지 못했습니다 (%s): %s", f.ref, e)
            out.append({"ref": f.ref, "later": f"{type(e).__name__}: {e}"[:300], "outage": outage(e)})
            continue
        await state.save_fact(f.ref, body.group, r)
        out.append(r)
    return {"results": out}


class NodeRef(BaseModel):
    kind: str = Field(min_length=1, max_length=40)
    key: str = Field(min_length=1, max_length=300)
    name: str = Field(min_length=1, max_length=300)


class RecordIn(BaseModel):
    key: str = Field(min_length=1, max_length=300)
    relation: str = Field(min_length=1, max_length=80)
    target: NodeRef
    source: NodeRef | None = None
    fact: str = Field(min_length=1, max_length=2000)
    validAt: str | None = None


class RecordsIn(BaseModel):
    group: str = Field(pattern=GROUP)
    records: list[RecordIn] = Field(max_length=200)


@app.post("/records", dependencies=[Depends(auth)])
async def add_records(body: RecordsIn):
    """Structured records mind read from the services that keep them
    (calendar, spending, assets, workouts, jobs, photos): one reader for each
    source, so what mind reasons over and what memory remembers are the same
    reading. Written straight to the graph like the collectors did (no LLM),
    on the same edge keys; a record already there word for word is skipped."""
    written = 0
    for r in body.records:
        done = f"record:{r.key}:{sources.h(r.fact, r.validAt)}"
        if await state.done(done):
            continue
        if not await graph.has_fact(body.group, r.key, r.fact):
            valid_at = datetime.fromisoformat(r.validAt) if r.validAt else None
            await graph.fact(body.group, r.key, r.relation, (r.target.kind, r.target.key, r.target.name), r.fact,
                             valid_at=valid_at, source=(r.source.kind, r.source.key, r.source.name) if r.source else None)
            written += 1
        await state.mark(done)
    if written:
        log.info("mind 가 보낸 기록 %d개를 넣었습니다", written)
    return {"written": written}


@app.get("/topics", dependencies=[Depends(reader)])
async def topics(group: str = Query(pattern=GROUP)):
    return {"topics": await graph.topics(group)}


@app.post("/topics", dependencies=[Depends(auth)])
async def build_topics(group: str = Query(pattern=GROUP)):
    """Rebuild now rather than at the weekly run."""
    return {"topics": await graph.communities(group)}


@app.get("/status", dependencies=[Depends(auth)])
async def status():
    s = await state.status()
    s.update({"running": running["now"], "llm": HAS_KEY, "model": MODEL, "dailyUsd": DAILY_USD, "spentToday": round(await state.spent_today(), 4)})
    # The search-side key: shown once there is a key or anything it has used.
    voyage = await state.voyage_usage()
    s["voyage"] = {**voyage, "enabled": VOYAGE_KEY} if VOYAGE_KEY or voyage["month"]["calls"] else None
    return s


@app.post("/run", dependencies=[Depends(auth)])
async def run_now():
    wake.set()
    return {"ok": True}
