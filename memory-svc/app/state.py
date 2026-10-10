"""Where collection stands, in jarvis' Postgres: each source's cursor, the keys
already ingested, and how much the extraction LLM was used today."""

import json
import os
from datetime import date

import psycopg

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

    async def add_diary(self, group: str, line: str):
        await self.conn.execute("insert into memory_diary (grp, line) values (%s, %s)", (group, line[:500]))

    async def diary(self, group: str, start, end) -> list[tuple[int, str]]:
        """Unused lines written in [start, end), oldest first."""
        rows = await (
            await self.conn.execute(
                "select id, line from memory_diary where grp = %s and used_at is null and at >= %s and at < %s order by at, id",
                (group, start, end),
            )
        ).fetchall()
        return [(r[0], r[1]) for r in rows]

    async def diary_used(self, ids: list[int]):
        if ids:
            await self.conn.execute("update memory_diary set used_at = now() where id = any(%s)", (ids,))

    async def add_usage(self, input_tokens: int, output_tokens: int):
        await self.conn.execute(
            """insert into memory_usage (day, input_tokens, output_tokens, calls) values (current_date, %s, %s, 1)
               on conflict (day) do update set input_tokens = memory_usage.input_tokens + excluded.input_tokens,
                 output_tokens = memory_usage.output_tokens + excluded.output_tokens, calls = memory_usage.calls + 1""",
            (input_tokens, output_tokens),
        )

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
