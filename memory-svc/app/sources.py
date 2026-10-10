"""Collectors: what each jarvis service knows, turned into graph episodes
(free text, extracted by the LLM) or facts (structured, written directly).

Every collector is incremental - a cursor in memory_state for streams, a
content hash in memory_done for records that can change (a calendar entry
moved, a job's status) - so a run only touches what is new or different.
"""

import hashlib
import json
import logging
import os
import re
from datetime import date, datetime, timedelta, timezone
from zoneinfo import ZoneInfo

import httpx

from .graph import OWNER, SELF_NAME, Graph
from .state import State

log = logging.getLogger("memory.sources")
KST = ZoneInfo("Asia/Seoul")
TOKEN = os.environ.get("API_TOKEN", "")
BACKFILL_DAYS = int(os.environ.get("MEMORY_BACKFILL_DAYS", "30"))
# Rooms that are notifications, not conversations: card and bank alerts are
# already the household book, and channels are ads.
EXCLUDE_ROOMS = re.compile(os.environ.get("MEMORY_EXCLUDE_ROOMS", r"카드|은행|뱅크|페이|증권|알림|광고|쿠폰|배송|택배|채널"))

URL = {
    "chat": os.environ.get("CHAT_URL", "http://chat:8094"),
    "kakao": os.environ.get("KAKAOTALK_URL", "http://kakaotalk:8090"),
    "imessage": os.environ.get("IMESSAGE_URL", "http://imessage:8099"),
    "calendar": os.environ.get("CALENDAR_URL", "http://calendar:8093"),
    "spending": os.environ.get("SPENDING_URL", "http://spending:8095"),
    "assets": os.environ.get("ASSETS_URL", "http://assets:8092"),
    "workout": os.environ.get("WORKOUT_URL", "http://workout:8091"),
    "jobs": os.environ.get("JOBS_URL", "http://jobs:8098"),
    "immich": os.environ.get("IMMICH_URL", "http://immich:2283"),
}


def http() -> httpx.AsyncClient:
    return httpx.AsyncClient(timeout=180, headers={"Authorization": f"Bearer {TOKEN}"} if TOKEN else {})


def h(*parts) -> str:
    return hashlib.sha256(json.dumps(parts, ensure_ascii=False, sort_keys=True, default=str).encode()).hexdigest()[:24]


def kst_day(d: str) -> datetime:
    return datetime.fromisoformat(d).replace(tzinfo=KST) if len(d) == 10 else datetime.fromisoformat(d)


def won(n) -> str:
    return f"{int(round(n or 0)):,}원"


class Ctx:
    def __init__(self, graph: Graph, state: State, llm_ok):
        self.graph = graph
        self.state = state
        # Callable: is there budget left for LLM extraction today?
        self.llm_ok = llm_ok


# --- free text: conversation, KakaoTalk, iMessage ---------------------------------


async def chat(ctx: Ctx):
    """jarvis conversations, one episode per stretch of talk. The operator's
    thread goes to the owner graph, the guest's to the guest graph."""
    added = 0
    for thread, group in (("", "owner"), ("guest", "guest")):
        if not await ctx.llm_ok():
            break
        source = f"chat:{group}"
        cur = await ctx.state.cursor(source)
        async with http() as c:
            r = await c.get(f"{URL['chat']}/turns", params={"thread": thread})
            r.raise_for_status()
            turns = r.json().get("turns", [])
        after = cur.get("after", "")
        fresh = [t for t in turns if (t.get("atIso") or "") > after and (t.get("text") or t.get("paragraphs"))]
        cutoff = datetime.now(timezone.utc) - timedelta(days=BACKFILL_DAYS)
        fresh = [t for t in fresh if datetime.fromisoformat(t["atIso"]) >= cutoff]
        # A stretch ends at a 30-minute pause or 12 turns; one still going
        # (last turn under 10 minutes ago) waits for the next run.
        chunks, cur_chunk = [], []
        for t in fresh:
            at = datetime.fromisoformat(t["atIso"])
            if cur_chunk and (at - datetime.fromisoformat(cur_chunk[-1]["atIso"]) > timedelta(minutes=30) or len(cur_chunk) >= 12):
                chunks.append(cur_chunk)
                cur_chunk = []
            cur_chunk.append(t)
        if cur_chunk and datetime.now(timezone.utc) - datetime.fromisoformat(cur_chunk[-1]["atIso"]) > timedelta(minutes=10):
            chunks.append(cur_chunk)
        for chunk in chunks:
            if not await ctx.llm_ok():
                break
            lines = []
            for t in chunk:
                who = SELF_NAME[group] if t["role"] == "user" else "Jarvis(비서)"
                text = t.get("text") or "\n".join(t.get("paragraphs") or [])
                lines.append(f"{who}: {text}")
            first = datetime.fromisoformat(chunk[0]["atIso"])
            key = f"chat:{group}:{chunk[0]['id']}"
            if not await ctx.state.done(key):
                await ctx.graph.episode(
                    group, key, f"대화 {first.astimezone(KST):%Y-%m-%d %H:%M}", "\n".join(lines)[:12000],
                    "jarvis 비서와 나눈 대화", first,
                )
                await ctx.state.mark(key)
                added += 1
            after = chunk[-1]["atIso"]
            await ctx.state.save(source, {"after": after}, 1)
        await ctx.state.save(source, {"after": after})
    return added


async def messenger(ctx: Ctx, name: str, label: str):
    """KakaoTalk or iMessage: per room, a stretch of messages (30-minute
    pause or 40 messages) becomes one episode. Rooms that are alerts or ads
    are skipped."""
    added = 0
    cur = await ctx.state.cursor(name)
    rowid = int(cur.get("rowid", 0))
    cutoff = (datetime.now(timezone.utc) - timedelta(days=BACKFILL_DAYS)).timestamp()
    async with http() as c:
        r = await c.get(f"{URL[name]}/messages/after", params={"rowid": rowid, "limit": 2000})
        r.raise_for_status()
        msgs = r.json().get("messages") or []
    if not msgs:
        return 0
    rooms: dict[int, list[dict]] = {}
    for m in msgs:
        if m["sent_at"] < cutoff or not (m.get("text") or "").strip() or EXCLUDE_ROOMS.search(m.get("chat_name") or ""):
            continue
        rooms.setdefault(m["chat_id"], []).append(m)

    pending_min = None
    now_ts = datetime.now(timezone.utc).timestamp()
    for chat_id, ms in rooms.items():
        chunk: list[dict] = []
        chunks = []
        for m in ms:
            if chunk and (m["sent_at"] - chunk[-1]["sent_at"] > 1800 or len(chunk) >= 40):
                chunks.append(chunk)
                chunk = []
            chunk.append(m)
        if chunk:
            if now_ts - chunk[-1]["sent_at"] > 900:
                chunks.append(chunk)
            else:
                # Still being written; come back for it.
                pending_min = min(pending_min or chunk[0]["rowid"], chunk[0]["rowid"])
        for ch in chunks:
            key = f"{name}:{ch[0]['message_id']}"
            if await ctx.state.done(key):
                continue
            if not await ctx.llm_ok():
                pending_min = min(pending_min or ch[0]["rowid"], ch[0]["rowid"])
                break
            room = ch[0].get("chat_name") or "대화방"
            first = datetime.fromtimestamp(ch[0]["sent_at"], tz=timezone.utc)
            lines = [
                f"[{datetime.fromtimestamp(m['sent_at'], tz=KST):%H:%M}] {OWNER if m.get('is_mine') else (m.get('author') or '상대')}: {m['text']}"
                for m in ch
            ]
            await ctx.graph.episode(
                "owner", key, f"{label} {room} {first.astimezone(KST):%Y-%m-%d %H:%M}", "\n".join(lines)[:12000],
                f"{label} '{room}' 대화방", first,
            )
            await ctx.state.mark(key)
            added += 1
    last = msgs[-1]["rowid"]
    next_rowid = (pending_min - 1) if pending_min else last
    await ctx.state.save(name, {"rowid": max(rowid, next_rowid)}, added)
    return added


# --- structured: written directly, no LLM ----------------------------------------


async def calendar(ctx: Ctx):
    today = date.today()
    async with http() as c:
        r = await c.get(f"{URL['calendar']}/events", params={
            "from": (today - timedelta(days=365)).isoformat(), "to": (today + timedelta(days=365)).isoformat()})
        r.raise_for_status()
        events = r.json().get("events") or []
    added = 0
    whose = {"partner": "상대(애인)의 일정", "shared": "함께하는 일정"}
    for e in events:
        if "#" in e["id"]:  # one day of a repeating entry; the series itself is enough
            continue
        when = e["date"] + (f" {e['start']}" + (f"~{e['end']}" if e.get("end") else "") if e.get("start") else " 종일")
        if e.get("endDate"):
            when += f" ~ {e['endDate']}"
        fact = f"{when} {e['title']}" + (f" (장소: {e['place']})" if e.get("place") else "") + \
               (f" - {whose[e['owner']]}" if e.get("owner") in whose else "") + (f". 메모: {e['memo']}" if e.get("memo") else "")
        key = f"calendar:{e['id']}:{h(fact)}"
        if await ctx.state.done(key):
            continue
        await ctx.graph.fact("owner", f"calendar:{e['id']}", "일정", ("Event", e["id"], e["title"]), fact, valid_at=kst_day(e["date"]))
        if e.get("place"):
            await ctx.graph.fact("owner", f"calendar-place:{e['id']}", "장소", ("Place", e["place"], e["place"]),
                                 f"{e['date']} '{e['title']}' 일정의 장소는 {e['place']}", valid_at=kst_day(e["date"]),
                                 source=("Event", e["id"], e["title"]))
        await ctx.state.mark(key)
        added += 1
    await ctx.state.save("calendar", {}, added)
    return added


async def spending(ctx: Ctx):
    added = 0
    first = date.today().replace(day=1) - timedelta(days=BACKFILL_DAYS)
    month = date(first.year, first.month, 1)
    async with http() as c:
        while month <= date.today():
            r = await c.get(f"{URL['spending']}/spending", params={"month": f"{month:%Y-%m}"})
            r.raise_for_status()
            for t in r.json().get("transactions") or []:
                if t.get("excluded"):
                    continue
                at = datetime.fromisoformat(t["approvedAt"])
                verb = "취소" if t.get("kind") == "cancel" else "결제"
                items = ", ".join(f"{i['name']}×{i.get('quantity', 1)}" for i in (t.get("items") or []))
                fact = f"{at.astimezone(KST):%Y-%m-%d %H:%M} {t['merchant']}에서 {won(t['amountKrw'])} {verb} ({t.get('category', '기타')})" + \
                       (f": {items}" if items else "") + (f". 메모: {t['memo']}" if t.get("memo") else "")
                key = f"spending:{t['id']}:{h(fact)}"
                if await ctx.state.done(key):
                    continue
                await ctx.graph.fact("owner", f"spending:{t['id']}", verb, ("Merchant", t["merchant"], t["merchant"]), fact, valid_at=at)
                await ctx.state.mark(key)
                added += 1
            month = (month + timedelta(days=32)).replace(day=1)
    await ctx.state.save("spending", {}, added)
    return added


async def assets(ctx: Ctx):
    """What is held (and since when), and money moved in and out. Prices and
    valuations stay out: they change every minute and the assets tools read
    them live - a copy here would only be a stale number.

    "Since" is the day the position was opened, from the trade history
    (assets-svc works it out); null means held since before the history
    reaches. A holding that is missing is only called sold when every venue
    answered: a venue that failed to load leaves its holdings out of the
    answer, and that once wrote "sold" over a position still held."""
    cur = await ctx.state.cursor("assets")
    held: dict = cur.get("held", {})
    async with http() as c:
        r = await c.get(f"{URL['assets']}/portfolio")
        r.raise_for_status()
        p = r.json()
    added = 0
    today = datetime.now(KST)
    complete = not (p.get("problems") or [])
    seen = {}
    for x in p.get("holdings") or []:
        unit = "주" if x["kind"] == "stock" else ("g" if x["kind"] == "gold" else "")
        qty = round(x["quantity"], 4)
        since = x.get("since")
        seen[x["id"]] = {"name": x["name"], "symbol": x["symbol"]}
        when = f"{since}부터" if since else "기록 시작(5월 1일) 전부터"
        fact = f"{x['name']}({x['symbol']})을 {qty}{unit} 보유 중 ({when})"
        key = f"assets:{x['id']}:{h(qty, since)}"
        if await ctx.state.done(key):
            continue
        await ctx.graph.fact("owner", f"holding:{x['id']}", "보유", ("Asset", x["symbol"], x["name"]), fact,
                             valid_at=kst_day(since) if since else None)
        await ctx.state.mark(key)
        added += 1
    if complete:
        for hid, info in held.items():
            if hid in seen:
                continue
            # Its own edge: the holding's edge keeps what was held, this says it ended.
            await ctx.graph.fact("owner", f"holding-sold:{hid}:{today:%Y-%m-%d}", "처분", ("Asset", info["symbol"], info["name"]),
                                 f"{info['name']}({info['symbol']})을 {today:%Y-%m-%d}에 모두 처분",
                                 valid_at=today)
            added += 1
    else:
        # What this answer left out is unknown, not sold: keep remembering it as held.
        seen = {**held, **seen}
    for f in ((p.get("principal") or {}).get("flows") or []):
        key = f"flow:{f['id']}"
        if await ctx.state.done(key):
            continue
        verb = "입금" if f["amountKrw"] >= 0 else "출금"
        venue = {"upbit": "업비트", "kis": "한국투자증권", "gold": "금현물 계좌"}.get(f["venue"], f["venue"])
        await ctx.graph.fact("owner", key, verb, ("Account", f["venue"], venue),
                             f"{f['date']} {venue}에 {won(abs(f['amountKrw']))} {verb}" + (f" ({f['memo']})" if f.get("memo") else ""),
                             valid_at=kst_day(f["date"]))
        await ctx.state.mark(key)
        added += 1
    for sale in ((p.get("realized") or {}).get("sales") or []):
        key = f"sale:{sale['id']}"
        if await ctx.state.done(key):
            continue
        unit = "주" if sale["kind"] == "stock" else ""
        await ctx.graph.fact("owner", key, "매도", ("Asset", sale["symbol"], sale["name"]),
                             f"{sale['date']} {sale['name']} {round(sale['quantity'], 8)}{unit} 매도, 실현손익 {won(sale['profitKrw'])}",
                             valid_at=kst_day(sale["date"]))
        await ctx.state.mark(key)
        added += 1
    await ctx.state.save("assets", {"held": seen}, added)
    return added


async def workout(ctx: Ctx):
    async with http() as c:
        r = await c.get(f"{URL['workout']}/workout")
        r.raise_for_status()
        book = r.json()
    added = 0
    cutoff = (date.today() - timedelta(days=max(BACKFILL_DAYS, 365))).isoformat()
    for s in book.get("sessions") or []:
        if s.get("date", "") < cutoff:
            continue
        for ex in s.get("exercises") or []:
            # A set with no reps was ticked by mistake or left blank: not a set.
            sets = [x for x in ex.get("sets") or [] if x.get("done") and not x.get("warmup") and (x.get("reps") or 0) > 0]
            if not sets:
                continue
            top = max(sets, key=lambda x: (x.get("weight") or 0, x.get("reps") or 0))
            best = f"{top['weight']}kg×{top['reps']}회" if top.get("weight") else f"맨몸 {top['reps']}회"
            fact = f"{s['date']} {ex['name']} {len(sets)}세트, 최고 {best}"
            key = f"workout:{s['id']}:{ex['id']}:{h(fact)}"
            if await ctx.state.done(key):
                continue
            await ctx.graph.fact("owner", f"workout:{s['id']}:{ex['id']}", "운동", ("Exercise", ex["name"], ex["name"]), fact,
                                 valid_at=kst_day(s["date"]))
            await ctx.state.mark(key)
            added += 1
    await ctx.state.save("workout", {}, added)
    return added


async def jobs(ctx: Ctx):
    async with http() as c:
        r = await c.get(f"{URL['jobs']}/jobs")
        r.raise_for_status()
        js = r.json().get("jobs") or []
    added = 0
    state_word = {"active": "진행 중", "waiting": "승인 대기", "paused": "멈춤", "done": "끝남", "cancelled": "그만둠", "failed": "실패"}
    for j in js:
        fact = f"{j['createdAt'][:10]}에 맡긴 작업 '{j['title']}': {j['goal']} - 지금 {state_word.get(j['state'], j['state'])}" + \
               (f". {j['summary']}" if j.get("summary") else "")
        key = f"jobs:{j['id']}:{h(fact)}"
        if await ctx.state.done(key):
            continue
        await ctx.graph.fact("owner", f"job:{j['id']}", "맡긴 작업", ("Job", str(j["id"]), j["title"]), fact,
                             valid_at=datetime.fromisoformat(j["createdAt"]))
        await ctx.state.mark(key)
        added += 1
    await ctx.state.save("jobs", {}, added)
    return added


async def photos(ctx: Ctx):
    """Immich, a day at a time once the day is over: where photos were taken
    and with whom (named people only)."""
    key_ = os.environ.get("IMMICH_API_KEY", "")
    if not key_:
        return 0
    cur = await ctx.state.cursor("photos")
    start = date.fromisoformat(cur["next"]) if cur.get("next") else date.today() - timedelta(days=BACKFILL_DAYS)
    today = date.today()
    added = 0
    async with httpx.AsyncClient(timeout=120, headers={"x-api-key": key_}) as c:
        day = start
        while day < today:
            items, page = [], 1
            while page:
                r = await c.post(f"{URL['immich']}/api/search/metadata", json={
                    "takenAfter": f"{day}T00:00:00.000Z", "takenBefore": f"{day + timedelta(days=1)}T00:00:00.000Z",
                    "withExif": True, "withPeople": True, "size": 1000, "page": page})
                r.raise_for_status()
                a = r.json().get("assets") or {}
                items += a.get("items") or []
                page = int(a["nextPage"]) if a.get("nextPage") else 0
            places: dict[str, int] = {}
            people: dict[str, int] = {}
            for it in items:
                exif = it.get("exifInfo") or {}
                place = ", ".join(x for x in (exif.get("city"), exif.get("country")) if x)
                if place:
                    places[place] = places.get(place, 0) + 1
                for p in it.get("people") or []:
                    if p.get("name"):
                        people[p["name"]] = people.get(p["name"], 0) + 1
            when = datetime.combine(day, datetime.min.time(), tzinfo=KST)
            if items:
                who = ", ".join(people) if people else ""
                for place, n in places.items():
                    await ctx.graph.fact("owner", f"photo-place:{day}:{place}", "사진 찍은 곳", ("Place", place, place),
                                         f"{day} {place}에서 사진 {n}장" + (f" (함께: {who})" if who else ""), valid_at=when)
                    added += 1
                for name, n in people.items():
                    await ctx.graph.fact("owner", f"photo-person:{day}:{name}", "함께 사진", ("Person", name, name),
                                         f"{day} {name}와(과) 함께 찍은 사진 {n}장" + (f" ({', '.join(places)})" if places else ""), valid_at=when)
                    added += 1
                if not places and not people:
                    await ctx.graph.fact("owner", f"photo-day:{day}", "사진", ("PhotoDay", str(day), f"{day} 사진"),
                                         f"{day}에 사진·영상 {len(items)}개", valid_at=when)
                    added += 1
            day += timedelta(days=1)
            await ctx.state.save("photos", {"next": day.isoformat()}, 0)
    await ctx.state.save("photos", {"next": today.isoformat()}, added)
    return added


async def diary(ctx: Ctx):
    """Every six hours (KST 00, 06, 12, 18), the structured facts written in
    the window just closed go to the LLM as one episode. The facts themselves
    are already in the graph, exact; the episode is for linking - the café a
    payment went to, the person on the calendar - to the same people and
    places the conversations mention, which only LLM extraction can resolve.
    A window waits while the day's LLM budget is spent."""
    cur = await ctx.state.cursor("diary")
    now = datetime.now(KST)
    end = now.replace(hour=now.hour - now.hour % 6, minute=0, second=0, microsecond=0)
    done = datetime.fromisoformat(cur["through"]) if cur.get("through") else end - timedelta(hours=6)
    added = 0
    while done < end:
        start, stop = done, done + timedelta(hours=6)
        lines = await ctx.state.diary("owner", start, stop)
        if lines:
            if not await ctx.llm_ok():
                break
            body = "\n".join(f"- {line}" for _, line in lines)
            await ctx.graph.episode(
                "owner", f"diary:{start:%Y%m%d%H}", f"{start:%m/%d %H}시~{stop:%H}시 기록",
                body, "jarvis 의 일정·지출·자산·운동·작업·사진 기록 6시간 묶음", stop,
            )
            await ctx.state.diary_used([i for i, _ in lines])
            added += 1
        done = stop
        await ctx.state.save("diary", {"through": done.isoformat()}, added)
    return added


ALL = {
    # Structured first: they cost nothing and make the free text's people and
    # places land next to things already known.
    "calendar": calendar,
    "spending": spending,
    "assets": assets,
    "workout": workout,
    "jobs": jobs,
    "photos": photos,
    "chat": chat,
    "kakao": lambda ctx: messenger(ctx, "kakao", "카카오톡"),
    "imessage": lambda ctx: messenger(ctx, "imessage", "iMessage"),
    # Last: it bundles what the structured sources above just wrote.
    "diary": diary,
}
