/**
 * How long each kind of number may be reused, by what actually changes it.
 *
 * - Balances and current prices at KIS move only while a market is open: a
 *   share's price is fixed from the close to the next open, and a balance
 *   changes when you trade or move money. So they are reused for seconds
 *   while trading and for minutes otherwise - and dropped the moment a
 *   principal entry is written (index.ts).
 * - Upbit trades around the clock and is not the rate-limited side, so its
 *   balance and prices are reused briefly, always.
 * - Past closes never change at all and are kept in the database (change.ts).
 */

const hm = (d: Date, tz: string) => {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).formatToParts(d);
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "";
  return { day: get("weekday"), minutes: Number(get("hour")) * 60 + Number(get("minute")) };
};

const weekday = (day: string) => day !== "Sat" && day !== "Sun";

/** KRX (shares and the gold market): weekdays 09:00-15:30 KST. Holidays read as open, which only costs a few extra calls. */
export function krxOpen(now = new Date()): boolean {
  const { day, minutes } = hm(now, "Asia/Seoul");
  return weekday(day) && minutes >= 9 * 60 && minutes <= 15 * 60 + 30;
}

/** US shares, extended hours included (04:00-20:00 ET) - KIS quotes them then too. */
export function usOpen(now = new Date()): boolean {
  const { day, minutes } = hm(now, "America/New_York");
  return weekday(day) && minutes >= 4 * 60 && minutes <= 20 * 60;
}

export const OPEN_TTL_MS = 20_000;
export const CLOSED_TTL_MS = 5 * 60_000;
export const UPBIT_TTL_MS = 15_000;

/**
 * A cached async call: one in flight at a time, reused while fresh, and
 * cleared on demand. A failure is not cached - the next call tries again.
 */
export function memo<T>(ttl: () => number, fn: () => Promise<T>) {
  let value: { at: number; v: T } | null = null;
  let inflight: Promise<T> | null = null;
  let generation = 0;
  return {
    async get(): Promise<T> {
      if (value && Date.now() - value.at < ttl()) return value.v;
      if (inflight) return inflight;
      const started = generation;
      inflight = fn()
        .then((v) => {
          if (started === generation) value = { at: Date.now(), v };
          return v;
        })
        .finally(() => {
          inflight = null;
        });
      return inflight;
    },
    clear() {
      value = null;
      generation += 1;
    },
  };
}
