import assert from "node:assert/strict";
import { test } from "node:test";

import {
  krwMarketsOf,
  upbitFillsOf,
  upbitFlowsOf,
  upbitHoldingsOf,
  upbitOrderAnswerOf,
  upbitPriceOf,
  usdtRateOf,
  walkUpbitCandles,
  walkUpbitTransfers,
  type UpbitAccount,
  type UpbitCandle,
  type UpbitOrder,
  type UpbitTransfer,
} from "../src/venues.js";

// Upbit's answers in the shapes it sends them - amounts as strings, prices as
// numbers, times in Seoul's offset - with made-up values.

/** /v1/market/all: won markets beside markets priced in BTC and USDT. */
const marketAll = [
  { market: "KRW-BTC", korean_name: "비트코인", english_name: "Bitcoin" },
  { market: "KRW-USDT", korean_name: "테더", english_name: "Tether" },
  { market: "BTC-XYZ", korean_name: "엑스와이지", english_name: "XYZ" },
  { market: "USDT-ETH", korean_name: "이더리움", english_name: "Ethereum" },
  { market: "KRW-ETH", korean_name: "이더리움", english_name: "Ethereum" },
];

/** /v1/accounts. */
const accounts: UpbitAccount[] = [
  { currency: "KRW", balance: "1250.5", locked: "0", avg_buy_price: "0", unit_currency: "KRW" },
  // Part of it on order: what is locked is still held.
  { currency: "BTC", balance: "0.125", locked: "0.0625", avg_buy_price: "96000000", unit_currency: "KRW" },
  { currency: "USDT", balance: "100.5", locked: "0", avg_buy_price: "1392", unit_currency: "KRW" },
  // Sold out: still listed, with nothing left.
  { currency: "ETH", balance: "0", locked: "0", avg_buy_price: "5100000", unit_currency: "KRW" },
  // Airdrops with no won market: one worth something at cost, one dust.
  { currency: "XYZ", balance: "2", locked: "0", avg_buy_price: "75", unit_currency: "KRW" },
  { currency: "DUST", balance: "1000", locked: "0", avg_buy_price: "0", unit_currency: "KRW" },
];

/** /v1/ticker?markets=KRW-BTC,KRW-USDT - only the markets that exist are asked. */
const tickers = [
  { market: "KRW-BTC", trade_price: 100_000_000 },
  { market: "KRW-USDT", trade_price: 1400 },
];

const coin = (symbol: string, quantity: number, avgPrice: number, price: number) => ({
  id: `upbit:${symbol}`,
  venue: "upbit",
  kind: "coin",
  symbol,
  name: symbol,
  quantity,
  avgPrice,
  price,
  currency: "KRW",
  valueKrw: quantity * price,
  costKrw: quantity * avgPrice,
});

test("only Upbit's won markets are priced in won", () => {
  assert.deepEqual([...krwMarketsOf(marketAll)], ["KRW-BTC", "KRW-USDT", "KRW-ETH"]);
});

test("Upbit's balances are coins at the ticker's price, dollar coins as dollars, and the won account always", () => {
  assert.deepEqual(upbitHoldingsOf(accounts, krwMarketsOf(marketAll), tickers), {
    holdings: [
      coin("BTC", 0.1875, 96_000_000, 100_000_000),
      // No won market: at its average, so the total adds up, and named below.
      coin("XYZ", 2, 75, 75),
      coin("DUST", 1000, 0, 0),
    ],
    cashKrw: 141_950.5,
    cash: [
      { id: "upbit:KRW", venue: "upbit", currency: "KRW", label: "업비트 원화", amount: 1250.5, valueKrw: 1250.5 },
      { id: "upbit:USDT", venue: "upbit", currency: "USD", label: "업비트 USDT", amount: 100.5, valueKrw: 140_700 },
    ],
    dollarCostKrw: 139_896,
    // Dust worth under ₩100 at cost is not worth naming.
    problems: ["업비트에 원화 마켓이 없어 시세를 못 구한 종목: XYZ"],
  });
});

test("a coin the ticker did not price is at its average, a dollar coin too", () => {
  const read = upbitHoldingsOf(accounts, krwMarketsOf(marketAll), []);
  assert.deepEqual(read.holdings[0], coin("BTC", 0.1875, 96_000_000, 96_000_000));
  assert.deepEqual(read.cash[1], { id: "upbit:USDT", venue: "upbit", currency: "USD", label: "업비트 USDT", amount: 100.5, valueKrw: 139_896 });
  assert.equal(read.cashKrw, 141_146.5);
  // The ticker failing is said where it was asked (upbitHoldings); only the unlisted coin is named here.
  assert.deepEqual(read.problems, ["업비트에 원화 마켓이 없어 시세를 못 구한 종목: XYZ"]);
});

test("without the market list no coin has a price, and each worth ₩100 at cost is named", () => {
  // No market is known to exist, so no ticker is asked (upbitHoldings).
  const read = upbitHoldingsOf(accounts, new Set(), []);
  assert.deepEqual(read.holdings.map((h) => [h.symbol, h.price]), [["BTC", 96_000_000], ["XYZ", 75], ["DUST", 0]]);
  assert.equal(read.cash[1]?.valueKrw, 139_896);
  assert.deepEqual(read.problems, ["업비트에 원화 마켓이 없어 시세를 못 구한 종목: BTC, USDT, XYZ"]);
});

test("a coin all on order is still held", () => {
  const onOrder: UpbitAccount = { currency: "ETH", balance: "0", locked: "0.5", avg_buy_price: "5000000", unit_currency: "KRW" };
  const read = upbitHoldingsOf([onOrder], krwMarketsOf(marketAll), [{ market: "KRW-ETH", trade_price: 5_200_000 }]);
  assert.deepEqual(read.holdings, [coin("ETH", 0.5, 5_000_000, 5_200_000)]);
  assert.equal(read.holdings[0]?.valueKrw, 2_600_000);
});

test("an empty wallet is the won account at zero", () => {
  const empty = {
    holdings: [],
    cashKrw: 0,
    cash: [{ id: "upbit:KRW", venue: "upbit", currency: "KRW", label: "업비트 원화", amount: 0, valueKrw: 0 }],
    dollarCostKrw: 0,
    problems: [],
  };
  assert.deepEqual(upbitHoldingsOf([], new Set(), []), empty);
  assert.deepEqual(
    upbitHoldingsOf([{ currency: "KRW", balance: "0", locked: "0", avg_buy_price: "0", unit_currency: "KRW" }], new Set(), []),
    empty,
  );
});

/** /v1/deposits and /v1/withdraws, newest first. */
const deposits: UpbitTransfer[] = [
  // Upbit's own interest on idle won: a return, not principal.
  { uuid: "dep-5", currency: "KRW", state: "ACCEPTED", amount: "37", fee: "0", transaction_type: "internal", created_at: "2026-10-01T00:05:12+09:00" },
  // A coin with no close that day cannot be valued.
  { uuid: "dep-4", currency: "XRP", state: "ACCEPTED", amount: "40", fee: "0", transaction_type: "default", created_at: "2026-09-30T08:00:00+09:00" },
  { uuid: "dep-3", currency: "BTC", state: "ACCEPTED", amount: "0.25", fee: "0", transaction_type: "default", created_at: "2026-09-20T14:00:00+09:00" },
  // Not arrived yet.
  { uuid: "dep-2", currency: "KRW", state: "PROCESSING", amount: "200000", fee: "0", transaction_type: "default", created_at: "2026-09-10T09:00:00+09:00" },
  // Worth less than a won.
  { uuid: "dep-1", currency: "BTC", state: "ACCEPTED", amount: "0.00000001", fee: "0", transaction_type: "default", created_at: "2026-09-05T11:00:00+09:00" },
  { uuid: "dep-0", currency: "KRW", state: "ACCEPTED", amount: "500000", fee: "0", transaction_type: "default", created_at: "2026-09-02T00:10:00+09:00" },
];
const withdraws: UpbitTransfer[] = [
  { uuid: "wd-2", currency: "BTC", state: "DONE", amount: "0.0625", fee: "0.0005", transaction_type: "default", created_at: "2026-09-28T21:00:00+09:00" },
  { uuid: "wd-1", currency: "KRW", state: "CANCELLED", amount: "200000", fee: "1000", transaction_type: "default", created_at: "2026-09-26T10:00:00+09:00" },
  { uuid: "wd-0", currency: "KRW", state: "DONE", amount: "300000", fee: "1000", transaction_type: "default", created_at: "2026-09-25T09:30:00+09:00" },
];
const btcCloses = new Map([
  ["2026-09-05", 80_000_000],
  ["2026-09-20", 80_000_000],
  ["2026-09-28", 88_000_000],
]);

test("Upbit's transfers are principal in won: coins at the day's close, the withdrawal fee and the interest named apart", () => {
  assert.deepEqual(upbitFlowsOf(deposits, withdraws, new Map([["BTC", btcCloses]])), {
    flows: [
      { id: "upbit:dep-3", date: "2026-09-20", venue: "upbit", amountKrw: 20_000_000, memo: "BTC 입금 0.25개", source: "auto" },
      { id: "upbit:dep-0", date: "2026-09-02", venue: "upbit", amountKrw: 500_000, memo: "원화 입금", source: "auto" },
      { id: "upbit:wd-2", date: "2026-09-28", venue: "upbit", amountKrw: -5_500_000, memo: "BTC 출금 0.0625개", source: "auto" },
      { id: "upbit:wd-0", date: "2026-09-25", venue: "upbit", amountKrw: -300_000, memo: "원화 출금", source: "auto" },
    ],
    // The cancelled withdrawal's fee is not a cost, nor a coin's network fee.
    feesKrw: 1000,
    interestKrw: 37,
    problems: ["업비트: 2026-09-30 XRP 이체를 원화로 환산하지 못해 원금에서 빠졌습니다."],
  });
});

const transfer = (n: number, created_at: string): UpbitTransfer => ({
  uuid: `t-${n}`,
  currency: "KRW",
  state: "ACCEPTED",
  amount: "10000",
  fee: "0",
  transaction_type: "default",
  created_at,
});
const fullPage = (from: number) => Array.from({ length: 100 }, (_, i) => transfer(from + i, "2026-10-20T12:00:00+09:00"));

test("Upbit's transfers are read a hundred a page until a page reaches back past the start, by Seoul's day", async () => {
  const asked: number[] = [];
  const rows = await walkUpbitTransfers(async (n) => {
    asked.push(n);
    if (n === 1) return fullPage(0);
    if (n === 2) {
      return [
        transfer(100, "2026-10-02T09:00:00+09:00"),
        // Just after midnight in Seoul: the first day, though UTC still says the day before.
        transfer(101, "2026-10-01T00:30:00+09:00"),
        transfer(102, "2026-09-30T23:59:00+09:00"),
      ];
    }
    return fullPage(1000);
  }, "2026-10-01");
  assert.deepEqual(asked, [1, 2]);
  assert.equal(rows.length, 102);
  assert.deepEqual(rows.slice(-2).map((r) => r.uuid), ["t-100", "t-101"]);
});

test("a page that is not full is the last, and so is a full one that reaches back past the start", async () => {
  const asked: number[] = [];
  const short = await walkUpbitTransfers(async (n) => {
    asked.push(n);
    return fullPage(0).slice(1);
  }, "2026-10-01");
  assert.deepEqual([asked, short.length], [[1], 99]);

  asked.length = 0;
  const reaching = await walkUpbitTransfers(async (n) => {
    asked.push(n);
    return [...fullPage(0).slice(1), transfer(99, "2026-09-29T12:00:00+09:00")];
  }, "2026-10-01");
  assert.deepEqual([asked, reaching.length], [[1], 99]);

  asked.length = 0;
  const none = await walkUpbitTransfers(async (n) => {
    asked.push(n);
    return [];
  }, "2026-10-01");
  assert.deepEqual([asked, none], [[1], []]);
});

test("Upbit's transfers are read twenty pages at most", async () => {
  let asked = 0;
  const rows = await walkUpbitTransfers(async (n) => {
    asked += 1;
    return fullPage(n * 100);
  }, "2026-10-01");
  assert.equal(asked, 20);
  assert.equal(rows.length, 2000);
});

/** /v1/orders/closed, newest first, as the week windows list them. */
const orders: UpbitOrder[] = [
  // A market sale just after midnight in Seoul (still the day before in UTC).
  { uuid: "ord-4", side: "ask", market: "KRW-BTC", created_at: "2026-10-21T00:30:00+09:00", executed_volume: "0.0625", executed_funds: "6250000", paid_fee: "3125" },
  // A market buy (ord_type price) ends "cancel" - the won it could not spend - and is a fill.
  { uuid: "ord-3", side: "bid", market: "KRW-BTC", created_at: "2026-10-20T23:41:05+09:00", executed_volume: "0.125", executed_funds: "12000000", paid_fee: "6000" },
  // A limit order cancelled with nothing filled.
  { uuid: "ord-2", side: "bid", market: "KRW-ETH", created_at: "2026-10-19T12:00:00+09:00", executed_volume: "0", executed_funds: "0", paid_fee: "0" },
  // Traded against BTC, not won.
  { uuid: "ord-1", side: "bid", market: "BTC-XRP", created_at: "2026-10-18T12:00:00+09:00", executed_volume: "100", executed_funds: "0.0015", paid_fee: "0.00000375" },
  // An answer without executed_funds.
  { uuid: "ord-0", side: "bid", market: "KRW-XRP", created_at: "2026-10-17T09:00:00+09:00", executed_volume: "40", paid_fee: "25" },
  // The market buy again, listed by the next window as well.
  { uuid: "ord-3", side: "bid", market: "KRW-BTC", created_at: "2026-10-20T23:41:05+09:00", executed_volume: "0.125", executed_funds: "12000000", paid_fee: "6000" },
];

test("Upbit's closed orders are fills on won markets, each once, oldest first, on Seoul's day", () => {
  assert.deepEqual(upbitFillsOf(orders), [
    { uuid: "ord-0", date: "2026-10-17", at: "2026-10-17T09:00:00+09:00", symbol: "XRP", side: "bid", volume: 40, fundsKrw: 0, feeKrw: 25 },
    { uuid: "ord-3", date: "2026-10-20", at: "2026-10-20T23:41:05+09:00", symbol: "BTC", side: "bid", volume: 0.125, fundsKrw: 12_000_000, feeKrw: 6000 },
    { uuid: "ord-4", date: "2026-10-21", at: "2026-10-21T00:30:00+09:00", symbol: "BTC", side: "ask", volume: 0.0625, fundsKrw: 6_250_000, feeKrw: 3125 },
  ]);
  assert.deepEqual(upbitFillsOf([]), []);
});

/** /v1/candles/days, newest first: a KST day's candle starts at 09:00 KST. */
const candle = (day: string, trade_price: number): UpbitCandle => ({ candle_date_time_kst: `${day}T09:00:00`, trade_price });

test("day closes are walked back from each page's oldest candle, by the candle's KST day", async () => {
  const asked: [number, string][] = [];
  const closes = await walkUpbitCandles(async (count, to) => {
    asked.push([count, to]);
    if (to === "") return [candle("2026-11-06", 101_000_000), candle("2026-11-05", 100_500_000)];
    if (to === "2026-11-05T09:00:00") return [candle("2026-11-04", 99_000_000)];
    return [];
  }, 3);
  assert.deepEqual(asked, [[3, ""], [1, "2026-11-05T09:00:00"]]);
  assert.deepEqual([...closes], [["2026-11-06", 101_000_000], ["2026-11-05", 100_500_000], ["2026-11-04", 99_000_000]]);
});

test("a coin listed for less than the window has only its own days", async () => {
  const asked: [number, string][] = [];
  const closes = await walkUpbitCandles(async (count, to) => {
    asked.push([count, to]);
    return to ? [] : [candle("2026-11-06", 1500), candle("2026-11-05", 1450)];
  }, 30);
  assert.deepEqual(asked, [[30, ""], [28, "2026-11-05T09:00:00"]]);
  assert.equal(closes.size, 2);
});

test("more than two hundred days are asked two hundred at a time", async () => {
  const asked: number[] = [];
  let day = Date.parse("2026-11-06T00:00:00Z");
  const closes = await walkUpbitCandles(async (count) => {
    asked.push(count);
    return Array.from({ length: count }, () => {
      const c = candle(new Date(day).toISOString().slice(0, 10), 1);
      day -= 86_400_000;
      return c;
    });
  }, 250);
  assert.deepEqual(asked, [200, 50]);
  assert.equal(closes.size, 250);
});

test("the dollar rate is USDT's price, or ₩1,350 when Upbit gives none", () => {
  assert.equal(usdtRateOf([{ market: "KRW-USDT", trade_price: 1400.5 }]), 1400.5);
  assert.equal(usdtRateOf([]), 1350);
  assert.equal(usdtRateOf([{ market: "KRW-USDT", trade_price: 0 }]), 1350);
});

test("a coin's last price is never read as zero", () => {
  assert.equal(upbitPriceOf("KRW-BTC", [{ market: "KRW-BTC", trade_price: 100_000_000 }]), 100_000_000);
  assert.throws(() => upbitPriceOf("KRW-BTC", []), { message: "KRW-BTC 시세를 읽지 못했습니다" });
  assert.throws(() => upbitPriceOf("KRW-BTC", [{ market: "KRW-BTC", trade_price: 0 }]), { message: "KRW-BTC 시세를 읽지 못했습니다" });
});

test("an order Upbit took is its answer; a refusal is Upbit's own words", () => {
  const placed = { uuid: "ord-new", state: "wait", side: "bid", ord_type: "price" };
  assert.deepEqual(upbitOrderAnswerOf({ ok: true, status: 201 }, placed), placed);
  assert.throws(
    () =>
      upbitOrderAnswerOf(
        { ok: false, status: 400 },
        { error: { name: "insufficient_funds_bid", message: "주문가능한 금액(KRW)이 부족합니다." } },
      ),
    { message: "주문가능한 금액(KRW)이 부족합니다." },
  );
  assert.throws(() => upbitOrderAnswerOf({ ok: false, status: 400 }, { error: { name: "under_min_total_bid" } }), {
    message: "under_min_total_bid",
  });
  // Not JSON at all.
  assert.throws(() => upbitOrderAnswerOf({ ok: false, status: 502 }, null), { message: "업비트가 거절했습니다 (502)" });
  // Taken, but with no order id: not an order.
  assert.throws(() => upbitOrderAnswerOf({ ok: true, status: 201 }, {}), { message: "업비트가 거절했습니다 (201)" });
});
