import * as db from "./db.js";
import type { Flow, LedgerCheck } from "./types.js";
import type { Settlement } from "./venues.js";

/**
 * KIS deposits and withdrawals, without a deposits API - for the share
 * account and the gold account alike.
 *
 * The share account holds won and dollars (the gold account only won, and
 * settles its trades in won; the same sums hold with the dollar side at zero). Won moves when money comes in or goes
 * out, and when it is exchanged; dollars move when a trade settles (KIS says
 * by how much - settlementsOf), when they are exchanged, and with the odd
 * dividend. An exchange takes from one side what it gives the other, so at
 * one rate it nets out:
 *
 *   in - out ≈ Δwon + rate × (Δdollars - settled dollars)
 *
 * Going forward that is exact to the day: the settled cash is written down
 * once a day per currency (db.recordKisCash) and each day is measured against
 * the last, at that day's rate - so dollars merely held while the rate moves
 * count as nothing. The past has no such record, so there the hand-kept ledger
 * stays and is checked instead (reconcile).
 */

/** Smaller than this is a dividend, interest or an exchange's spread, not someone moving money. */
export const FLOW_MIN_KRW = 5_000;
/** The ledger check's floor; the exchanges it has to guess add to it (see reconcile). */
const CHECK_MIN_KRW = 10_000;
/** How far a guessed exchange rate may be off: KIS's rate on a nearby day, and the spread. */
const RATE_SLACK = 0.005;

const nextDay = (date: string) => {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().slice(0, 10);
};
const md = (date: string) => `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;
const won = (v: number) => `${v < 0 ? "−" : ""}₩${Math.round(Math.abs(v)).toLocaleString("ko-KR")}`;

export type CashVenue = "kis" | "gold";
const ACCOUNT: Record<CashVenue, string> = { kis: "한국투자증권", gold: "금현물 계좌" };

/** Net settled money from `since` through `date`, per currency. */
export function settledThrough(settlements: Settlement[], date: string): { krw: number; usd: number } {
  const upTo = settlements.filter((s) => s.date <= date);
  return {
    krw: upTo.reduce((sum, s) => sum + s.krw, 0),
    usd: upTo.reduce((sum, s) => sum + s.usd, 0),
  };
}

/**
 * Write today's settled cash and turn the change since the last day into a
 * found flow, stored on today's date (source 'auto'). Recomputed on every
 * look, so the day's last look wins.
 *
 * A trade KIS books a day later than the cash shows it would read as money
 * in one day and the same money out the next. Such a pair cancels: when
 * today's flow undoes yesterday's, both are dropped.
 */
export async function detectFlow(
  venue: CashVenue,
  today: string,
  cash: { krw: number; usd: number },
  settlements: Settlement[],
  usdKrw: number,
): Promise<void> {
  const settled = settledThrough(settlements, today);
  const prev = await db.recordCash({
    venue,
    date: today,
    cashKrw: cash.krw,
    cashUsd: cash.usd,
    settledKrw: settled.krw,
    settledUsd: settled.usd,
  });
  if (!prev) return; // the first day written: nothing to measure against yet

  const wonSide = cash.krw - prev.cashKrw - (settled.krw - prev.settledKrw);
  const dollarSide = usdKrw * (cash.usd - prev.cashUsd - (settled.usd - prev.settledUsd));
  const flow = wonSide + dollarSide;
  // When the two sides moved opposite ways, that much was exchanged, and the
  // exchange's own rate and spread leave a remainder that is nobody's money.
  const exchanged = Math.sign(wonSide) !== Math.sign(dollarSide) ? Math.min(Math.abs(wonSide), Math.abs(dollarSide)) : 0;
  const min = FLOW_MIN_KRW + exchanged * RATE_SLACK;
  const before = await db.autoFlow(venue, prev.date);
  if (before !== null && Math.abs(before + flow) < min) {
    await db.setAutoFlow(venue, prev.date, null, "");
    await db.setAutoFlow(venue, today, null, "");
    return;
  }
  const from = nextDay(prev.date);
  await db.setAutoFlow(
    venue,
    today,
    Math.abs(flow) >= min ? Math.round(flow) : null,
    // The row is already marked as found; the memo only says when.
    `${flow > 0 ? "입금" : "출금"}${from === today ? "" : ` (${md(from)}~${md(today)} 사이)`}`,
  );
}

/**
 * The KIS ledger against what the trades say.
 *
 * The recorded flows and the settled trades are replayed day by day from an
 * empty account, won and dollars apart. Exchanges are not recorded anywhere,
 * so they are inferred the only way they can have happened: a purchase the
 * dollars cannot cover was paid with won exchanged that day, and a
 * withdrawal the won cannot cover was paid with dollars exchanged that day,
 * at KIS's rate from the nearest trade. Two things can then be caught:
 * - a day the account runs dry: something settled or went out with too
 *   little recorded in - a deposit missing, or written down too late;
 * - an end that does not match today's cash: a flow left out, or written
 *   down with the wrong amount.
 * The inferred exchanges can be off by their rate, so the end is only
 * flagged past that margin. What cannot be caught is a mistake that cancels
 * out while cash sat in the account; that needs the brokerage's statement.
 */
export function reconcile(
  venue: CashVenue,
  flows: Flow[],
  settlements: Settlement[],
  now: { krw: number; usd: number },
  usdKrw: number,
): LedgerCheck[] {
  const wonIn = new Map<string, number>();
  for (const f of flows) if (f.venue === venue) wonIn.set(f.date, (wonIn.get(f.date) ?? 0) + f.amountKrw);
  for (const s of settlements) wonIn.set(s.date, (wonIn.get(s.date) ?? 0) + s.krw);
  const usdIn = new Map(settlements.map((s) => [s.date, s.usd]));
  const days = [...new Set([...wonIn.keys(), ...usdIn.keys()])].sort();
  // KIS's rate on the nearest trading day, before or after.
  const rateOn = (date: string) => {
    let best = usdKrw;
    let gap = Infinity;
    for (const s of settlements) {
      if (!s.rate) continue;
      const g = Math.abs(Date.parse(s.date) - Date.parse(date));
      if (g < gap) {
        gap = g;
        best = s.rate;
      }
    }
    return best;
  };

  const checks: LedgerCheck[] = [];
  let krw = 0;
  let usd = 0;
  let exchanged = 0;
  let dry = false;
  for (const date of days) {
    krw += wonIn.get(date) ?? 0;
    usd += usdIn.get(date) ?? 0;
    const rate = rateOn(date);
    if (usd < 0) {
      // Dollars short: won was exchanged for them.
      krw += usd * rate;
      exchanged += -usd * rate;
      usd = 0;
    } else if (krw < 0 && usd > 0) {
      // Won short: dollars were exchanged for it.
      const take = Math.min(usd, -krw / rate);
      usd -= take;
      krw += take * rate;
      exchanged += take * rate;
    }
    // Once per stretch below zero: the day it started is the one to look at.
    const below = krw <= -CHECK_MIN_KRW;
    if (below && !dry) {
      checks.push({
        venue,
        kind: "short",
        date,
        amountKrw: -krw,
        message: `${md(date)} 기록상 ${ACCOUNT[venue]} 현금이 ${won(-krw)} 모자라요. 그 전 입금이 빠졌거나 날짜가 늦게 적혔어요.`,
      });
    }
    dry = below;
  }

  const expected = krw + usd * usdKrw;
  const actual = now.krw + now.usd * usdKrw;
  const diff = expected - actual;
  if (Math.abs(diff) >= CHECK_MIN_KRW + exchanged * RATE_SLACK) {
    checks.push({
      venue,
      kind: "total",
      amountKrw: diff,
      message:
        diff > 0
          ? `기록대로면 ${ACCOUNT[venue]}에 ${won(expected)}이 남아야 하는데 지금 ${won(actual)}이에요. 적지 않은 출금이나 크게 적힌 입금이 ${won(diff)}쯤 있어요.`
          : `기록대로면 ${ACCOUNT[venue]}에 ${won(expected)}이 남아야 하는데 지금 ${won(actual)}이에요. 적지 않은 입금이나 작게 적힌 금액이 ${won(-diff)}쯤 있어요.`,
    });
  }
  return checks;
}
