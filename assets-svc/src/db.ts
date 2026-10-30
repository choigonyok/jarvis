import { Pool } from "pg";

import type { Flow, OutflowAnswer, OutflowUse, Portfolio } from "./types.js";

/**
 * Daily snapshots.
 *
 * This service can answer without Postgres - the brokerages are the source of
 * truth for what is held right now, and the windowed changes are computed by
 * re-pricing today's basket against past closes. So the database is not on the
 * critical path, and a snapshot that fails to write must not take the page
 * down with it.
 *
 * What snapshots add is the one thing re-pricing cannot know: what was
 * actually held back then. The two answer different questions, so both are
 * kept.
 */

let pool: Pool | null = null;

export function connect(dsn: string | undefined): void {
  if (!dsn) return;
  pool = new Pool({
    connectionString: dsn,
    // 개인용 서비스에 커넥션이 많을 이유가 없다.
    max: 3,
    idleTimeoutMillis: 5 * 60_000,
    // 스냅샷은 부가 작업이다. 데이터베이스가 느릴 때 자산 조회까지 같이
    // 느려지면 안 된다.
    connectionTimeoutMillis: 5_000,
  });
  // 풀은 유휴 커넥션이 서버측에서 끊길 때 error 를 올리고, 핸들러가 없으면
  // 프로세스가 죽는다. 증권사 조회는 데이터베이스와 무관하게 되어야 한다.
  pool.on("error", (err) => {
    console.error("postgres 풀 오류(무시하고 계속):", err.message);
  });
}

export function connected(): boolean {
  return pool !== null;
}

export async function healthy(): Promise<boolean> {
  if (!pool) return false;
  try {
    await pool.query("select 1");
    return true;
  } catch {
    return false;
  }
}

/**
 * Write today's snapshot, replacing it if one was already taken today.
 *
 * One per day is what makes a baseline: several rows for one day would leave
 * "what did it close at" without an answer. The row is upserted rather than
 * appended for that reason, and the holdings under it are replaced wholesale -
 * a position sold yesterday still sitting in today's snapshot would make it a
 * snapshot of no particular day.
 */
export async function saveSnapshot(p: Portfolio, tz: string): Promise<void> {
  if (!pool) return;

  const onDate = new Date(p.at).toLocaleDateString("sv-SE", { timeZone: tz });
  const client = await pool.connect();
  try {
    await client.query("begin");
    const { rows } = await client.query<{ upsert_snapshot: string }>(
      "select upsert_snapshot($1::date, $2, $3, $4, $5, $6::text[])",
      [onDate, p.totalKrw, p.cashKrw, p.costKrw, p.usdKrw, p.problems],
    );
    const snapshotId = rows[0]?.upsert_snapshot;

    for (const h of p.holdings) {
      await client.query(
        `insert into portfolio_holdings
           (snapshot_id, venue, kind, symbol, name, quantity, avg_price, price,
            currency, value_krw, cost_krw)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
         on conflict (snapshot_id, venue, symbol) do update set
           quantity = excluded.quantity, avg_price = excluded.avg_price,
           price = excluded.price, value_krw = excluded.value_krw,
           cost_krw = excluded.cost_krw`,
        [snapshotId, h.venue, h.kind, h.symbol, h.name, h.quantity,
         h.avgPrice, h.price, h.currency, h.valueKrw, h.costKrw],
      );
    }
    await client.query("commit");
  } catch (e) {
    await client.query("rollback").catch(() => undefined);
    throw e;
  } finally {
    client.release();
  }
}

export type SnapshotRow = {
  date: string;
  totalKrw: number;
  cashKrw: number;
  costKrw: number;
};

/**
 * The recorded history, oldest first. Empty until snapshots accumulate.
 *
 * The date comes back as text rather than as a `date`, on purpose. node-postgres
 * turns a `date` into a JS Date at *local* midnight, and formatting that with
 * toISOString() converts to UTC - which moves KST midnight to 15:00 the day
 * before, so every row reported the previous day. Letting Postgres format it
 * removes the round trip that created the bug.
 */
export async function history(days: number): Promise<SnapshotRow[]> {
  if (!pool) return [];
  const { rows } = await pool.query<{
    on_date: string; total_krw: string; cash_krw: string; cost_krw: string;
  }>(
    `select to_char(on_date, 'YYYY-MM-DD') as on_date,
            total_krw, cash_krw, cost_krw
       from portfolio_snapshots
      where on_date > current_date - $1::int
      order by on_date`,
    [days],
  );
  return rows.map((r) => ({
    date: r.on_date,
    totalKrw: Number(r.total_krw),
    cashKrw: Number(r.cash_krw),
    costKrw: Number(r.cost_krw),
  }));
}

/**
 * Stored flows on or after `since`, oldest first: hand-recorded ones, and the
 * KIS and gold-account deposits found from their cash (source 'auto', see kis-flows.ts).
 */
export async function manualFlows(since: string): Promise<Flow[]> {
  if (!pool) return [];
  const { rows } = await pool.query<{
    id: string; on_date: string; venue: Flow["venue"]; amount_krw: string; memo: string; source: Flow["source"];
  }>(
    `select id, to_char(on_date, 'YYYY-MM-DD') as on_date, venue, amount_krw, memo, source
       from principal_flows
      where on_date >= $1::date
      order by on_date, id`,
    [since],
  );
  return rows.map((r) => ({
    id: `manual:${r.id}`,
    date: r.on_date,
    venue: r.venue,
    amountKrw: Number(r.amount_krw),
    memo: r.memo,
    source: r.source,
  }));
}

export async function addFlow(f: {
  date: string;
  venue: Flow["venue"];
  amountKrw: number;
  memo: string;
}): Promise<void> {
  if (!pool) throw new Error("데이터베이스가 연결되지 않았습니다.");
  await pool.query(
    `insert into principal_flows (on_date, venue, amount_krw, memo)
     values ($1::date, $2, $3, $4)`,
    [f.date, f.venue, f.amountKrw, f.memo],
  );
}

export async function deleteFlow(id: number): Promise<boolean> {
  if (!pool) throw new Error("데이터베이스가 연결되지 않았습니다.");
  // Only what was written by hand: a found deposit would come back on the next look anyway.
  const { rowCount } = await pool.query("delete from principal_flows where id = $1 and source = 'manual'", [id]);
  return (rowCount ?? 0) > 0;
}

/**
 * Every answer about where a withdrawal went (outflow_uses). Null without a
 * database: then nothing can be told answered from unanswered, and nothing
 * should be asked.
 */
export async function outflowAnswers(): Promise<OutflowAnswer[] | null> {
  if (!pool) return null;
  const { rows } = await pool.query<{
    flow_key: string; flow_krw: string; use: OutflowUse; amount_krw: string; category: string | null; memo: string; decided_at: Date;
  }>(`select flow_key, flow_krw, use, amount_krw, category, memo, decided_at from outflow_uses order by decided_at, flow_key, use`);
  return rows.map((r) => ({
    key: r.flow_key,
    flowKrw: Number(r.flow_krw),
    use: r.use,
    amountKrw: Number(r.amount_krw),
    category: r.category,
    memo: r.memo,
    decidedAt: r.decided_at.toISOString(),
  }));
}

/**
 * Add an answer about part of a withdrawal: `amountKrw` of it went to `use`.
 * The same use again adds to it - the card asks about what is left, part by
 * part. Answers about another amount (the withdrawal changed since) and money
 * parked past `parkedDays` are cleared first: this answer is what replaces
 * them. More answered than `maxKrw` is refused.
 */
export async function answerOutflow(a: {
  key: string;
  flowKrw: number;
  use: OutflowUse;
  amountKrw: number;
  category: string | null;
  memo: string;
  maxKrw: number;
  parkedDays: number;
}): Promise<void> {
  if (!pool) throw new Error("데이터베이스가 연결되지 않았습니다.");
  const client = await pool.connect();
  try {
    await client.query("begin");
    await client.query(
      `delete from outflow_uses
        where flow_key = $1
          and (flow_krw <> $2 or (use = 'parked' and decided_at < now() - make_interval(days => $3)))`,
      [a.key, a.flowKrw, a.parkedDays],
    );
    const { rows } = await client.query<{ total: string | null }>(
      `select sum(amount_krw) as total from outflow_uses where flow_key = $1`,
      [a.key],
    );
    if (Number(rows[0]?.total ?? 0) + a.amountKrw > a.maxKrw) {
      throw new Error("답한 금액을 합치면 출금액보다 커요.");
    }
    await client.query(
      `insert into outflow_uses (flow_key, flow_krw, use, amount_krw, category, memo)
       values ($1, $2, $3, $4, $5, $6)
       on conflict (flow_key, use) do update
         set amount_krw = outflow_uses.amount_krw + excluded.amount_krw,
             category   = coalesce(excluded.category, outflow_uses.category),
             memo       = case when excluded.memo = '' then outflow_uses.memo else excluded.memo end,
             decided_at = now()`,
      [a.key, a.flowKrw, a.use, a.amountKrw, a.category, a.memo],
    );
    await client.query("commit");
  } catch (e) {
    await client.query("rollback").catch(() => {});
    throw e;
  } finally {
    client.release();
  }
}

/** Forget every answer about a withdrawal, so it is asked about again. */
export async function clearOutflow(key: string): Promise<number> {
  if (!pool) throw new Error("데이터베이스가 연결되지 않았습니다.");
  const { rowCount } = await pool.query(`delete from outflow_uses where flow_key = $1`, [key]);
  return rowCount ?? 0;
}

/** One account's settled cash on one day, per currency, and the net settled trades up to it. */
export type CashDay = {
  venue: "kis" | "gold";
  date: string;
  cashKrw: number;
  cashUsd: number;
  settledKrw: number;
  settledUsd: number;
  /** The exchanges seen between looks so far that day (won into dollars +), and the dollars they made (072). */
  exchKrw: number;
  exchUsd: number;
};

type CashRow = {
  on_date: string; cash_krw: string; cash_usd: string; settled_krw: string; settled_usd: string;
  exch_krw: string; exch_usd: string;
};

const cashDay = (venue: CashDay["venue"], r: CashRow): CashDay => ({
  venue,
  date: r.on_date,
  cashKrw: Number(r.cash_krw),
  cashUsd: Number(r.cash_usd),
  settledKrw: Number(r.settled_krw),
  settledUsd: Number(r.settled_usd),
  exchKrw: Number(r.exch_krw),
  exchUsd: Number(r.exch_usd),
});

/**
 * An account's cash as last seen today (`earlier`, before this look writes
 * over it) and on its last day before (`prev`, what today is measured
 * against). Either is null when there is none.
 */
export async function cashDays(venue: CashDay["venue"], today: string): Promise<{ earlier: CashDay | null; prev: CashDay | null }> {
  if (!pool) return { earlier: null, prev: null };
  const { rows } = await pool.query<CashRow>(
    `select to_char(on_date, 'YYYY-MM-DD') as on_date, cash_krw, cash_usd, settled_krw, settled_usd, exch_krw, exch_usd
       from kis_cash_daily where venue = $1 and on_date <= $2::date order by on_date desc limit 2`,
    [venue, today],
  );
  const days = rows.map((r) => cashDay(venue, r));
  return days[0]?.date === today ? { earlier: days[0], prev: days[1] ?? null } : { earlier: null, prev: days[0] ?? null };
}

/** Write today's cash for an account, replacing an earlier look today. */
export async function recordCash(day: CashDay): Promise<void> {
  if (!pool) return;
  await pool.query(
    `insert into kis_cash_daily (venue, on_date, cash_krw, cash_usd, settled_krw, settled_usd, exch_krw, exch_usd)
     values ($1, $2::date, $3, $4, $5, $6, $7, $8)
     on conflict (venue, on_date) do update
       set cash_krw = excluded.cash_krw, cash_usd = excluded.cash_usd,
           settled_krw = excluded.settled_krw, settled_usd = excluded.settled_usd,
           exch_krw = excluded.exch_krw, exch_usd = excluded.exch_usd, observed_at = now()`,
    [day.venue, day.date, day.cashKrw, day.cashUsd, day.settledKrw, day.settledUsd, day.exchKrw, day.exchUsd],
  );
}

/** The found flow for an account on `date`, if any. */
export async function autoFlow(venue: CashDay["venue"], date: string): Promise<number | null> {
  if (!pool) return null;
  const { rows } = await pool.query<{ amount_krw: string }>(
    `select amount_krw from principal_flows where venue = $1 and source = 'auto' and on_date = $2::date`,
    [venue, date],
  );
  return rows[0] ? Number(rows[0].amount_krw) : null;
}

/** Set (or, with null, clear) the found flow for an account on `date`. */
export async function setAutoFlow(
  venue: CashDay["venue"],
  date: string,
  amountKrw: number | null,
  memo: string,
): Promise<void> {
  if (!pool) return;
  if (amountKrw === null) {
    await pool.query(
      `delete from principal_flows where venue = $1 and source = 'auto' and on_date = $2::date`,
      [venue, date],
    );
    return;
  }
  await pool.query(
    `insert into principal_flows (on_date, venue, amount_krw, memo, source) values ($1::date, $2, $3, $4, 'auto')
     on conflict (venue, on_date) where source = 'auto'
       do update set amount_krw = excluded.amount_krw, memo = excluded.memo`,
    [date, venue, amountKrw, memo],
  );
}

export async function loadKisToken(keyHash: string): Promise<{ token: string; expires: number } | null> {
  if (!pool) return null;
  const { rows } = await pool.query<{ token: string; expires: Date }>(
    `select token, expires from kis_tokens where key_hash = $1`,
    [keyHash],
  );
  return rows[0] ? { token: rows[0].token, expires: rows[0].expires.getTime() } : null;
}

export async function saveKisToken(keyHash: string, token: string, expires: number): Promise<void> {
  if (!pool) return;
  await pool.query(
    `insert into kis_tokens (key_hash, token, expires) values ($1, $2, $3)
     on conflict (key_hash) do update set token = excluded.token, expires = excluded.expires`,
    [keyHash, token, new Date(expires)],
  );
}

/** Stored closes for one holding since `since` (YYYY-MM-DD). Empty without a database. */
export async function loadCloses(key: string, since: string): Promise<Map<string, number>> {
  const out = new Map<string, number>();
  if (!pool) return out;
  const { rows } = await pool.query<{ d: string; close: string }>(
    `select to_char(on_date, 'YYYY-MM-DD') as d, close from price_closes
      where key = $1 and on_date >= $2 order by on_date`,
    [key, since],
  );
  for (const r of rows) out.set(r.d, Number(r.close));
  return out;
}

export async function saveCloses(key: string, closes: Map<string, number>): Promise<void> {
  if (!pool || closes.size === 0) return;
  const dates = [...closes.keys()];
  const values = dates.map((d) => closes.get(d)!);
  await pool.query(
    `insert into price_closes (key, on_date, close)
     select $1, d::date, c from unnest($2::text[], $3::numeric[]) as t(d, c)
     on conflict (key, on_date) do update set close = excluded.close`,
    [key, dates, values],
  );
}

/**
 * Leave events in input.outbox for input-svc to pick up - so one sent while
 * input-svc is restarting arrives late instead of never. False when there is
 * no database or the write failed; the caller then posts them directly.
 */
export async function enqueueEvents(events: unknown[]): Promise<boolean> {
  if (!pool || events.length === 0) return false;
  try {
    await pool.query(
      "insert into input.outbox (payload) select * from unnest($1::jsonb[])",
      [events.map((e) => JSON.stringify(asRecord(e as Reported)))],
    );
    return true;
  } catch (err) {
    console.error("이벤트를 outbox 에 쓰지 못해 바로 보냅니다:", (err as Error).message);
    return false;
  }
}

/** What a report of this service's says, whether a record already or the notification shape. */
export type Reported = {
  source?: string; kind?: string; type?: string; tier?: string; level?: string; title?: string; body?: string; url?: string;
  key?: string; audience?: string; data?: Record<string, unknown>; source_key?: string;
};

/** A report as the record input-svc takes: the wording goes in the payload, under notify. */
export function asRecord(e: Reported): unknown {
  if (e.source_key) return e;
  const now = new Date().toISOString();
  const notify = e.title
    ? { tier: e.tier, level: e.level, title: e.title, body: e.body, url: e.url, audience: e.audience }
    : undefined;
  return {
    kind: e.type ?? e.kind, source: e.source ?? "assets",
    source_key: e.key ?? `ev-${Date.now()}-${Math.random().toString(36).slice(2)}`,
    actor: e.audience === "guest" ? "guest" : (e.source ?? "assets"),
    occurred_at: now, tier: "raw", schema_version: 1,
    payload: { ...(e.data ?? {}), ...(notify ? { notify } : {}) },
  };
}

/** An order as placed (or refused), for asset_orders (migration 055). */
export type OrderRow = {
  id?: number;
  createdAt?: string;
  venue: "upbit" | "kis" | "gold";
  symbol: string;
  side: "buy" | "sell";
  units: number | null;
  amountKrw: number;
  limitPrice: number | null;
  currency: "KRW" | "USD";
  reason: string;
  planId: string | null;
  status: "placed" | "failed" | "manual";
  brokerId: string | null;
  message: string;
};

/**
 * Write down an order. Never throws: the order is already at the broker (or
 * refused) whatever happens here, and the caller reports that either way.
 */
export async function recordOrder(o: OrderRow): Promise<void> {
  if (!pool) return;
  try {
    await pool.query(
      `insert into asset_orders (venue, symbol, side, units, amount_krw, limit_price, currency, reason, plan_id, status, broker_id, message)
       values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
      [o.venue, o.symbol, o.side, o.units, Math.round(o.amountKrw), o.limitPrice, o.currency, o.reason, o.planId, o.status, o.brokerId, o.message.slice(0, 500)],
    );
  } catch (err) {
    console.error("주문 기록을 남기지 못했습니다:", (err as Error).message);
  }
}

/** The latest orders, newest first. */
export async function recentOrders(limit: number): Promise<OrderRow[]> {
  if (!pool) return [];
  const { rows } = await pool.query<{
    id: string; created_at: Date; venue: OrderRow["venue"]; symbol: string; side: OrderRow["side"];
    units: string | null; amount_krw: string; limit_price: string | null; currency: OrderRow["currency"];
    reason: string; plan_id: string | null; status: OrderRow["status"]; broker_id: string | null; message: string;
  }>(
    `select id, created_at, venue, symbol, side, units, amount_krw, limit_price, currency, reason, plan_id, status, broker_id, message
       from asset_orders order by created_at desc, id desc limit $1`,
    [limit],
  );
  return rows.map((r) => ({
    id: Number(r.id),
    createdAt: r.created_at.toISOString(),
    venue: r.venue,
    symbol: r.symbol,
    side: r.side,
    units: r.units === null ? null : Number(r.units),
    amountKrw: Number(r.amount_krw),
    limitPrice: r.limit_price === null ? null : Number(r.limit_price),
    currency: r.currency,
    reason: r.reason,
    planId: r.plan_id,
    status: r.status,
    brokerId: r.broker_id,
    message: r.message,
  }));
}

/** A standing order (migration 057): placed again each trading day until filled. */
export type Standing = {
  id: number;
  createdAt: string;
  venue: "kis";
  symbol: string;
  side: "buy" | "sell";
  units: number;
  limitPrice: number;
  heldBefore: number;
  reason: string;
  status: "active" | "filled" | "cancelled" | "expired";
  placedOn: string | null;
  brokerId: string | null;
  message: string;
  expiresOn: string | null;
  doneAt: string | null;
};

const standingCols = `id, created_at, venue, symbol, side, units, limit_price, held_before, reason, status,
  to_char(placed_on, 'YYYY-MM-DD'), broker_id, message, to_char(expires_on, 'YYYY-MM-DD'), done_at`;

function standingOf(r: unknown[]): Standing {
  const [id, created, venue, symbol, side, units, limit, held, reason, status, placed, broker, message, expires, done] = r as [
    string, Date, "kis", string, "buy" | "sell", number, string, string, string, Standing["status"], string | null, string | null, string, string | null, Date | null,
  ];
  return {
    id: Number(id), createdAt: created.toISOString(), venue, symbol, side, units: Number(units), limitPrice: Number(limit),
    heldBefore: Number(held), reason, status, placedOn: placed, brokerId: broker, message, expiresOn: expires,
    doneAt: done ? done.toISOString() : null,
  };
}

export async function addStanding(o: {
  symbol: string; side: "buy" | "sell"; units: number; limitPrice: number; heldBefore: number; reason: string; expiresOn: string | null;
}): Promise<Standing> {
  if (!pool) throw new Error("데이터베이스가 연결되지 않았습니다.");
  const { rows } = await pool.query({
    text: `insert into standing_orders (venue, symbol, side, units, limit_price, held_before, reason, expires_on)
           values ('kis', $1, $2, $3, $4, $5, $6, $7::date) returning ${standingCols}`,
    values: [o.symbol, o.side, o.units, o.limitPrice, o.heldBefore, o.reason, o.expiresOn],
    rowMode: "array",
  });
  return standingOf(rows[0]);
}

/** Active ones first, then the latest done. */
export async function standingOrders(limit = 20): Promise<Standing[]> {
  if (!pool) return [];
  const { rows } = await pool.query({
    text: `select ${standingCols} from standing_orders
            order by (status = 'active') desc, created_at desc limit $1`,
    values: [limit],
    rowMode: "array",
  });
  return rows.map(standingOf);
}

export async function updateStanding(
  id: number,
  patch: { status?: Standing["status"]; placedOn?: string; brokerId?: string | null; message?: string },
): Promise<void> {
  if (!pool) return;
  await pool.query(
    `update standing_orders set
        status = coalesce($2, status),
        placed_on = coalesce($3::date, placed_on),
        broker_id = case when $4::boolean then $5 else broker_id end,
        message = coalesce($6, message),
        done_at = case when $2 in ('filled', 'cancelled', 'expired') then now() else done_at end
      where id = $1`,
    [id, patch.status ?? null, patch.placedOn ?? null, patch.brokerId !== undefined, patch.brokerId ?? null, patch.message ?? null],
  );
}

export async function close(): Promise<void> {
  await pool?.end();
  pool = null;
}
