import assert from "node:assert/strict";
import { test } from "node:test";
import { allocationOf } from "../src/target.js";
import { depositPlan, rebalancePlan } from "../src/trade-plan.js";
import type { Holding } from "../src/types.js";

const UNIT = 100_000;

const stock = (symbol: string, valueKrw: number): Holding => ({
  id: `kis:${symbol}`, venue: "kis", kind: "stock", symbol, name: symbol, quantity: valueKrw / UNIT,
  avgPrice: UNIT, price: UNIT, currency: "KRW", valueKrw, costKrw: valueKrw,
});

const coin = (valueKrw: number): Holding => ({
  id: "upbit:BTC", venue: "upbit", kind: "coin", symbol: "BTC", name: "BTC", quantity: 0.1,
  avgPrice: valueKrw * 10, price: valueKrw * 10, currency: "KRW", valueKrw, costKrw: valueKrw,
});

/** Shares of ten million won: 유망주, 코인, 현금, 배당, 금. */
function pot(growth: number, btc: number, cash: number, dividend: number, gold: number) {
  const of = (p: number) => p * 100_000;
  const holdings = [stock("NVDA", of(growth)), coin(of(btc)), stock("KO", of(dividend)), stock("GLD", of(gold))];
  return { holdings, allocation: allocationOf(holdings, of(cash)) };
}

const row = (a: ReturnType<typeof pot>["allocation"], id: string) => a.rows.find((r) => r.id === id)!;

test("목표보다 적은 현금은 벗어난 것도 부족한 것도 아니다", () => {
  const { allocation } = pot(40.7, 33.9, 4.8, 14.3, 6.3);
  const cash = row(allocation, "cash");
  assert.equal(cash.spent, true);
  assert.equal(cash.inBand, true);
  assert.equal(cash.gapKrw, 0);
  assert.equal(row(allocation, "dividend").inBand, false);
  assert.ok(allocation.moves.every((m) => m.to !== "cash"), JSON.stringify(allocation.moves));
});

test("범위 안에서 조금 모자란 현금도 계산에서 뺀다", () => {
  const { allocation } = pot(45, 30, 10, 10, 5);
  const cash = row(allocation, "cash");
  assert.equal(cash.spent, true);
  assert.equal(cash.gapKrw, 0);
});

test("목표 이상인 현금은 그대로 계산한다", () => {
  const { allocation } = pot(36, 26, 25, 8, 5);
  const cash = row(allocation, "cash");
  assert.equal(cash.spent, false);
  assert.equal(cash.inBand, false);
  assert.ok(cash.gapKrw < 0);
  assert.ok(allocation.moves.some((m) => m.from === "cash" && !m.optional), JSON.stringify(allocation.moves));
});

test("현금을 채우려고 파는 것이 없고, 범위를 벗어난 쪽이 먼저 옮긴다", () => {
  const { allocation, holdings } = pot(40.7, 33.9, 4.8, 14.3, 6.3);
  assert.deepEqual(
    allocation.moves.map((m) => [m.from, m.to, Math.round(m.amountKrw), m.optional]),
    [["dividend", "growth", 430_000, false]],
  );
  const plan = rebalancePlan(allocation, holdings);
  const sold = plan.orders.filter((o) => o.side === "sell").map((o) => o.bucket);
  assert.deepEqual(sold, ["dividend"], "범위 안의 코인·금은 팔지 않는다");
  assert.ok(Math.abs(plan.cashChangeKrw) <= UNIT, `현금은 거의 그대로: ${plan.cashChangeKrw}`);
  const after = plan.buckets.find((b) => b.id === "dividend")!;
  assert.ok(Math.abs(after.after - after.target) <= 0.04, `배당은 범위 안으로: ${after.after}`);
});

test("옮겨도 범위를 벗어나 있으면 목표까지 팔고 남은 돈은 현금이 된다", () => {
  const { allocation, holdings } = pot(44, 40, 4, 7, 5);
  const plan = rebalancePlan(allocation, holdings);
  const btc = plan.orders.find((o) => o.bucket === "coin")!;
  assert.equal(btc.side, "sell");
  assert.ok(Math.abs(btc.amountKrw - 1_000_000) < 2_000, `코인은 목표까지: ${btc.amountKrw}`);
  assert.ok(plan.cashChangeKrw > 700_000, `남은 돈은 현금: ${plan.cashChangeKrw}`);
});

test("현금만 모자라면 리밸런싱할 것이 없다", () => {
  const { allocation, holdings } = pot(49, 33, 4, 9, 5);
  assert.ok(allocation.rows.every((r) => r.inBand));
  assert.equal(rebalancePlan(allocation, holdings).orders.length, 0);
});

test("현금이 넉넉할 때의 리밸런싱은 그대로 모든 자산을 목표로 옮긴다", () => {
  const { allocation, holdings } = pot(30, 30, 27, 8, 5);
  const plan = rebalancePlan(allocation, holdings);
  const growth = plan.orders.find((o) => o.bucket === "growth")!;
  assert.equal(growth.side, "buy");
  assert.equal(growth.amountKrw, 1_500_000);
  assert.equal(plan.orders.length, 1);
});

test("새 돈은 현금 몫을 남기지 않고 모자란 자산을 산다", () => {
  const { allocation, holdings } = pot(36, 26, 4, 30, 4);
  const plan = depositPlan(allocation, holdings, 1_000_000);
  const bought = plan.orders.reduce((sum, o) => sum + o.amountKrw, 0);
  assert.equal(bought, 1_000_000);
  assert.equal(plan.cashChangeKrw, 0);
});
