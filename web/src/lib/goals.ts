// A goal, and how it stands, as mind worked it out from its marks. Nothing here
// adds up: the screen shows what mind says, so a phone and a laptop never differ.

export type Mark = "done" | "missed";
export type Cadence = "daily" | "days" | "weekly" | "monthly";

export type Cell = {
  day: string;
  /** The goal asks of this day in particular. */
  asks?: boolean;
  /** It may be marked: not yet to come, not rested. */
  open?: boolean;
  mark?: Mark;
};

export type Period = { from: string; to: string; target: number; done: number };

export type Progress = {
  today: string;
  asks: boolean;
  mark?: Mark;
  period: Period;
  need: number;
  room: number;
  tight?: boolean;
  lost?: boolean;
  streak: number;
  best: number;
  unit: string;
  /** Of what it has asked so far, how much was done. Null before it has asked. */
  rate: number | null;
  times: number;
  week: Cell[];
  says: string;
};

export type Goal = {
  id: string;
  title: string;
  note?: string;
  cadence: Cadence;
  times?: number;
  weekdays?: number[];
  start: string;
  end?: string;
  remind?: string;
  auto?: string;
  status: "active" | "paused" | "ended";
  rule: string;
  progress: Progress;
  line: string;
};

export type Bar = { from: string; to: string; target: number; done: number; open?: boolean };
export type WeekdayStat = { day: number; done: number; asks: number };
export type Stats = { bars: Bar[]; weekdays: WeekdayStat[]; marks: Record<string, Mark> };

/** What the form sends: every field a goal is made of. */
export type Draft = {
  title: string;
  note?: string;
  cadence: Cadence;
  times?: number;
  weekdays?: number[];
  start?: string;
  end?: string;
  remind?: string;
  auto?: string;
};

const BASE = "/api/mind/goals";

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    cache: "no-store",
    ...init,
    headers: init?.body ? { "content-type": "application/json" } : undefined,
  });
  const body = (await res.json().catch(() => null)) as (T & { error?: string }) | null;
  // 202: mind is away. The record is in and will be done; there is nothing to
  // draw yet, and the next read has it.
  if (!res.ok || res.status === 202 || !body) throw new Error(body?.error ?? "목표를 불러오지 못했습니다.");
  return body;
}

export async function listGoals(all = false): Promise<{ goals: Goal[]; today: string }> {
  return call(all ? "?all=1" : "");
}

export async function readGoal(id: string): Promise<{ goal: Goal; stats: Stats }> {
  return call(`/${encodeURIComponent(id)}`);
}

export async function makeGoal(draft: Draft): Promise<{ goal: Goal }> {
  return call("", { method: "POST", body: JSON.stringify(draft) });
}

export async function changeGoal(id: string, change: Partial<Draft> & { pause?: boolean; finish?: boolean }): Promise<{ goal: Goal }> {
  return call(`/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(change) });
}

export async function removeGoal(id: string): Promise<unknown> {
  return call(`/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** Says how a day went; "" takes it back. */
export async function markDay(id: string, day: string, mark: Mark | ""): Promise<{ goal: Goal; stats: Stats }> {
  return call(`/${encodeURIComponent(id)}/checks`, { method: "POST", body: JSON.stringify({ day, mark }) });
}

const WEEK = ["일", "월", "화", "수", "목", "금", "토"];

/** The week as a row reads it, Monday first. */
export const WEEK_ROW = ["월", "화", "수", "목", "금", "토", "일"];

/** The day of a date string: 0 Sunday, as mind counts. */
export function weekdayOf(day: string): number {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d)).getUTCDay();
}

/** "11/2(월)". */
export function dayLabel(day: string): string {
  return `${Number(day.slice(5, 7))}/${Number(day.slice(8, 10))}(${WEEK[weekdayOf(day)]})`;
}

/** "오늘", "어제", or the date. */
export function whenLabel(day: string, today: string): string {
  if (day === today) return "오늘";
  return day === shift(today, -1) ? "어제" : dayLabel(day);
}

/** day moved by n days. */
export function shift(day: string, n: number): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10);
}

/** The days of a month, in weeks from Monday, with the days of the months beside it empty. */
export function monthWeeks(year: number, month: number): (string | null)[][] {
  const first = new Date(Date.UTC(year, month - 1, 1));
  const count = new Date(Date.UTC(year, month, 0)).getUTCDate();
  const lead = (first.getUTCDay() + 6) % 7;
  const cells: (string | null)[] = Array.from({ length: lead }, () => null);
  for (let i = 1; i <= count; i++) cells.push(first.toISOString().slice(0, 8) + String(i).padStart(2, "0"));
  while (cells.length % 7) cells.push(null);
  const weeks: (string | null)[][] = [];
  for (let i = 0; i < cells.length; i += 7) weeks.push(cells.slice(i, i + 7));
  return weeks;
}

/** What is still open today: asked of, or wanted this week and not said. */
export function waitsToday(g: Goal): boolean {
  const p = g.progress;
  if (g.status !== "active" || p.mark) return false;
  if (g.cadence === "weekly" || g.cadence === "monthly") return p.need > 0;
  return p.asks;
}

/** A percent as the screen says it. */
export function percent(rate: number | null): string {
  return rate === null ? "-" : `${Math.round(rate * 100)}%`;
}
