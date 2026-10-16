const ZONE = "Asia/Seoul";

/** The calendar day a moment falls on, on the operator's clock (YYYY-MM-DD). */
export function dayOf(d: Date): string {
  return d.toLocaleDateString("sv-SE", { timeZone: ZONE });
}

function parse(iso: string | undefined): Date | null {
  if (!iso) return null;
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? null : t;
}

/** "14:02" - the wall clock of a turn. `at` is the fallback for one with no full stamp. */
export function clock(iso: string | undefined, at?: string): string {
  const t = parse(iso);
  if (!t) return at ?? "";
  return t.toLocaleTimeString("ko-KR", { timeZone: ZONE, hour: "2-digit", minute: "2-digit", hour12: false });
}

/** The day a turn was said on, or "" when it has no full stamp. */
export function turnDay(iso: string | undefined): string {
  const t = parse(iso);
  return t ? dayOf(t) : "";
}

/**
 * A day as the divider between days names it: "오늘", "어제", "10월 15일
 * 수요일", and the year only when it is not this one.
 */
export function dayLabel(day: string, now = new Date()): string {
  const today = dayOf(now);
  if (day === today) return "오늘";
  if (day === dayOf(new Date(now.getTime() - 86_400_000))) return "어제";
  const [y, m, d] = day.split("-").map(Number);
  const weekday = new Date(Date.UTC(y, m - 1, d)).toLocaleDateString("ko-KR", { timeZone: "UTC", weekday: "long" });
  const date = `${m}월 ${d}일 ${weekday}`;
  return String(y) === today.slice(0, 4) ? date : `${y}년 ${date}`;
}

/**
 * When a message was said, in one phrase: the time alone today, "어제" the
 * day before, the date further back. For a quote, which stands apart from the
 * day dividers that place a turn in the thread.
 */
export function turnTime(iso: string | undefined, at: string | undefined, now = new Date()): string {
  const t = parse(iso);
  if (!t) return at ?? "";
  const day = dayOf(t);
  const hm = clock(iso);
  if (day === dayOf(now)) return hm;
  const label = dayLabel(day, now);
  // "어제 14:02", "10월 15일 14:02" - the weekday is the divider's, not a quote's.
  return `${label === "어제" ? label : label.replace(/ \S+요일$/, "")} ${hm}`;
}

/**
 * The agent's own stamp on a quote ("10-15 14:02", Seoul time) as a person
 * reads it, for a quoted turn that is not loaded on the page.
 */
export function refTime(at: string | undefined, now = new Date()): string {
  const m = at?.match(/^(\d{2})-(\d{2}) (\d{2}:\d{2})$/);
  if (!m) return at ?? "";
  const today = dayOf(now);
  const day = `${today.slice(0, 4)}-${m[1]}-${m[2]}`;
  if (day === today) return m[3];
  if (day === dayOf(new Date(now.getTime() - 86_400_000))) return `어제 ${m[3]}`;
  return `${Number(m[1])}월 ${Number(m[2])}일 ${m[3]}`;
}
