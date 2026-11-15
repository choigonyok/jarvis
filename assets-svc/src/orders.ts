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
 * placed or refused. A send named by a planId goes once, however often the
 * request arrives (placeOnce).
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
      // Said whatever was thrown: once an order may have gone this must not
      // throw - placeOnce forgets a send that threw, as one that placed nothing.
      results.push({ ...base, status: "failed", message: (e instanceof Error ? e.message : String(e)).slice(0, 300) });
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

/** A send's answer: its planId and how each order went. */
export type Sent = { planId: string; results: OrderResult[] };

/**
 * How long a planId answers for what it placed. A retry comes within
 * minutes; the console's planIds are eight hex digits, so two sends years
 * apart can share one, and the old must not answer for the new.
 */
const REPLAY_MS = 24 * 60 * 60_000;

/**
 * Sends with a planId in this process, under way or done. The entry goes in
 * before anything is awaited, so of two requests for one planId the second
 * always finds the first: Node runs one thing at a time, and there is one
 * assets-svc.
 */
const sends = new Map<string, { sent: Promise<Sent>; doneAt?: number }>();

/** Whether a planId went could not be read, so nothing was sent. */
export class Unchecked extends Error {}

/** A recorded order as the send's answer had it. */
export function resultOf(r: db.OrderRow): OrderResult {
  return {
    venue: r.venue,
    symbol: r.symbol,
    side: r.side,
    units: r.units,
    amountKrw: r.amountKrw,
    status: r.status,
    message: r.message,
    currency: r.currency,
    ...(r.brokerId ? { brokerId: r.brokerId } : {}),
    ...(r.limitPrice !== null ? { limitPrice: r.limitPrice } : {}),
  };
}

/**
 * POST /orders: check or place a list of orders - a send with a planId at
 * most once. The console names each press with a planId, and mind a
 * rebalance card with its own; a planId that has placed (a retry, the
 * network carrying the request twice) places nothing again and answers what
 * it placed, from the record, with replay. One that comes while the first is
 * still out waits for it and answers the same. The accounts are read only for
 * a send that goes.
 *
 * Checks (dryRun) and sends without a planId go as before: a check records
 * nothing, so it never answers for a send, and a planId made here is new.
 */
export async function placeOnce(
  requests: OrderRequest[],
  holdings: () => Promise<Holding[]>,
  opts: { reason: string; dryRun?: boolean; planId?: string },
  deps: { place?: typeof placeOrders; recorded?: (planId: string) => Promise<db.OrderRow[]> } = {},
): Promise<Sent & { replay: boolean }> {
  const place = deps.place ?? placeOrders;
  const planId = opts.planId;
  if (opts.dryRun || !planId) return { ...(await place(requests, await holdings(), opts)), replay: false };

  const now = Date.now();
  for (const [id, s] of sends) if (s.doneAt !== undefined && now - s.doneAt > REPLAY_MS) sends.delete(id);
  const first = sends.get(planId);
  if (first) return { ...(await first.sent), replay: true };

  const recorded = deps.recorded ?? ((id: string) => db.planOrders(id, REPLAY_MS));
  let replay = false;
  const entry: { sent: Promise<Sent>; doneAt?: number } = {
    sent: (async () => {
      const rows = await recorded(planId).catch((e: unknown) => {
        console.error("주문 기록을 읽지 못했습니다:", e instanceof Error ? e.message : e);
        throw new Unchecked("주문 기록을 읽지 못해 보내지 않았어요. 잠시 뒤 다시 보내 주세요.");
      });
      if (rows.length) {
        replay = true;
        return { planId, results: rows.map(resultOf) };
      }
      return place(requests, await holdings(), opts);
    })(),
  };
  sends.set(planId, entry);
  try {
    const sent = await entry.sent;
    entry.doneAt = Date.now();
    return { ...sent, replay };
  } catch (e) {
    // Failed before any order went (the record or the accounts could not be
    // read): forgotten, so the same planId can go once that is fixed.
    if (sends.get(planId) === entry) sends.delete(planId);
    throw e;
  }
}
