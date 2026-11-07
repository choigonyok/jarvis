import { createHash, createHmac, randomUUID } from "node:crypto";
import * as db from "./db.js";
import type { CashLine, Flow, Holding } from "./types.js";
import { problem } from "./accounts.js";

/**
 * Reading the two brokerages.
 *
 * The awkward parts of these two APIs were learned the hard way, and the
 * notes beside the code are the record of it. The keys can place orders,
 * which is the main reason this file is in a service of its own rather than
 * one import away from a browser bundle.
 *
 * Reading is most of it: balances, prices and past trades. The one writing
 * part is at the end - placing an order (upbitOrder, kisUsOrder) - and only
 * orders.ts calls it, after a person approved exactly those orders.
 *
 * Each call asks, then hands what came back to a function that turns it into
 * the service's data (the ...Of functions) or walks its pages (the walk...
 * functions, told how to ask for one). Those touch no network, so
 * test/venues-*.test.ts pins them against answers in the venues' real shapes.
 */

function env(name: string): string {
  return process.env[name]?.trim() ?? "";
}

const KIS_BASE: string =
  env("KIS_MOCK") === "true"
    ? "https://openapivts.koreainvestment.com:29443"
    : "https://openapi.koreainvestment.com:9443";

/**
 * KIS allows each app key a handful of calls a second (20 on a real account)
 * and answers the excess with EGW00201 "초당 거래건수를 초과하였습니다".
 * Everything a portfolio build asks for - balances on three exchanges,
 * foreign cash, a page of closes per share, the gold account - leaving at
 * once, a single tap after the cache expired could trip it. So calls to KIS
 * queue per key, a fixed gap apart, and a rate-limit answer is waited out and
 * asked again instead of becoming an error on screen.
 */
const KIS_GAP_MS = Number(process.env.KIS_GAP_MS ?? 80);
const kisQueues = new Map<string, Promise<void>>();

function kisSlot(key: string): Promise<void> {
  const prev = kisQueues.get(key) ?? Promise.resolve();
  const next = prev.then(() => new Promise<void>((r) => setTimeout(r, KIS_GAP_MS)));
  kisQueues.set(key, next);
  return prev;
}

const isRateLimited = (text: string) => text.includes("EGW00201") || text.includes("초당 거래건수");

// `cache: "no-store"` 가 빠져 있다. 그건 Next.js 가 fetch 를 감싸서 응답을
// 캐시하는 것을 끄기 위한 것이었고, Node 의 fetch 는 애초에 캐시하지 않는다.
// 신선도는 index.ts 와 portfolio.ts 의 캐시가 관리한다.
async function json<T>(url: string, init?: RequestInit): Promise<T> {
  const kis = url.startsWith(KIS_BASE);
  const key = kis ? String((init?.headers as Record<string, string> | undefined)?.appkey ?? "token") : "";
  for (let attempt = 0; ; attempt += 1) {
    if (kis) await kisSlot(key);
    const res = await fetch(url, init);
    if (res.ok) {
      const body = (await res.json()) as T;
      // KIS sometimes reports the limit inside a 200.
      const code = (body as { msg_cd?: string } | null)?.msg_cd;
      if (kis && code === "EGW00201" && attempt < 3) {
        await new Promise((r) => setTimeout(r, 400 * (attempt + 1)));
        continue;
      }
      return body;
    }
    const text = await res.text();
    if (kis && isRateLimited(text) && attempt < 3) {
      await new Promise((r) => setTimeout(r, 400 * (attempt + 1)));
      continue;
    }
    throw new Error(`${res.status} ${text.slice(0, 200)}`);
  }
}

/* ─────────────────────────── Upbit ─────────────────────────── */

/**
 * A request with parameters has to carry a SHA-512 of its query string in the
 * token, hashed exactly as sent - except that Upbit verifies against the
 * decoded form, so `+09:00` must be hashed as `+` and sent as `%2B`.
 */
function upbitToken(query?: string): string {
  const b64 = (value: string | Buffer) =>
    Buffer.from(value).toString("base64url");
  const header = b64(JSON.stringify({ alg: "HS256", typ: "JWT" }));
  const payload = b64(
    JSON.stringify({
      access_key: env("UPBIT_ACCESS_KEY"),
      nonce: randomUUID(),
      ...(query
        ? {
            query_hash: createHash("sha512").update(query).digest("hex"),
            query_hash_alg: "SHA512",
          }
        : {}),
    }),
  );
  const signature = createHmac("sha256", env("UPBIT_SECRET_KEY"))
    .update(`${header}.${payload}`)
    .digest("base64url");
  return `${header}.${payload}.${signature}`;
}

/** One row of Upbit's balances (/v1/accounts). */
export type UpbitAccount = {
  currency: string;
  balance: string;
  locked: string;
  avg_buy_price: string;
  unit_currency: string;
};

/** One row of Upbit's ticker answer (/v1/ticker). */
export type UpbitTicker = { market: string; trade_price: number };

/**
 * Which KRW markets Upbit actually lists.
 *
 * Asking for a market that does not exist makes Upbit reject the *whole*
 * batched ticker request with a 404 - one unlisted airdrop token was enough
 * to leave every real coin priced at its own average, which reads on screen
 * as a confident "0.0%" rather than as missing data. So the batch is built
 * from the listing rather than from what happens to be in the wallet.
 *
 * Cached for the life of the process: listings change on the order of weeks.
 */
let krwMarkets: { value: Set<string>; expires: number } | null = null;

async function upbitKrwMarkets(): Promise<Set<string>> {
  if (krwMarkets && krwMarkets.expires > Date.now()) return krwMarkets.value;
  const all = await json<{ market: string }[]>(
    "https://api.upbit.com/v1/market/all",
  );
  const set = krwMarketsOf(all);
  krwMarkets = { value: set, expires: Date.now() + 6 * 3600_000 };
  return set;
}

/** The won markets in Upbit's listing (/v1/market/all): "KRW-BTC", not "BTC-ETH". */
export function krwMarketsOf(all: { market: string }[]): Set<string> {
  return new Set(
    all.map((m) => m.market).filter((m) => m.startsWith("KRW-")),
  );
}

/** Coins held to be dollars. Shown and allocated as dollar cash. */
export const DOLLAR_COINS = new Set(["USDT", "USDC"]);

/** Upbit's side of the portfolio, as upbitHoldings answers it. */
export type UpbitHoldings = {
  holdings: Holding[];
  cashKrw: number;
  cash: CashLine[];
  /** What the dollar coins held cost (Upbit's average), so their own move can be told apart. */
  dollarCostKrw: number;
  problems: string[];
};

/** The coins in a balance answer with anything held, locked included. */
function heldCoins(accounts: UpbitAccount[]): UpbitAccount[] {
  return accounts.filter(
    (a) => a.currency !== "KRW" && Number(a.balance) + Number(a.locked) > 0,
  );
}

export async function upbitHoldings(): Promise<UpbitHoldings> {
  if (!env("UPBIT_ACCESS_KEY")) throw new Error("업비트 키가 설정되지 않았습니다.");

  const accounts = await json<UpbitAccount[]>("https://api.upbit.com/v1/accounts", {
    headers: { Authorization: `Bearer ${upbitToken()}` },
  });

  const coins = heldCoins(accounts);
  if (coins.length === 0) return upbitHoldingsOf(accounts, new Set(), []);

  const problems: string[] = [];
  const listed = await upbitKrwMarkets().catch(() => {
    problems.push("업비트 마켓 목록을 불러오지 못했습니다.");
    return new Set<string>();
  });

  const quotable = coins.filter((c) => listed.has(`KRW-${c.currency}`));

  let tickers: UpbitTicker[] = [];
  if (quotable.length > 0) {
    // One call for every price rather than one per coin - but only for
    // markets that exist, or the whole call fails.
    const markets = quotable.map((c) => `KRW-${c.currency}`).join(",");
    try {
      tickers = await json<UpbitTicker[]>(
        `https://api.upbit.com/v1/ticker?markets=${encodeURIComponent(markets)}`,
      );
    } catch (e) {
      // Silently falling back to cost would print a confident 0.0% for every
      // coin. Say it instead.
      problems.push(`업비트 시세를 불러오지 못했습니다: ${(e as Error).message}`);
    }
  }

  const read = upbitHoldingsOf(accounts, listed, tickers);
  return { ...read, problems: [...problems, ...read.problems] };
}

/**
 * Upbit's balances as holdings and cash: each coin at its ticker price, and
 * one without a price (no won market in `listed`, or none in `tickers`) at
 * its own average.
 */
export function upbitHoldingsOf(
  accounts: UpbitAccount[],
  listed: Set<string>,
  tickers: UpbitTicker[],
): UpbitHoldings {
  const coins = heldCoins(accounts);
  const cashKrw = Number(
    accounts.find((a) => a.currency === "KRW")?.balance ?? 0,
  );
  const problems: string[] = [];
  // The won account is listed even when it is empty: the 자산 tab names each
  // account's cash, and Upbit's won read as missing beside the others.
  const cash: CashLine[] = [
    { id: "upbit:KRW", venue: "upbit", currency: "KRW", label: "업비트 원화", amount: cashKrw, valueKrw: cashKrw },
  ];

  const unquotable = coins.filter((c) => !listed.has(`KRW-${c.currency}`));
  const priceOf = new Map<string, number>();
  for (const t of tickers) priceOf.set(t.market, t.trade_price);

  const dollars = coins.filter((c) => DOLLAR_COINS.has(c.currency));
  let dollarKrw = 0;
  let dollarCostKrw = 0;
  for (const c of dollars) {
    const qty = Number(c.balance) + Number(c.locked);
    dollarCostKrw += qty * Number(c.avg_buy_price);
    const price = priceOf.get(`KRW-${c.currency}`) ?? Number(c.avg_buy_price);
    dollarKrw += qty * price;
    cash.push({
      id: `upbit:${c.currency}`,
      venue: "upbit",
      currency: "USD",
      label: `업비트 ${c.currency}`,
      amount: qty,
      valueKrw: qty * price,
    });
  }

  const holdings = coins.filter((c) => !DOLLAR_COINS.has(c.currency)).map<Holding>((c) => {
    const qty = Number(c.balance) + Number(c.locked);
    const avg = Number(c.avg_buy_price);
    const price = priceOf.get(`KRW-${c.currency}`) ?? avg;
    return {
      id: `upbit:${c.currency}`,
      venue: "upbit",
      kind: "coin",
      symbol: c.currency,
      name: c.currency,
      quantity: qty,
      avgPrice: avg,
      price,
      currency: "KRW",
      valueKrw: qty * price,
      costKrw: qty * avg,
    };
  });

  // A coin with no KRW market has no price to quote. It is valued at cost so
  // the total still adds up, and named so that is not mistaken for "flat".
  const named = unquotable
    .map((c) => c.currency)
    .filter((symbol) => {
      const coin = coins.find((c) => c.currency === symbol);
      const qty = Number(coin?.balance ?? 0) + Number(coin?.locked ?? 0);
      return qty * Number(coin?.avg_buy_price ?? 0) >= 100;
    });
  if (named.length > 0) {
    problems.push(`업비트에 원화 마켓이 없어 시세를 못 구한 종목: ${named.join(", ")}`);
  }

  return { holdings, cashKrw: cashKrw + dollarKrw, cash, dollarCostKrw, problems };
}

/** One row of Upbit's deposits or withdrawals (/v1/deposits, /v1/withdraws). */
export type UpbitTransfer = {
  uuid: string;
  currency: string;
  state: string;
  amount: string;
  fee?: string;
  transaction_type: string;
  created_at: string;
};

/** Money across Upbit's edge, as upbitFlows answers it. */
export type UpbitFlows = { flows: Flow[]; feesKrw: number; interestKrw: number; problems: string[] };

/**
 * Money that crossed Upbit's edge since `since` (YYYY-MM-DD), in won.
 *
 * Won deposits add to principal and won withdrawals take from it, by the
 * amount that actually left - the 1,000 won fee is a cost, so it shows up as
 * a loss rather than as money taken home. Upbit's own interest on idle won
 * (`internal`) is a return, not a deposit: left out, and added up to be
 * named (`interestKrw`). A coin sent in or
 * out is valued at that day's close; a coin with no won market cannot be,
 * and is named instead of guessed.
 */
export async function upbitFlows(since: string): Promise<UpbitFlows> {
  if (!env("UPBIT_ACCESS_KEY")) return { flows: [], feesKrw: 0, interestKrw: 0, problems: [] };

  const list = (path: "deposits" | "withdraws") =>
    walkUpbitTransfers((page) => {
      const query = `limit=100&page=${page}&order_by=desc`;
      return json<UpbitTransfer[]>(
        `https://api.upbit.com/v1/${path}?${query}`,
        { headers: { Authorization: `Bearer ${upbitToken(query)}` } },
      );
    }, since);

  const [deposits, withdraws] = await Promise.all([list("deposits"), list("withdraws")]);
  // The closes of each coin moved, read once per coin.
  const closes = new Map<string, Map<string, number>>();
  for (const { row } of principalMoves(deposits, withdraws)) {
    if (row.currency === "KRW" || closes.has(row.currency)) continue;
    const days = Math.ceil((Date.now() - Date.parse(since)) / 86_400_000) + 2;
    closes.set(row.currency, await upbitDailyCloses(row.currency, days));
  }
  return upbitFlowsOf(deposits, withdraws, closes);
}

/**
 * Every row since `since` of one of Upbit's transfer listings, asked a page
 * (1, 2, ...) at a time.
 */
export async function walkUpbitTransfers(
  page: (n: number) => Promise<UpbitTransfer[]>,
  since: string,
): Promise<UpbitTransfer[]> {
  const out: UpbitTransfer[] = [];
  // Newest first; stop at the first page that reaches back past `since`.
  for (let n = 1; n <= 20; n += 1) {
    const rows = await page(n);
    out.push(...rows);
    if (rows.length < 100 || rows.some((r) => r.created_at.slice(0, 10) < since)) break;
  }
  return out.filter((r) => r.created_at.slice(0, 10) >= since);
}

/** The transfers that move principal, signed: deposits accepted, Upbit's own interest aside, and withdrawals done. */
function principalMoves(
  deposits: UpbitTransfer[],
  withdraws: UpbitTransfer[],
): { row: UpbitTransfer; sign: number }[] {
  return [
    ...deposits
      .filter((d) => d.state === "ACCEPTED" && d.transaction_type !== "internal")
      .map((d) => ({ row: d, sign: 1 })),
    ...withdraws.filter((w) => w.state === "DONE").map((w) => ({ row: w, sign: -1 })),
  ];
}

/**
 * Upbit's deposits and withdrawals as upbitFlows counts them, each coin
 * moved valued at its close that day (`closes`: coin -> day -> close).
 */
export function upbitFlowsOf(
  deposits: UpbitTransfer[],
  withdraws: UpbitTransfer[],
  closes: Map<string, Map<string, number>>,
): UpbitFlows {
  const problems: string[] = [];
  const flows: Flow[] = [];
  // Won withdrawals carry a ₩1,000 fee on top of the amount; the amount is
  // what counts as taken out, so the fee is a cost - added up here to be named.
  const feesKrw = withdraws
    .filter((w) => w.state === "DONE" && w.currency === "KRW")
    .reduce((sum, w) => sum + (Number(w.fee) || 0), 0);
  const interestKrw = deposits
    .filter((d) => d.state === "ACCEPTED" && d.transaction_type === "internal" && d.currency === "KRW")
    .reduce((sum, d) => sum + (Number(d.amount) || 0), 0);
  for (const { row, sign } of principalMoves(deposits, withdraws)) {
    const date = row.created_at.slice(0, 10);
    let krw = Number(row.amount);
    if (row.currency !== "KRW") {
      const close = closes.get(row.currency)?.get(date);
      if (!close) {
        problems.push(problem("upbit", `${date} ${row.currency} 이체를 원화로 환산하지 못해 원금에서 빠졌습니다.`));
        continue;
      }
      krw *= close;
    }
    if (krw < 1) continue;
    flows.push({
      id: `upbit:${row.uuid}`,
      date,
      venue: "upbit",
      amountKrw: sign * krw,
      memo:
        row.currency === "KRW"
          ? sign > 0 ? "원화 입금" : "원화 출금"
          : `${row.currency} ${sign > 0 ? "입금" : "출금"} ${Number(row.amount)}개`,
      source: "auto",
    });
  }
  return { flows, feesKrw, interestKrw, problems };
}

/** One filled (or part-filled) Upbit order, as the replay in realized.ts needs it. */
export type UpbitFill = {
  uuid: string;
  /** KST, YYYY-MM-DD. */
  date: string;
  at: string;
  symbol: string;
  side: "bid" | "ask";
  volume: number;
  /** Won paid or received, before the fee. */
  fundsKrw: number;
  feeKrw: number;
};

/** One row of Upbit's closed orders (/v1/orders/closed). */
export type UpbitOrder = {
  uuid: string;
  side: "bid" | "ask";
  market: string;
  created_at: string;
  executed_volume: string;
  executed_funds?: string;
  paid_fee: string;
};

/**
 * Every won-market order that filled since `since` (YYYY-MM-DD).
 *
 * Upbit answers closed orders seven days at a time, so the range is walked
 * back a week per call. A market buy (`ord_type: price`) ends as "cancel" -
 * the leftover won it could not spend is cancelled - so cancelled orders are
 * read too and kept when anything filled; leaving them out lost every market
 * buy and made later sales look like coins sold out of nowhere.
 */
export async function upbitFills(since: string): Promise<UpbitFill[]> {
  if (!env("UPBIT_ACCESS_KEY")) return [];
  const iso = (d: Date) => d.toISOString().replace(/\.\d+Z$/, "+00:00");
  const floor = Date.parse(`${since}T00:00:00+09:00`);
  const orders: UpbitOrder[] = [];

  for (let end = Date.now(); end > floor; end -= 7 * 86_400_000) {
    const start = Math.max(floor, end - 7 * 86_400_000);
    for (const state of ["done", "cancel"]) {
      const query = `state=${state}&limit=1000&order_by=desc&start_time=${iso(new Date(start))}&end_time=${iso(new Date(end))}`;
      const rows = await json<UpbitOrder[]>(
        `https://api.upbit.com/v1/orders/closed?${query.replace(/\+/g, "%2B")}`,
        { headers: { Authorization: `Bearer ${upbitToken(query)}` } },
      );
      orders.push(...rows);
      // Upbit allows a handful of these a second.
      await new Promise((r) => setTimeout(r, 120));
    }
  }
  return upbitFillsOf(orders);
}

/**
 * Closed orders as fills, oldest first and each order once, however many
 * windows listed it: only what filled on a won market, whether the order
 * ended done or cancelled.
 */
export function upbitFillsOf(orders: UpbitOrder[]): UpbitFill[] {
  const out = new Map<string, UpbitFill>();
  for (const o of orders) {
    const volume = Number(o.executed_volume);
    if (!(volume > 0) || !o.market.startsWith("KRW-")) continue;
    const at = o.created_at;
    out.set(o.uuid, {
      uuid: o.uuid,
      date: new Date(at).toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" }),
      at,
      symbol: o.market.slice(4),
      side: o.side,
      volume,
      fundsKrw: Number(o.executed_funds ?? 0),
      feeKrw: Number(o.paid_fee),
    });
  }
  return [...out.values()].sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
}

/**
 * What a coin closed at on a given day, as YYYY-MM-DD -> price.
 *
 * Upbit's day candles go back far enough for a year; `to` walks the window
 * back when more than 200 days are wanted.
 */
export async function upbitDailyCloses(
  symbol: string,
  days: number,
): Promise<Map<string, number>> {
  return walkUpbitCandles((count, to) => {
    const url =
      `https://api.upbit.com/v1/candles/days?market=KRW-${symbol}&count=${count}` +
      (to ? `&to=${encodeURIComponent(to)}` : "");
    return json<UpbitCandle[]>(url).catch(() => []);
  }, days);
}

/** One of Upbit's day candles (/v1/candles/days), newest first. */
export type UpbitCandle = { candle_date_time_kst: string; trade_price: number };

/**
 * Day closes by the candle's own KST day, asked `count` candles at a time
 * before `to` (the oldest candle of the page before), until `days` are in
 * hand or a page comes back empty.
 */
export async function walkUpbitCandles(
  page: (count: number, to: string) => Promise<UpbitCandle[]>,
  days: number,
): Promise<Map<string, number>> {
  const out = new Map<string, number>();
  let to = "";
  while (out.size < days) {
    const count = Math.min(200, days - out.size);
    const candles = await page(count, to);
    if (candles.length === 0) break;
    for (const c of candles) {
      out.set(c.candle_date_time_kst.slice(0, 10), c.trade_price);
    }
    to = candles[candles.length - 1].candle_date_time_kst;
  }
  return out;
}

/* ──────────────────── 한국투자증권 (KIS) ──────────────────── */

// The token is good for a day and KIS rate-limits issuing them, so it is held
// for the life of the server process rather than fetched per request.
// Keyed by app key: the gold-spot account has a key of its own, and each key
// is limited to one new token a minute.
type KisApp = { key: string; secret: string };
const kisTokens = new Map<string, { value: string; expires: number }>();

function stockApp(): KisApp {
  return { key: env("KIS_APP_KEY"), secret: env("KIS_APP_SECRET") };
}

function goldApp(): KisApp {
  return { key: env("KIS_GOLD_APP_KEY"), secret: env("KIS_GOLD_APP_SECRET") };
}

const keyHash = (key: string) => createHash("sha256").update(key).digest("hex");
// One issue at a time per key: two builds asking at once must not both hit
// the once-a-minute endpoint.
const kisTokenInflight = new Map<string, Promise<string>>();

async function kisAccessToken(app: KisApp = stockApp()): Promise<string> {
  const kisToken = kisTokens.get(app.key);
  if (kisToken && kisToken.expires > Date.now() + 60_000) return kisToken.value;
  const pending = kisTokenInflight.get(app.key);
  if (pending) return pending;
  const p = issueKisToken(app).finally(() => kisTokenInflight.delete(app.key));
  kisTokenInflight.set(app.key, p);
  return p;
}

async function issueKisToken(app: KisApp): Promise<string> {
  // A restart reuses the day-long token it already had (db.kisToken):
  // issuing is limited to once a minute per key.
  const saved = await db.loadKisToken(keyHash(app.key)).catch(() => null);
  if (saved && saved.expires > Date.now() + 60_000) {
    kisTokens.set(app.key, { value: saved.token, expires: saved.expires });
    return saved.token;
  }

  const body = await json<{ access_token: string; expires_in?: number }>(
    `${KIS_BASE}/oauth2/tokenP`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        grant_type: "client_credentials",
        appkey: app.key,
        appsecret: app.secret,
      }),
    },
  );
  const token = {
    value: body.access_token,
    expires: Date.now() + (body.expires_in ?? 86_400) * 1000,
  };
  kisTokens.set(app.key, token);
  await db.saveKisToken(keyHash(app.key), token.value, token.expires).catch(() => {});
  return token.value;
}

function kisHeaders(token: string, trId: string, app: KisApp = stockApp()) {
  return {
    "content-type": "application/json",
    authorization: `Bearer ${token}`,
    appkey: app.key,
    appsecret: app.secret,
    tr_id: trId,
    custtype: "P",
  };
}

function kisAccount() {
  return {
    cano: env("KIS_ACCOUNT_NO").replace(/-/g, "").slice(0, 8),
    prod: env("KIS_ACCOUNT_PRODUCT") || "01",
  };
}

/** One row of KIS's domestic balance (TTTC8434R output1). */
export type KisDomestic = {
  pdno: string;
  prdt_name: string;
  hldg_qty: string;
  pchs_avg_pric: string;
  prpr: string;
};

/** One row of KIS's overseas balance (TTTS3012R output1). */
export type KisOverseas = {
  ovrs_pdno: string;
  ovrs_item_name: string;
  ovrs_cblc_qty: string;
  pchs_avg_pric: string;
  now_pric2: string;
};

/** One currency of KIS's present balance (CTRP6504R output2). */
export type KisCurrency = {
  crcy_cd: string;
  /** Settled foreign-currency deposit. */
  frcr_dncl_amt_2: string;
  /** Sold today, not yet settled - money that is already yours. */
  frcr_sll_amt_smtl: string;
  /** Bought today, not yet settled - money that is already spent. */
  frcr_buy_amt_smtl: string;
};

/** KIS's present balance (CTRP6504R): a row per holding, carrying KIS's rate, and a row per currency. */
export type KisPresentBalance = {
  output1?: { bass_exrt?: string; buy_crcy_cd?: string }[];
  output2?: KisCurrency[];
};

/** The share account's foreign cash, as kisForeignCash answers it. */
export type KisForeignCash = {
  usd: number;
  /**
   * The same dollars for the cash checks (kis-flows.ts): settled, sold and
   * bought alike, signed, with a waiting limit buy's dollars still counted.
   * They move when a fill happens and not when it settles - a settlement only
   * moves dollars from "unsettled" into the deposit - so a check against the
   * fills by trade day never has to guess when KIS posts a settlement.
   * Null when the waiting buys could not be read: a half would read as money
   * moved.
   */
  bookUsd: number | null;
  rate: number | null;
  problems: string[];
};

/**
 * Dollars sitting in the account, settled or about to be, and KIS's own rate
 * for the day.
 *
 * The domestic balance only counts won. Without this, selling a share would
 * drop the total by the whole sale until settlement, and the dollars would
 * not show up even after it.
 *
 * The rate is KIS's base rate (bass_exrt), the one its own valuation uses.
 * Upbit's USDT price runs a premium over it and would read the dollar side
 * a percent or two richer than KIS says it is.
 */
async function kisForeignCash(token: string): Promise<KisForeignCash> {
  const { cano, prod } = kisAccount();
  const query = new URLSearchParams({
    CANO: cano,
    ACNT_PRDT_CD: prod,
    WCRC_FRCR_DVSN_CD: "02",
    NATN_CD: "000",
    TR_MKET_CD: "00",
    INQR_DVSN_CD: "00",
  });
  const body = await json<KisPresentBalance>(
    `${KIS_BASE}/uapi/overseas-stock/v1/trading/inquire-present-balance?${query}`,
    { headers: kisHeaders(token, "CTRP6504R") },
  );

  // A limit buy waiting for its price holds dollars that KIS already counts
  // as spent (frcr_buy_amt_smtl) though nothing was bought: without this the
  // total, the return and the deposit check all dropped by it while the
  // order waited.
  let held: number | null = null;
  try {
    held = await kisUnfilledBuyUsd(token);
  } catch {
    // shown as if nothing waited; the checks skip this look
  }
  return kisForeignCashOf(body, held);
}

/**
 * The present balance's dollars and KIS's rate. `held` is the dollars waiting
 * buys hold (kisUnfilledBuyUsd), null when they could not be read. A currency
 * other than the dollar has no rate here, so it is named rather than counted.
 */
export function kisForeignCashOf(body: KisPresentBalance, held: number | null): KisForeignCash {
  let usd = 0;
  let bookUsd = 0;
  const unpriced: string[] = [];
  for (const row of body.output2 ?? []) {
    const amount =
      (Number(row.frcr_dncl_amt_2) || 0) +
      (Number(row.frcr_sll_amt_smtl) || 0) -
      (Number(row.frcr_buy_amt_smtl) || 0);
    if (row.crcy_cd === "USD") bookUsd += amount;
    if (!(amount > 0)) continue;
    if (row.crcy_cd === "USD") usd += amount;
    else unpriced.push(row.crcy_cd);
  }
  usd += held ?? 0;
  const rate = Number(body.output1?.find((r) => r.buy_crcy_cd === "USD" && Number(r.bass_exrt) > 0)?.bass_exrt);
  return {
    usd,
    bookUsd: held === null ? null : bookUsd + held,
    rate: rate > 0 ? rate : null,
    problems: unpriced.length
      ? [problem("kis", `환율을 몰라 빠진 외화 예수금 ${unpriced.join(", ")}`)]
      : [],
  };
}

/** KIS's unfilled orders (TTTS3018R). */
export type KisUnfilled = { rt_cd?: string; msg1?: string; output?: Record<string, string>[] };

/** Dollars held by US buy orders not yet filled (KIS 미체결, TTTS3018R): shares × limit price. */
export async function kisUnfilledBuyUsd(token?: string): Promise<number> {
  const t = token ?? (await kisAccessToken());
  const { cano, prod } = kisAccount();
  const mock = env("KIS_MOCK") === "true";
  const q = new URLSearchParams({
    CANO: cano,
    ACNT_PRDT_CD: prod,
    OVRS_EXCG_CD: "NASD", // the whole US market
    SORT_SQN: "DS",
    CTX_AREA_FK200: "",
    CTX_AREA_NK200: "",
  });
  const body = await json<KisUnfilled>(
    `${KIS_BASE}/uapi/overseas-stock/v1/trading/inquire-nccs?${q}`,
    { headers: kisHeaders(t, mock ? "VTTS3018R" : "TTTS3018R") },
  );
  return kisUnfilledBuyUsdOf(body);
}

/** The buys among KIS's unfilled orders, in dollars; a refusal throws what KIS said. */
export function kisUnfilledBuyUsdOf(body: KisUnfilled): number {
  if (body.rt_cd !== "0") throw new Error((body.msg1 ?? "미체결 내역을 읽지 못했습니다").trim());
  let usd = 0;
  for (const o of body.output ?? []) {
    if (o.sll_buy_dvsn_cd !== "02") continue; // 02 매수
    const qty = Number(o.nccs_qty ?? 0);
    const price = Number(o.ft_ord_unpr3 ?? o.ft_ord_unpr ?? o.ovrs_ord_unpr ?? 0);
    if (qty > 0 && price > 0) usd += qty * price;
  }
  return usd;
}

/** KIS's domestic balance (TTTC8434R): the shares, and the won deposit in output2. */
export type KisDomesticBalance = { output1?: KisDomestic[]; output2?: { dnca_tot_amt?: string }[] };

/** KIS's overseas balance under one exchange code (TTTS3012R). */
export type KisOverseasBalance = { output1?: KisOverseas[] };

/** The share account, as kisHoldings answers it. */
export type KisHoldings = {
  holdings: Holding[];
  cashKrw: number;
  cash: CashLine[];
  /**
   * Cash as the checks count it (kis-flows.ts), each currency on its own - a
   * sale's proceeds stay dollars until exchanged, so adding the two at today's
   * rate would turn every move of the rate into money in or out. The dollars
   * include what is sold or bought but not settled yet (bookUsd). Null when
   * the dollar side could not be read: a half would read as money gone missing.
   */
  bookCash: { krw: number; usd: number } | null;
  /** The rate everything dollar here was valued at. */
  usdKrw: number;
  problems: string[];
};

/**
 * The share account: holdings, cash, and the rate they are valued at.
 * `fallbackRate` (Upbit's USDT) is used only when KIS gives no rate - with no
 * dollar holding to quote one against.
 */
export async function kisHoldings(fallbackRate: number): Promise<KisHoldings> {
  if (!env("KIS_APP_KEY")) throw new Error("한국투자증권 키가 설정되지 않았습니다.");

  const token = await kisAccessToken();
  const { cano, prod } = kisAccount();

  // Asked first: its rate values everything below. A failed lookup is named,
  // not fatal - the shares still answer, at the fallback rate.
  const foreign = await kisForeignCash(token).catch((e: Error) => ({
    usd: 0,
    bookUsd: null,
    rate: null,
    problems: [`한국투자증권 외화 예수금: ${e.message}`],
  }));

  // 국내
  const domesticQuery = new URLSearchParams({
    CANO: cano,
    ACNT_PRDT_CD: prod,
    AFHR_FLPR_YN: "N",
    OFL_YN: "",
    INQR_DVSN: "02",
    UNPR_DVSN: "01",
    FUND_STTL_ICLD_YN: "N",
    FNCG_AMT_AUTO_RDPT_YN: "N",
    PRCS_DVSN: "00",
    CTX_AREA_FK100: "",
    CTX_AREA_NK100: "",
  });
  const domestic = await json<KisDomesticBalance>(
    `${KIS_BASE}/uapi/domestic-stock/v1/trading/inquire-balance?${domesticQuery}`,
    { headers: kisHeaders(token, "TTTC8434R") },
  );

  // 해외, under each exchange code in turn (see kisHoldingsOf).
  const overseas: KisOverseasBalance[] = [];
  for (const exchange of ["NASD", "NYSE", "AMEX"]) {
    const query = new URLSearchParams({
      CANO: cano,
      ACNT_PRDT_CD: prod,
      OVRS_EXCG_CD: exchange,
      TR_CRCY_CD: "USD",
      CTX_AREA_FK200: "",
      CTX_AREA_NK200: "",
    });
    overseas.push(
      await json<KisOverseasBalance>(
        `${KIS_BASE}/uapi/overseas-stock/v1/trading/inquire-balance?${query}`,
        { headers: kisHeaders(token, "TTTS3012R") },
      ).catch(() => ({ output1: [] as KisOverseas[] })),
    );
  }

  return kisHoldingsOf({ foreign, domestic, overseas }, fallbackRate);
}

/**
 * The share account from KIS's answers: its foreign cash, the domestic
 * balance, and the overseas balance under each exchange code in the order
 * asked. Everything dollar is valued at KIS's rate, or at `fallbackRate`
 * when KIS gave none.
 */
export function kisHoldingsOf(
  read: { foreign: KisForeignCash; domestic: KisDomesticBalance; overseas: KisOverseasBalance[] },
  fallbackRate: number,
): KisHoldings {
  const { foreign, domestic, overseas } = read;
  const usdKrw = foreign.rate ?? fallbackRate;
  const holdings: Holding[] = [];

  for (const row of domestic.output1 ?? []) {
    const qty = Number(row.hldg_qty);
    if (qty <= 0) continue;
    const avg = Number(row.pchs_avg_pric);
    const price = Number(row.prpr) || avg;
    holdings.push({
      id: `kis:${row.pdno}`,
      venue: "kis",
      kind: "stock",
      symbol: row.pdno,
      name: row.prdt_name,
      quantity: qty,
      avgPrice: avg,
      price,
      currency: "KRW",
      valueKrw: qty * price,
      costKrw: qty * avg,
    });
  }
  const cashKrw = Number(domestic.output2?.[0]?.dnca_tot_amt ?? 0);

  // 해외. KIS answers some exchange codes with the same holding, so the
  // symbol decides identity and the first answer wins.
  const seen = new Set<string>();
  for (const page of overseas) {
    for (const row of page.output1 ?? []) {
      const qty = Number(row.ovrs_cblc_qty);
      if (qty <= 0 || seen.has(row.ovrs_pdno)) continue;
      seen.add(row.ovrs_pdno);
      const avg = Number(row.pchs_avg_pric);
      const price = Number(row.now_pric2) || avg;
      holdings.push({
        id: `kis:${row.ovrs_pdno}`,
        venue: "kis",
        kind: "stock",
        symbol: row.ovrs_pdno,
        name: row.ovrs_item_name || row.ovrs_pdno,
        quantity: qty,
        avgPrice: avg,
        price,
        currency: "USD",
        valueKrw: qty * price * usdKrw,
        // Dollars at today's rate until realized.ts replaces it with the won
        // actually paid - the rate the shares were bought at is not here.
        costKrw: qty * avg * usdKrw,
      });
    }
  }

  const cash: CashLine[] = [];
  if (cashKrw > 0) cash.push({ id: "kis:KRW", venue: "kis", currency: "KRW", label: "한국투자 원화", amount: cashKrw, valueKrw: cashKrw });
  if (foreign.usd > 0) {
    cash.push({ id: "kis:USD", venue: "kis", currency: "USD", label: "한국투자 달러", amount: foreign.usd, valueKrw: foreign.usd * usdKrw });
  }

  return {
    holdings,
    cashKrw: cashKrw + foreign.usd * usdKrw,
    cash,
    bookCash: foreign.bookUsd === null ? null : { krw: cashKrw, usd: foreign.bookUsd },
    usdKrw,
    problems: foreign.problems,
  };
}

/**
 * One day's net money from trades - sales +, purchases -, fees already in -
 * on the day it moved the cash the checks read. The share account trades in
 * dollars, counted on the trade day with KIS's rate that day (the nearest
 * thing to what an exchange then cost); the gold account settles in won the
 * same day.
 */
export type Settlement = { date: string; krw: number; usd: number; rate: number };

/** One overseas fill, as CTOS4001R reports it. */
export type KisTrade = {
  /** YYYY-MM-DD. */
  date: string;
  settleDate: string;
  side: "buy" | "sell";
  symbol: string;
  name: string;
  quantity: number;
  /**
   * The won this fill moved, at KIS's rate on its own day: a purchase's
   * amount plus fees, a sale's minus. Buying at one rate and selling at
   * another is in the difference - which is what a won return, and the
   * capital-gains return, are made of. 0 until the fill settles (`settled`).
   */
  netKrw: number;
  /** A sale's amount before fees, in won. */
  grossKrw: number;
  /** The same fill in dollars, as the dollar balance moved. */
  netUsd: number;
  /** The amount before fees, in dollars. */
  grossUsd: number;
  rate: number;
  /**
   * False until KIS settles the fill (a US trade, the next business day):
   * until then it has its dollars but no rate, no fees and no won - a sale
   * made today reads as bringing in ₩0. See priceUnsettled.
   */
  settled: boolean;
};

/** One row of KIS's overseas fills by period (CTOS4001R output1). */
export type KisTrans = {
  trad_dt: string;
  sttl_dt: string;
  sll_buy_dvsn_cd: string;
  pdno: string;
  ovrs_item_name: string;
  ccld_qty: string;
  tr_amt: string;
  tr_frcr_amt2: string;
  wcrc_excc_amt: string;
  frcr_excc_amt_1: string;
  erlm_exrt: string;
};

const dashed = (d: string) => `${d.slice(0, 4)}-${d.slice(4, 6)}-${d.slice(6, 8)}`;

/** One page of a KIS listing paged by continuation keys (CTX_AREA_FK100 / NK100). */
export type KisPage<T> = {
  rt_cd: string;
  msg1?: string;
  output1?: T[];
  ctx_area_fk100?: string;
  ctx_area_nk100?: string;
};

/**
 * Every row of a KIS listing paged by continuation keys, at most twenty
 * pages. `ask` gets the keys the page before handed back, trimmed, and the
 * page's index from 0; an empty key or an empty page is the last. A refusal
 * throws what KIS said, or `failure`.
 */
export async function walkKisPages<T>(
  ask: (fk: string, nk: string, page: number) => Promise<KisPage<T>>,
  failure: string,
): Promise<T[]> {
  const rows: T[] = [];
  let lastPage = "";
  let fk = "";
  let nk = "";
  for (let page = 0; page < 20; page += 1) {
    const body = await ask(fk, nk, page);
    if (body.rt_cd !== "0") throw new Error(body.msg1?.trim() || failure);
    // Two identical fills on one day are two trades, so rows are not deduped;
    // only a page handed back twice is.
    const pageKey = JSON.stringify(body.output1 ?? []);
    if (pageKey === lastPage) break;
    lastPage = pageKey;
    rows.push(...(body.output1 ?? []));
    fk = body.ctx_area_fk100?.trim() ?? "";
    nk = body.ctx_area_nk100?.trim() ?? "";
    if (!nk || (body.output1 ?? []).length === 0) break;
  }
  return rows;
}

/**
 * Overseas fills traded since `since` (CTOS4001R), oldest first. Both the
 * realized returns (realized.ts) and the cash checks (movementsOf) are
 * made from these, so they are asked once. Domestic trades are not read; the
 * domestic side is unused.
 */
export async function kisTrades(since: string): Promise<KisTrade[]> {
  if (!env("KIS_APP_KEY")) return [];
  const token = await kisAccessToken();
  const { cano, prod } = kisAccount();
  const today = new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const rows = await walkKisPages<KisTrans>((fk, nk, page) => {
    const query = new URLSearchParams({
      CANO: cano,
      ACNT_PRDT_CD: prod,
      ERLM_STRT_DT: since.replace(/-/g, ""),
      ERLM_END_DT: today.replace(/-/g, ""),
      OVRS_EXCG_CD: "",
      PDNO: "",
      SLL_BUY_DVSN_CD: "00",
      LOAN_DVSN_CD: "",
      CTX_AREA_FK100: fk,
      CTX_AREA_NK100: nk,
    });
    return json<KisPage<KisTrans>>(`${KIS_BASE}/uapi/overseas-stock/v1/trading/inquire-period-trans?${query}`, {
      headers: { ...kisHeaders(token, "CTOS4001R"), ...(page > 0 ? { tr_cont: "N" } : {}) },
    });
  }, "거래내역 조회 실패");
  return kisTradesOf(rows);
}

/** CTOS4001R's rows as fills, oldest first: a row without both dates or with nothing filled is none. */
export function kisTradesOf(rows: KisTrans[]): KisTrade[] {
  const trades: KisTrade[] = [];
  for (const row of rows) {
    if (!row.trad_dt || !row.sttl_dt || !(Number(row.ccld_qty) > 0)) continue;
    const sell = row.sll_buy_dvsn_cd === "01";
    trades.push({
      date: dashed(row.trad_dt),
      settleDate: dashed(row.sttl_dt),
      side: sell ? "sell" : "buy",
      symbol: row.pdno,
      name: row.ovrs_item_name || row.pdno,
      quantity: Number(row.ccld_qty),
      netKrw: Number(row.wcrc_excc_amt),
      grossKrw: Number(row.tr_amt),
      netUsd: Number(row.frcr_excc_amt_1),
      grossUsd: Number(row.tr_frcr_amt2),
      rate: Number(row.erlm_exrt),
      settled: Number(row.erlm_exrt) > 0,
    });
  }
  return trades.sort((a, b) => a.date.localeCompare(b.date));
}

/**
 * The share account's dollars by trade day, for fills traded on or after
 * `since`: the account trades in dollars, and a sale's proceeds stay dollars
 * until exchanged by hand.
 *
 * By the trade day and not the settlement day, because the cash the checks
 * read (bookCash) moves at the fill: KIS posts a settlement into the deposit
 * the Korean morning after New York settles, at an hour nothing here can
 * know, and counting a sale on any guessed day reads the gap as money taken
 * out.
 */
export function movementsOf(trades: KisTrade[], since: string): Settlement[] {
  const byDay = new Map<string, Settlement>();
  for (const t of trades) {
    if (t.date < since) continue;
    const day = byDay.get(t.date) ?? { date: t.date, krw: 0, usd: 0, rate: t.rate };
    day.usd += t.side === "sell" ? t.netUsd : -t.netUsd;
    byDay.set(t.date, day);
  }
  return [...byDay.values()].sort((a, b) => a.date.localeCompare(b.date));
}

/**
 * What an overseas share closed at, as YYYY-MM-DD -> price.
 *
 * KIS answers ~100 rows at a time, so the window is walked back with BYMD
 * until enough days are in hand or the listing runs out - a ticker that has
 * only traded for three months simply has no year to compare against, and
 * the caller is expected to notice the gap rather than invent one.
 */
export async function kisDailyCloses(
  symbol: string,
  days: number,
): Promise<Map<string, number>> {
  if (!env("KIS_APP_KEY")) return new Map<string, number>();

  const token = await kisAccessToken();
  return walkKisCloses(async (excd, before) => {
    const query = new URLSearchParams({
      AUTH: "",
      EXCD: excd,
      SYMB: symbol,
      GUBN: "0",
      BYMD: before,
      MODP: "1",
    });
    const body = await json<{ output2?: KisDailyPrice[] }>(
      `${KIS_BASE}/uapi/overseas-price/v1/quotations/dailyprice?${query}`,
      { headers: kisHeaders(token, "HHDFS76240000") },
    ).catch(() => ({ output2: [] as KisDailyPrice[] }));
    return body.output2 ?? [];
  }, days);
}

/** One row of KIS's overseas daily prices (HHDFS76240000 output2), newest first. */
export type KisDailyPrice = { xymd: string; clos: string };

/**
 * The closes kisDailyCloses walks, from `ask`: one page of rows under an
 * exchange code (NAS, NYS or AMS), back from a day (BYMD, YYYYMMDD; empty
 * for the latest).
 */
export async function walkKisCloses(
  ask: (excd: string, before: string) => Promise<KisDailyPrice[]>,
  days: number,
): Promise<Map<string, number>> {
  const out = new Map<string, number>();
  let before = "";

  // The daily-price endpoint wants the exchange, and answers nothing for the
  // wrong one - KO is NYSE and came back empty under NAS, which silently left
  // it out of every window. The right one is found once and then kept.
  let exchange: string | null = null;

  for (let page = 0; page < 5 && out.size < days; page += 1) {
    const closes = async (excd: string) =>
      (await ask(excd, before)).filter((r) => r.xymd && Number(r.clos) > 0);

    let rows: KisDailyPrice[] = [];
    if (exchange) {
      rows = await closes(exchange);
    } else {
      for (const excd of ["NAS", "NYS", "AMS"]) {
        rows = await closes(excd);
        if (rows.length > 0) {
          exchange = excd;
          break;
        }
      }
    }

    if (rows.length === 0) break;
    for (const row of rows) {
      const date = `${row.xymd.slice(0, 4)}-${row.xymd.slice(4, 6)}-${row.xymd.slice(6, 8)}`;
      out.set(date, Number(row.clos));
    }
    // Step one day past the oldest row to page further back.
    const oldest = rows[rows.length - 1].xymd;
    const stepped = new Date(
      Number(oldest.slice(0, 4)),
      Number(oldest.slice(4, 6)) - 1,
      Number(oldest.slice(6, 8)) - 1,
    );
    const next = `${stepped.getFullYear()}${String(stepped.getMonth() + 1).padStart(2, "0")}${String(stepped.getDate()).padStart(2, "0")}`;
    if (next === before) break;
    before = next;
  }
  return out;
}

/** The stale figure the page adds up against when Upbit gives no rate. */
const USD_KRW_FALLBACK = 1350;

/**
 * One rate for the whole page, so a dollar holding and the total never
 * disagree. Upbit publishes the same USDT market everyone prices off; if it
 * is unreachable the page still adds up, just against a stale figure.
 */
export async function usdKrwRate(): Promise<number> {
  try {
    return usdtRateOf(await json<UpbitTicker[]>(
      "https://api.upbit.com/v1/ticker?markets=KRW-USDT",
    ));
  } catch {
    return USD_KRW_FALLBACK;
  }
}

/** The USDT ticker's price as the dollar rate, or the fallback when it has none. */
export function usdtRateOf(tickers: UpbitTicker[]): number {
  const [t] = tickers;
  return t?.trade_price > 0 ? t.trade_price : USD_KRW_FALLBACK;
}

/* ─────────────────── 한국투자증권 금현물 (KRX 금시장) ─────────────────── */

/** KRX 금시장 금 99.99% 1kg 종목. 1주가 1g 이다. */
const KRX_GOLD = "M04020000";

/**
 * The gold-spot account.
 *
 * It is a separate KIS account with a key of its own, and the ordinary
 * domestic balance call refuses it outright ("금현물계좌는 해당서비스가
 * 불가합니다"). What does answer is the asset-overview call (CTRP6548R), which
 * gives the account's total and its cash; the difference is the gold. Grams
 * are that value over today's KRX gold price, since nothing hands back the
 * quantity itself.
 *
 * Null when no key is configured - the account is simply not connected.
 */
export async function kisGoldAccount(): Promise<KisGoldAccount | null> {
  const app = goldApp();
  if (!app.key) return null;

  const token = await kisAccessToken(app);
  const query = new URLSearchParams({
    CANO: env("KIS_GOLD_ACCOUNT_NO").replace(/-/g, "").slice(0, 8),
    ACNT_PRDT_CD: env("KIS_GOLD_ACCOUNT_PRODUCT") || "01",
    INQR_DVSN_1: "",
    BSPR_BF_DT_APLY_YN: "",
  });
  const balance = kisGoldBalanceOf(await json<KisGoldBalance>(
    `${KIS_BASE}/uapi/domestic-stock/v1/trading/inquire-account-balance?${query}`,
    { headers: kisHeaders(token, "CTRP6548R", app) },
  ));

  const quote = await json<KisQuote>(
    `${KIS_BASE}/uapi/domestic-stock/v1/quotations/inquire-price?${new URLSearchParams({
      FID_COND_MRKT_DIV_CODE: "J",
      FID_INPUT_ISCD: KRX_GOLD,
    })}`,
    { headers: kisHeaders(token, "FHKST01010100", app) },
  ).catch(() => null);
  return kisGoldAccountOf(balance, quote);
}

/** KIS's asset overview (CTRP6548R) of the gold account. */
export type KisGoldBalance = {
  rt_cd: string;
  msg1?: string;
  output2?: { tot_asst_amt?: string; dncl_amt?: string; pchs_amt_smtl?: string };
};

/** The gold account's balance in won: everything in it, its cash, and what was paid for the gold (0: KIS did not say). */
export type GoldBalance = { totalKrw: number; cashKrw: number; boughtKrw: number };

/** A KRX quote (FHKST01010100). */
export type KisQuote = { output?: { stck_prpr?: string } };

/** The gold account, as kisGoldAccount answers it. */
export type KisGoldAccount = {
  holdings: Holding[];
  cashKrw: number;
  cash: CashLine[];
  /** Won per gram, asked even when nothing is held, so gold can be planned. */
  gramKrw: number | null;
};

/** The gold account's balance from the asset overview; a refusal throws what KIS said. */
export function kisGoldBalanceOf(body: KisGoldBalance): GoldBalance {
  if (body.rt_cd !== "0") throw new Error(body.msg1?.trim() || "잔고 조회 실패");
  return {
    totalKrw: Number(body.output2?.tot_asst_amt ?? 0),
    cashKrw: Number(body.output2?.dncl_amt ?? 0),
    boughtKrw: Number(body.output2?.pchs_amt_smtl ?? 0),
  };
}

/** The gold account from its balance and KRX's quote for gold (null: it could not be read). */
export function kisGoldAccountOf(balance: GoldBalance, quote: KisQuote | null): KisGoldAccount {
  const cashKrw = balance.cashKrw;
  const valueKrw = Math.max(0, balance.totalKrw - cashKrw);
  const cash: CashLine[] = cashKrw > 0
    ? [{ id: "gold:KRW", venue: "gold", currency: "KRW", label: "금현물 계좌 원화", amount: cashKrw, valueKrw: cashKrw }]
    : [];

  const price = Number(quote?.output?.stck_prpr ?? 0);
  const gramKrw = price > 0 ? price : null;
  if (valueKrw < 1) return { holdings: [], cashKrw, cash, gramKrw };

  const grams = price > 0 ? valueKrw / price : 0;
  // A zero purchase total means KIS did not say - fall back to value, which
  // reads as flat rather than as a 100% gain.
  const costKrw = balance.boughtKrw || valueKrw;
  return {
    holdings: [
      {
        id: `gold:${KRX_GOLD}`,
        venue: "gold",
        kind: "gold",
        symbol: KRX_GOLD,
        name: "금현물",
        quantity: grams,
        avgPrice: grams > 0 ? costKrw / grams : 0,
        price: price || valueKrw,
        currency: "KRW",
        valueKrw,
        costKrw,
      },
    ],
    cashKrw,
    cash,
    gramKrw,
  };
}

/** One row of KIS's period trade profit (TTTC8715R output1). */
export type KisPeriodTrade = {
  trad_dt: string;
  buy_amt: string;
  sll_amt: string;
  fee: string;
  tl_tax: string;
};

/**
 * The gold account's trades since `since`, as the won each trading day
 * moved (TTTC8715R, period trade profit - the one trade history this account
 * answers; the daily-fill calls refuse it). KRX gold settles the same day.
 */
export async function goldSettlements(since: string): Promise<Settlement[]> {
  const app = goldApp();
  if (!app.key) return [];
  const token = await kisAccessToken(app);
  const today = new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const rows = await walkKisPages<KisPeriodTrade>((fk, nk, page) => {
    const query = new URLSearchParams({
      CANO: env("KIS_GOLD_ACCOUNT_NO").replace(/-/g, "").slice(0, 8),
      ACNT_PRDT_CD: env("KIS_GOLD_ACCOUNT_PRODUCT") || "01",
      SORT_DVSN: "00",
      PDNO: "",
      INQR_STRT_DT: since.replace(/-/g, ""),
      INQR_END_DT: today.replace(/-/g, ""),
      CBLC_DVSN: "00",
      CTX_AREA_FK100: fk,
      CTX_AREA_NK100: nk,
    });
    return json<KisPage<KisPeriodTrade>>(`${KIS_BASE}/uapi/domestic-stock/v1/trading/inquire-period-trade-profit?${query}`, {
      headers: { ...kisHeaders(token, "TTTC8715R", app), ...(page > 0 ? { tr_cont: "N" } : {}) },
    });
  }, "매매 내역 조회 실패");
  return goldSettlementsOf(rows);
}

/** TTTC8715R's rows as the won each trading day moved: sales in; purchases, fees and tax out. */
export function goldSettlementsOf(rows: KisPeriodTrade[]): Settlement[] {
  const byDay = new Map<string, Settlement>();
  for (const row of rows) {
    const d = row.trad_dt;
    if (!d) continue;
    const date = `${d.slice(0, 4)}-${d.slice(4, 6)}-${d.slice(6, 8)}`;
    const krw = Number(row.sll_amt) - Number(row.buy_amt) - Number(row.fee) - Number(row.tl_tax);
    const day = byDay.get(date) ?? { date, krw: 0, usd: 0, rate: 0 };
    day.krw += krw;
    byDay.set(date, day);
  }
  return [...byDay.values()].sort((a, b) => a.date.localeCompare(b.date));
}

/** KRX gold closes per gram, YYYY-MM-DD -> price, a hundred days a call. */
export async function kisGoldDailyCloses(days: number): Promise<Map<string, number>> {
  const app = goldApp();
  if (!app.key) return new Map<string, number>();
  const token = await kisAccessToken(app);
  return walkKisGoldCloses(async (from, to) => {
    const body = await json<{ output2?: KisDailyChart[] }>(
      `${KIS_BASE}/uapi/domestic-stock/v1/quotations/inquire-daily-itemchartprice?${new URLSearchParams({
        FID_COND_MRKT_DIV_CODE: "J",
        FID_INPUT_ISCD: KRX_GOLD,
        FID_INPUT_DATE_1: from,
        FID_INPUT_DATE_2: to,
        FID_PERIOD_DIV_CODE: "D",
        FID_ORG_ADJ_PRC: "0",
      })}`,
      { headers: kisHeaders(token, "FHKST03010100", app) },
    ).catch(() => ({ output2: [] }));
    return body.output2 ?? [];
  }, days);
}

/** One day of KIS's daily chart (FHKST03010100 output2), newest first. */
export type KisDailyChart = { stck_bsop_date: string; stck_clpr: string };

/**
 * The closes kisGoldDailyCloses walks back from `now`, from `ask`: the
 * chart between two Seoul days (YYYYMMDD), 140 days a call, each next window
 * ending the day before the last one began.
 */
export async function walkKisGoldCloses(
  ask: (from: string, to: string) => Promise<KisDailyChart[]>,
  days: number,
  now = new Date(),
): Promise<Map<string, number>> {
  const out = new Map<string, number>();
  const ymd = (d: Date) => d.toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" }).replace(/-/g, "");

  let end = now;
  for (let page = 0; page < 5 && out.size < days; page += 1) {
    const start = new Date(end.getTime() - 140 * 86_400_000);
    const rows = (await ask(ymd(start), ymd(end))).filter((r) => r.stck_bsop_date && Number(r.stck_clpr) > 0);
    if (rows.length === 0) break;
    for (const r of rows) {
      const d = r.stck_bsop_date;
      out.set(`${d.slice(0, 4)}-${d.slice(4, 6)}-${d.slice(6, 8)}`, Number(r.stck_clpr));
    }
    end = new Date(start.getTime() - 86_400_000);
  }
  return out;
}

/* ─────────────────────────── Orders ─────────────────────────── */

/** Upbit's last trade price for a KRW market, for turning won into a coin amount. */
export async function upbitPrice(market: string): Promise<number> {
  return upbitPriceOf(market, await json<UpbitTicker[]>(`https://api.upbit.com/v1/ticker?markets=${encodeURIComponent(market)}`));
}

/** The ticker's last price for `market`; none is an error, never a price of zero. */
export function upbitPriceOf(market: string, tickers: UpbitTicker[]): number {
  const [t] = tickers;
  if (!t?.trade_price) throw new Error(`${market} 시세를 읽지 못했습니다`);
  return t.trade_price;
}

export type UpbitOrderAnswer = { uuid: string; state: string; side: string; ord_type: string };

/**
 * A market order on Upbit: buying spends `price` won ("price" order),
 * selling sells `volume` coins ("market" order). The token carries a hash
 * of the parameters as a query string, as for any request with parameters.
 */
export async function upbitOrder(o: {
  market: string;
  side: "bid" | "ask";
  price?: string;
  volume?: string;
}): Promise<UpbitOrderAnswer> {
  const params: Record<string, string> = {
    market: o.market,
    side: o.side,
    ord_type: o.side === "bid" ? "price" : "market",
    ...(o.price ? { price: o.price } : {}),
    ...(o.volume ? { volume: o.volume } : {}),
  };
  const query = Object.entries(params)
    .map(([k, v]) => `${k}=${v}`)
    .join("&");
  const res = await fetch("https://api.upbit.com/v1/orders", {
    method: "POST",
    headers: { authorization: `Bearer ${upbitToken(query)}`, "content-type": "application/json" },
    body: JSON.stringify(params),
  });
  return upbitOrderAnswerOf(res, await res.json().catch(() => null));
}

/** Upbit's answer to an order (`answer`: its body, null when it was not JSON); a refusal throws Upbit's own words. */
export function upbitOrderAnswerOf(res: { ok: boolean; status: number }, answer: unknown): UpbitOrderAnswer {
  const body = answer as (UpbitOrderAnswer & { error?: { message?: string; name?: string } }) | null;
  if (!res.ok || !body?.uuid) {
    throw new Error(body?.error?.message ?? body?.error?.name ?? `업비트가 거절했습니다 (${res.status})`);
  }
  return body;
}

/** The quote exchange codes (NAS/NYS/AMS) and the order ones KIS wants for them. */
const ORDER_EXCHANGE: Record<string, string> = { NAS: "NASD", NYS: "NYSE", AMS: "AMEX" };
const usExchange = new Map<string, string>();

/**
 * The last price of a US share and the exchange it trades on - the order
 * must name the right one, and only the right one quotes it (KO is NYSE
 * and is empty under NAS). Found once per symbol.
 */
export async function kisUsQuote(symbol: string): Promise<{ excd: string; last: number }> {
  const token = await kisAccessToken();
  const known = usExchange.get(symbol);
  for (const excd of known ? [known] : ["NAS", "NYS", "AMS"]) {
    const q = new URLSearchParams({ AUTH: "", EXCD: excd, SYMB: symbol });
    const body = await json<KisUsPrice>(`${KIS_BASE}/uapi/overseas-price/v1/quotations/price?${q}`, {
      headers: kisHeaders(token, "HHDFS00000300"),
    }).catch(() => ({}) as KisUsPrice);
    const last = kisLastOf(body);
    if (last > 0) {
      usExchange.set(symbol, excd);
      return { excd, last };
    }
  }
  throw new Error(`${symbol} 의 시세를 찾지 못했습니다`);
}

/** KIS's quote for a US share (HHDFS00000300). */
export type KisUsPrice = { output?: { last?: string } };

/** The last trade in KIS's quote for a US share; 0 when it has none, as under the wrong exchange. */
export function kisLastOf(body: KisUsPrice): number {
  return Number(body.output?.last ?? 0);
}

/**
 * A limit order for US shares at `price` dollars. KIS's API takes no plain
 * market order for the US, so orders.ts prices a buy a little over the last
 * trade and a sell a little under it: filled at once in a normal market,
 * and never at a price far from what the person saw.
 */
export async function kisUsOrder(o: {
  symbol: string;
  excd: string;
  side: "buy" | "sell";
  qty: number;
  price: number;
}): Promise<{ odno: string; message: string }> {
  const token = await kisAccessToken();
  const { cano, prod } = kisAccount();
  const mock = env("KIS_MOCK") === "true";
  const trId = o.side === "buy" ? (mock ? "VTTT1002U" : "TTTT1002U") : mock ? "VTTT1001U" : "TTTT1006U";
  const body = {
    CANO: cano,
    ACNT_PRDT_CD: prod,
    OVRS_EXCG_CD: ORDER_EXCHANGE[o.excd] ?? o.excd,
    PDNO: o.symbol,
    ORD_QTY: String(o.qty),
    OVRS_ORD_UNPR: o.price.toFixed(2),
    CTAC_TLNO: "",
    MGCO_APTM_ODNO: "",
    SLL_TYPE: o.side === "sell" ? "00" : "",
    ORD_SVR_DVSN_CD: "0",
    ORD_DVSN: "00",
  };
  const res = await json<KisOrderAnswer>(
    `${KIS_BASE}/uapi/overseas-stock/v1/trading/order`,
    { method: "POST", headers: kisHeaders(token, trId), body: JSON.stringify(body) },
  );
  return kisOrderAnswerOf(res);
}

/** KIS's answer to an order or a cancel. */
export type KisOrderAnswer = { rt_cd?: string; msg_cd?: string; msg1?: string; output?: { ODNO?: string } };

/** The order number KIS gave an order, and its message; a refusal throws its message and code. */
export function kisOrderAnswerOf(res: KisOrderAnswer): { odno: string; message: string } {
  if (res.rt_cd !== "0") throw new Error(`${(res.msg1 ?? "").trim()} (${res.msg_cd ?? "?"})`);
  return { odno: res.output?.ODNO ?? "", message: (res.msg1 ?? "").trim() };
}

/**
 * How many shares the account's dollars buy at `price`, as KIS counts it
 * (its commission included) - the honest answer to "can I afford one".
 */
export async function kisUsBuyable(symbol: string, excd: string, price: number): Promise<{ qty: number; amount: number; raw: Record<string, string> }> {
  const token = await kisAccessToken();
  const { cano, prod } = kisAccount();
  const mock = env("KIS_MOCK") === "true";
  const q = new URLSearchParams({
    CANO: cano,
    ACNT_PRDT_CD: prod,
    OVRS_EXCG_CD: ORDER_EXCHANGE[excd] ?? excd,
    OVRS_ORD_UNPR: price.toFixed(2),
    ITEM_CD: symbol,
  });
  const body = await json<KisBuyable>(
    `${KIS_BASE}/uapi/overseas-stock/v1/trading/inquire-psamount?${q}`,
    { headers: kisHeaders(token, mock ? "VTTS3007R" : "TTTS3007R") },
  );
  return kisBuyableOf(body);
}

/** KIS's buyable amount for a US share (TTTS3007R). */
export type KisBuyable = { rt_cd?: string; msg1?: string; output?: Record<string, string> };

/** The shares and dollars KIS says the account can buy; a refusal throws what KIS said. */
export function kisBuyableOf(body: KisBuyable): { qty: number; amount: number; raw: Record<string, string> } {
  if (body.rt_cd !== "0" || !body.output) throw new Error((body.msg1 ?? "매수 가능 금액을 읽지 못했습니다").trim());
  const o = body.output;
  return {
    qty: Number(o.max_ord_psbl_qty ?? o.ovrs_max_ord_psbl_qty ?? o.ord_psbl_qty ?? 0),
    amount: Number(o.ovrs_ord_psbl_amt ?? o.ord_psbl_frcr_amt ?? 0),
    raw: o,
  };
}

/** Cancel a resting US order (its order number from kisUsOrder). */
export async function kisUsCancel(o: { symbol: string; excd: string; odno: string; qty: number }): Promise<string> {
  const token = await kisAccessToken();
  const { cano, prod } = kisAccount();
  const mock = env("KIS_MOCK") === "true";
  const res = await json<KisOrderAnswer>(
    `${KIS_BASE}/uapi/overseas-stock/v1/trading/order-rvsecncl`,
    {
      method: "POST",
      headers: kisHeaders(token, mock ? "VTTT1004U" : "TTTT1004U"),
      body: JSON.stringify({
        CANO: cano,
        ACNT_PRDT_CD: prod,
        OVRS_EXCG_CD: ORDER_EXCHANGE[o.excd] ?? o.excd,
        PDNO: o.symbol,
        ORGN_ODNO: o.odno,
        RVSE_CNCL_DVSN_CD: "02",
        ORD_QTY: String(o.qty),
        OVRS_ORD_UNPR: "0",
        MGCO_APTM_ODNO: "",
        ORD_SVR_DVSN_CD: "0",
      }),
    },
  );
  return kisCancelAnswerOf(res);
}

/** KIS's message on a cancel; a refusal throws its message and code. */
export function kisCancelAnswerOf(res: KisOrderAnswer): string {
  if (res.rt_cd !== "0") throw new Error(`${(res.msg1 ?? "").trim()} (${res.msg_cd ?? "?"})`);
  return (res.msg1 ?? "").trim();
}
