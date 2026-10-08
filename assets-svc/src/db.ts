import { Pool } from "pg";

import type { Flow, Portfolio } from "./types.js";

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

/** Hand-recorded flows on or after `since`, oldest first. */
export async function manualFlows(since: string): Promise<Flow[]> {
  if (!pool) return [];
  const { rows } = await pool.query<{
    id: string; on_date: string; venue: Flow["venue"]; amount_krw: string; memo: string;
  }>(
    `select id, to_char(on_date, 'YYYY-MM-DD') as on_date, venue, amount_krw, memo
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
    source: "manual",
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
  const { rowCount } = await pool.query("delete from principal_flows where id = $1", [id]);
  return (rowCount ?? 0) > 0;
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

export async function close(): Promise<void> {
  await pool?.end();
  pool = null;
}
