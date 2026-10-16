import * as db from "./db.js";
import { tell } from "./notify.js";
import type { Holding } from "./types.js";
import { kisUsBuyable, kisUsCancel, kisUsOrder, kisUsQuote } from "./venues.js";

/**
 * Standing orders - "내려오면 사" - for US shares.
 *
 * A KIS limit order for a US share lasts one session; one that has not
 * filled by the close is gone. A standing order is the person's instruction
 * that outlives the session: the same shares at the same price, placed again
 * each trading day until it fills, it is cancelled, or its date passes. The
 * price is never chased - a buy waits for the price to come down to it.
 *
 * Whether it filled is read off the holdings, not the broker's fills: the
 * shares held when it was made, plus (or minus) the units. That also counts
 * a purchase made by hand in the app as the instruction done, which it is.
 */

/** New York's date and whether its regular session (9:30–16:00, Mon–Fri) is on. Holidays: KIS refuses, and it is tried again later. */
export function nyClock(now = new Date()): { date: string; open: boolean } {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: "America/New_York",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    weekday: "short",
  }).formatToParts(now);
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "";
  const minutes = (Number(get("hour")) % 24) * 60 + Number(get("minute"));
  const weekend = get("weekday") === "Sat" || get("weekday") === "Sun";
  return { date: `${get("year")}-${get("month")}-${get("day")}`, open: !weekend && minutes >= 570 && minutes < 960 };
}

/** Filled: the holding moved by the order's units since it was made. */
export function filled(o: Pick<db.Standing, "side" | "units" | "heldBefore">, held: number): boolean {
  return o.side === "buy" ? held >= o.heldBefore + o.units - 1e-6 : held <= o.heldBefore - o.units + 1e-6;
}

const lastTry = new Map<number, number>();
const RETRY_MS = 30 * 60_000;

/**
 * One pass: a filled order is closed and told, one past its date is let go,
 * and one not yet out in today's session goes out at its own price. A
 * refusal (a holiday, too few dollars) is tried again half an hour later.
 */
export async function standingPass(holdings: () => Promise<Holding[]>, now = new Date(), only?: number): Promise<void> {
  const active = (await db.standingOrders(50)).filter((o) => o.status === "active" && (only === undefined || o.id === only));
  if (active.length === 0) return;
  const { date, open } = nyClock(now);
  const held = await holdings();
  for (const o of active) {
    const h = held.find((x) => x.venue === "kis" && x.symbol === o.symbol);
    const name = h?.name ?? o.symbol;
    if (filled(o, h?.quantity ?? 0)) {
      await db.updateStanding(o.id, { status: "filled", message: `${date} 체결` });
      tell({
        kind: "assets.order",
        tier: "now",
        title: `${name} ${o.units}주 ${o.side === "buy" ? "매수" : "매도"} 체결`,
        body: `대기 주문 $${o.limitPrice.toFixed(2)} 지정가`,
        url: "/assets?focus=orders",
        key: `assets:standing:${o.id}:filled`,
      });
      continue;
    }
    if (o.expiresOn && date > o.expiresOn) {
      await db.updateStanding(o.id, { status: "expired", message: `${o.expiresOn} 까지 체결되지 않았어요` });
      continue;
    }
    if (!open || o.placedOn === date) continue;
    if (only === undefined && Date.now() - (lastTry.get(o.id) ?? 0) < RETRY_MS) continue;
    lastTry.set(o.id, Date.now());
    try {
      const quote = await kisUsQuote(o.symbol);
      const answer = await kisUsOrder({ symbol: o.symbol, excd: quote.excd, side: o.side, qty: o.units, price: o.limitPrice });
      const message = `${date} 장에 $${o.limitPrice.toFixed(2)} 지정가로 냈어요 (지금 $${quote.last.toFixed(2)})`;
      await db.updateStanding(o.id, { placedOn: date, brokerId: answer.odno, message });
      await db.recordOrder({
        venue: "kis", symbol: o.symbol, side: o.side, units: o.units, amountKrw: 0, limitPrice: o.limitPrice, currency: "USD",
        reason: "standing", planId: `standing-${o.id}`, status: "placed", brokerId: answer.odno, message,
      });
    } catch (e) {
      await db.updateStanding(o.id, { message: `${date}: ${(e as Error).message.slice(0, 200)} - 30분 뒤 다시 낼게요` });
    }
  }
}

/**
 * The highest price (to the cent, under the last trade) at which the
 * account's dollars buy `units` shares, KIS's commission included - for a
 * "buy it back when it comes down" whose limit is what the money allows.
 */
export async function affordable(symbol: string, units: number): Promise<{ last: number; excd: string; price: number | null }> {
  const quote = await kisUsQuote(symbol);
  const can = async (cents: number) => (await kisUsBuyable(symbol, quote.excd, cents / 100)).qty >= units;
  let hi = Math.floor(quote.last * 100);
  if (await can(hi)) return { last: quote.last, excd: quote.excd, price: hi / 100 };
  let lo = Math.floor(quote.last * 70);
  if (!(await can(lo))) return { last: quote.last, excd: quote.excd, price: null };
  while (hi - lo > 1) {
    const mid = Math.floor((lo + hi) / 2);
    if (await can(mid)) lo = mid;
    else hi = mid;
  }
  return { last: quote.last, excd: quote.excd, price: lo / 100 };
}

/** Cancel a standing order, and today's order at the broker if it is out. */
export async function cancelStanding(o: db.Standing, now = new Date()): Promise<string> {
  let note = "취소했어요";
  if (o.placedOn === nyClock(now).date && o.brokerId) {
    try {
      const quote = await kisUsQuote(o.symbol);
      await kisUsCancel({ symbol: o.symbol, excd: quote.excd, odno: o.brokerId, qty: o.units });
      note = "취소했어요 (오늘 낸 주문도 취소)";
    } catch (e) {
      note = `취소했어요. 오늘 낸 주문은 증권사에서 취소하지 못했어요: ${(e as Error).message.slice(0, 120)}`;
    }
  }
  await db.updateStanding(o.id, { status: "cancelled", message: note });
  return note;
}
