import assert from "node:assert/strict";
import { test } from "node:test";

import type { OrderRow } from "../src/db.js";
import { type OrderRequest, type Sent, Unchecked, placeOnce, placeOrders } from "../src/orders.js";
import type { Holding } from "../src/types.js";

const btc: OrderRequest = { venue: "upbit", symbol: "BTC", side: "buy", units: null, amountKrw: 50_000 };
const ko: OrderRequest = { venue: "kis", symbol: "KO", side: "sell", units: 2, amountKrw: 190_000 };
const tick = (ms = 0) => new Promise((r) => setTimeout(r, ms));

/** The accounts, counting how often they were read. */
function accounts() {
  const read = { times: 0 };
  const holdings = async (): Promise<Holding[]> => {
    read.times += 1;
    return [];
  };
  return { read, holdings };
}

/** A broker that takes every order - a send after `ms`, a check at once - and what it was given. */
function broker(ms = 0) {
  const given: { planId?: string; dryRun?: boolean }[] = [];
  const place = async (requests: OrderRequest[], _held: Holding[], opts: { reason: string; dryRun?: boolean; planId?: string }): Promise<Sent> => {
    given.push({ planId: opts.planId, dryRun: opts.dryRun });
    if (!opts.dryRun) await tick(ms);
    const status = opts.dryRun ? ("preview" as const) : ("placed" as const);
    return { planId: opts.planId ?? "made-here", results: requests.map((r) => ({ ...r, currency: "KRW" as const, status, message: "" })) };
  };
  return { given, place };
}

/** The record, with nothing of the planId in it, read a moment later. */
const empty = async (): Promise<OrderRow[]> => {
  await tick();
  return [];
};

test("a planId already in the record places nothing: it answers what was placed, as a replay", async () => {
  const b = broker();
  const a = accounts();
  const sold: OrderRow = {
    id: 7, createdAt: "2026-11-15T01:00:00.000Z", venue: "kis", symbol: "KO", side: "sell", units: 2, amountKrw: 190_000,
    limitPrice: 63.1, currency: "USD", reason: "manual", planId: "a1b2c3d4", status: "placed", brokerId: "0030",
    message: "2주 $63.10 지정가 매도",
  };
  const refused: OrderRow = {
    ...sold, id: 8, venue: "upbit", symbol: "BTC", side: "buy", units: null, amountKrw: 50_000, limitPrice: null, currency: "KRW",
    status: "failed", brokerId: null, message: "주문가능한 금액이 부족합니다",
  };
  const out = await placeOnce([ko, btc], a.holdings, { reason: "manual", planId: "a1b2c3d4" }, { place: b.place, recorded: async () => [sold, refused] });
  assert.deepEqual(b.given, [], "nothing goes to the broker");
  assert.equal(a.read.times, 0, "nor are the accounts read for it");
  assert.deepEqual(out, {
    planId: "a1b2c3d4",
    replay: true,
    results: [
      { venue: "kis", symbol: "KO", side: "sell", units: 2, amountKrw: 190_000, status: "placed", message: "2주 $63.10 지정가 매도", currency: "USD", brokerId: "0030", limitPrice: 63.1 },
      { venue: "upbit", symbol: "BTC", side: "buy", units: null, amountKrw: 50_000, status: "failed", message: "주문가능한 금액이 부족합니다", currency: "KRW" },
    ],
  });
});

test("the same send twice at once: one places, the other waits for it and answers the same", async () => {
  const b = broker(20);
  const a = accounts();
  const opts = { reason: "manual", planId: "race0001" };
  const deps = { place: b.place, recorded: empty };
  // Both in while the record is being read.
  const [first, again] = await Promise.all([placeOnce([btc], a.holdings, opts, deps), placeOnce([btc], a.holdings, opts, deps)]);
  assert.equal(b.given.length, 1);
  assert.equal(a.read.times, 1);
  assert.equal(first.replay, false);
  assert.equal(again.replay, true);
  assert.deepEqual(again.results, first.results);

  // One in while the first is at the broker.
  const out = placeOnce([btc], a.holdings, { ...opts, planId: "race0002" }, deps);
  await tick(10);
  const late = await placeOnce([btc], a.holdings, { ...opts, planId: "race0002" }, deps);
  assert.equal((await out).replay, false);
  assert.equal(late.replay, true);
  assert.equal(b.given.length, 2);

  // Done, and the record still without it (no database, or the write failed): the send answers.
  const after = await placeOnce([btc], a.holdings, opts, deps);
  assert.equal(after.replay, true);
  assert.deepEqual(after.results, first.results);
  assert.equal(b.given.length, 2);
});

test("a check is never a send: it waits for none, answers for none, and a send after it goes", async () => {
  const b = broker(30);
  const a = accounts();
  const deps = { place: b.place, recorded: empty };
  const said: string[] = [];
  const sending = placeOnce([btc], a.holdings, { reason: "manual", planId: "dry00001" }, deps).then((x) => (said.push("send"), x));
  const check = await placeOnce([btc], a.holdings, { reason: "manual", planId: "dry00001", dryRun: true }, deps);
  said.push("check");
  const sent = await sending;
  assert.deepEqual(said, ["check", "send"], "the check did not wait for the send out under its planId");
  assert.equal(check.replay, false);
  assert.deepEqual(check.results.map((r) => r.status), ["preview"]);
  assert.equal(sent.replay, false);
  assert.deepEqual(sent.results.map((r) => r.status), ["placed"]);

  await placeOnce([btc], a.holdings, { reason: "manual", planId: "dry00002", dryRun: true }, deps);
  const afterCheck = await placeOnce([btc], a.holdings, { reason: "manual", planId: "dry00002" }, deps);
  assert.equal(afterCheck.replay, false);
  assert.deepEqual(afterCheck.results.map((r) => r.status), ["placed"]);
  // The send reads the record first, so the check reached the broker before it.
  assert.deepEqual(b.given, [
    { planId: "dry00001", dryRun: true },
    { planId: "dry00001", dryRun: undefined },
    { planId: "dry00002", dryRun: true },
    { planId: "dry00002", dryRun: undefined },
  ]);
});

test("a check goes to no broker: each order comes back as it would go, or why it cannot", async () => {
  const coin: Holding = {
    id: "upbit:BTC", venue: "upbit", kind: "coin", symbol: "BTC", name: "비트코인", quantity: 0.01, avgPrice: 90_000_000,
    price: 100_000_000, currency: "KRW", valueKrw: 1_000_000, costKrw: 900_000,
  };
  const out = await placeOrders([btc, { ...btc, side: "sell", amountKrw: 1_200_000 }, ko], [coin], { reason: "manual", dryRun: true });
  assert.deepEqual(
    out.results.map((r) => [r.symbol, r.side, r.status]),
    [["BTC", "sell", "failed"], ["KO", "sell", "failed"], ["BTC", "buy", "preview"]],
  );
});

test("a record that cannot be read sends nothing, and the planId can go once it can", async () => {
  const b = broker();
  const a = accounts();
  const down = async (): Promise<OrderRow[]> => {
    throw new Error("connect ECONNREFUSED 127.0.0.1:5432");
  };
  await assert.rejects(placeOnce([btc], a.holdings, { reason: "manual", planId: "down0001" }, { place: b.place, recorded: down }), Unchecked);
  assert.deepEqual(b.given, []);
  assert.equal(a.read.times, 0);

  const out = await placeOnce([btc], a.holdings, { reason: "manual", planId: "down0001" }, { place: b.place, recorded: empty });
  assert.equal(out.replay, false);
  assert.equal(b.given.length, 1);
});

test("a send that failed before any order went is not held against its planId", async () => {
  const b = broker();
  const unread = async (): Promise<Holding[]> => {
    throw new Error("업비트: 응답 없음");
  };
  const opts = { reason: "manual", planId: "fail0001" };
  const deps = { place: b.place, recorded: empty };
  const both = await Promise.allSettled([placeOnce([btc], unread, opts, deps), placeOnce([btc], unread, opts, deps)]);
  assert.deepEqual(both.map((x) => x.status), ["rejected", "rejected"], "the one that waited answers as the first did");
  assert.deepEqual(b.given, []);

  const out = await placeOnce([btc], accounts().holdings, opts, deps);
  assert.equal(out.replay, false);
  assert.equal(b.given.length, 1);
});

test("without a planId each send is its own", async () => {
  const b = broker();
  const deps = { place: b.place, recorded: empty };
  const [x, y] = await Promise.all([
    placeOnce([btc], accounts().holdings, { reason: "manual" }, deps),
    placeOnce([btc], accounts().holdings, { reason: "manual" }, deps),
  ]);
  assert.equal(b.given.length, 2);
  assert.equal(x.replay || y.replay, false);
});
