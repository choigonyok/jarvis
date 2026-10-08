"""memory-svc: jarvis' long-term memory, a Graphiti knowledge graph.

Every MEMORY_INTERVAL it collects what is new across jarvis (sources.py) into
the graph; agents ask it with /search. The operator's data lives in group
"owner", the guest's conversation in group "guest" - and a search names
exactly one group, so neither can read the other's.
"""

import asyncio
import logging
import os
import secrets

from fastapi import Depends, FastAPI, Header, HTTPException, Query

from . import sources
from .graph import Graph
from .llm import MODEL
from .state import State

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("memory")

TOKEN = os.environ.get("API_TOKEN", "")
INTERVAL = int(os.environ.get("MEMORY_INTERVAL", "300"))
DAILY_USD = float(os.environ.get("MEMORY_DAILY_USD", "1.0"))
HAS_KEY = bool(os.environ.get("ANTHROPIC_API_KEY"))

state = State()
graph: Graph | None = None
wake = asyncio.Event()
running = {"now": None}


def auth(authorization: str = Header(default="")):
    if TOKEN and not secrets.compare_digest(authorization, f"Bearer {TOKEN}"):
        raise HTTPException(401, "인증이 필요합니다.")


async def llm_ok() -> bool:
    return HAS_KEY and await state.spent_today() < DAILY_USD


async def loop():
    ctx = sources.Ctx(graph, state, llm_ok)
    while True:
        for name, run in sources.ALL.items():
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

    graph = Graph(on_usage)
    await graph.setup()
    task = asyncio.create_task(loop())
    yield
    task.cancel()


app = FastAPI(lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/search", dependencies=[Depends(auth)])
async def search(q: str = Query(min_length=1), group: str = Query(pattern="^(owner|guest)$"), limit: int = 10):
    return {"facts": await graph.search(q, group, max(1, min(limit, 30)))}


@app.get("/status", dependencies=[Depends(auth)])
async def status():
    s = await state.status()
    s.update({"running": running["now"], "llm": HAS_KEY, "model": MODEL, "dailyUsd": DAILY_USD, "spentToday": round(await state.spent_today(), 4)})
    return s


@app.post("/run", dependencies=[Depends(auth)])
async def run_now():
    wake.set()
    return {"ok": True}
