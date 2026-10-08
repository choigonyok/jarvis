import type { Holding } from "@/lib/portfolio";
import type { Allocation, Bucket } from "@/lib/target";

/**
 * Turning "move 1,234,567원 into 배당주" into orders that can actually be
 * placed: a share is bought whole, KRX gold by the gram, a coin by amount
 * (with Upbit's minimum order). Whatever the rounding leaves over is cash -
 * the one bucket that takes any amount.
 *
 * Two questions, one calculation:
 * - rebalance: what to buy and sell so every bucket lands nearest its target.
 * - deposit: new cash arrives; what to buy with it (buying only) so the
 *   allocation moves toward target.
 *
 * Prices are the ones the brokerages last reported; fees and the FX spread
 * are left out.
 */

export type Order = {
  bucket: Bucket;
  /** The holding traded; null when the bucket holds nothing yet. */
  holding: Holding | null;
  side: "buy" | "sell";
  /** Whole shares / grams; for a coin, null (it trades by amount). */
  units: number | null;
  /** One unit's price in KRW; null for a coin. */
  unitKrw: number | null;
  amountKrw: number;
};

export type BucketResult = {
  id: Bucket;
  label: string;
  target: number;
  before: number;
  after: number;
  afterKrw: number;
};

export type Plan = {
  orders: Order[];
  /** Cash after the orders (and the deposit), in KRW. */
  cashAfterKrw: number;
  /** Change in cash; for a deposit, what stays uninvested. */
  cashChangeKrw: number;
  buckets: BucketResult[];
  /** Buckets with a gap but nothing in them to trade - the person picks. */
  unpicked: { bucket: Bucket; label: string; amountKrw: number }[];
};

const MIN_COIN_ORDER = 5_000;
/** Below this a bucket's gap is not worth an order. */
const MIN_ORDER = 10_000;

/** Whole units unless it is a coin. KRX gold's unit is a gram. */
function unitOf(h: Holding): number | null {
  if (h.kind === "coin") return null;
  if (h.quantity > 0) return h.valueKrw / h.quantity;
  // Not held yet (the KRX gold stand-in): its quoted price is the unit.
  return h.currency === "KRW" && h.price > 0 ? h.price : null;
}

/** Whole units held - KRX gold's grams come back as 4.9999 from value/price. */
const wholeHeld = (h: Holding) => Math.floor(h.quantity + 1e-6);

export type PlanOptions = {
  /** KRX gold, won per gram: lets the gold bucket be filled before any gold is held. */
  goldGramKrw?: number | null;
};

const GOLD_STANDIN = (gramKrw: number): Holding => ({
  id: "gold:KRX",
  venue: "gold",
  kind: "gold",
  symbol: "M04020000",
  name: "금현물 (KRX)",
  quantity: 0,
  avgPrice: 0,
  price: gramKrw,
  currency: "KRW",
  valueKrw: 0,
  costKrw: 0,
});

type Ctx = {
  rows: Allocation["rows"];
  byBucket: Map<Bucket, Holding[]>;
  value: Map<Bucket, number>;
  total: number;
};

function context(allocation: Allocation, holdings: Holding[], opts: PlanOptions = {}): Ctx {
  const bucketOf = new Map<string, Bucket>();
  for (const r of allocation.rows) for (const s of r.symbols) bucketOf.set(s, r.id);
  const byBucket = new Map<Bucket, Holding[]>();
  for (const h of holdings) {
    const b = bucketOf.get(h.symbol);
    if (!b || b === "cash") continue;
    byBucket.set(b, [...(byBucket.get(b) ?? []), h]);
  }
  if (!byBucket.get("gold")?.length && opts.goldGramKrw && opts.goldGramKrw > 0) {
    byBucket.set("gold", [GOLD_STANDIN(opts.goldGramKrw)]);
  }
  // Largest position first: that is where a buy or a sell goes.
  for (const list of byBucket.values()) list.sort((a, b) => b.valueKrw - a.valueKrw);
  const value = new Map(allocation.rows.map((r) => [r.id, r.valueKrw]));
  return { rows: allocation.rows, byBucket, value, total: allocation.totalKrw };
}

/** Orders for one bucket to move by `delta` KRW (negative sells). */
function ordersFor(bucket: Bucket, delta: number, ctx: Ctx, round: "nearest" | "down"): Order[] {
  const list = ctx.byBucket.get(bucket) ?? [];
  if (Math.abs(delta) < MIN_ORDER || list.length === 0) return [];
  const out: Order[] = [];
  if (delta > 0) {
    const h = list[0];
    const unit = unitOf(h);
    if (unit === null) {
      const amount = Math.floor(delta / 1000) * 1000;
      if (amount >= MIN_COIN_ORDER) out.push({ bucket, holding: h, side: "buy", units: null, unitKrw: null, amountKrw: amount });
      return out;
    }
    const units = round === "down" ? Math.floor(delta / unit) : Math.round(delta / unit);
    if (units > 0) out.push({ bucket, holding: h, side: "buy", units, unitKrw: unit, amountKrw: units * unit });
    return out;
  }
  // Selling: from the largest position, then the next, never more than held.
  let left = -delta;
  for (const h of list) {
    if (left < MIN_ORDER) break;
    const unit = unitOf(h);
    if (unit === null) {
      const amount = Math.min(h.valueKrw, Math.floor(left / 1000) * 1000);
      if (amount >= MIN_COIN_ORDER) {
        out.push({ bucket, holding: h, side: "sell", units: null, unitKrw: null, amountKrw: amount });
        left -= amount;
      }
      continue;
    }
    const units = Math.min(wholeHeld(h), Math.round(left / unit));
    if (units > 0) {
      out.push({ bucket, holding: h, side: "sell", units, unitKrw: unit, amountKrw: units * unit });
      left -= units * unit;
    }
  }
  return out;
}

function finish(ctx: Ctx, orders: Order[], deposit: number, unpicked: Plan["unpicked"]): Plan {
  const after = new Map(ctx.value);
  let cashChange = deposit;
  for (const o of orders) {
    const signed = o.side === "buy" ? o.amountKrw : -o.amountKrw;
    after.set(o.bucket, (after.get(o.bucket) ?? 0) + signed);
    cashChange -= signed;
  }
  after.set("cash", (after.get("cash") ?? 0) + cashChange);
  const total = ctx.total + deposit;
  return {
    orders,
    cashAfterKrw: after.get("cash") ?? 0,
    cashChangeKrw: cashChange,
    unpicked,
    buckets: ctx.rows.map((r) => ({
      id: r.id,
      label: r.label,
      target: r.target,
      before: r.current,
      after: total > 0 ? (after.get(r.id) ?? 0) / total : 0,
      afterKrw: after.get(r.id) ?? 0,
    })),
  };
}

/** Buy and sell so each bucket lands as near its target as whole units allow. */
export function rebalancePlan(allocation: Allocation, holdings: Holding[], opts: PlanOptions = {}): Plan {
  const ctx = context(allocation, holdings, opts);
  const orders: Order[] = [];
  const unpicked: Plan["unpicked"] = [];
  for (const r of ctx.rows) {
    if (r.id === "cash") continue;
    const delta = r.target * ctx.total - (ctx.value.get(r.id) ?? 0);
    const got = ordersFor(r.id, delta, ctx, "nearest");
    if (got.length === 0 && delta >= MIN_ORDER && !(ctx.byBucket.get(r.id)?.length)) {
      unpicked.push({ bucket: r.id, label: r.label, amountKrw: delta });
    }
    orders.push(...got);
  }
  return finish(ctx, orders, 0, unpicked);
}

/**
 * Put `depositKrw` of new cash to work, buying only. Each underweight bucket
 * gets its share of the shortfall at the new total, rounded down to whole
 * units; then the leftover buys one more unit at a time where the shortfall
 * is largest, until nothing more fits.
 */
export function depositPlan(
  allocation: Allocation,
  holdings: Holding[],
  depositKrw: number,
  opts: PlanOptions = {},
): Plan {
  const ctx = context(allocation, holdings, opts);
  const total = ctx.total + depositKrw;
  const need = new Map<Bucket, number>();
  // Cash is a bucket too: when it is short, its share of the deposit simply
  // stays cash.
  let cashShort = 0;
  for (const r of ctx.rows) {
    const gap = r.target * total - (ctx.value.get(r.id) ?? 0);
    if (gap <= 0) continue;
    if (r.id === "cash") cashShort = gap;
    else need.set(r.id, gap);
  }
  const needSum = [...need.values()].reduce((s, v) => s + v, 0) + cashShort;
  const scale = needSum > depositKrw ? depositKrw / needSum : 1;
  const keepCash = cashShort * scale;

  const orders: Order[] = [];
  const unpicked: Plan["unpicked"] = [];
  let spent = 0;
  for (const [b, gap] of need) {
    const want = gap * scale;
    if (!(ctx.byBucket.get(b)?.length)) {
      if (want >= MIN_ORDER) unpicked.push({ bucket: b, label: ctx.rows.find((r) => r.id === b)!.label, amountKrw: want });
      continue;
    }
    const got = ordersFor(b, want, ctx, "down");
    for (const o of got) spent += o.amountKrw;
    orders.push(...got);
  }

  // Top up with whole units where the shortfall is still largest.
  const reserved = unpicked.reduce((s, u) => s + u.amountKrw, 0);
  let left = depositKrw - spent - reserved - keepCash;
  const bought = new Map<Bucket, number>();
  for (const o of orders) bought.set(o.bucket, (bought.get(o.bucket) ?? 0) + o.amountKrw);
  for (;;) {
    let best: { b: Bucket; h: Holding; unit: number; short: number } | null = null;
    for (const [b, gap] of need) {
      const h = ctx.byBucket.get(b)?.[0];
      if (!h) continue;
      const unit = unitOf(h);
      if (unit === null || unit > left) continue;
      const short = gap - (bought.get(b) ?? 0);
      // Only while it still brings the bucket nearer its target.
      if (short < unit / 2) continue;
      if (!best || short > best.short) best = { b, h, unit, short };
    }
    if (!best) break;
    const existing = orders.find((o) => o.bucket === best!.b && o.side === "buy" && o.holding === best!.h);
    if (existing) {
      existing.units = (existing.units ?? 0) + 1;
      existing.amountKrw += best.unit;
    } else {
      orders.push({ bucket: best.b, holding: best.h, side: "buy", units: 1, unitKrw: best.unit, amountKrw: best.unit });
    }
    bought.set(best.b, (bought.get(best.b) ?? 0) + best.unit);
    left -= best.unit;
  }
  // What no whole unit fits can still go to a coin that is short.
  for (const [b, gap] of need) {
    const h = ctx.byBucket.get(b)?.[0];
    if (!h || unitOf(h) !== null) continue;
    const add = Math.floor(Math.min(left, gap - (bought.get(b) ?? 0)) / 1000) * 1000;
    if (add < 1000) continue;
    const existing = orders.find((o) => o.bucket === b && o.holding === h);
    if (existing) existing.amountKrw += add;
    else if (add >= MIN_COIN_ORDER) orders.push({ bucket: b, holding: h, side: "buy", units: null, unitKrw: null, amountKrw: add });
    else continue;
    left -= add;
  }
  return finish(ctx, orders, depositKrw, unpicked);
}
