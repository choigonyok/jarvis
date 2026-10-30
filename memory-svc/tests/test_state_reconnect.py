"""The one connection comes back after Postgres restarts."""

import os
import unittest
from unittest import mock

os.environ.setdefault("DATABASE_URL", "postgres://x")

import psycopg

from app.state import State


class FakeConn:
    def __init__(self, fail=False):
        self.closed = False
        self.fail = fail
        self.calls = 0

    async def execute(self, query, params=None):
        self.calls += 1
        if self.fail:
            self.closed = True
            raise psycopg.OperationalError("the connection is closed")
        return "ok"


class Reconnect(unittest.IsolatedAsyncioTestCase):
    async def test_a_lost_connection_is_made_again(self):
        fresh = FakeConn()
        connect = mock.AsyncMock(return_value=fresh)
        state = State()
        state.conn = FakeConn(fail=True)
        with mock.patch.object(psycopg.AsyncConnection, "connect", connect):
            self.assertEqual(await state.execute("select 1"), "ok")
        connect.assert_awaited_once()
        self.assertIs(state.conn, fresh)
        self.assertEqual(fresh.calls, 1)

    async def test_a_closed_connection_is_not_tried(self):
        fresh = FakeConn()
        dead = FakeConn()
        dead.closed = True
        state = State()
        state.conn = dead
        with mock.patch.object(psycopg.AsyncConnection, "connect", mock.AsyncMock(return_value=fresh)):
            await state.execute("select 1")
        self.assertEqual(dead.calls, 0)

    async def test_an_outage_that_lasts_is_still_an_error(self):
        state = State()
        state.conn = FakeConn(fail=True)
        with mock.patch.object(psycopg.AsyncConnection, "connect", mock.AsyncMock(return_value=FakeConn(fail=True))):
            with self.assertRaises(psycopg.OperationalError):
                await state.execute("select 1")


if __name__ == "__main__":
    unittest.main()
