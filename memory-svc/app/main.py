"""memory-svc: jarvis' long-term memory, a Graphiti knowledge graph.

Every MEMORY_INTERVAL it collects what is new across jarvis (sources.py) into
the graph, and mind puts in the facts it has checked (/facts); agents and mind
ask it with /search, /nodes and /topics. The operator's data lives in group
"owner", the guest's conversation in group "guest" - and every call names
exactly one group, so neither can read the other's.
"""

import asyncio
import logging
import os
import re
import secrets
from datetime import datetime, timedelta, timezone

import httpx
from fastapi import Depends, FastAPI, Header, HTTPException, Query
from pydantic import BaseModel, Field

from . import sources
from .graph import EMBED_MODEL, Graph, kst
from .llm import MODEL
from .state import State

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("memory")

TOKEN = os.environ.get("API_TOKEN", "")
INTERVAL = int(os.environ.get("MEMORY_INTERVAL", "300"))
DAILY_USD = float(os.environ.get("MEMORY_DAILY_USD", "1.0"))
HAS_KEY = bool(os.environ.get("ANTHROPIC_API_KEY"))
VOYAGE_KEY = bool(os.environ.get("VOYAGE_API_KEY"))
EVENTS_URL = os.environ.get("EVENTS_URL", "")
# Which collectors run. Once mind reads the conversations, chat, kakao and
# imessage come off this list.
SOURCES = [s.strip() for s in (os.environ.get("MEMORY_SOURCES") or ",".join(sources.ALL)).split(",") if s.strip()]
if unknown := [s for s in SOURCES if s not in sources.ALL]:
    log.warning("MEMORY_SOURCES 에 모르는 수집기가 있어 뺍니다: %s", ", ".join(unknown))
    SOURCES = [s for s in SOURCES if s in sources.ALL]
GROUP = "^(owner|guest)$"

state = State()
graph: Graph | None = None
wake = asyncio.Event()
running = {"now": None}


def auth(authorization: str = Header(default="")):
    if TOKEN and not secrets.compare_digest(authorization, f"Bearer {TOKEN}"):
        raise HTTPException(401, "인증이 필요합니다.")


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


# What in a conversation reads like a plan: worth telling the assistant, which
# decides whether it becomes a suggestion (a calendar entry, say).
# "~기로" covers 하기로, 먹기로, 가기로, 보기로...: how a plan gets settled in
# Korean, whatever the verb. A false hit costs one judging turn; the agent
# decides whether it is worth a card.
PLAN_WORDS = re.compile(r"약속|만나|만날|기로|예약|일정|모임|방문|확정|식사|저녁|점심|브런치")


async def on_extract(group: str, source: str, edges):
    if group != "owner" or "6시간 묶음" in source:
        return
    since = datetime.now(timezone.utc) - timedelta(days=1)
    for e in edges:
        fact = (getattr(e, "fact", "") or "").strip()
        valid_at = getattr(e, "valid_at", None)
        if not fact or not PLAN_WORDS.search(fact):
            continue
        if valid_at is not None and valid_at < since:
            continue  # already past: nothing to put on a calendar
        await emit({
            "source": "memory",
            "type": "memory.plan",
            "subject": fact[:80],
            "key": f"memory.plan:{e.uuid}",
            "data": {"fact": fact, "validAt": kst(valid_at), "source": source},
        })


async def emit(event: dict):
    """Fire and forget: the event log is a nicety, never a reason to stop
    remembering. The outbox first, so a restarting events-svc loses nothing;
    straight to events-svc if that fails."""
    try:
        await state.enqueue_event(event)
        return
    except Exception as e:
        log.warning("이벤트를 outbox 에 쓰지 못해 바로 보냅니다: %s", e)
    if not EVENTS_URL:
        return
    try:
        async with httpx.AsyncClient(timeout=10, headers={"Authorization": f"Bearer {TOKEN}"} if TOKEN else {}) as c:
            r = await c.post(EVENTS_URL.rstrip("/") + "/events", json=event)
            if r.status_code >= 300:
                log.warning("이벤트를 보내지 못했습니다: %s %s", r.status_code, r.text[:200])
    except Exception as e:
        log.warning("이벤트를 보내지 못했습니다: %s", e)


async def lifespan(app: FastAPI):
    global graph
    await state.open()

    def on_usage(i: int, o: int):
        asyncio.get_running_loop().create_task(state.add_usage(i, o))

    graph = Graph(on_usage, state.add_voyage_usage)
    await graph.setup()

    # Each direct fact also becomes a diary line, for the 6-hour episode
    # (sources.diary) - but only news: a backfill or a re-write of something
    # long past would turn the next diary into a history lesson.
    async def on_fact(group: str, fact: str, valid_at):
        if group == "owner" and (valid_at is None or valid_at >= datetime.now(timezone.utc) - timedelta(days=3)):
            await state.add_diary(group, fact)

    graph.on_fact = on_fact
    graph.on_extract = on_extract
    task = asyncio.create_task(loop())
    yield
    task.cancel()


app = FastAPI(lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/search", dependencies=[Depends(auth)])
async def search(
    q: str = Query(min_length=1),
    group: str = Query(pattern=GROUP),
    limit: int = 10,
    center: str | None = Query(None, description="사람·장소 노드 id: 그 주변 두 단계의 사실도 후보로"),
    mode: str = Query("rerank", pattern="^(rerank|mmr|rrf)$"),
):
    return {"facts": await graph.search(q, group, max(1, min(limit, 30)), center=center, mode=mode)}


@app.get("/nodes", dependencies=[Depends(auth)])
async def find(q: str = Query(min_length=1), group: str = Query(pattern=GROUP), limit: int = 5):
    return {"nodes": await graph.find(q, group, max(1, min(limit, 20)))}


@app.get("/nodes/{uid}", dependencies=[Depends(auth)])
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


@app.post("/facts", dependencies=[Depends(auth)])
async def add_facts(body: FactsIn):
    """Facts mind has checked against their source, one add_triplet each. A
    ref seen before is answered from memory_facts without asking Graphiti
    again; one that cannot go in now (the day's LLM budget, an error) comes
    back with `later`, for mind to send again."""
    for f in body.facts:
        if not all(e.id or e.name.strip() or e.self_ for e in (f.source, f.target)):
            raise HTTPException(422, f"{f.ref}: 사실의 양 끝에는 id, 이름, self 중 하나가 있어야 합니다.")
    out = []
    for f in body.facts:
        if (done := await state.fact_result(f.ref)) is not None:
            out.append(done)
            continue
        if not await llm_ok():
            out.append({"ref": f.ref, "later": "오늘 LLM 예산을 다 써서 내일 넣습니다."})
            continue
        try:
            r = await graph.triplet(body.group, f.ref, f.model_dump(by_alias=True))
        except Exception as e:
            log.warning("사실을 넣지 못했습니다 (%s): %s", f.ref, e)
            out.append({"ref": f.ref, "later": f"{type(e).__name__}: {e}"[:300]})
            continue
        await state.save_fact(f.ref, body.group, r)
        out.append(r)
    return {"results": out}


@app.get("/topics", dependencies=[Depends(auth)])
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
