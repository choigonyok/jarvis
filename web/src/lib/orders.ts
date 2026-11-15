// Orders, as assets-svc places and records them (assets-svc/src/orders.ts).

import { SAY } from "@/lib/say";
import type { Plan } from "@/lib/trade-plan";

export type OrderRequest = {
  venue: "upbit" | "kis" | "gold";
  symbol: string;
  side: "buy" | "sell";
  /** Whole shares or grams; null for a coin, which trades by amount. */
  units: number | null;
  amountKrw: number;
};

export type OrderResult = OrderRequest & {
  /** preview: checked, not sent. manual: to be placed by hand in the app (KRX gold). */
  status: "placed" | "failed" | "manual" | "preview";
  message: string;
  brokerId?: string;
  limitPrice?: number;
  currency: "KRW" | "USD";
};

/** One row of the record (asset_orders). */
export type PlacedOrder = OrderRequest & {
  id: number;
  createdAt: string;
  limitPrice: number | null;
  currency: "KRW" | "USD";
  reason: string;
  planId: string | null;
  status: "placed" | "failed" | "manual";
  brokerId: string | null;
  message: string;
};

/** A plan's orders as requests, the way assets-svc turns them (requestsOf). */
export function requestsOf(plan: Plan): OrderRequest[] {
  return plan.orders
    .filter((o) => o.holding)
    .map((o) => ({
      venue: o.holding!.venue,
      symbol: o.holding!.symbol,
      side: o.side,
      units: o.units,
      amountKrw: Math.round(o.amountKrw),
    }));
}

/**
 * Orders sent and no answer back: the line dropped on the way, or something
 * between gave up before assets-svc said how they went. They may have been
 * placed, so this is not said as a refusal, and they are not to be sent again
 * blind - the record says what came of them (findSent).
 */
export class Unanswered extends Error {
  constructor() {
    super("주문 결과를 받지 못했어요. 주문이 들어갔을 수도 있어요.");
  }
}

async function post<T>(path: string, body: { dryRun?: boolean; [key: string]: unknown }): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  }).catch(() => null);
  const out = res ? ((await res.json().catch(() => null)) as (T & { error?: string }) | null) : null;
  if (res?.ok && out) return out;
  // A check moves nothing. An order is known not to have gone only when it
  // was turned down (4xx): a dropped line, a gateway that gave up, a failure
  // after the broker answered may each have left it placed.
  const refused = res !== null && res.status >= 400 && res.status < 500;
  if (!body.dryRun && !refused) throw new Unanswered();
  throw new Error(res ? (out?.error ?? "주문 서비스에 닿지 못했어요.") : SAY.offline);
}

/** Check (dryRun) or place a list of orders. */
export function sendOrders(orders: OrderRequest[], opts: { reason: string; dryRun?: boolean; planId?: string }) {
  return post<{ planId: string; results: OrderResult[] }>("/api/portfolio/orders", { orders, ...opts });
}

/** The current rebalance plan, checked: exactly the orders a confirm would send. */
export function checkRebalance() {
  return post<{ planId: string | null; results: OrderResult[]; unpicked: Plan["unpicked"]; inBand: boolean }>(
    "/api/portfolio/orders/rebalance",
    { dryRun: true },
  );
}

/** The record, newest first; null when it could not be read, which is not an empty record. */
export async function recentOrders(limit = 20): Promise<PlacedOrder[] | null> {
  const res = await fetch(`/api/portfolio/orders?limit=${limit}`, { cache: "no-store" }).catch(() => null);
  const body = res?.ok ? ((await res.json().catch(() => null)) as { orders?: PlacedOrder[] } | null) : null;
  return body?.orders ?? null;
}

/**
 * What the record has of one send, by its planId: assets-svc writes down
 * every order it placed or turned down once it is done placing. Null when the
 * record could not be read. Placing first reads every account afresh, so a
 * send still being worked on is asked after again for a while before none
 * is taken to mean it did not go.
 */
export async function findSent(planId: string): Promise<PlacedOrder[] | null> {
  for (const wait of [1_500, 4_000, 8_000]) {
    await new Promise((r) => setTimeout(r, wait));
    const record = await recentOrders(100);
    if (!record) return null;
    const rows = record.filter((o) => o.planId === planId);
    if (rows.length) return rows;
  }
  return [];
}

/** A standing order: the same limit order each session until it fills (assets-svc standing.ts). */
export type Standing = {
  id: number;
  createdAt: string;
  symbol: string;
  side: "buy" | "sell";
  units: number;
  limitPrice: number;
  reason: string;
  status: "active" | "filled" | "cancelled" | "expired";
  placedOn: string | null;
  message: string;
  expiresOn: string | null;
  doneAt: string | null;
};

/** Null when they could not be read: an order still waiting must not read as gone. */
export async function standingOrders(): Promise<Standing[] | null> {
  const res = await fetch("/api/portfolio/standing", { cache: "no-store" }).catch(() => null);
  const body = res?.ok ? ((await res.json().catch(() => null)) as { standing?: Standing[] } | null) : null;
  return body?.standing ?? null;
}

/**
 * Cancel a standing order. failed: today's order at the broker could not be
 * cancelled and may still fill - the standing order itself is cancelled
 * either way, and text says which half did not go.
 */
export async function cancelStanding(id: number): Promise<{ text: string; failed: boolean }> {
  const res = await fetch(`/api/portfolio/standing/${id}`, { method: "DELETE" }).catch(() => null);
  if (!res) throw new Error(SAY.offline);
  const body = (await res.json().catch(() => null)) as { message?: string; error?: string; cancelled?: boolean } | null;
  if (!res.ok) throw new Error(body?.error ?? "취소하지 못했어요.");
  const text = body?.message ?? "취소했어요";
  // assets-svc answers 200 either way and says which in cancelled; an older
  // one said it only in its words.
  return { text, failed: typeof body?.cancelled === "boolean" ? !body.cancelled : text.includes("못했어요") };
}
