import { randomUUID } from "node:crypto";

import * as db from "./db.js";
import type { Plan } from "./trade-plan.js";
import type { Holding } from "./types.js";
import { kisUsOrder, kisUsQuote, upbitOrder, upbitPrice } from "./venues.js";

/**
 * Placing orders - the one thing this service does that moves money.
 *
 * Nothing reaches here unasked: the 자산 screen's order sheet and its "이
 * 계획대로 주문" both make the person confirm the exact list, and mind's
 * rebalance card executes only the orders it showed, when approved
 * (mind-svc act.Rebalance). This checks what it is given anyway - a share
 * not held is not sold, a sale is never larger than the holding, one order
 * and one plan have a ceiling - and writes every order down (asset_orders),
 * placed or refused.
 *
 * Sells go first, so a rebalance's buys can use what they freed. Upbit
 * orders are market orders; KIS takes no market order for US shares, so a
 * buy is priced a little over the last trade and a sell a little under -
 * filled at once in a normal market. KRX gold is left to the person: the
 * gold account's order API is not one this service has been able to check,
 * so it comes back as "manual" with what to do.
 *
 * Trades do not touch the principal: that is deposits and withdrawals only,
 * and a settled trade is taken out of the cash KIS reports before a flow is
 * found (kis-flows.ts). Upbit's flows come from its deposit records.
 */

export type OrderRequest = {
  venue: "upbit" | "kis" | "gold";
  symbol: string;
  side: "buy" | "sell";
  /** Whole shares (or grams) for a stock or gold; a coin trades by amount. */
  units?: number | null;
  /** Won: what a coin buy spends or a coin sale is worth; the estimate for a share. */
  amountKrw: number;
};

export type OrderResult = OrderRequest & {
  /** The holding's name, for saying the order in words. */
  name?: string;
  status: "placed" | "failed" | "manual" | "preview";
  message: string;
  brokerId?: string;
  /** The limit price sent, in the share's currency. */
  limitPrice?: number;
  currency: "KRW" | "USD";
};

const MAX_ORDER_KRW = Number(process.env.ORDER_MAX_KRW ?? 1_500_000);
const MAX_PLAN_KRW = Number(process.env.ORDER_PLAN_MAX_KRW ?? 4_000_000);
const MIN_COIN_KRW = 5_000;
/** How far past the last trade a US limit order is priced, to be filled at once. */
const US_SLIP = 0.003;

/** A plan's orders as requests - what the 자산 screen shows and a card carries. */
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

/** Why a request cannot go as it is; null when it can. */
export function problem(r: OrderRequest, held: Holding | undefined): string | null {
  if (r.venue !== "upbit" && r.venue !== "kis" && r.venue !== "gold") return "모르는 계좌예요";
  if (r.side !== "buy" && r.side !== "sell") return "매수·매도 중 하나여야 해요";
  if (!/^[A-Za-z0-9.\-]{1,20}$/.test(r.symbol)) return "종목 코드가 이상해요";
  if (!Number.isFinite(r.amountKrw) || r.amountKrw <= 0) return "금액이 필요해요";
  if (r.amountKrw > MAX_ORDER_KRW) return `한 번에 ₩${MAX_ORDER_KRW.toLocaleString("ko-KR")}까지만 주문해요`;
  if (r.venue === "upbit") {
    if (r.amountKrw < MIN_COIN_KRW) return "업비트는 ₩5,000부터 주문할 수 있어요";
  } else if (!Number.isInteger(r.units) || (r.units ?? 0) <= 0) {
    return "주식은 1주 단위로 주문해요";
  }
  if (r.side === "sell") {
    if (!held || held.quantity <= 0) return "갖고 있지 않은 종목은 팔 수 없어요";
    if (r.venue !== "upbit" && (r.units ?? 0) > Math.floor(held.quantity + 1e-6)) return `${Math.floor(held.quantity + 1e-6)}주까지만 팔 수 있어요`;
    if (r.venue === "upbit" && r.amountKrw > held.valueKrw * 1.02) return "갖고 있는 것보다 많이 팔 수 없어요";
  }
  return null;
}

const won = (v: number) => `₩${Math.round(v).toLocaleString("ko-KR")}`;
const roundUp = (v: number) => Math.ceil(v * 100) / 100;
const roundDown = (v: number) => Math.floor(v * 100) / 100;

/**
 * Place (or, with dryRun, only check) a list of orders. Sells first. A
 * refused order does not stop the rest - each says how it went.
 */
export async function placeOrders(
  requests: OrderRequest[],
  holdings: Holding[],
  opts: { reason: string; dryRun?: boolean; planId?: string },
): Promise<{ planId: string; results: OrderResult[] }> {
  const planId = opts.planId ?? randomUUID().slice(0, 8);
  const byKey = new Map(holdings.map((h) => [`${h.venue}:${h.symbol}`, h]));
  const ordered = [...requests].sort((a, b) => (a.side === b.side ? 0 : a.side === "sell" ? -1 : 1));
  const total = ordered.reduce((s, r) => s + (Number.isFinite(r.amountKrw) ? r.amountKrw : 0), 0);

  const results: OrderResult[] = [];
  for (const r of ordered) {
    const held = byKey.get(`${r.venue}:${r.symbol}`);
    const currency: "KRW" | "USD" = held?.currency ?? (r.venue === "kis" ? "USD" : "KRW");
    const base = { ...r, units: r.units ?? null, currency, ...(held ? { name: held.name } : {}) };
    const bad = total > MAX_PLAN_KRW ? `한 번에 ${won(MAX_PLAN_KRW)}까지만 주문해요 (이번 ${won(total)})` : problem(r, held);
    if (bad) {
      results.push({ ...base, status: "failed", message: bad });
      continue;
    }
    if (r.venue === "gold" || (r.venue === "kis" && currency !== "USD")) {
      results.push({
        ...base,
        status: "manual",
        message: `한국투자 앱에서 직접 ${r.side === "buy" ? "사세요" : "파세요"}: ${held?.name ?? r.symbol} ${r.units}${r.venue === "gold" ? "g" : "주"} (약 ${won(r.amountKrw)})`,
      });
      continue;
    }
    if (opts.dryRun) {
      results.push({ ...base, status: "preview", message: "" });
      continue;
    }
    try {
      if (r.venue === "upbit") {
        const market = `KRW-${r.symbol}`;
        if (r.side === "buy") {
          const answer = await upbitOrder({ market, side: "bid", price: String(Math.floor(r.amountKrw)) });
          results.push({ ...base, status: "placed", brokerId: answer.uuid, message: `${won(r.amountKrw)}어치 시장가 매수` });
        } else {
          const price = await upbitPrice(market);
          // Selling about all of it sells all of it: no dust left behind.
          const all = held && r.amountKrw >= held.valueKrw * 0.98;
          const volume = all ? held!.quantity : Math.floor((r.amountKrw / price) * 1e8) / 1e8;
          const answer = await upbitOrder({ market, side: "ask", volume: volume.toFixed(8).replace(/0+$/, "").replace(/\.$/, "") });
          results.push({ ...base, units: volume, status: "placed", brokerId: answer.uuid, message: `${volume} ${r.symbol} 시장가 매도` });
        }
      } else {
        const quote = await kisUsQuote(r.symbol);
        const price = r.side === "buy" ? roundUp(quote.last * (1 + US_SLIP)) : roundDown(quote.last * (1 - US_SLIP));
        const answer = await kisUsOrder({ symbol: r.symbol, excd: quote.excd, side: r.side, qty: r.units!, price });
        results.push({
          ...base,
          status: "placed",
          brokerId: answer.odno,
          limitPrice: price,
          message: `${r.units}주 $${price.toFixed(2)} 지정가 ${r.side === "buy" ? "매수" : "매도"}${answer.message ? ` · ${answer.message}` : ""}`,
        });
      }
    } catch (e) {
      results.push({ ...base, status: "failed", message: (e as Error).message.slice(0, 300) });
    }
  }

  if (!opts.dryRun) {
    for (const x of results) {
      if (x.status === "preview") continue;
      await db.recordOrder({
        venue: x.venue,
        symbol: x.symbol,
        side: x.side,
        units: x.units ?? null,
        amountKrw: x.amountKrw,
        limitPrice: x.limitPrice ?? null,
        currency: x.currency,
        reason: opts.reason,
        planId,
        status: x.status,
        brokerId: x.brokerId ?? null,
        message: x.message,
      });
    }
  }
  return { planId, results };
}
