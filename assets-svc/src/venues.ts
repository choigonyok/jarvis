import { createHash, createHmac, randomUUID } from "node:crypto";
import * as db from "./db.js";
import type { CashLine, Flow, Holding } from "./types.js";

/**
 * Reading the two brokerages.
 *
 * Moved here from the web service unchanged, comments included: the awkward
 * parts of these two APIs were learned the hard way and the notes are the
 * record of it. The keys can place orders, which is the main reason this file
 * is now in a service of its own rather than one import away from a browser
 * bundle.
 *
 * Read-only by design: balances and prices. Nothing here writes.
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
 * foreign cash, a page of closes per share, the gold account - used to leave
 * at once, so a single tap after the cache expired could trip it. Calls to KIS
 * now queue per key, a fixed gap apart, and a rate-limit answer is waited out
 * and asked again instead of becoming an error on screen.
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

type UpbitAccount = {
  currency: string;
  balance: string;
  locked: string;
  avg_buy_price: string;
  unit_currency: string;
};

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
  const set = new Set(
    all.map((m) => m.market).filter((m) => m.startsWith("KRW-")),
  );
  krwMarkets = { value: set, expires: Date.now() + 6 * 3600_000 };
  return set;
}

/** Coins held to be dollars. Shown and allocated as dollar cash. */
const DOLLAR_COINS = new Set(["USDT", "USDC"]);

export async function upbitHoldings(): Promise<{
  holdings: Holding[];
  cashKrw: number;
  cash: CashLine[];
  problems: string[];
}> {
  if (!env("UPBIT_ACCESS_KEY")) throw new Error("업비트 키가 설정되지 않았습니다.");

  const accounts = await json<UpbitAccount[]>("https://api.upbit.com/v1/accounts", {
    headers: { Authorization: `Bearer ${upbitToken()}` },
  });

  const coins = accounts.filter(
    (a) => a.currency !== "KRW" && Number(a.balance) + Number(a.locked) > 0,
  );
  const cashKrw = Number(
    accounts.find((a) => a.currency === "KRW")?.balance ?? 0,
  );
  const problems: string[] = [];
  const cash: CashLine[] = cashKrw > 0
    ? [{ id: "upbit:KRW", venue: "upbit", currency: "KRW", label: "업비트 원화", amount: cashKrw, valueKrw: cashKrw }]
    : [];
  if (coins.length === 0) return { holdings: [], cashKrw, cash, problems };

  const listed = await upbitKrwMarkets().catch(() => {
    problems.push("업비트 마켓 목록을 불러오지 못했습니다.");
    return new Set<string>();
  });

  const quotable = coins.filter((c) => listed.has(`KRW-${c.currency}`));
  const unquotable = coins.filter((c) => !listed.has(`KRW-${c.currency}`));

  const priceOf = new Map<string, number>();
  if (quotable.length > 0) {
    // One call for every price rather than one per coin - but only for
    // markets that exist, or the whole call fails.
    const markets = quotable.map((c) => `KRW-${c.currency}`).join(",");
    try {
      const tickers = await json<{ market: string; trade_price: number }[]>(
        `https://api.upbit.com/v1/ticker?markets=${encodeURIComponent(markets)}`,
      );
      for (const t of tickers) priceOf.set(t.market, t.trade_price);
    } catch (e) {
      // Silently falling back to cost would print a confident 0.0% for every
      // coin. Say it instead.
      problems.push(`업비트 시세를 불러오지 못했습니다: ${(e as Error).message}`);
    }
  }

  const dollars = coins.filter((c) => DOLLAR_COINS.has(c.currency));
  let dollarKrw = 0;
  for (const c of dollars) {
    const qty = Number(c.balance) + Number(c.locked);
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

  return { holdings, cashKrw: cashKrw + dollarKrw, cash, problems };
}

type UpbitTransfer = {
  uuid: string;
  currency: string;
  state: string;
  amount: string;
  transaction_type: string;
  created_at: string;
};

/**
 * Money that crossed Upbit's edge since `since` (YYYY-MM-DD), in won.
 *
 * Won deposits add to principal and won withdrawals take from it, by the
 * amount that actually left - the 1,000 won fee is a cost, so it shows up as
 * a loss rather than as money taken home. Upbit's own interest on idle won
 * (`internal`) is a return, not a deposit, and is left out. A coin sent in or
 * out is valued at that day's close; a coin with no won market cannot be,
 * and is named instead of guessed.
 */
export async function upbitFlows(
  since: string,
): Promise<{ flows: Flow[]; problems: string[] }> {
  if (!env("UPBIT_ACCESS_KEY")) return { flows: [], problems: [] };

  const list = async (path: "deposits" | "withdraws") => {
    const out: UpbitTransfer[] = [];
    // Newest first; stop at the first page that reaches back past `since`.
    for (let page = 1; page <= 20; page += 1) {
      const query = `limit=100&page=${page}&order_by=desc`;
      const rows = await json<UpbitTransfer[]>(
        `https://api.upbit.com/v1/${path}?${query}`,
        { headers: { Authorization: `Bearer ${upbitToken(query)}` } },
      );
      out.push(...rows);
      if (rows.length < 100 || rows.some((r) => r.created_at.slice(0, 10) < since)) break;
    }
    return out.filter((r) => r.created_at.slice(0, 10) >= since);
  };

  const [deposits, withdraws] = await Promise.all([list("deposits"), list("withdraws")]);
  const done = [
    ...deposits
      .filter((d) => d.state === "ACCEPTED" && d.transaction_type !== "internal")
      .map((d) => ({ row: d, sign: 1 })),
    ...withdraws.filter((w) => w.state === "DONE").map((w) => ({ row: w, sign: -1 })),
  ];

  const problems: string[] = [];
  const closes = new Map<string, Map<string, number>>();
  const flows: Flow[] = [];
  for (const { row, sign } of done) {
    const date = row.created_at.slice(0, 10);
    let krw = Number(row.amount);
    if (row.currency !== "KRW") {
      if (!closes.has(row.currency)) {
        const days = Math.ceil((Date.now() - Date.parse(since)) / 86_400_000) + 2;
        closes.set(row.currency, await upbitDailyCloses(row.currency, days));
      }
      const close = closes.get(row.currency)!.get(date);
      if (!close) {
        problems.push(`업비트: ${date} ${row.currency} 이체를 원화로 환산하지 못해 원금에서 빠졌습니다.`);
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
  return { flows, problems };
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
  const out = new Map<string, number>();
  let to = "";
  while (out.size < days) {
    const count = Math.min(200, days - out.size);
    const url =
      `https://api.upbit.com/v1/candles/days?market=KRW-${symbol}&count=${count}` +
      (to ? `&to=${encodeURIComponent(to)}` : "");
    const page = await json<
      { candle_date_time_kst: string; trade_price: number }[]
    >(url).catch(() => []);
    if (page.length === 0) break;
    for (const c of page) {
      out.set(c.candle_date_time_kst.slice(0, 10), c.trade_price);
    }
    to = page[page.length - 1].candle_date_time_kst;
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

type KisDomestic = {
  pdno: string;
  prdt_name: string;
  hldg_qty: string;
  pchs_avg_pric: string;
  prpr: string;
};

type KisOverseas = {
  ovrs_pdno: string;
  ovrs_item_name: string;
  ovrs_cblc_qty: string;
  pchs_avg_pric: string;
  now_pric2: string;
};

type KisCurrency = {
  crcy_cd: string;
  /** Settled foreign-currency deposit. */
  frcr_dncl_amt_2: string;
  /** Sold today, not yet settled - money that is already yours. */
  frcr_sll_amt_smtl: string;
  /** Bought today, not yet settled - money that is already spent. */
  frcr_buy_amt_smtl: string;
};

/**
 * Dollars sitting in the account, settled or about to be.
 *
 * The domestic balance only counts won. Without this, selling a share made
 * the total drop by the whole sale until settlement - and even after it, the
 * dollars never showed up at all.
 */
async function kisForeignCash(
  token: string,
  usdKrw: number,
): Promise<{ cashKrw: number; usd: number; problems: string[] }> {
  const { cano, prod } = kisAccount();
  const query = new URLSearchParams({
    CANO: cano,
    ACNT_PRDT_CD: prod,
    WCRC_FRCR_DVSN_CD: "02",
    NATN_CD: "000",
    TR_MKET_CD: "00",
    INQR_DVSN_CD: "00",
  });
  const body = await json<{ output2?: KisCurrency[] }>(
    `${KIS_BASE}/uapi/overseas-stock/v1/trading/inquire-present-balance?${query}`,
    { headers: kisHeaders(token, "CTRP6504R") },
  );

  let cashKrw = 0;
  let usd = 0;
  const unpriced: string[] = [];
  for (const row of body.output2 ?? []) {
    const amount =
      Number(row.frcr_dncl_amt_2) +
      Number(row.frcr_sll_amt_smtl) -
      Number(row.frcr_buy_amt_smtl);
    if (!(amount > 0)) continue;
    if (row.crcy_cd === "USD") {
      cashKrw += amount * usdKrw;
      usd += amount;
    }
    else unpriced.push(row.crcy_cd);
  }
  return {
    cashKrw,
    usd,
    problems: unpriced.length
      ? [`한국투자증권: 환율을 몰라 빠진 외화 예수금 ${unpriced.join(", ")}`]
      : [],
  };
}

export async function kisHoldings(usdKrw: number): Promise<{
  holdings: Holding[];
  cashKrw: number;
  cash: CashLine[];
  problems: string[];
}> {
  if (!env("KIS_APP_KEY")) throw new Error("한국투자증권 키가 설정되지 않았습니다.");

  const token = await kisAccessToken();
  const { cano, prod } = kisAccount();
  const holdings: Holding[] = [];

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
  const domestic = await json<{
    output1?: KisDomestic[];
    output2?: { dnca_tot_amt?: string }[];
  }>(
    `${KIS_BASE}/uapi/domestic-stock/v1/trading/inquire-balance?${domesticQuery}`,
    { headers: kisHeaders(token, "TTTC8434R") },
  );

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
  for (const exchange of ["NASD", "NYSE", "AMEX"]) {
    const query = new URLSearchParams({
      CANO: cano,
      ACNT_PRDT_CD: prod,
      OVRS_EXCG_CD: exchange,
      TR_CRCY_CD: "USD",
      CTX_AREA_FK200: "",
      CTX_AREA_NK200: "",
    });
    const overseas = await json<{ output1?: KisOverseas[] }>(
      `${KIS_BASE}/uapi/overseas-stock/v1/trading/inquire-balance?${query}`,
      { headers: kisHeaders(token, "TTTS3012R") },
    ).catch(() => ({ output1: [] as KisOverseas[] }));

    for (const row of overseas.output1 ?? []) {
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
        costKrw: qty * avg * usdKrw,
      });
    }
  }

  // A failed dollar lookup is named, not fatal: the shares still answered.
  const foreign = await kisForeignCash(token, usdKrw).catch((e: Error) => ({
    cashKrw: 0,
    usd: 0,
    problems: [`한국투자증권 외화 예수금: ${e.message}`],
  }));

  const cash: CashLine[] = [];
  if (cashKrw > 0) cash.push({ id: "kis:KRW", venue: "kis", currency: "KRW", label: "한국투자 원화", amount: cashKrw, valueKrw: cashKrw });
  if (foreign.usd > 0) {
    cash.push({ id: "kis:USD", venue: "kis", currency: "USD", label: "한국투자 달러", amount: foreign.usd, valueKrw: foreign.cashKrw });
  }

  return {
    holdings,
    cashKrw: cashKrw + foreign.cashKrw,
    cash,
    problems: foreign.problems,
  };
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
  const out = new Map<string, number>();
  if (!env("KIS_APP_KEY")) return out;

  const token = await kisAccessToken();
  let before = "";

  // The daily-price endpoint wants the exchange, and answers nothing for the
  // wrong one - KO is NYSE and came back empty under NAS, which silently left
  // it out of every window. The right one is found once and then kept.
  let exchange: string | null = null;

  for (let page = 0; page < 5 && out.size < days; page += 1) {
    const ask = async (excd: string) => {
      const query = new URLSearchParams({
        AUTH: "",
        EXCD: excd,
        SYMB: symbol,
        GUBN: "0",
        BYMD: before,
        MODP: "1",
      });
      const body = await json<{ output2?: { xymd: string; clos: string }[] }>(
        `${KIS_BASE}/uapi/overseas-price/v1/quotations/dailyprice?${query}`,
        { headers: kisHeaders(token, "HHDFS76240000") },
      ).catch(() => ({ output2: [] as { xymd: string; clos: string }[] }));
      return (body.output2 ?? []).filter((r) => r.xymd && Number(r.clos) > 0);
    };

    let rows: { xymd: string; clos: string }[] = [];
    if (exchange) {
      rows = await ask(exchange);
    } else {
      for (const excd of ["NAS", "NYS", "AMS"]) {
        rows = await ask(excd);
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

/**
 * One rate for the whole page, so a dollar holding and the total never
 * disagree. Upbit publishes the same USDT market everyone prices off; if it
 * is unreachable the page still adds up, just against a stale figure.
 */
export async function usdKrwRate(): Promise<number> {
  const fallback = 1350;
  try {
    const [t] = await json<{ trade_price: number }[]>(
      "https://api.upbit.com/v1/ticker?markets=KRW-USDT",
    );
    return t?.trade_price > 0 ? t.trade_price : fallback;
  } catch {
    return fallback;
  }
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
export async function kisGoldAccount(): Promise<{
  holdings: Holding[];
  cashKrw: number;
  cash: CashLine[];
  /** Won per gram, asked even when nothing is held, so gold can be planned. */
  gramKrw: number | null;
} | null> {
  const app = goldApp();
  if (!app.key) return null;

  const token = await kisAccessToken(app);
  const query = new URLSearchParams({
    CANO: env("KIS_GOLD_ACCOUNT_NO").replace(/-/g, "").slice(0, 8),
    ACNT_PRDT_CD: env("KIS_GOLD_ACCOUNT_PRODUCT") || "01",
    INQR_DVSN_1: "",
    BSPR_BF_DT_APLY_YN: "",
  });
  const body = await json<{
    rt_cd: string;
    msg1?: string;
    output2?: { tot_asst_amt?: string; dncl_amt?: string; pchs_amt_smtl?: string };
  }>(
    `${KIS_BASE}/uapi/domestic-stock/v1/trading/inquire-account-balance?${query}`,
    { headers: kisHeaders(token, "CTRP6548R", app) },
  );
  if (body.rt_cd !== "0") throw new Error(body.msg1?.trim() || "잔고 조회 실패");

  const total = Number(body.output2?.tot_asst_amt ?? 0);
  const cashKrw = Number(body.output2?.dncl_amt ?? 0);
  const valueKrw = Math.max(0, total - cashKrw);
  const cash: CashLine[] = cashKrw > 0
    ? [{ id: "gold:KRW", venue: "gold", currency: "KRW", label: "금현물 계좌 원화", amount: cashKrw, valueKrw: cashKrw }]
    : [];

  const price = await json<{ output?: { stck_prpr?: string } }>(
    `${KIS_BASE}/uapi/domestic-stock/v1/quotations/inquire-price?${new URLSearchParams({
      FID_COND_MRKT_DIV_CODE: "J",
      FID_INPUT_ISCD: KRX_GOLD,
    })}`,
    { headers: kisHeaders(token, "FHKST01010100", app) },
  ).then((b) => Number(b.output?.stck_prpr ?? 0)).catch(() => 0);
  const gramKrw = price > 0 ? price : null;
  if (valueKrw < 1) return { holdings: [], cashKrw, cash, gramKrw };

  const grams = price > 0 ? valueKrw / price : 0;
  // A zero purchase total means KIS did not say - fall back to value, which
  // reads as flat rather than as a 100% gain.
  const costKrw = Number(body.output2?.pchs_amt_smtl ?? 0) || valueKrw;
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

/** KRX gold closes per gram, YYYY-MM-DD -> price, a hundred days a call. */
export async function kisGoldDailyCloses(days: number): Promise<Map<string, number>> {
  const out = new Map<string, number>();
  const app = goldApp();
  if (!app.key) return out;
  const token = await kisAccessToken(app);
  const ymd = (d: Date) => d.toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" }).replace(/-/g, "");

  let end = new Date();
  for (let page = 0; page < 5 && out.size < days; page += 1) {
    const start = new Date(end.getTime() - 140 * 86_400_000);
    const body = await json<{ output2?: { stck_bsop_date: string; stck_clpr: string }[] }>(
      `${KIS_BASE}/uapi/domestic-stock/v1/quotations/inquire-daily-itemchartprice?${new URLSearchParams({
        FID_COND_MRKT_DIV_CODE: "J",
        FID_INPUT_ISCD: KRX_GOLD,
        FID_INPUT_DATE_1: ymd(start),
        FID_INPUT_DATE_2: ymd(end),
        FID_PERIOD_DIV_CODE: "D",
        FID_ORG_ADJ_PRC: "0",
      })}`,
      { headers: kisHeaders(token, "FHKST03010100", app) },
    ).catch(() => ({ output2: [] }));
    const rows = (body.output2 ?? []).filter((r) => r.stck_bsop_date && Number(r.stck_clpr) > 0);
    if (rows.length === 0) break;
    for (const r of rows) {
      const d = r.stck_bsop_date;
      out.set(`${d.slice(0, 4)}-${d.slice(4, 6)}-${d.slice(6, 8)}`, Number(r.stck_clpr));
    }
    end = new Date(start.getTime() - 86_400_000);
  }
  return out;
}
