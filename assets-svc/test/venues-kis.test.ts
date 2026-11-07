import assert from "node:assert/strict";
import { test } from "node:test";

import {
  goldSettlementsOf,
  kisBuyableOf,
  kisCancelAnswerOf,
  kisForeignCashOf,
  kisGoldAccountOf,
  kisGoldBalanceOf,
  kisHoldingsOf,
  kisLastOf,
  kisOrderAnswerOf,
  kisTradesOf,
  kisUnfilledBuyUsdOf,
  movementsOf,
  walkKisCloses,
  walkKisGoldCloses,
  walkKisPages,
  type KisDailyChart,
  type KisDailyPrice,
  type KisDomesticBalance,
  type KisForeignCash,
  type KisGoldBalance,
  type KisOverseasBalance,
  type KisPage,
  type KisPresentBalance,
  type KisTrans,
} from "../src/venues.js";

// KIS's answers in the shapes it sends them - every number a string, days as
// YYYYMMDD, continuation keys padded with spaces - with made-up values.

/** CTRP6504R: output1 a row per holding with the day's base rate, output2 a row per currency. */
const present: KisPresentBalance = {
  output1: [
    // A row without a rate is passed over for the dollar row with one.
    { buy_crcy_cd: "USD", bass_exrt: "0.00000000" },
    { buy_crcy_cd: "JPY", bass_exrt: "9.45000000" },
    { buy_crcy_cd: "USD", bass_exrt: "1400.50000000" },
  ],
  output2: [
    // Settled $120.25; sold today, not settled (already the account's); bought today, not settled (already spent).
    { crcy_cd: "USD", frcr_dncl_amt_2: "120.25000000", frcr_sll_amt_smtl: "30.50000000", frcr_buy_amt_smtl: "45.75000000" },
    // Yen, with no rate here to count it at.
    { crcy_cd: "JPY", frcr_dncl_amt_2: "1500.00000000", frcr_sll_amt_smtl: "0.00000000", frcr_buy_amt_smtl: "0.00000000" },
    { crcy_cd: "HKD", frcr_dncl_amt_2: "0.00000000", frcr_sll_amt_smtl: "0.00000000", frcr_buy_amt_smtl: "0.00000000" },
  ],
};

test("KIS's dollars are settled plus sold minus bought, with what waiting buys hold; another currency is named", () => {
  assert.deepEqual(kisForeignCashOf(present, 50.5), {
    usd: 155.5,
    bookUsd: 155.5,
    rate: 1400.5,
    problems: ["한국투자증권: 환율을 몰라 빠진 외화 예수금 JPY"],
  });
});

test("dollars bought past the deposit are in the book's dollars, not in the cash shown", () => {
  const over: KisPresentBalance = {
    // No dollar holding to carry a rate.
    output1: [],
    output2: [{ crcy_cd: "USD", frcr_dncl_amt_2: "10.00000000", frcr_sll_amt_smtl: "", frcr_buy_amt_smtl: "50.50000000" }],
  };
  assert.deepEqual(kisForeignCashOf(over, 0), { usd: 0, bookUsd: -40.5, rate: null, problems: [] });
});

test("when the waiting buys cannot be read, the book's dollars are unknown rather than short", () => {
  assert.deepEqual(kisForeignCashOf(present, null), {
    usd: 105,
    bookUsd: null,
    rate: 1400.5,
    problems: ["한국투자증권: 환율을 몰라 빠진 외화 예수금 JPY"],
  });
  assert.deepEqual(kisForeignCashOf({}, 0), { usd: 0, bookUsd: 0, rate: null, problems: [] });
});

test("waiting buys hold their shares left at their limit price; sells and filled orders hold nothing", () => {
  assert.equal(
    kisUnfilledBuyUsdOf({
      rt_cd: "0",
      msg1: "정상처리 되었습니다.",
      output: [
        { sll_buy_dvsn_cd: "02", pdno: "INTC", nccs_qty: "2", ft_ord_unpr3: "45.25000000" },
        { sll_buy_dvsn_cd: "01", pdno: "MSFT", nccs_qty: "1", ft_ord_unpr3: "300.00000000" },
        { sll_buy_dvsn_cd: "02", pdno: "JNJ", nccs_qty: "0", ft_ord_unpr3: "10.00000000" },
        // An answer that names the limit price by another field.
        { sll_buy_dvsn_cd: "02", pdno: "F", nccs_qty: "4", ft_ord_unpr: "12.50000000" },
      ],
    }),
    140.5,
  );
  assert.equal(kisUnfilledBuyUsdOf({ rt_cd: "0", output: [] }), 0);
  assert.throws(() => kisUnfilledBuyUsdOf({ rt_cd: "1", msg1: "  조회 권한이 없습니다.  " }), { message: "조회 권한이 없습니다." });
  assert.throws(() => kisUnfilledBuyUsdOf({}), { message: "미체결 내역을 읽지 못했습니다" });
});

/** TTTC8434R: the domestic side. */
const domestic: KisDomesticBalance = {
  output1: [
    { pdno: "005930", prdt_name: "삼성전자", hldg_qty: "10", pchs_avg_pric: "71250.0000", prpr: "72500" },
    // Sold today: the row stays, with nothing held.
    { pdno: "000660", prdt_name: "SK하이닉스", hldg_qty: "0", pchs_avg_pric: "0.0000", prpr: "180000" },
    // No price: at its average.
    { pdno: "035720", prdt_name: "카카오", hldg_qty: "4", pchs_avg_pric: "40125.5000", prpr: "0" },
  ],
  output2: [{ dnca_tot_amt: "8" }],
};

/** TTTS3012R under NASD, NYSE and AMEX, in the order kisHoldings asks. */
const overseas: KisOverseasBalance[] = [
  {
    output1: [
      { ovrs_pdno: "AAPL", ovrs_item_name: "애플", ovrs_cblc_qty: "3", pchs_avg_pric: "182.2500", now_pric2: "190.500000" },
      // Nothing held under this code; under the next one it is.
      { ovrs_pdno: "JNJ", ovrs_item_name: "존슨앤드존슨", ovrs_cblc_qty: "0", pchs_avg_pric: "0.0000", now_pric2: "0.000000" },
    ],
  },
  {
    output1: [
      // The same holding answered again under another code.
      { ovrs_pdno: "AAPL", ovrs_item_name: "애플", ovrs_cblc_qty: "3", pchs_avg_pric: "182.2500", now_pric2: "190.500000" },
      // No name and no price.
      { ovrs_pdno: "JNJ", ovrs_item_name: "", ovrs_cblc_qty: "2", pchs_avg_pric: "150.5000", now_pric2: "" },
    ],
  },
  // Nothing under AMEX - or the call failed, which reads the same.
  {},
];

test("KIS's balances are shares in won and in dollars at KIS's rate, each holding once, and the cash on both sides", () => {
  assert.deepEqual(kisHoldingsOf({ foreign: kisForeignCashOf(present, 50.5), domestic, overseas }, 1392.5), {
    holdings: [
      { id: "kis:005930", venue: "kis", kind: "stock", symbol: "005930", name: "삼성전자", quantity: 10, avgPrice: 71_250, price: 72_500, currency: "KRW", valueKrw: 725_000, costKrw: 712_500 },
      { id: "kis:035720", venue: "kis", kind: "stock", symbol: "035720", name: "카카오", quantity: 4, avgPrice: 40_125.5, price: 40_125.5, currency: "KRW", valueKrw: 160_502, costKrw: 160_502 },
      { id: "kis:AAPL", venue: "kis", kind: "stock", symbol: "AAPL", name: "애플", quantity: 3, avgPrice: 182.25, price: 190.5, currency: "USD", valueKrw: 800_385.75, costKrw: 765_723.375 },
      { id: "kis:JNJ", venue: "kis", kind: "stock", symbol: "JNJ", name: "JNJ", quantity: 2, avgPrice: 150.5, price: 150.5, currency: "USD", valueKrw: 421_550.5, costKrw: 421_550.5 },
    ],
    cashKrw: 217_785.75,
    cash: [
      { id: "kis:KRW", venue: "kis", currency: "KRW", label: "한국투자 원화", amount: 8, valueKrw: 8 },
      { id: "kis:USD", venue: "kis", currency: "USD", label: "한국투자 달러", amount: 155.5, valueKrw: 217_777.75 },
    ],
    bookCash: { krw: 8, usd: 155.5 },
    usdKrw: 1400.5,
    problems: ["한국투자증권: 환율을 몰라 빠진 외화 예수금 JPY"],
  });
});

test("without KIS's foreign cash the shares still answer, at the fallback rate, and the book's cash is unknown", () => {
  const failed: KisForeignCash = { usd: 0, bookUsd: null, rate: null, problems: ["한국투자증권 외화 예수금: 500 서버 오류"] };
  assert.deepEqual(kisHoldingsOf({ foreign: failed, domestic: {}, overseas: [overseas[0], {}, {}] }, 1392.5), {
    holdings: [
      { id: "kis:AAPL", venue: "kis", kind: "stock", symbol: "AAPL", name: "애플", quantity: 3, avgPrice: 182.25, price: 190.5, currency: "USD", valueKrw: 795_813.75, costKrw: 761_349.375 },
    ],
    cashKrw: 0,
    cash: [],
    bookCash: null,
    usdKrw: 1392.5,
    problems: ["한국투자증권 외화 예수금: 500 서버 오류"],
  });
});

/** CTOS4001R rows, as a walk of its pages hands them back. */
const trans: KisTrans[] = [
  // Sold today: its dollars, but no rate, no fees and no won until it settles.
  { trad_dt: "20261021", sttl_dt: "20261022", sll_buy_dvsn_cd: "01", pdno: "AAPL", ovrs_item_name: "애플", ccld_qty: "1", tr_amt: "0", tr_frcr_amt2: "190.50", wcrc_excc_amt: "0", frcr_excc_amt_1: "0.00", erlm_exrt: "0.0000" },
  // Two identical buys the same day are two fills.
  { trad_dt: "20261014", sttl_dt: "20261015", sll_buy_dvsn_cd: "02", pdno: "AAPL", ovrs_item_name: "애플", ccld_qty: "1", tr_amt: "255150", tr_frcr_amt2: "182.25", wcrc_excc_amt: "255850", frcr_excc_amt_1: "182.75", erlm_exrt: "1400.0000" },
  { trad_dt: "20261014", sttl_dt: "20261015", sll_buy_dvsn_cd: "02", pdno: "AAPL", ovrs_item_name: "애플", ccld_qty: "1", tr_amt: "255150", tr_frcr_amt2: "182.25", wcrc_excc_amt: "255850", frcr_excc_amt_1: "182.75", erlm_exrt: "1400.0000" },
  { trad_dt: "20261016", sttl_dt: "20261017", sll_buy_dvsn_cd: "01", pdno: "JNJ", ovrs_item_name: "", ccld_qty: "2", tr_amt: "421400", tr_frcr_amt2: "301.00", wcrc_excc_amt: "420700", frcr_excc_amt_1: "300.50", erlm_exrt: "1400.0000" },
  // No fill: a row without dates, and an order that filled nothing.
  { trad_dt: "", sttl_dt: "", sll_buy_dvsn_cd: "", pdno: "", ovrs_item_name: "", ccld_qty: "", tr_amt: "", tr_frcr_amt2: "", wcrc_excc_amt: "", frcr_excc_amt_1: "", erlm_exrt: "" },
  { trad_dt: "20261013", sttl_dt: "20261014", sll_buy_dvsn_cd: "02", pdno: "MSFT", ovrs_item_name: "마이크로소프트", ccld_qty: "0", tr_amt: "0", tr_frcr_amt2: "0.00", wcrc_excc_amt: "0", frcr_excc_amt_1: "0.00", erlm_exrt: "0.0000" },
];

test("KIS's overseas fills are oldest first, a sale not settled yet with its dollars and nothing else", () => {
  const bought = {
    date: "2026-10-14",
    settleDate: "2026-10-15",
    side: "buy",
    symbol: "AAPL",
    name: "애플",
    quantity: 1,
    netKrw: 255_850,
    grossKrw: 255_150,
    netUsd: 182.75,
    grossUsd: 182.25,
    rate: 1400,
    settled: true,
  };
  assert.deepEqual(kisTradesOf(trans), [
    bought,
    bought,
    { date: "2026-10-16", settleDate: "2026-10-17", side: "sell", symbol: "JNJ", name: "JNJ", quantity: 2, netKrw: 420_700, grossKrw: 421_400, netUsd: 300.5, grossUsd: 301, rate: 1400, settled: true },
    { date: "2026-10-21", settleDate: "2026-10-22", side: "sell", symbol: "AAPL", name: "애플", quantity: 1, netKrw: 0, grossKrw: 0, netUsd: 0, grossUsd: 190.5, rate: 0, settled: false },
  ]);
});

test("the fills move dollars on their trade day, sales in and purchases out, at the day's first rate", () => {
  const trades = kisTradesOf(trans);
  assert.deepEqual(movementsOf(trades, "2026-10-01"), [
    { date: "2026-10-14", krw: 0, usd: -365.5, rate: 1400 },
    { date: "2026-10-16", krw: 0, usd: 300.5, rate: 1400 },
    // No dollars of its own until priced at the page's rate (priceUnsettled).
    { date: "2026-10-21", krw: 0, usd: 0, rate: 0 },
  ]);
  assert.deepEqual(movementsOf(trades, "2026-10-15").map((m) => m.date), ["2026-10-16", "2026-10-21"]);
});

type Row = { id: string };
const pad = (key: string) => key.padEnd(100);
const pageOf = (rows: Row[], fk: string, nk: string): KisPage<Row> => ({
  rt_cd: "0",
  msg1: "정상처리 되었습니다.",
  output1: rows,
  ctx_area_fk100: pad(fk),
  ctx_area_nk100: pad(nk),
});

test("a KIS listing is walked by the keys each page hands back, trimmed, until the next key is empty", async () => {
  const pages = [pageOf([{ id: "a" }, { id: "a" }], "FK1", "NK1"), pageOf([{ id: "b" }], "FK2", "NK2"), pageOf([{ id: "c" }], "", "")];
  const asked: [string, string, number][] = [];
  const rows = await walkKisPages(async (fk, nk, page) => {
    asked.push([fk, nk, page]);
    return pages[page];
  }, "조회 실패");
  // Two identical rows are two fills: only a whole page is told apart.
  assert.deepEqual(rows, [{ id: "a" }, { id: "a" }, { id: "b" }, { id: "c" }]);
  assert.deepEqual(asked, [["", "", 0], ["FK1", "NK1", 1], ["FK2", "NK2", 2]]);
});

test("a page handed back twice, an empty page, or no key at all ends a KIS listing", async () => {
  let asks = 0;
  const twice = await walkKisPages(async () => {
    asks += 1;
    return pageOf([{ id: "a" }], "FK1", "NK1");
  }, "조회 실패");
  assert.deepEqual([twice, asks], [[{ id: "a" }], 2]);

  const pages = [pageOf([{ id: "a" }], "FK1", "NK1"), pageOf([], "FK2", "NK2"), pageOf([{ id: "z" }], "", "")];
  asks = 0;
  const empty = await walkKisPages(async (_fk, _nk, page) => {
    asks += 1;
    return pages[page];
  }, "조회 실패");
  assert.deepEqual([empty, asks], [[{ id: "a" }], 2]);

  asks = 0;
  const keyless = await walkKisPages<Row>(async () => {
    asks += 1;
    return { rt_cd: "0", output1: [{ id: "a" }] };
  }, "조회 실패");
  assert.deepEqual([keyless, asks], [[{ id: "a" }], 1]);
});

test("a KIS listing is walked twenty pages at most", async () => {
  let asks = 0;
  const rows = await walkKisPages(async () => {
    asks += 1;
    return pageOf([{ id: `r${asks}` }], `FK${asks}`, `NK${asks}`);
  }, "조회 실패");
  assert.equal(asks, 20);
  assert.equal(rows.length, 20);
});

test("a refusal anywhere in a KIS listing is KIS's own message, or the listing's", async () => {
  const refusing = (answer: KisPage<Row>) =>
    walkKisPages<Row>(async (_fk, _nk, page) => (page === 0 ? pageOf([{ id: "a" }], "FK1", "NK1") : answer), "거래내역 조회 실패");
  await assert.rejects(refusing({ rt_cd: "1", msg1: "기간이 만료된 token 입니다.   " }), { message: "기간이 만료된 token 입니다." });
  await assert.rejects(refusing({ rt_cd: "1" }), { message: "거래내역 조회 실패" });
  await assert.rejects(refusing({ rt_cd: "1", msg1: "   " }), { message: "거래내역 조회 실패" });
});

const daily = (xymd: string, clos: string): KisDailyPrice => ({ xymd, clos });

test("a share's closes are found under its own exchange code, then walked back a day past each page's oldest row", async () => {
  const asked: [string, string][] = [];
  const closes = await walkKisCloses(async (excd, before) => {
    asked.push([excd, before]);
    // Listed on NYSE: empty under NAS.
    if (excd !== "NYS") return [];
    if (before === "") return [daily("20261106", "61.2500"), daily("20261105", "60.7500"), daily("", "")];
    if (before === "20261104") return [daily("20261104", "60.5000")];
    return [];
  }, 3);
  assert.deepEqual(asked, [["NAS", ""], ["NYS", ""], ["NYS", "20261104"]]);
  assert.deepEqual([...closes], [["2026-11-06", 61.25], ["2026-11-05", 60.75], ["2026-11-04", 60.5]]);
});

test("walking a share's closes back crosses months and years, and stops where the listing does", async () => {
  const asked: string[] = [];
  const closes = await walkKisCloses(async (_excd, before) => {
    asked.push(before);
    if (before === "") return [daily("20260302", "10.0000"), daily("20260301", "10.2500")];
    if (before === "20260228") return [daily("20260105", "9.5000"), daily("20260101", "9.7500")];
    return [];
  }, 100);
  assert.deepEqual(asked, ["", "20260228", "20251231"]);
  assert.equal(closes.size, 4);
});

test("a share no exchange code quotes has no closes, each code asked once", async () => {
  const asked: string[] = [];
  const closes = await walkKisCloses(async (excd) => {
    asked.push(excd);
    // KIS's blank rows are no closes.
    return [daily("", ""), daily("20261106", "0")];
  }, 30);
  assert.deepEqual(asked, ["NAS", "NYS", "AMS"]);
  assert.equal(closes.size, 0);
});

test("a page that reaches no further back ends the walk, and five pages are the most", async () => {
  const asked: string[] = [];
  const stuck = await walkKisCloses(async (_excd, before) => {
    asked.push(before);
    return [daily("20261106", "61.2500"), daily("20261105", "60.7500")];
  }, 100);
  assert.deepEqual(asked, ["", "20261104"]);
  assert.equal(stuck.size, 2);

  let asks = 0;
  const long = await walkKisCloses(async () => {
    asks += 1;
    return [daily(`202610${String(30 - asks).padStart(2, "0")}`, "1.0000")];
  }, 100);
  assert.equal(asks, 5);
  assert.equal(long.size, 5);
});

const chart = (stck_bsop_date: string, stck_clpr: string): KisDailyChart => ({ stck_bsop_date, stck_clpr });

test("gold's closes are asked 140 days at a time back from today, in Seoul's days", async () => {
  const asked: [string, string][] = [];
  // 01:30 on 11/07 in Seoul, still 11/06 in UTC.
  const now = new Date("2026-11-06T16:30:00Z");
  const closes = await walkKisGoldCloses(async (from, to) => {
    asked.push([from, to]);
    if (to === "20261107") return [chart("20261106", "151230"), chart("20261105", "0"), chart("", "")];
    if (to === "20260619") return [chart("20260618", "140000")];
    return [];
  }, 2, now);
  assert.deepEqual(asked, [["20260620", "20261107"], ["20260130", "20260619"]]);
  assert.deepEqual([...closes], [["2026-11-06", 151_230], ["2026-06-18", 140_000]]);

  asked.length = 0;
  const none = await walkKisGoldCloses(async (from, to) => {
    asked.push([from, to]);
    return [];
  }, 30, now);
  assert.deepEqual([asked.length, none.size], [1, 0]);
});

/** CTRP6548R: the gold account's total, cash and purchase total. */
const goldBalance: KisGoldBalance = {
  rt_cd: "0",
  msg1: "조회가 완료되었습니다.",
  output2: { tot_asst_amt: "3200000", dncl_amt: "200000", pchs_amt_smtl: "2900000" },
};

test("the gold account's gold is its total less its cash, in grams at KRX's price", () => {
  const balance = kisGoldBalanceOf(goldBalance);
  assert.deepEqual(balance, { totalKrw: 3_200_000, cashKrw: 200_000, boughtKrw: 2_900_000 });
  assert.deepEqual(kisGoldAccountOf(balance, { output: { stck_prpr: "150000" } }), {
    holdings: [
      { id: "gold:M04020000", venue: "gold", kind: "gold", symbol: "M04020000", name: "금현물", quantity: 20, avgPrice: 145_000, price: 150_000, currency: "KRW", valueKrw: 3_000_000, costKrw: 2_900_000 },
    ],
    cashKrw: 200_000,
    cash: [{ id: "gold:KRW", venue: "gold", currency: "KRW", label: "금현물 계좌 원화", amount: 200_000, valueKrw: 200_000 }],
    gramKrw: 150_000,
  });
});

test("with no gold held the account still says what a gram costs", () => {
  const balance = kisGoldBalanceOf({ rt_cd: "0", output2: { tot_asst_amt: "0", dncl_amt: "0", pchs_amt_smtl: "0" } });
  assert.deepEqual(kisGoldAccountOf(balance, { output: { stck_prpr: "150000" } }), { holdings: [], cashKrw: 0, cash: [], gramKrw: 150_000 });
  assert.deepEqual(kisGoldBalanceOf({ rt_cd: "0" }), { totalKrw: 0, cashKrw: 0, boughtKrw: 0 });
});

test("without KRX's price gold is counted at its value, and without KIS's purchase total it reads as flat", () => {
  assert.deepEqual(kisGoldAccountOf({ totalKrw: 3_000_000, cashKrw: 0, boughtKrw: 0 }, null), {
    holdings: [
      { id: "gold:M04020000", venue: "gold", kind: "gold", symbol: "M04020000", name: "금현물", quantity: 0, avgPrice: 0, price: 3_000_000, currency: "KRW", valueKrw: 3_000_000, costKrw: 3_000_000 },
    ],
    cashKrw: 0,
    cash: [],
    gramKrw: null,
  });
  assert.equal(kisGoldAccountOf({ totalKrw: 3_000_000, cashKrw: 0, boughtKrw: 0 }, { output: { stck_prpr: "" } }).gramKrw, null);
});

test("a refused gold balance is KIS's own message, or a plain one", () => {
  assert.throws(() => kisGoldBalanceOf({ rt_cd: "1", msg1: "  조회 권한이 없습니다.  " }), { message: "조회 권한이 없습니다." });
  assert.throws(() => kisGoldBalanceOf({ rt_cd: "1" }), { message: "잔고 조회 실패" });
});

test("gold trades are the won each trading day moved: sales in; purchases, fees and tax out", () => {
  assert.deepEqual(
    goldSettlementsOf([
      { trad_dt: "20261015", buy_amt: "1500000", sll_amt: "0", fee: "4500", tl_tax: "0" },
      { trad_dt: "20261003", buy_amt: "0", sll_amt: "450000", fee: "1350", tl_tax: "0" },
      // A second trade the same day is the same day's money.
      { trad_dt: "20261015", buy_amt: "300000", sll_amt: "0", fee: "900", tl_tax: "0" },
      { trad_dt: "", buy_amt: "", sll_amt: "", fee: "", tl_tax: "" },
    ]),
    [
      { date: "2026-10-03", krw: 448_650, usd: 0, rate: 0 },
      { date: "2026-10-15", krw: -1_805_400, usd: 0, rate: 0 },
    ],
  );
  assert.deepEqual(goldSettlementsOf([]), []);
});

test("a US quote's last trade is its price; an empty one, as under the wrong exchange code, is none", () => {
  assert.equal(kisLastOf({ output: { last: "61.2500" } }), 61.25);
  assert.equal(kisLastOf({ output: { last: "" } }), 0);
  assert.equal(kisLastOf({}), 0);
});

test("an order KIS took is its order number and message; a refusal is its message and code", () => {
  assert.deepEqual(
    kisOrderAnswerOf({ rt_cd: "0", msg_cd: "APBK0013", msg1: "주문 전송 완료 되었습니다.", output: { ODNO: "0000012345" } }),
    { odno: "0000012345", message: "주문 전송 완료 되었습니다." },
  );
  assert.throws(() => kisOrderAnswerOf({ rt_cd: "1", msg_cd: "APBK1741", msg1: "주문가능금액이 부족합니다.      " }), {
    message: "주문가능금액이 부족합니다. (APBK1741)",
  });
  assert.throws(() => kisOrderAnswerOf({}), { message: " (?)" });
});

test("a cancel KIS took is its message; a refusal is its message and code", () => {
  assert.equal(kisCancelAnswerOf({ rt_cd: "0", msg_cd: "APBK0013", msg1: " 정정취소 주문이 완료되었습니다. " }), "정정취소 주문이 완료되었습니다.");
  assert.throws(() => kisCancelAnswerOf({ rt_cd: "1", msg_cd: "APBK0918", msg1: "취소할 수량이 없습니다." }), {
    message: "취소할 수량이 없습니다. (APBK0918)",
  });
});

test("what the account can buy is KIS's maximum quantity and dollars, its own answer kept", () => {
  const output = { tr_crcy_cd: "USD", ord_psbl_frcr_amt: "250.25", ovrs_ord_psbl_amt: "250.25", max_ord_psbl_qty: "4", ord_psbl_qty: "4", exrt: "1400.5" };
  assert.deepEqual(kisBuyableOf({ rt_cd: "0", output }), { qty: 4, amount: 250.25, raw: output });
  // An answer that names them by other fields.
  assert.deepEqual(kisBuyableOf({ rt_cd: "0", output: { ovrs_max_ord_psbl_qty: "3", ord_psbl_frcr_amt: "199.5" } }), {
    qty: 3,
    amount: 199.5,
    raw: { ovrs_max_ord_psbl_qty: "3", ord_psbl_frcr_amt: "199.5" },
  });
  assert.throws(() => kisBuyableOf({ rt_cd: "1", msg1: "해외주식 주문가능금액 조회 오류  " }), { message: "해외주식 주문가능금액 조회 오류" });
  assert.throws(() => kisBuyableOf({ rt_cd: "0" }), { message: "매수 가능 금액을 읽지 못했습니다" });
});
