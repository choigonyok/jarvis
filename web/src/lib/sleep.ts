import { HttpError } from "@/lib/use-resource";
import { dayOf, shiftDay } from "@/lib/when";

/**
 * The operator's nights (context-svc GET /sleep, through /api/context/sleep):
 * what the phone's health store said of each night, sent each morning by a
 * shortcut or Health Auto Export and made into one night a day there - the day
 * it ended on, in Seoul. The guest has none of it.
 */

/** What a stretch of a night was. */
export type Stage = "inbed" | "awake" | "asleep" | "core" | "deep" | "rem";

/** A stretch of a night, in minutes from when it began. */
export type Segment = { from: number; to: number; stage: Stage };

/** A sleep of a day other than its night. */
export type Nap = { bedAt: string; wakeAt: string; asleepMinutes: number };

export type Night = {
  /** The day it ended on (YYYY-MM-DD): the night of the 15th to the 16th is the 16th's. */
  night: string;
  bedAt: string;
  wakeAt: string;
  asleepMinutes: number;
  inBedMinutes: number;
  awakeMinutes: number;
  /** The stages, when the source told them (a watch). */
  deepMinutes?: number;
  coreMinutes?: number;
  remMinutes?: number;
  /** Only the time in bed came (an iPhone without a watch): asleepMinutes is that. */
  bedOnly?: boolean;
  source?: string;
  naps: Nap[];
  /** Stretch by stretch, for as long as the raw reports are kept (keepDays). */
  timeline?: Segment[];
  updatedAt?: string;
};

/** The nights of the last days summed up: the mean, the usual clock, and how far a night strays from it. */
export type Window = {
  days: number;
  nights: number;
  asleepMinutes?: number;
  /** "23:48" */
  bedAt?: string;
  wakeAt?: string;
  bedSpreadMinutes: number;
  wakeSpreadMinutes: number;
};

export type Sleep = {
  from: string;
  to: string;
  /** Newest first. */
  nights: Night[];
  summary: { week: Window; month: Window };
  keepDays: number;
};

export async function readSleep(signal?: AbortSignal): Promise<Sleep> {
  const res = await fetch("/api/context/sleep", { cache: "no-store", signal });
  const body = (await res.json().catch(() => null)) as (Sleep & { error?: string }) | null;
  const message = body?.error ?? "수면 기록을 불러오지 못했어요.";
  if (!res.ok) throw new HttpError(res.status, message);
  if (!body) throw new Error(message);
  return body;
}

/** "7시간 12분", "45분", "8시간" - as its numbers and their units, for a figure that sets them apart. */
export function durationParts(minutes: number): { n: number; unit: string }[] {
  const m = Math.max(0, Math.round(minutes));
  const h = Math.floor(m / 60);
  const parts = [];
  if (h > 0) parts.push({ n: h, unit: "시간" });
  if (m % 60 > 0 || h === 0) parts.push({ n: m % 60, unit: "분" });
  return parts;
}

/** "7시간 12분". */
export function duration(minutes: number): string {
  return durationParts(minutes)
    .map((p) => `${p.n}${p.unit}`)
    .join(" ");
}

/** "7:12": a night's length where a column of them is read down. */
export function shortDuration(minutes: number): string {
  const m = Math.max(0, Math.round(minutes));
  return `${Math.floor(m / 60)}:${String(m % 60).padStart(2, "0")}`;
}

/** Seoul is UTC+9 the year round: its wall clock is the UTC one moved on by nine hours. */
const SEOUL = 9 * 60;

/** Minutes past midnight on Seoul's clock. */
export function minuteOfDay(iso: string): number {
  const t = new Date(iso);
  return (((t.getUTCHours() * 60 + t.getUTCMinutes() + SEOUL) % 1440) + 1440) % 1440;
}

/** "23:41" on Seoul's clock. */
export function clockAt(iso: string): string {
  const m = minuteOfDay(iso);
  return `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
}

/**
 * Where the diary's clock starts: 18:00. A night is laid out in minutes past
 * it, so one that crosses midnight is one unbroken span, and an afternoon nap
 * still falls before the next evening.
 */
const EVENING = 18 * 60;

/** Minutes past 18:00, for a time of day given as minutes past midnight. */
export const sinceEvening = (minute: number) => (((minute - EVENING) % 1440) + 1440) % 1440;

/** "23:48" as minutes past 18:00. */
export function clockSinceEvening(clock: string): number {
  const [h, m] = clock.split(":").map(Number);
  return sinceEvening(h * 60 + m);
}

/** A night on the diary's clock: from bed to waking, in minutes past 18:00. */
export function span(n: Night): { from: number; to: number } {
  const from = sinceEvening(minuteOfDay(n.bedAt));
  return { from, to: from + Math.max(0, (Date.parse(n.wakeAt) - Date.parse(n.bedAt)) / 60_000) };
}

/**
 * The stretch of the clock the diary shows: the nights' with half an hour to
 * spare, on whole hours, and never less than 22:00 to 08:00 - one night alone
 * does not fill the width.
 */
export function clockRange(nights: Night[]): { from: number; to: number } {
  let from = 4 * 60;
  let to = 14 * 60;
  for (const n of nights) {
    const s = span(n);
    from = Math.min(from, s.from - 30);
    to = Math.max(to, s.to + 30);
  }
  return { from: Math.max(0, Math.floor(from / 60) * 60), to: Math.ceil(to / 60) * 60 };
}

/** The hour marks under the diary: every two hours, four when the range is long. */
export function hourTicks(range: { from: number; to: number }): { at: number; label: string }[] {
  const step = range.to - range.from > 16 * 60 ? 240 : 120;
  const out = [];
  for (let at = Math.ceil(range.from / step) * step; at <= range.to; at += step) {
    out.push({ at, label: String((18 + at / 60) % 24).padStart(2, "0") });
  }
  return out;
}

/** The days the diary has a row for: `count` of them, newest first, ending on `last`. */
export function daysBack(last: string, count: number): string[] {
  return Array.from({ length: count }, (_, i) => shiftDay(last, -i));
}

/** Today, on Seoul's clock. */
export const today = () => dayOf(new Date());

/**
 * How a night compares with the operator's own usual: the mean of the nights
 * in the thirty days before it, from three of them on.
 */
export function usualBefore(nights: Night[], night: Night): number | null {
  const first = shiftDay(night.night, -30);
  const before = nights.filter((n) => n.night < night.night && n.night >= first);
  if (before.length < 3) return null;
  return before.reduce((sum, n) => sum + n.asleepMinutes, 0) / before.length;
}

/** "평소보다 24분 더 잤어요", or null when there is no usual to say it against. */
export function againstUsual(nights: Night[], night: Night): string | null {
  const usual = usualBefore(nights, night);
  if (usual === null) return null;
  const diff = Math.round(night.asleepMinutes - usual);
  if (Math.abs(diff) < 10) return "평소만큼 잤어요";
  return diff > 0 ? `평소보다 ${duration(diff)} 더 잤어요` : `평소보다 ${duration(-diff)} 덜 잤어요`;
}

/** The stages as they are named, deepest first, and how each is drawn. */
export const STAGES: { stage: Stage; label: string }[] = [
  { stage: "deep", label: "깊은 수면" },
  { stage: "core", label: "코어 수면" },
  { stage: "rem", label: "렘 수면" },
];

export function stageMinutes(n: Night, stage: Stage): number | undefined {
  switch (stage) {
    case "deep":
      return n.deepMinutes;
    case "core":
      return n.coreMinutes;
    case "rem":
      return n.remMinutes;
  }
  return undefined;
}

/** Whether a night came with its stages. */
export const staged = (n: Night) => n.deepMinutes !== undefined || n.coreMinutes !== undefined || n.remMinutes !== undefined;

/** The days between two (YYYY-MM-DD): how old a night is. */
export function daysBetween(from: string, to: string): number {
  return Math.round((Date.parse(`${to}T00:00:00Z`) - Date.parse(`${from}T00:00:00Z`)) / 86_400_000);
}
