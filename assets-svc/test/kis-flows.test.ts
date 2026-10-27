import assert from "node:assert/strict";
import { test } from "node:test";

import { dayFlow, exchangeBetween, reconcile } from "../src/kis-flows.js";
import type { Flow } from "../src/types.js";
import type { Settlement } from "../src/venues.js";

// 2026-10-27 23:56 and 2026-10-28 01:04, the share account as kis_cash_daily
// kept it: ₩130,000 put in after midnight (seen at 00:21, before anything
// else), changed into dollars, and two shares of KO bought for $184.53.
const oct27 = { cashKrw: 0, cashUsd: 93.23, settledKrw: 0, settledUsd: -679.07 };
const atDeposit = { cashKrw: 130_000, cashUsd: 93.23, settledKrw: 0, settledUsd: -679.07 };
const after = { cash: { krw: 8, usd: 5.28 }, settled: { krw: 0, usd: -863.6 } };
const rate = 1322.6;

test("an exchange between two looks is told apart, at about KIS's rate", () => {
  const x = exchangeBetween(atDeposit, after.cash, after.settled, rate);
  assert.ok(x);
  assert.equal(Math.round(x.krw), 129_992);
  assert.equal(Math.round(x.usd * 100) / 100, 96.58);
  // Dollars into won, as before taking money out.
  const back = exchangeBetween({ cashKrw: 0, cashUsd: 100, settledKrw: 0, settledUsd: 0 }, { krw: 130_500, usd: 0 }, { krw: 0, usd: 0 }, rate);
  assert.deepEqual(back, { krw: -130_500, usd: -100 });
  // Not an exchange: money put in, a trade, nothing, or a rate far from KIS's.
  assert.equal(exchangeBetween(oct27, { krw: 130_000, usd: 93.23 }, { krw: 0, usd: -679.07 }, rate), null);
  assert.equal(exchangeBetween(atDeposit, { krw: 130_000, usd: 5.28 }, { krw: 0, usd: -767.02 }, rate), null);
  assert.equal(exchangeBetween(atDeposit, { krw: 130_000, usd: 93.23 }, { krw: 0, usd: -679.07 }, rate), null);
  assert.equal(exchangeBetween(atDeposit, { krw: 30_000, usd: 193.23 }, { krw: 0, usd: -679.07 }, rate), null);
});

test("money put in and exchanged the same night is the won put in, not the dollars at the day's rate", () => {
  const exch = exchangeBetween(atDeposit, after.cash, after.settled, rate)!;
  assert.equal(Math.round(dayFlow(oct27, after.cash, after.settled, exch, rate).flow), 130_000);
  // The rate moving later in the day does not move it.
  assert.equal(Math.round(dayFlow(oct27, after.cash, after.settled, exch, 1340).flow), 130_000);
  // Put in and exchanged between two looks it cannot be told apart: the dollars at the day's rate, as before.
  assert.equal(Math.round(dayFlow(oct27, after.cash, after.settled, { krw: 0, usd: 0 }, rate).flow), 127_745);
});

const kis = (date: string, amountKrw: number): Flow => ({ id: `t:${date}:${amountKrw}`, date, venue: "kis", amountKrw, memo: "", source: "auto" });
const trade = (date: string, usd: number): Settlement => ({ date, krw: 0, usd, rate });

test("a purchase dated the day before the deposit that paid for it is not a day short", () => {
  // $93.23 held from a sale; New York's 10/27 is Seoul's 10/28 after midnight, so
  // two KO shares are dated 10/27 and the ₩130,000 found that night is dated 10/28.
  const trades = [trade("2026-10-02", 93.23), trade("2026-10-27", -184.53)];
  const night = reconcile("kis", [kis("2026-10-28", 130_000)], trades, { krw: 8, usd: 5.28 }, rate, "2026-10-28");
  assert.deepEqual(night.checks.filter((c) => c.kind === "short"), []);
  // Two days on, it is a deposit missing or written down late.
  const late = reconcile("kis", [kis("2026-10-29", 130_000)], trades, { krw: 8, usd: 5.28 }, rate, "2026-10-29");
  assert.equal(late.checks.filter((c) => c.kind === "short")[0]?.date, "2026-10-27");
  // Never paid for at all, too.
  const never = reconcile("kis", [], trades, { krw: 8, usd: 5.28 }, rate, "2026-10-28");
  assert.equal(never.checks.filter((c) => c.kind === "short")[0]?.date, "2026-10-27");
});
