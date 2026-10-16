// Orders, as assets-svc places and records them (assets-svc/src/orders.ts).

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

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  const out = (await res.json().catch(() => null)) as (T & { error?: string }) | null;
  if (!res.ok || !out) throw new Error(out?.error ?? "주문 서비스에 닿지 못했어요.");
  return out;
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

export async function recentOrders(limit = 20): Promise<PlacedOrder[]> {
  const res = await fetch(`/api/portfolio/orders?limit=${limit}`, { cache: "no-store" });
  if (!res.ok) return [];
  const body = (await res.json().catch(() => null)) as { orders?: PlacedOrder[] } | null;
  return body?.orders ?? [];
}
