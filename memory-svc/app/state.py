"""Where collection stands, in jarvis' Postgres: each source's cursor, the keys
already ingested, and how much the extraction LLM was used today."""

import json
import os
from datetime import date

import psycopg

from .voyage import FREE_TOKENS, free_tokens, price

# Claude Haiku 5.5, prompts up to 100K tokens: $0.10 / $0.50 per MTok.
USD_PER_INPUT = 0.10 / 1_000_000
USD_PER_OUTPUT = 0.50 / 1_000_000


class State:
    def __init__(self):
        self.dsn = os.environ["DATABASE_URL"]
        self.conn: psycopg.AsyncConnection | None = None

    async def open(self):
        self.conn = await psycopg.AsyncConnection.connect(self.dsn, autocommit=True)

    async def cursor(self, source: str) -> dict:
        row = await (await self.conn.execute("select cursor from memory_state where source = %s", (source,))).fetchone()
        return row[0] if row else {}

    async def save(self, source: str, cursor: dict, added: int = 0, error: str = ""):
        await self.conn.execute(
            """insert into memory_state (source, cursor, last_run, last_error, items)
               values (%s, %s, now(), %s, %s)
               on conflict (source) do update set cursor = excluded.cursor, last_run = now(),
                 last_error = excluded.last_error, items = memory_state.items + excluded.items""",
            (source, json.dumps(cursor), error, added),
        )

    async def fail(self, source: str, error: str):
        await self.conn.execute(
            """insert into memory_state (source, last_run, last_error) values (%s, now(), %s)
               on conflict (source) do update set last_run = now(), last_error = excluded.last_error""",
            (source, error[:500]),
        )

    async def done(self, key: str) -> bool:
        row = await (await self.conn.execute("select 1 from memory_done where key = %s", (key,))).fetchone()
        return row is not None

    async def mark(self, key: str):
        await self.conn.execute("insert into memory_done (key) values (%s) on conflict do nothing", (key,))

    async def add_usage(self, input_tokens: int, output_tokens: int):
        await self.conn.execute(
            """insert into memory_usage (day, input_tokens, output_tokens, calls) values (current_date, %s, %s, 1)
               on conflict (day) do update set input_tokens = memory_usage.input_tokens + excluded.input_tokens,
                 output_tokens = memory_usage.output_tokens + excluded.output_tokens, calls = memory_usage.calls + 1""",
            (input_tokens, output_tokens),
        )

    async def fact_result(self, ref: str) -> dict | None:
        """What Graphiti made of a fact already put in (migration 037)."""
        row = await (await self.conn.execute("select result from memory_facts where ref = %s", (ref,))).fetchone()
        return row[0] if row else None

    async def save_fact(self, ref: str, group: str, result: dict):
        await self.conn.execute(
            "insert into memory_facts (ref, grp, result) values (%s, %s, %s) on conflict (ref) do nothing",
            (ref, group, json.dumps(result, ensure_ascii=False)),
        )

    async def add_voyage_usage(self, model: str, tokens: int):
        await self.conn.execute(
            """insert into voyage_usage (day, model, tokens, calls) values (current_date, %s, %s, 1)
               on conflict (day, model) do update set tokens = voyage_usage.tokens + excluded.tokens,
                 calls = voyage_usage.calls + 1""",
            (model, tokens),
        )

    async def voyage_usage(self) -> dict:
        """What the Voyage key has cost at list price - today, this month, the
        last week - and how far into its free tokens the most used model is.
        Atlas bills only past a model's free tokens, so `billedUsd` is this
        month's tokens beyond them, counting every day since the first."""
        rows = await (
            await self.conn.execute(
                """select day, model, tokens, calls, day = current_date, day >= date_trunc('month', current_date),
                          day > current_date - 7
                   from voyage_usage order by day, model"""
            )
        ).fetchall()
        used: dict[str, int] = {}
        today = {"usd": 0.0, "tokens": 0}
        month = {"usd": 0.0, "tokens": 0, "calls": 0}
        models: dict[str, dict] = {}
        days: dict[str, dict] = {}
        billed = 0.0
        for day, model, tokens, calls, is_today, in_month, in_week in rows:
            usd = tokens * price(model)
            before = used.get(model, 0)
            used[model] = before + tokens
            beyond = max(0, used[model] - max(before, free_tokens(model)))
            if is_today:
                today["usd"] += usd
                today["tokens"] += tokens
            if in_month:
                month["usd"] += usd
                month["tokens"] += tokens
                month["calls"] += calls
                billed += beyond * price(model)
                m = models.setdefault(model, {"model": model, "tokens": 0, "usd": 0.0})
                m["tokens"] += tokens
                m["usd"] += usd
            if in_week:
                key = day.isoformat()
                d = days.setdefault(key, {"day": key, "usd": 0.0, "tokens": 0, "calls": 0})
                d["usd"] += usd
                d["tokens"] += tokens
                d["calls"] += calls
        # The gauge follows models that have free tokens; one without them is
        # billed from its first token and shows up in billedUsd instead.
        top = max((m for m in used if free_tokens(m)), key=lambda m: used[m], default=None)
        return {
            "todayUsd": round(today["usd"], 6),
            "todayTokens": today["tokens"],
            "month": {**month, "usd": round(month["usd"], 6)},
            "billedUsd": round(billed, 6),
            "free": {
                "model": top,
                "tokens": FREE_TOKENS,
                "usedPct": 0.0 if top is None else min(100.0, round(100 * used[top] / FREE_TOKENS, 4)),
            },
            "models": sorted(({**m, "usd": round(m["usd"], 6)} for m in models.values()), key=lambda m: -m["tokens"]),
            "days": [{**d, "usd": round(d["usd"], 6)} for d in days.values()],
        }

    async def spent_today(self) -> float:
        row = await (
            await self.conn.execute(
                "select input_tokens, output_tokens from memory_usage where day = current_date"
            )
        ).fetchone()
        return 0.0 if not row else row[0] * USD_PER_INPUT + row[1] * USD_PER_OUTPUT

    async def status(self) -> dict:
        rows = await (
            await self.conn.execute("select source, last_run, last_error, items from memory_state order by source")
        ).fetchall()
        usage = await (
            await self.conn.execute(
                "select day, input_tokens, output_tokens, calls from memory_usage order by day desc limit 7"
            )
        ).fetchall()
        month = await (
            await self.conn.execute(
                """select coalesce(sum(input_tokens), 0)::bigint, coalesce(sum(output_tokens), 0)::bigint, coalesce(sum(calls), 0)::bigint
                   from memory_usage where day >= date_trunc('month', current_date)"""
            )
        ).fetchone()
        return {
            "month": {
                "inputTokens": month[0],
                "outputTokens": month[1],
                "calls": month[2],
                "usd": round(month[0] * USD_PER_INPUT + month[1] * USD_PER_OUTPUT, 4),
            },
            "sources": [
                {"source": r[0], "lastRun": r[1].isoformat() if r[1] else None, "lastError": r[2], "items": r[3]}
                for r in rows
            ],
            "usage": [
                {
                    "day": d.isoformat() if isinstance(d, date) else str(d),
                    "inputTokens": i,
                    "outputTokens": o,
                    "calls": c,
                    "usd": round(i * USD_PER_INPUT + o * USD_PER_OUTPUT, 4),
                }
                for d, i, o, c in usage
            ],
        }
