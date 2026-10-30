/**
 * assets-svc owns reading the two brokerages.
 *
 * It used to be a route handler in the web service, which meant the Upbit and
 * KIS keys - keys that can place orders - sat in the environment of a Next.js
 * process that also serves the browser. They are here now, and the web service
 * proxies to this.
 *
 * Toward the brokerages it reads - balances and prices - and places orders
 * only through /orders, the orders a person approved (orders.ts). It writes
 * its own principal ledger and the record of every order.
 */
import { timingSafeEqual } from "node:crypto";
import { createServer } from "node:http";

import * as db from "./db.js";
import type { Allocation, Holding, Outflow, OutflowUse, Portfolio } from "./types.js";
import { announce, halfRead } from "./notify.js";
import { PARKED_DAYS, tolerance } from "./outflows.js";
import { PRINCIPAL_SINCE, buildPortfolio, invalidateVenues } from "./portfolio.js";
import { placeOrders, requestsOf, type OrderRequest } from "./orders.js";
import { affordable, cancelStanding, nyClock, standingPass } from "./standing.js";
import { depositPlan, rebalancePlan } from "./trade-plan.js";

const PORT = Number(process.env.PORT ?? process.env.LISTEN_PORT ?? 8092);
const TOKEN = process.env.API_TOKEN ?? "";
// An empty token would open the brokerage balances to anything on the
// network. Only a local trial may run so, and has to say it.
if (!TOKEN && process.env.API_TOKEN_OPTIONAL !== "true") {
  console.error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true");
  process.exit(1);
}

/** Whether the header carries the token, compared in constant time. */
function presented(header: string | undefined): boolean {
  if (!TOKEN) return true; // API_TOKEN_OPTIONAL: a local trial
  const want = Buffer.from(`Bearer ${TOKEN}`);
  const got = Buffer.from(header ?? "");
  return got.length === want.length && timingSafeEqual(got, want);
}
const TZ = process.env.TZ ?? "Asia/Seoul";

db.connect(process.env.DATABASE_URL);

/**
 * The assembled portfolio. Each venue keeps its own freshness underneath
 * (portfolio.ts, freshness.ts) and past closes live in the database, so a
 * rebuild is cheap; this only stops two tabs from building it twice.
 *
 * Past FRESH_MS the last answer is still served at once - marked stale - and
 * a rebuild starts behind it (stale-while-revalidate). The page shows the
 * time the numbers are from and asks again a moment later, so nobody waits
 * on the brokerages to see their balance.
 */
const FRESH_MS = 10_000;
let cached: { at: number; body: string } | null = null;
let inflight: Promise<string> | null = null;
// Bumped by a ledger write, so a build that started before it does not put
// the old principal back into the cache.
let generation = 0;

async function portfolioJson(force = false): Promise<string> {
  if (force) {
    invalidateVenues();
    cached = null;
  }
  if (cached) {
    if (Date.now() - cached.at < FRESH_MS) return cached.body;
    rebuild().catch((e: Error) => console.error("자산을 다시 계산하지 못했습니다:", e.message));
    return `{"stale":true,${cached.body.slice(1)}`;
  }
  return rebuild();
}

/**
 * The portfolio as of at most `maxAgeMs` ago, built afresh when the one held
 * is older. For what asks on a timer (the outflow card, the 가계부's feed):
 * the brokerages are not read again for each of them, and an answer clears
 * the cache, so what follows it is fresh.
 */
async function portfolioWithin(maxAgeMs: number): Promise<string> {
  if (cached && Date.now() - cached.at < maxAgeMs) return cached.body;
  return rebuild();
}

/** The day the last build was saved as a snapshot, Seoul time. */
let builtOn = "";
const seoulToday = () => new Date().toLocaleDateString("sv-SE", { timeZone: TZ });

async function rebuild(): Promise<string> {
  if (inflight) return inflight;

  const started = generation;
  inflight = (async () => {
    const portfolio = await buildPortfolio();
    const body = JSON.stringify(portfolio);
    if (started === generation) cached = { at: Date.now(), body };

    // 스냅샷은 곁일이다. 실패해도 자산 조회는 그대로 답해야 하므로 await 하지
    // 않고, 오류는 로그로만 남긴다.
    // 알릴 만한 것(비중 이탈, 입출금 감지, 공제 임박, 하루 요약)은 output-svc 로.
    announce(portfolio);

    db.saveSnapshot(portfolio, TZ)
      .then(() => {
        builtOn = seoulToday();
      })
      .catch((e: Error) => {
        console.error("스냅샷을 저장하지 못했습니다:", e.message);
      });
    return body;
  })();

  try {
    return await inflight;
  } finally {
    inflight = null;
  }
}

function send(res: import("node:http").ServerResponse, code: number, body: unknown) {
  const payload = typeof body === "string" ? body : JSON.stringify(body);
  res.writeHead(code, {
    "content-type": "application/json; charset=utf-8",
    "cache-control": "no-store",
  });
  res.end(payload);
}

async function readBody(req: import("node:http").IncomingMessage): Promise<unknown> {
  let raw = "";
  for await (const chunk of req) {
    raw += chunk;
    if (raw.length > 10_000) throw new Error("too large");
  }
  return JSON.parse(raw || "{}");
}

/**
 * The principal ledger: POST adds a row, DELETE /principal/<id> removes one.
 * Either one invalidates the cached portfolio, since its return is measured
 * against this.
 */
async function principal(
  req: import("node:http").IncomingMessage,
  res: import("node:http").ServerResponse,
  url: URL,
) {
  if (req.method === "POST" && url.pathname === "/principal") {
    const body = (await readBody(req).catch(() => null)) as {
      date?: unknown; venue?: unknown; amountKrw?: unknown; memo?: unknown;
    } | null;
    const date = typeof body?.date === "string" ? body.date : "";
    const venue = body?.venue;
    const amountKrw = Number(body?.amountKrw);
    if (
      !/^\d{4}-\d{2}-\d{2}$/.test(date) ||
      date < PRINCIPAL_SINCE ||
      (venue !== "upbit" && venue !== "kis" && venue !== "gold" && venue !== "other") ||
      !Number.isFinite(amountKrw) ||
      amountKrw === 0
    ) {
      send(res, 400, { error: `날짜(${PRINCIPAL_SINCE} 이후), 계좌, 0이 아닌 금액이 필요합니다.` });
      return;
    }
    await db.addFlow({
      date,
      venue,
      amountKrw: Math.round(amountKrw),
      memo: typeof body?.memo === "string" ? body.memo.slice(0, 100) : "",
    });
    cached = null;
    generation += 1;
    send(res, 201, { ok: true });
    return;
  }

  const match = url.pathname.match(/^\/principal\/(\d+)$/);
  if (req.method === "DELETE" && match) {
    const gone = await db.deleteFlow(Number(match[1]));
    cached = null;
    generation += 1;
    send(res, gone ? 200 : 404, gone ? { ok: true } : { error: "그런 기록이 없습니다." });
    return;
  }

  send(res, 405, { error: "지원하지 않는 요청입니다." });
}

const OUTFLOW_USES: readonly OutflowUse[] = ["spend", "card", "saved", "parked", "transfer"];
/** How old a portfolio the outflow routes may answer from. */
const OUTFLOWS_MAX_AGE_MS = 10 * 60_000;

/** Every withdrawal and where it went, as of the portfolio; null when that could not be read. */
async function currentOutflows(): Promise<Outflow[] | null> {
  const p = JSON.parse(await portfolioWithin(OUTFLOWS_MAX_AGE_MS)) as Portfolio;
  return p.principal?.outflows ?? null;
}

/**
 * Where withdrawals went (outflows.ts). GET /outflows lists them all; POST
 * /outflows/answer says where part of one went ({key, flowKrw, use,
 * amountKrw, category?, memo?} - the card's answer); DELETE
 * /outflows/answer?key= forgets what was said about one, so it is asked
 * again. An answer changes no figure, but the next look must show it.
 */
async function outflows(
  req: import("node:http").IncomingMessage,
  res: import("node:http").ServerResponse,
  url: URL,
) {
  if (req.method === "GET" && url.pathname === "/outflows") {
    const list = await currentOutflows();
    if (!list) {
      send(res, 503, { error: "입출금 기록이나 출금 용도 기록을 읽지 못했습니다." });
      return;
    }
    send(res, 200, { since: PRINCIPAL_SINCE, outflows: list });
    return;
  }
  if (url.pathname !== "/outflows/answer") {
    send(res, 404, { error: "그런 경로가 없습니다." });
    return;
  }
  if (req.method === "DELETE") {
    const key = url.searchParams.get("key") ?? "";
    if (!key) {
      send(res, 400, { error: "어느 출금인지(key) 알려 주세요." });
      return;
    }
    const removed = await db.clearOutflow(key);
    cached = null;
    generation += 1;
    send(res, 200, { ok: true, removed });
    return;
  }
  if (req.method !== "POST") {
    send(res, 405, { error: "지원하지 않는 요청입니다." });
    return;
  }

  const body = (await readBody(req).catch(() => null)) as Record<string, unknown> | null;
  const key = typeof body?.key === "string" ? body.key : "";
  const use = body?.use as OutflowUse;
  const flowKrw = Number(body?.flowKrw);
  const amountKrw = Math.round(Number(body?.amountKrw));
  const category = typeof body?.category === "string" ? body.category.trim() : "";
  const memo = typeof body?.memo === "string" ? body.memo.trim() : "";
  if (!key || !OUTFLOW_USES.includes(use) || !Number.isFinite(flowKrw) || !Number.isFinite(amountKrw) || amountKrw <= 0) {
    send(res, 400, { error: "출금(key), 출금액(flowKrw), 용도, 0보다 큰 금액이 필요합니다." });
    return;
  }
  if (use === "spend" && !category) {
    send(res, 400, { error: "지출이면 가계부 분류를 골라 주세요." });
    return;
  }
  if ([...category].length > 20 || [...memo].length > 40) {
    send(res, 400, { error: "분류는 20자, 어디에 썼는지는 40자까지예요." });
    return;
  }
  const list = await currentOutflows();
  if (!list) {
    send(res, 503, { error: "입출금 기록을 읽지 못해 답을 적지 못했습니다." });
    return;
  }
  const out = list.find((o) => o.key === key);
  if (!out) {
    send(res, 404, { error: "그런 출금이 없습니다." });
    return;
  }
  if (Math.abs(out.amountKrw - flowKrw) >= 1) {
    send(res, 409, { error: `출금액이 ₩${out.amountKrw.toLocaleString("ko-KR")}(으)로 바뀌었어요. 다시 물을게요.` });
    return;
  }
  try {
    await db.answerOutflow({
      key,
      flowKrw: out.amountKrw,
      use,
      amountKrw,
      category: use === "spend" ? category : null,
      memo,
      maxKrw: out.amountKrw + tolerance(out.amountKrw),
      parkedDays: PARKED_DAYS,
    });
  } catch (e) {
    const message = (e as Error).message;
    if (message.startsWith("답한 금액")) {
      send(res, 400, { error: message });
      return;
    }
    throw e;
  }
  cached = null;
  generation += 1;
  send(res, 200, { ok: true });
}

/** What a request may ask to have placed - only the fields an order has. */
function requestOf(raw: unknown): OrderRequest {
  const o = (raw ?? {}) as Record<string, unknown>;
  return {
    venue: o.venue as OrderRequest["venue"],
    symbol: String(o.symbol ?? ""),
    side: o.side as OrderRequest["side"],
    units: o.units === null || o.units === undefined ? null : Number(o.units),
    amountKrw: Number(o.amountKrw),
  };
}

/**
 * GET /orders: the latest orders placed. POST /orders: place these
 * ({orders, reason, planId?, dryRun?}). POST /orders/rebalance: the current
 * rebalance plan, checked (dryRun) or placed. Placing reads the brokerages
 * afresh first - what is held now, not ten seconds ago - and the next look
 * at the portfolio is fresh too.
 */
async function orders(
  req: import("node:http").IncomingMessage,
  res: import("node:http").ServerResponse,
  url: URL,
) {
  if (req.method === "GET" && url.pathname === "/orders") {
    const limit = Number(url.searchParams.get("limit") ?? 20);
    send(res, 200, { orders: await db.recentOrders(Number.isFinite(limit) && limit > 0 ? Math.min(limit, 100) : 20) });
    return;
  }
  if (req.method !== "POST") {
    send(res, 405, { error: "지원하지 않는 요청입니다." });
    return;
  }
  const body = (await readBody(req).catch(() => null)) as {
    orders?: unknown[]; reason?: unknown; planId?: unknown; dryRun?: unknown;
  } | null;
  const dryRun = body?.dryRun === true;
  const p = JSON.parse(await portfolioJson(!dryRun)) as {
    holdings: Holding[]; allocation?: Allocation; goldGramKrw: number | null; problems?: string[];
  };
  const placed = () => {
    if (dryRun) return;
    invalidateVenues();
    cached = null;
    generation += 1;
  };

  if (url.pathname === "/orders") {
    const list = Array.isArray(body?.orders) ? body.orders : [];
    if (list.length === 0 || list.length > 12) {
      send(res, 400, { error: "주문이 1~12개 필요합니다." });
      return;
    }
    const out = await placeOrders(list.map(requestOf), p.holdings, {
      reason: typeof body?.reason === "string" ? body.reason.slice(0, 40) : "manual",
      planId: typeof body?.planId === "string" ? body.planId.slice(0, 40) : undefined,
      dryRun,
    });
    placed();
    send(res, 200, out);
    return;
  }

  if (url.pathname === "/orders/rebalance") {
    if (!p.allocation) {
      send(res, 503, { error: "목표 비중을 계산하지 못했습니다." });
      return;
    }
    // A plan from half the accounts would sell what is held to buy what is
    // only unread.
    if (halfRead(p.problems)) {
      send(res, 503, { error: "잔고를 다 읽지 못해 리밸런싱 계획을 세우지 않습니다: " + p.problems!.join(" · ") });
      return;
    }
    const plan = rebalancePlan(p.allocation, p.holdings, { goldGramKrw: p.goldGramKrw });
    const requests = requestsOf(plan);
    const out = requests.length
      ? await placeOrders(requests, p.holdings, { reason: "rebalance", dryRun })
      : { planId: null, results: [] };
    placed();
    send(res, 200, { ...out, unpicked: plan.unpicked, buckets: plan.buckets, inBand: p.allocation.rows.every((r) => r.inBand) });
    return;
  }

  send(res, 404, { error: "그런 경로가 없습니다." });
}

/** What is held now, for a standing order's fill: the portfolio as last built. */
async function holdingsNow(): Promise<Holding[]> {
  return (JSON.parse(await portfolioJson()) as { holdings: Holding[] }).holdings;
}

/**
 * GET /standing: the standing orders. POST /standing: a new one
 * ({symbol, side, units, limitPrice | "afford", expiresOn?, reason?}) -
 * "afford" is the highest price the dollars cover. DELETE /standing/<id>:
 * cancel it, and today's order at the broker if it is out.
 */
async function standing(
  req: import("node:http").IncomingMessage,
  res: import("node:http").ServerResponse,
  url: URL,
) {
  if (req.method === "GET") {
    send(res, 200, { standing: await db.standingOrders(30), market: nyClock() });
    return;
  }
  const match = url.pathname.match(/^\/standing\/(\d+)$/);
  if (req.method === "DELETE" && match) {
    const o = (await db.standingOrders(100)).find((x) => x.id === Number(match[1]));
    if (!o || o.status !== "active") {
      send(res, 404, { error: "진행 중인 대기 주문이 아닙니다." });
      return;
    }
    send(res, 200, { ok: true, message: await cancelStanding(o) });
    return;
  }
  if (req.method !== "POST" || url.pathname !== "/standing") {
    send(res, 405, { error: "지원하지 않는 요청입니다." });
    return;
  }
  const body = (await readBody(req).catch(() => null)) as {
    symbol?: unknown; side?: unknown; units?: unknown; limitPrice?: unknown; expiresOn?: unknown; reason?: unknown;
  } | null;
  const symbol = String(body?.symbol ?? "").toUpperCase();
  const side = body?.side === "sell" ? "sell" : body?.side === "buy" ? "buy" : null;
  const units = Number(body?.units);
  const expiresOn = typeof body?.expiresOn === "string" && /^\d{4}-\d{2}-\d{2}$/.test(body.expiresOn) ? body.expiresOn : null;
  if (!/^[A-Z.]{1,10}$/.test(symbol) || !side || !Number.isInteger(units) || units < 1 || units > 1000) {
    send(res, 400, { error: "종목(미국 주식), 매수·매도, 1 이상의 주 수가 필요합니다." });
    return;
  }
  const p = JSON.parse(await portfolioJson(true)) as { holdings: Holding[]; usdKrw: number };
  const held = p.holdings.find((h) => h.venue === "kis" && h.symbol === symbol)?.quantity ?? 0;
  if (side === "sell" && units > Math.floor(held + 1e-6)) {
    send(res, 400, { error: `${Math.floor(held + 1e-6)}주까지만 팔 수 있어요.` });
    return;
  }
  let limitPrice = Number(body?.limitPrice);
  let note = "";
  if (body?.limitPrice === "afford") {
    const a = await affordable(symbol, units);
    if (a.price === null) {
      send(res, 400, { error: `지금 달러로는 ${symbol} ${units}주를 살 수 없어요 (지금 $${a.last.toFixed(2)}).` });
      return;
    }
    limitPrice = a.price;
    note = `지금 달러로 살 수 있는 가장 높은 가격 $${a.price.toFixed(2)} (지금 $${a.last.toFixed(2)})`;
  }
  if (!Number.isFinite(limitPrice) || limitPrice <= 0) {
    send(res, 400, { error: "지정가(달러)가 필요합니다." });
    return;
  }
  if (units * limitPrice * (p.usdKrw || 1400) > Number(process.env.ORDER_MAX_KRW ?? 1_500_000)) {
    send(res, 400, { error: "한 번에 주문할 수 있는 금액을 넘어요." });
    return;
  }
  const made = await db.addStanding({
    symbol, side, units, limitPrice: Math.round(limitPrice * 100) / 100, heldBefore: held,
    reason: typeof body?.reason === "string" ? body.reason.slice(0, 60) : "", expiresOn,
  });
  // In a session now: out at once, not at the next pass.
  await standingPass(holdingsNow, new Date(), made.id);
  const now = (await db.standingOrders(100)).find((x) => x.id === made.id) ?? made;
  send(res, 201, { standing: now, note });
}

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", "http://localhost");
  const writes =
    url.pathname.startsWith("/principal") ||
    url.pathname.startsWith("/orders") ||
    url.pathname.startsWith("/standing") ||
    url.pathname.startsWith("/outflows");

  if (req.method !== "GET" && !writes) {
    send(res, 405, { error: "GET 만 받습니다." });
    return;
  }

  // 데이터베이스는 이 서비스의 필수 조건이 아니다. 증권사만 읽어도 자산은
  // 답할 수 있으므로, 연결 상태는 알려주되 healthy 를 가로막지 않는다.
  if (url.pathname === "/health") {
    void db.healthy().then((ok) => {
      send(res, 200, {
        status: "ok",
        database: db.connected() ? (ok ? "ok" : "unreachable") : "none",
      });
    });
    return;
  }

  if (!presented(req.headers.authorization)) {
    send(res, 401, { error: "인증이 필요합니다." });
    return;
  }

  if (url.pathname.startsWith("/principal")) {
    void principal(req, res, url).catch((e: Error) => {
      console.error("원금 기록을 바꾸지 못했습니다:", e.message);
      send(res, 503, { error: "원금 기록을 바꾸지 못했습니다." });
    });
    return;
  }

  if (url.pathname.startsWith("/outflows")) {
    void outflows(req, res, url).catch((e: Error) => {
      console.error("출금 용도를 처리하지 못했습니다:", e.message);
      send(res, 503, { error: "출금 용도를 처리하지 못했습니다." });
    });
    return;
  }

  if (url.pathname.startsWith("/standing")) {
    void standing(req, res, url).catch((e: Error) => {
      console.error("대기 주문을 처리하지 못했습니다:", e.message);
      send(res, 502, { error: "대기 주문을 처리하지 못했습니다: " + e.message.slice(0, 200) });
    });
    return;
  }

  if (url.pathname === "/orders" || url.pathname === "/orders/rebalance") {
    void orders(req, res, url).catch((e: Error) => {
      console.error("주문을 처리하지 못했습니다:", e.message);
      send(res, 502, { error: "주문을 처리하지 못했습니다." });
    });
    return;
  }

  if (url.pathname === "/portfolio") {
    void portfolioJson(url.searchParams.get("fresh") === "1")
      .then((body) => send(res, 200, body))
      .catch((e: Error) => {
        console.error("자산을 계산하지 못했습니다:", e.message);
        send(res, 502, { error: "자산을 불러오지 못했습니다." });
      });
    return;
  }

  // 리밸런싱·입금 매매 계획. 가격·보유는 화면이 본 것과 같은 캐시에서 온다.
  if (url.pathname === "/plan") {
    const deposit = Number(url.searchParams.get("deposit") ?? 0);
    void portfolioJson()
      .then((body) => {
        const p = JSON.parse(body) as { holdings: Holding[]; allocation?: Allocation; goldGramKrw: number | null; problems?: string[] };
        if (!p.allocation) return send(res, 503, { error: "목표 비중을 계산하지 못했습니다." });
        if (halfRead(p.problems)) return send(res, 503, { error: "잔고를 다 읽지 못해 매매 계획을 세우지 않습니다: " + p.problems!.join(" · ") });
        const opts = { goldGramKrw: p.goldGramKrw };
        send(res, 200, {
          rebalance: rebalancePlan(p.allocation, p.holdings, opts),
          deposit: Number.isFinite(deposit) && deposit > 0 ? depositPlan(p.allocation, p.holdings, deposit, opts) : null,
        });
      })
      .catch((e: Error) => {
        console.error("매매 계획을 세우지 못했습니다:", e.message);
        send(res, 502, { error: "매매 계획을 세우지 못했습니다." });
      });
    return;
  }

  // 기록된 일별 총액. 스냅샷이 쌓이기 전에는 비어 있다.
  if (url.pathname === "/history") {
    const days = Number(url.searchParams.get("days") ?? 90);
    void db
      .history(Number.isFinite(days) && days > 0 ? Math.min(days, 3650) : 90)
      .then((rows) => send(res, 200, { snapshots: rows }))
      .catch((e: Error) => {
        console.error("스냅샷 이력을 읽지 못했습니다:", e.message);
        send(res, 503, { error: "스냅샷 이력을 읽지 못했습니다." });
      });
    return;
  }

  send(res, 404, { error: "그런 경로가 없습니다." });
});

server.listen(PORT, () => {
  console.log(`assets-svc 가 듣기 시작했습니다: :${PORT}`);
});

// 자산 변화는 하루 한 장의 스냅샷으로 기록된다. 스냅샷은 누가 자산을 볼 때마다
// 찍히는데, 아무도 안 본 날은 비게 된다. 그래서 한 시간마다 오늘 것이 있는지
// 보고 없으면 직접 계산해 남긴다. 한투 입출금 감지도 같은 계산에서 돌아서,
// 이게 있어야 하루 단위로 정확하다.
const daily = setInterval(() => {
  if (builtOn === seoulToday()) return;
  rebuild().catch((e: Error) => console.error("오늘 스냅샷을 남기지 못했습니다:", e.message));
}, 60 * 60_000);
daily.unref();
setTimeout(() => {
  if (builtOn !== seoulToday()) rebuild().catch(() => {});
}, 60_000).unref();

// Standing orders: every two minutes, out again in each session until filled.
const standingTimer = setInterval(() => {
  standingPass(holdingsNow).catch((e: Error) => console.error("대기 주문을 돌리지 못했습니다:", e.message));
}, 2 * 60_000);
standingTimer.unref();

for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => {
    console.log("assets-svc 를 내립니다");
    server.close(() => {
      void db.close().then(() => process.exit(0));
    });
    // 열린 연결이 남아 있어도 오래 붙잡지 않는다.
    setTimeout(() => process.exit(0), 5_000).unref();
  });
}
