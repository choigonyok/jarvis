/**
 * assets-svc owns reading the two brokerages.
 *
 * It used to be a route handler in the web service, which meant the Upbit and
 * KIS keys - keys that can place orders - sat in the environment of a Next.js
 * process that also serves the browser. They are here now, and the web service
 * proxies to this.
 *
 * Read-only toward the brokerages: balances and prices, never an order. The
 * one thing it writes is its own principal ledger.
 */
import { createServer } from "node:http";

import * as db from "./db.js";
import { PRINCIPAL_SINCE, buildPortfolio, invalidateVenues } from "./portfolio.js";

const PORT = Number(process.env.PORT ?? process.env.LISTEN_PORT ?? 8092);
const TOKEN = process.env.API_TOKEN ?? "";
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

async function rebuild(): Promise<string> {
  if (inflight) return inflight;

  const started = generation;
  inflight = (async () => {
    const portfolio = await buildPortfolio();
    const body = JSON.stringify(portfolio);
    if (started === generation) cached = { at: Date.now(), body };

    // 스냅샷은 곁일이다. 실패해도 자산 조회는 그대로 답해야 하므로 await 하지
    // 않고, 오류는 로그로만 남긴다.
    db.saveSnapshot(portfolio, TZ).catch((e: Error) => {
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

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", "http://localhost");

  if (req.method !== "GET" && !url.pathname.startsWith("/principal")) {
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

  if (TOKEN && req.headers.authorization !== `Bearer ${TOKEN}`) {
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

  if (url.pathname === "/portfolio") {
    void portfolioJson(url.searchParams.get("fresh") === "1")
      .then((body) => send(res, 200, body))
      .catch((e: Error) => {
        console.error("자산을 계산하지 못했습니다:", e.message);
        send(res, 502, { error: "자산을 불러오지 못했습니다." });
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
