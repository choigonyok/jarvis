import type { Holding, Change, Changes, Point, ReturnPoint, Window } from "./types.js";
import { kisDailyCloses, kisGoldDailyCloses, upbitDailyCloses } from "./venues.js";

/**
 * How the basket you hold today has moved over a day, a month and a year.
 *
 * Measured by re-pricing *today's* holdings at past closes, not by comparing
 * against a recorded total. That distinction matters: a recorded total moves
 * when you deposit money, which is not a return; this answers "what I own has
 * gone up or down this much", which is the question the number on a portfolio
 * screen is normally read as.
 *
 * A holding whose history does not reach back far enough is left out of that
 * window and named, because averaging it in at today's price would quietly
 * report a young position as flat.
 */

const DAYS: Record<Window, number> = { day: 1, month: 30, year: 365 };

function isoDaysAgo(days: number, from = new Date()): string {
  const d = new Date(from);
  d.setDate(d.getDate() - days);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(
    d.getDate(),
  ).padStart(2, "0")}`;
}

/**
 * The close on or just before `date`.
 *
 * Markets shut at weekends and holidays, so asking for "30 days ago" lands on
 * a day with no bar about two times in seven. Walking back to the last day
 * that traded is what makes the comparison a real one.
 */
function closeOnOrBefore(
  closes: Map<string, number>,
  date: string,
  limit = 10,
): number | null {
  const [y, m, d] = date.split("-").map(Number);
  const cursor = new Date(y, m - 1, d);
  for (let i = 0; i <= limit; i += 1) {
    const key = `${cursor.getFullYear()}-${String(cursor.getMonth() + 1).padStart(2, "0")}-${String(
      cursor.getDate(),
    ).padStart(2, "0")}`;
    const price = closes.get(key);
    if (price !== undefined) return price;
    cursor.setDate(cursor.getDate() - 1);
  }
  return null;
}

/**
 * Fetch every holding's price history once.
 *
 * Both the windowed rates and the curve are built from the same data, so it
 * would be wasteful - and worse, capable of disagreeing - to ask twice.
 */
async function historiesFor(holdings: Holding[]) {
  const histories = new Map<string, Map<string, number>>();
  await Promise.all(
    holdings.map(async (h) => {
      const closes =
        h.venue === "upbit"
          ? await upbitDailyCloses(h.symbol, 400).catch(() => new Map())
          : h.venue === "gold"
            ? await kisGoldDailyCloses(400).catch(() => new Map())
          : h.currency === "USD"
            ? await kisDailyCloses(h.symbol, 400).catch(() => new Map())
            : // Domestic shares would need a third endpoint; none are held.
              new Map<string, number>();
      histories.set(h.id, closes);
    }),
  );
  return histories;
}

/**
 * The total value of today's basket, day by day.
 *
 * Only days where *every* holding has a close are plotted. A day where one
 * position is missing would dip the line by that position's whole value and
 * read as a crash that never happened - a gap in the data must not look like
 * a gap in the money.
 */
export function seriesFrom(
  holdings: Holding[],
  histories: Map<string, Map<string, number>>,
  usdKrw: number,
  days: number,
  now = new Date(),
): Point[] {
  const points: Point[] = [];

  for (let i = days; i >= 0; i -= 1) {
    const date = isoDaysAgo(i, now);
    let total = 0;
    let complete = true;

    for (const holding of holdings) {
      const close = closeOnOrBefore(histories.get(holding.id) ?? new Map(), date, 5);
      if (close === null) {
        complete = false;
        break;
      }
      total += holding.quantity * close * (holding.currency === "USD" ? usdKrw : 1);
    }

    if (complete && total > 0) points.push({ date, valueKrw: total });
  }
  return points;
}

/**
 * One holding's price return since the window began, day by day: how that
 * asset moved, whatever amount of it is held. The first trading day of the
 * window is 0%. Price only - for a dollar share the dollar price, so the
 * curve is the share's, not the exchange rate's.
 */
export function returnSeries(
  closes: Map<string, number>,
  days: number,
  now = new Date(),
): ReturnPoint[] {
  const points: ReturnPoint[] = [];
  let base: number | null = null;
  let last: number | null = null;
  for (let i = days; i >= 0; i -= 1) {
    const date = isoDaysAgo(i, now);
    const close = closeOnOrBefore(closes, date, 5);
    if (close === null) continue;
    if (base === null) base = close;
    // A weekend repeats Friday's close; one point per trading day is enough.
    if (close === last && i !== 0) continue;
    last = close;
    points.push({ date, rate: close / base - 1 });
  }
  return points;
}

export async function changesFor(
  holdings: Holding[],
  usdKrw: number,
): Promise<{
  changes: Changes;
  series: Record<Window, Point[]>;
  holdingSeries: Record<string, Record<Window, ReturnPoint[]>>;
}> {
  const histories = await historiesFor(holdings);
  const result = {} as Changes;

  for (const window of ["day", "month", "year"] as Window[]) {
    const when = isoDaysAgo(DAYS[window]);
    let then = 0;
    let now = 0;
    const missing: string[] = [];

    for (const holding of holdings) {
      const past = closeOnOrBefore(histories.get(holding.id) ?? new Map(), when);
      if (past === null) {
        missing.push(holding.name);
        continue;
      }
      const rate = holding.currency === "USD" ? usdKrw : 1;
      then += holding.quantity * past * rate;
      now += holding.valueKrw;
    }

    result[window] =
      then > 0
        ? { rate: (now - then) / then, amountKrw: now - then, missing }
        : { rate: null, amountKrw: null, missing };
  }

  const series: Record<Window, Point[]> = {
    day: seriesFrom(holdings, histories, usdKrw, DAYS.day),
    month: seriesFrom(holdings, histories, usdKrw, DAYS.month),
    year: seriesFrom(holdings, histories, usdKrw, DAYS.year),
  };

  const holdingSeries: Record<string, Record<Window, ReturnPoint[]>> = {};
  for (const h of holdings) {
    const closes = histories.get(h.id) ?? new Map<string, number>();
    if (closes.size === 0) continue;
    holdingSeries[h.id] = {
      day: returnSeries(closes, DAYS.day),
      month: returnSeries(closes, DAYS.month),
      year: returnSeries(closes, DAYS.year),
    };
  }

  return { changes: result, series, holdingSeries };
}
