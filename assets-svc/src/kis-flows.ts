import * as db from "./db.js";
import type { Flow, LedgerCheck } from "./types.js";
import type { Settlement } from "./venues.js";

/**
 * KIS deposits and withdrawals, without a deposits API - for the share
 * account and the gold account alike.
 *
 * The share account holds won and dollars (the gold account only won, and
 * settles its trades in won the same day; the same sums hold with the dollar
 * side at zero). Won moves when money comes in or goes out, and when it is
 * exchanged; dollars move when a trade fills (KIS says by how much -
 * movementsOf), when they are exchanged, and with the odd dividend. An
 * exchange takes from one side what it gives the other, so at one rate it
 * nets out:
 *
 *   in - out ≈ Δwon + rate × (Δdollars - traded dollars)
 *
 * The dollars are the book's (venues.ts bookCash): settled, sold and bought
 * alike, so a fill moves them the moment it happens and its settlement does
 * not move them at all - and the trades are counted on their trade day. Both
 * sides then move together; nothing here has to know when KIS posts a
 * settlement into the deposit (it is the Korean morning after New York
 * settles, at no fixed hour: guessing it read a sale's dollars as taken out,
 * twice - 2026-10-19 and 2026-10-20).
 *
 * Going forward that is exact to the day: the cash is written down once a day
 * per currency (db.recordCash) and each day is measured against the last, at
 * that day's rate - so dollars merely held while the rate moves count as
 * nothing. An exchange seen between two looks the same day is taken out of
 * the day's sum (exchangeBetween): money put in and changed into dollars that
 * night is the won put in, not the dollars at the day's rate, which left the
 * exchange's spread off the deposit (2026-10-28). Put in and changed between
 * two looks, it cannot be told apart, and the rate stands. The past has no
 * such record, so there the hand-kept ledger stays and is checked instead
 * (reconcile).
 */

/** Smaller than this is a dividend, interest or an exchange's spread, not someone moving money. */
export const FLOW_MIN_KRW = 5_000;
/** The ledger check's floor; the exchanges it has to guess add to it (see reconcile). */
const CHECK_MIN_KRW = 10_000;
/** How far a guessed exchange rate may be off: KIS's rate on a nearby day, and the spread. */
const RATE_SLACK = 0.005;
/** The least a change between two looks is to be read as an exchange. */
const EXCHANGE_MIN_KRW = 1_000;
/** How far an exchange's own rate may sit from KIS's: its spread, and the rate moving meanwhile. */
const EXCHANGE_RATE_SLACK = 0.05;

const nextDay = (date: string) => {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().slice(0, 10);
};
const md = (date: string) => `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;
const won = (v: number) => `${v < 0 ? "−" : ""}₩${Math.round(Math.abs(v)).toLocaleString("ko-KR")}`;

export type CashVenue = "kis" | "gold";
const ACCOUNT: Record<CashVenue, string> = { kis: "한국투자증권", gold: "금현물 계좌" };

/** Net traded money through `date`, per currency: what the trades have moved the cash by. */
export function movedThrough(movements: Settlement[], date: string): { krw: number; usd: number } {
  const upTo = movements.filter((m) => m.date <= date);
  return {
    krw: upTo.reduce((sum, m) => sum + m.krw, 0),
    usd: upTo.reduce((sum, m) => sum + m.usd, 0),
  };
}

/**
 * The money put in (+) or taken out (-) since the last day that neither the
 * trades nor the day's exchanges explain, at today's rate - and the least it
 * must be to count as someone moving money (`min`).
 */
export function dayFlow(
  prev: Pick<db.CashDay, "cashKrw" | "cashUsd" | "settledKrw" | "settledUsd">,
  cash: { krw: number; usd: number },
  settled: { krw: number; usd: number },
  exch: { krw: number; usd: number },
  usdKrw: number,
): { flow: number; min: number } {
  // What was only exchanged today is neither put in nor taken out: the won
  // it took is added back, the dollars it made are not counted as come in.
  const wonSide = cash.krw - prev.cashKrw - (settled.krw - prev.settledKrw) + exch.krw;
  const dollarSide = usdKrw * (cash.usd - prev.cashUsd - (settled.usd - prev.settledUsd) - exch.usd);
  // When the two sides still moved opposite ways, that much was exchanged
  // between two looks, and the exchange's own rate and spread leave a
  // remainder that is nobody's money.
  const exchanged = Math.sign(wonSide) !== Math.sign(dollarSide) ? Math.min(Math.abs(wonSide), Math.abs(dollarSide)) : 0;
  return { flow: wonSide + dollarSide, min: FLOW_MIN_KRW + exchanged * RATE_SLACK };
}

/**
 * An exchange between two looks the same day: won out and dollars in, or the
 * other way, at about KIS's rate (its spread, and the rate moving meanwhile).
 * The won that went into dollars (negative when dollars went into won) and
 * the dollars it made; null when the change was something else - money in or
 * out, a dividend, nothing.
 */
export function exchangeBetween(
  earlier: Pick<db.CashDay, "cashKrw" | "cashUsd" | "settledKrw" | "settledUsd">,
  cash: { krw: number; usd: number },
  settled: { krw: number; usd: number },
  usdKrw: number,
): { krw: number; usd: number } | null {
  const won = cash.krw - earlier.cashKrw - (settled.krw - earlier.settledKrw);
  const usd = cash.usd - earlier.cashUsd - (settled.usd - earlier.settledUsd);
  if (Math.abs(won) < EXCHANGE_MIN_KRW || Math.abs(usd) < 0.01 || Math.sign(won) === Math.sign(usd)) return null;
  if (usdKrw <= 0 || Math.abs(-won / usd / usdKrw - 1) > EXCHANGE_RATE_SLACK) return null;
  return { krw: -won, usd };
}

/**
 * Write today's cash and turn the change since the last day that the trades
 * do not explain into a found flow, stored on today's date (source 'auto').
 * Recomputed on every look, so the day's last look wins.
 *
 * A trade KIS books a day later than the cash shows it would read as money
 * in one day and the same money out the next. Such a pair cancels: when
 * today's flow undoes yesterday's, both are dropped.
 */
export async function detectFlow(
  venue: CashVenue,
  today: string,
  cash: { krw: number; usd: number },
  movements: Settlement[],
  usdKrw: number,
): Promise<void> {
  const settled = movedThrough(movements, today);
  const { earlier, prev } = await db.cashDays(venue, today);
  // The day's exchanges so far, and one made since the last look.
  const exch = { krw: earlier?.exchKrw ?? 0, usd: earlier?.exchUsd ?? 0 };
  const made = earlier ? exchangeBetween(earlier, cash, settled, usdKrw) : null;
  if (made) {
    exch.krw += made.krw;
    exch.usd += made.usd;
  }
  await db.recordCash({
    venue,
    date: today,
    cashKrw: cash.krw,
    cashUsd: cash.usd,
    settledKrw: settled.krw,
    settledUsd: settled.usd,
    exchKrw: exch.krw,
    exchUsd: exch.usd,
  });
  if (!prev) return; // the first day written: nothing to measure against yet

  const { flow, min } = dayFlow(prev, cash, settled, exch, usdKrw);
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
 * The recorded flows and the trades are replayed day by day from an empty
 * account, won and dollars apart. Exchanges are not recorded anywhere,
 * so they are inferred the only way they can have happened: a purchase the
 * dollars cannot cover was paid with won exchanged that day, and a
 * withdrawal the won cannot cover was paid with dollars exchanged that day,
 * at KIS's rate from the nearest trade. Two things can then be caught:
 * - a day the account runs dry: something was bought or went out with too
 *   little recorded in - a deposit missing, or written down too late;
 * - an end that does not match today's cash: a flow left out, or written
 *   down with the wrong amount.
 * The inferred exchanges can be off by their rate, so the end is only
 * flagged past that margin. What cannot be caught is a mistake that cancels
 * out while cash sat in the account; that needs the brokerage's statement.
 *
 * The same replay says what holding dollars cost or made (`fxKrw`): each
 * dollar is carried at the won it was booked at, and when it is spent,
 * exchanged, or still held today, the rate then against that is a currency
 * gain or loss - the part of the return neither a sale nor a holding carries.
 *
 * What the account holds beyond what the ledger and the trades explain
 * (`unrecordedKrw`, actual minus expected) is money that moved with no record
 * of its own: what exchanges cost over KIS's booked rate (the spread, and the
 * day an exchange was really made), and dividends, which no KIS API used here
 * lists. Within the check's margin it is that; past it, the check says so.
 */
export function reconcile(
  venue: CashVenue,
  flows: Flow[],
  movements: Settlement[],
  now: { krw: number; usd: number },
  usdKrw: number,
  today = new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" }),
): { checks: LedgerCheck[]; fxKrw: number; unrecordedKrw: number } {
  // `now` is the book's cash (settled or not) and the trades are on their
  // trade day, so a trade is on both sides from the moment it fills - when
  // it settles changes neither (see the head of this file).
  const due = movements.filter((m) => m.date <= today);
  const wonIn = new Map<string, number>();
  for (const f of flows) if (f.venue === venue) wonIn.set(f.date, (wonIn.get(f.date) ?? 0) + f.amountKrw);
  for (const m of due) wonIn.set(m.date, (wonIn.get(m.date) ?? 0) + m.krw);
  const usdIn = new Map(due.map((m) => [m.date, m.usd]));
  const days = [...new Set([...wonIn.keys(), ...usdIn.keys()])].sort();
  // KIS's rate on the nearest trading day, before or after.
  const rateOn = (date: string) => {
    let best = usdKrw;
    let gap = Infinity;
    for (const m of movements) {
      if (!m.rate) continue;
      const g = Math.abs(Date.parse(m.date) - Date.parse(date));
      if (g < gap) {
        gap = g;
        best = m.rate;
      }
    }
    return best;
  };

  const checks: LedgerCheck[] = [];
  // Won the ledger says was put in, by day: what may end a day short.
  const putIn = new Map<string, number>();
  for (const f of flows) if (f.venue === venue && f.amountKrw > 0) putIn.set(f.date, (putIn.get(f.date) ?? 0) + f.amountKrw);
  // A stretch short, told once it is known how it ended.
  let short: LedgerCheck | null = null;
  let krw = 0;
  let usd = 0;
  // The won the dollars held were booked at, all together.
  let usdCost = 0;
  let fxKrw = 0;
  let exchanged = 0;
  let dry = false;
  // Dollars leaving the balance at `rate`: a gain or loss against their cost.
  const spend = (amount: number, rate: number) => {
    const avg = usd > 0 ? usdCost / usd : rate;
    fxKrw += amount * (rate - avg);
    usdCost -= amount * avg;
    usd -= amount;
  };
  for (const date of days) {
    krw += wonIn.get(date) ?? 0;
    const rate = rateOn(date);
    const moved = usdIn.get(date) ?? 0;
    if (moved > 0) {
      usd += moved;
      usdCost += moved * rate;
    } else if (moved < 0) {
      const fromBalance = Math.min(-moved, Math.max(0, usd));
      if (fromBalance > 0) spend(fromBalance, rate);
      const short = -moved - fromBalance;
      if (short > 0) {
        // Dollars short: won was exchanged for them, at the day's rate.
        krw -= short * rate;
        exchanged += short * rate;
      }
    }
    if (krw < 0 && usd > 0) {
      // Won short: dollars were exchanged for it.
      const take = Math.min(usd, -krw / rate);
      spend(take, rate);
      krw += take * rate;
      exchanged += take * rate;
    }
    // Once per stretch below zero: the day it started is the one to look at.
    const below = krw <= -CHECK_MIN_KRW;
    if (below && !dry) {
      short = {
        venue,
        kind: "short",
        date,
        amountKrw: -krw,
        message: `${md(date)} 기록상 ${ACCOUNT[venue]} 현금이 ${won(-krw)} 모자라요. 그 전 입금이 빠졌거나 날짜가 늦게 적혔어요.`,
      };
    }
    if (!below && dry && short) {
      // Ended the next day by money put in: the trade's day against the
      // deposit's. KIS dates a fill by New York's day, and a deposit found
      // from the day's cash takes Seoul's - won put in after midnight for that
      // night's purchase read as a day short (2026-10-27).
      const nextDayIn = date <= nextDay(short.date ?? date) && (putIn.get(date) ?? 0) > 0;
      if (!nextDayIn) checks.push(short);
      short = null;
    }
    dry = below;
  }
  if (short) checks.push(short);

  // Dollars still held, at today's rate against what they were booked at.
  if (usd > 0) fxKrw += usd * usdKrw - usdCost;
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
  return { checks, fxKrw, unrecordedKrw: -diff };
}
