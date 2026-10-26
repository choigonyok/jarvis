import assert from "node:assert/strict";
import { test } from "node:test";

import { flowKey, outflowsOf } from "../src/outflows.js";
import type { Flow, OutflowAnswer } from "../src/types.js";

let seq = 0;
const upbit = (date: string, amountKrw: number): Flow => ({
  id: `upbit:${++seq}`, date, venue: "upbit", amountKrw, memo: amountKrw > 0 ? "원화 입금" : "원화 출금", source: "auto",
});
const kis = (date: string, amountKrw: number, memo = ""): Flow => ({
  id: `manual:${++seq}`, date, venue: "kis", amountKrw, memo, source: "manual",
});
const gold = (date: string, amountKrw: number): Flow => ({
  id: `manual:${++seq}`, date, venue: "gold", amountKrw, memo: "", source: "manual",
});
/** A KIS row found from the cash (kis-flows.ts): stored like a hand row, dated the day it was seen. */
const found = (date: string, amountKrw: number): Flow => ({
  id: `manual:${++seq}`, date, venue: "kis", amountKrw, memo: "", source: "auto",
});
const answer = (key: string, flowKrw: number, a: Partial<OutflowAnswer>): OutflowAnswer => ({
  key, flowKrw, use: "spend", amountKrw: flowKrw, category: null, memo: "", decidedAt: "2026-03-13T03:00:00.000Z", ...a,
});
const at = (iso: string) => Date.parse(iso);

test("money moved between the accounts here is found; what nothing explains is asked about once it is three days old", () => {
  const flows = [
    upbit("2026-03-04", -120_000),
    kis("2026-04-02", -7, "이체출금"),
    kis("2026-04-21", -600_000, "업비트로 이체"),
    kis("2026-04-21", -300_000, "이체"),
    gold("2026-04-21", 90_000),
    kis("2026-04-21", -90_000, "금 계좌로 이체"),
    kis("2026-04-21", -20_000, "이체출금"),
    upbit("2026-04-21", 600_000),
    // Topped up from the bank on the way: still the same money moved.
    upbit("2026-04-21", 310_000),
    upbit("2026-04-27", -55_000),
  ];
  const out = outflowsOf(flows, [], "2026-04-27");
  assert.deepEqual(
    out.map((o) => [o.date, o.amountKrw, o.status, o.askKrw]),
    [
      ["2026-03-04", 120_000, "ask", 120_000],
      ["2026-04-21", 600_000, "transfer", 0],
      ["2026-04-21", 300_000, "transfer", 0],
      ["2026-04-21", 90_000, "transfer", 0],
      ["2026-04-21", 20_000, "ask", 20_000],
      ["2026-04-27", 55_000, "waiting", 55_000],
    ],
    "₩7 is no one moving money; the rest is paired or asked about",
  );
  const moved = out.find((o) => o.amountKrw === 300_000)!;
  assert.equal(moved.parts[0]?.to?.amountKrw, 310_000);
  assert.equal(out.find((o) => o.amountKrw === 90_000)!.parts[0]?.to?.venue, "gold");
});

test("a deposit that is one withdrawal's whole is not used to fill part of a bigger one", () => {
  const out = outflowsOf(
    [upbit("2026-03-09", -900_000), kis("2026-03-09", -250_000), upbit("2026-03-10", 250_000)],
    [],
    "2026-03-20",
  );
  const big = out.find((o) => o.amountKrw === 900_000)!;
  const small = out.find((o) => o.amountKrw === 250_000)!;
  assert.equal(small.status, "transfer");
  assert.equal(big.status, "ask");
  assert.equal(big.askKrw, 900_000);
});

test("a smaller deposit soon after takes its part, and only the rest is asked about", () => {
  const [o] = outflowsOf([upbit("2026-03-09", -900_000), kis("2026-03-10", 650_000)], [], "2026-03-20");
  assert.equal(o!.status, "ask");
  assert.equal(o!.askKrw, 250_000);
  assert.deepEqual(o!.parts.map((p) => [p.use, p.amountKrw]), [["transfer", 650_000]]);
});

test("a deposit long after is not taken for the same money, but offered as what it may have become", () => {
  const [o] = outflowsOf([upbit("2026-03-04", -120_000), upbit("2026-03-11", 175_000)], [], "2026-03-20");
  assert.equal(o!.status, "ask");
  assert.deepEqual(o!.candidates.map((c) => c.amountKrw), [175_000]);
});

test("a found KIS row is dated when it was seen: a deposit days before it can still be its pair", () => {
  const out = found("2026-03-20", -240_000);
  const [o] = outflowsOf([upbit("2026-03-18", 240_000), out], [], "2026-03-30");
  assert.equal(o!.key, "auto:kis:2026-03-20");
  assert.equal(flowKey(out), "auto:kis:2026-03-20");
  assert.equal(o!.status, "transfer");
});

test("what a person said accounts for the withdrawal - if it is still the same withdrawal", () => {
  const flow = upbit("2026-03-09", -120_000);
  const key = flowKey(flow);
  const spent = outflowsOf([flow], [answer(key, 120_000, { category: "생활·통신", memo: "관리비" })], "2026-03-20")[0]!;
  assert.equal(spent.status, "answered");
  assert.deepEqual(spent.parts, [
    { use: "spend", amountKrw: 120_000, category: "생활·통신", memo: "관리비", decidedAt: "2026-03-13T03:00:00.000Z" },
  ]);

  const stale = outflowsOf([flow], [answer(key, 110_000, {})], "2026-03-20")[0]!;
  assert.equal(stale.status, "ask", "an answer about another amount is about a withdrawal no longer there");

  const half = outflowsOf([flow], [answer(key, 120_000, { use: "card", amountKrw: 70_000 })], "2026-03-20")[0]!;
  assert.equal(half.status, "ask");
  assert.equal(half.askKrw, 50_000);
});

test("an answer for the whole of it outweighs a part found by pairing", () => {
  const flow = upbit("2026-03-09", -120_000);
  const o = outflowsOf([flow, kis("2026-03-10", 40_000)], [answer(flowKey(flow), 120_000, { category: "식비" })], "2026-03-20")[0]!;
  assert.equal(o.status, "answered");
  assert.deepEqual(o.parts.map((p) => p.use), ["spend"]);
});

test("parked money is moved when it comes back within thirty days, and asked about again when it does not", () => {
  const flow = upbit("2026-03-09", -120_000);
  const parked = answer(flowKey(flow), 120_000, { use: "parked", decidedAt: "2026-03-13T03:00:00.000Z" });

  const waiting = outflowsOf([flow], [parked], "2026-03-28", at("2026-03-28T03:00:00Z"))[0]!;
  assert.equal(waiting.status, "answered");
  assert.equal(waiting.parts[0]?.use, "parked");

  const back = outflowsOf([flow, kis("2026-04-02", 120_000)], [parked], "2026-04-20", at("2026-04-20T03:00:00Z"))[0]!;
  assert.equal(back.status, "answered");
  assert.equal(back.parts[0]?.use, "transfer");
  assert.equal(back.parts[0]?.to?.date, "2026-04-02");

  const gone = outflowsOf([flow], [parked], "2026-04-20", at("2026-04-20T03:00:00Z"))[0]!;
  assert.equal(gone.status, "ask");
  assert.equal(gone.askKrw, 120_000);
});
