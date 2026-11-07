/**
 * Dates and times as the console says them, in one place, so a date read on
 * one tab is the date read on the next - all on Seoul's clock whatever
 * timezone the phone is in:
 *
 * - dateLabel  "11월 7일 (토)"       a day, named; the year first when it is not this one
 * - monthDay   "11월 7일"            the same without its weekday, for a dense row
 * - dayRange   "11월 3일–5일"        a stretch of days
 * - dayLabel   "오늘" "어제" "내일"  a day as a person says it, else dateLabel
 * - dateTime   "오늘 14:02" "11월 7일 14:02"  a moment
 * - ago        "방금" "3분 전" "2시간 전"     and past twelve hours, dateTime
 */
const ZONE = "Asia/Seoul";

// The formatters, made once: toLocale* builds a formatter on every call, and a
// thread works out every turn's day and time on each frame of an answer.
const DAY = new Intl.DateTimeFormat("sv-SE", { timeZone: ZONE });
const CLOCK = new Intl.DateTimeFormat("ko-KR", { timeZone: ZONE, hour: "2-digit", minute: "2-digit", hour12: false });
const WEEK = ["일", "월", "화", "수", "목", "금", "토"];

/** The calendar day a moment falls on, on the operator's clock (YYYY-MM-DD). */
export function dayOf(d: Date): string {
  return DAY.format(d);
}

function parse(iso: string | Date | undefined): Date | null {
  if (!iso) return null;
  const t = iso instanceof Date ? iso : new Date(iso);
  return Number.isNaN(t.getTime()) ? null : t;
}

/** "14:02" - the wall clock of a turn. `at` is the fallback for one with no full stamp. */
export function clock(iso: string | undefined, at?: string): string {
  const t = parse(iso);
  if (!t) return at ?? "";
  return CLOCK.format(t);
}

/** The day a turn was said on, or "" when it has no full stamp. */
export function turnDay(iso: string | undefined): string {
  const t = parse(iso);
  return t ? dayOf(t) : "";
}

function parts(day: string): { y: number; m: number; d: number; weekday: string } {
  const [y, m, d] = day.split("-").map(Number);
  return { y, m, d, weekday: WEEK[new Date(Date.UTC(y, m - 1, d)).getUTCDay()] };
}

/** A day shifted by n days (YYYY-MM-DD). */
export function shiftDay(day: string, n: number): string {
  const { y, m, d } = parts(day);
  return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10);
}

/** "11월 7일 (토)"; "2025년 11월 7일 (금)" when the year is not this one. */
export function dateLabel(day: string, now = new Date()): string {
  const { y, m, d, weekday } = parts(day);
  const date = `${m}월 ${d}일 (${weekday})`;
  return String(y) === dayOf(now).slice(0, 4) ? date : `${y}년 ${date}`;
}

/** "11월 7일"; "2025년 11월 7일" when the year is not this one. */
export function monthDay(day: string, now = new Date()): string {
  const { y, m, d } = parts(day);
  const date = `${m}월 ${d}일`;
  return String(y) === dayOf(now).slice(0, 4) ? date : `${y}년 ${date}`;
}

/** "11월 3일–5일", or "11월 30일–12월 2일" across a month. */
export function dayRange(from: string, to: string, now = new Date()): string {
  if (from.slice(0, 7) === to.slice(0, 7)) return `${monthDay(from, now)}–${Number(to.slice(8, 10))}일`;
  return `${monthDay(from, now)}–${monthDay(to, now)}`;
}

/** "오늘", "어제", "내일", else the date: how a person names a day. */
export function dayLabel(day: string, now = new Date()): string {
  const today = dayOf(now);
  if (day === today) return "오늘";
  if (day === shiftDay(today, -1)) return "어제";
  if (day === shiftDay(today, 1)) return "내일";
  return dateLabel(day, now);
}

/** A moment: "오늘 14:02", "어제 09:00", "내일 09:00", "11월 7일 14:02". */
export function dateTime(iso: string | Date | undefined, now = new Date()): string {
  const t = parse(iso);
  if (!t) return "";
  return `${dayLabel(dayOf(t), now)} ${CLOCK.format(t)}`;
}

/** How long ago: "방금", "3분 전", "2시간 전" within twelve hours; further back, the moment itself. */
export function ago(iso: string | Date | undefined, now = new Date()): string {
  const t = parse(iso);
  if (!t) return "";
  const min = Math.round((now.getTime() - t.getTime()) / 60_000);
  if (min < 1) return "방금";
  if (min < 60) return `${min}분 전`;
  if (min < 12 * 60) return `${Math.round(min / 60)}시간 전`;
  return dateTime(t, now);
}

/**
 * When a message was said, in one phrase: the time alone today, "어제" the
 * day before, the date further back. For a quote, which stands apart from the
 * day dividers that place a turn in the thread.
 */
export function turnTime(iso: string | undefined, at: string | undefined, now = new Date()): string {
  const t = parse(iso);
  if (!t) return at ?? "";
  if (dayOf(t) === dayOf(now)) return CLOCK.format(t);
  return dateTime(t, now);
}

/**
 * The agent's own stamp on a quote ("10-15 14:02", Seoul time) as a person
 * reads it, for a quoted turn that is not loaded on the page.
 */
export function refTime(at: string | undefined, now = new Date()): string {
  const m = at?.match(/^(\d{2})-(\d{2}) (\d{2}:\d{2})$/);
  if (!m) return at ?? "";
  const day = `${dayOf(now).slice(0, 4)}-${m[1]}-${m[2]}`;
  if (day === dayOf(now)) return m[3];
  return `${dayLabel(day, now)} ${m[3]}`;
}
