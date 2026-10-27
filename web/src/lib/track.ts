/**
 * A day of where the operator went (context-svc GET /track, through
 * /api/context/track), made into what the 위치 screen draws and replays:
 * the places stayed at, the ways between them, and a clock for the replay
 * that runs slowly through what moved and quickly through what did not.
 *
 * The fixes come as the phone sent them. Here the rough ones are left out (a
 * cell tower's guess jumps kilometres), a stay is found where the fixes keep
 * inside a small circle for a while, and a long silence between two fixes far
 * apart is read as staying put until it was time to go: the phone reports
 * when it moves, not while it sits still.
 */

export type Fix = {
  at: string;
  lat: number;
  lon: number;
  /** Meters. */
  acc: number;
  /** km/h, as the phone measured it. */
  vel?: number;
  /** automotive, cycling, running, walking or stationary. */
  act?: string;
};

export type Visit = { region: string; kind?: string; enteredAt: string; leftAt?: string };

export type Region = { name: string; kind: string; lat: number; lon: number; radiusM: number; area?: string };

/** Whose phone: the operator's, or the guest's (the partner on the shared calendar). */
export type Who = "owner" | "guest";

export type Track = {
  who?: Who;
  date: string;
  /** The day's midnight and the next, in Seoul. */
  from: string;
  to: string;
  fixes: Fix[];
  /** The last good fix before the day: where it began. */
  before: Fix | null;
  visits: Visit[];
  /** The regions set on this phone: the guest's own, apart from the operator's. */
  regions: Region[];
  /** For the guest, the operator's regions: a place it named none of goes by these. */
  otherRegions?: Region[];
  /** How many days of fixes are kept, and the day of the oldest one. */
  keepDays: number;
  since?: string;
  /** How the phone was last told to watch, and why (context-svc's judgment). */
  watch?: Watch;
  /** Regions made in the console still on their way to the phone. */
  pending?: string[];
};

/** OwnTracks' monitoring modes, as context-svc says them. */
export const SIGNIFICANT = 1;
export const MOVE = 2;

/**
 * How a phone watches now, and why: in a region, still in one spot outside
 * them, in a car, moving, just out of a region, or a mode asked for by hand.
 * downgraded: it would be in Move but for its battery (below 20%, not
 * charging), so its way is drawn sparsely until it is charged.
 */
export type Watch = {
  at: string;
  mode: number;
  why: "region" | "still" | "car" | "moving" | "left" | "held";
  region?: string;
  batt?: number;
  charging?: boolean;
  downgraded?: boolean;
};

export type LatLon = { lat: number; lon: number };

/** How a way was gone. gap: the phone said nothing on the way. */
export type Mode = "drive" | "walk" | "move" | "gap";

/** A place in the day's order: a stay, or a fix passed on the way. */
export type Node = LatLon & {
  /** When it was reached - for the first, possibly the day's start. */
  start: number;
  /** The last moment there. */
  leave: number;
  stay: boolean;
  /** The region it lies in. */
  name?: string;
  kind?: string;
  /** What the phone said the body was doing on arriving, and on leaving. */
  actIn?: string;
  actOut?: string;
  vel?: number;
  /** The way out of it went unreported: a long silence, then a fix far off. */
  unseen?: boolean;
};

/** The way from one node to the next, gone between t0 and t1. */
export type Step = { a: Node; b: Node; t0: number; t1: number; meters: number; mode: Mode; kmh: number };

export type Stay = { type: "stay"; start: number; end: number; node: Node; ongoing: boolean };
export type Move = { type: "move"; start: number; end: number; meters: number; mode: Mode; from?: Node; to?: Node; steps: Step[] };
export type Item = Stay | Move;

/** The replay's clock: how real time and time into the replay map onto each other. */
export type Clock = {
  /** The replay's length at 1×, ms. */
  length: number;
  /** The moment shown at ms into the replay. */
  realAt(ms: number): number;
  /** How far into the replay a moment is shown. */
  replayAt(t: number): number;
};

export type Day = {
  date: string;
  start: number;
  /** Midnight at the day's end, or now for today. */
  end: number;
  dayEnd: number;
  today: boolean;
  nodes: Node[];
  steps: Step[];
  items: Item[];
  /** [[west, south], [east, north]], or null with nothing to show. */
  bounds: [[number, number], [number, number]] | null;
  /** The last report of the day, rough or not. */
  lastReport?: number;
  /** How many days of fixes are kept, and the day of the oldest one. */
  keepDays: number;
  since?: string;
  clock: Clock;
};

/** Rougher than this (meters), a fix is not drawn. */
const TRUSTED = 100;
/** A stay: fixes keep within this many meters of the first … */
const STAY_RADIUS = 120;
/** … for at least this long. */
const STAY_MIN = 10 * 60_000;
/** Longer than this between two fixes on the way, and the time was probably spent waiting. */
const SILENCE = 10 * 60_000;
/** Farther than this across a silence, the way itself is unknown. */
const GAP_METERS = 300;
/** Two stays this close are the same place. */
const SAME_PLACE = 180;

export function meters(a: LatLon, b: LatLon): number {
  const r = 6_371_000;
  const rad = Math.PI / 180;
  const dLat = (b.lat - a.lat) * rad;
  const dLon = (b.lon - a.lon) * rad;
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLon / 2) ** 2;
  return 2 * r * Math.asin(Math.min(1, Math.sqrt(h)));
}

type Point = LatLon & { t: number; act?: string; vel?: number };

const point = (f: Fix, t = Date.parse(f.at)): Point => ({ lat: f.lat, lon: f.lon, t, act: f.act, vel: f.vel });

/** Walking, riding or driving, by the phone's word or its speed. */
const moving = (p: Point) => (p.act !== undefined && p.act !== "stationary") || (p.vel ?? 0) >= 3;

const single = (p: Point): Node => ({ lat: p.lat, lon: p.lon, start: p.t, leave: p.t, stay: false, actIn: p.act, actOut: p.act, vel: p.vel });

/** Where the fixes keep still for a while, a stay; every other fix, a node of its own. */
function places(points: Point[]): Node[] {
  const nodes: Node[] = [];
  for (let i = 0; i < points.length; ) {
    let j = i + 1;
    while (j < points.length && meters(points[i], points[j]) <= STAY_RADIUS) j++;
    // The moving fixes at either end of a still stretch are the way in and
    // the way out - walking up to the door - not the stay.
    let a = i;
    let b = j - 1;
    while (a < b && moving(points[a])) a++;
    while (b > a && moving(points[b])) b--;
    if (b > a && points[b].t - points[a].t >= STAY_MIN) {
      for (let k = i; k < a; k++) nodes.push(single(points[k]));
      const group = points.slice(a, b + 1);
      nodes.push({
        lat: group.reduce((s, p) => s + p.lat, 0) / group.length,
        lon: group.reduce((s, p) => s + p.lon, 0) / group.length,
        start: points[a].t,
        leave: points[b].t,
        stay: true,
        actIn: points[a].act,
        actOut: points[b].act,
      });
      i = b + 1;
    } else {
      nodes.push(single(points[i]));
      i++;
    }
  }
  return nodes;
}

/**
 * A silence on the way: the phone sat still, then went. The wait is spent
 * where it was and the way takes as long as it plausibly could; if it went
 * far, the way itself went unreported. A long wait at a fix on the way is
 * then a stay. The last place is still where the operator is at the end of
 * the day - a stay goes on until a report says otherwise, and so does a fix
 * not followed by another for a while.
 */
function waits(nodes: Node[], end: number) {
  for (let k = 0; k + 1 < nodes.length; k++) {
    const a = nodes[k];
    const b = nodes[k + 1];
    const d = meters(a, b);
    const need = Math.max(30_000, (d / (d < 1000 ? 1.4 : 8.3)) * 1000);
    if (b.start - a.leave > SILENCE && b.start - a.leave > need * 2) {
      a.leave = b.start - need;
      a.unseen = d >= GAP_METERS;
    }
  }
  const last = nodes.at(-1);
  if (last && (last.stay || end - last.leave >= STAY_MIN)) last.leave = end;
  for (const n of nodes) if (!n.stay && n.leave - n.start >= STAY_MIN) n.stay = true;
}

/**
 * Two stays at the same place are one: back to back (a still phone says
 * little, an hour apart), or a short while apart with a few fixes between
 * them that never went far (a GPS jump, walking about the house) - not a way
 * out and back.
 */
function merged(nodes: Node[]): Node[] {
  const out = [...nodes];
  for (let k = 0; k < out.length; k++) {
    const a = out[k];
    if (!a.stay) continue;
    let n = k + 1;
    while (n < out.length && !out[n].stay && n - k <= 3) n++;
    const b = out[n];
    if (!b?.stay || meters(a, b) > SAME_PLACE) continue;
    const between = out.slice(k + 1, n);
    if (between.length > 0 && (b.start - a.leave > 15 * 60_000 || between.some((x) => meters(a, x) > GAP_METERS))) continue;
    a.leave = b.leave;
    a.actOut = b.actOut;
    a.unseen = b.unseen;
    out.splice(k + 1, n - k);
    k--;
  }
  return out;
}

/** The ways between the places, in order. */
function stepsOf(nodes: Node[]): Step[] {
  const steps: Step[] = [];
  for (let k = 0; k + 1 < nodes.length; k++) {
    const a = nodes[k];
    const b = nodes[k + 1];
    const d = meters(a, b);
    const kmh = (d / Math.max(1, (b.start - a.leave) / 1000)) * 3.6;
    steps.push({ a, b, t0: a.leave, t1: b.start, meters: d, kmh, mode: a.unseen ? "gap" : modeOf(a, b, kmh) });
  }
  return steps;
}

/**
 * How a way was gone: by what the phone said on leaving one place and on
 * reaching the next, unless the speed says otherwise; by the speed alone
 * without it.
 */
function modeOf(a: Node, b: Node, kmh: number): Mode {
  const said = [a.actOut, b.actIn];
  if (said.includes("automotive")) return "drive";
  if ((said.includes("walking") || said.includes("running")) && kmh < 15) return "walk";
  if (said.includes("cycling")) return "move";
  if (kmh >= 25) return "drive";
  // Under 12 km/h is on foot - a brisk walk or a run (it was 7, 2026-10-27).
  return kmh < 12 ? "walk" : "move";
}

/** The region a place lies in, the smallest that holds it. */
function regionOf(at: LatLon, regions: Region[]): Region | undefined {
  return regions
    .filter((r) => meters(at, r) <= Math.max(r.radiusM, 50) + 30)
    .sort((a, b) => a.radiusM - b.radiusM)[0];
}

export function buildDay(track: Track, now: number): Day {
  const start = Date.parse(track.from);
  const dayEnd = Date.parse(track.to);
  const today = now >= start && now < dayEnd;
  const end = today ? now : dayEnd;

  const points = track.fixes
    .filter((f) => f.acc <= TRUSTED)
    .map((f) => point(f))
    .filter((p) => p.t >= start && p.t <= end)
    .sort((a, b) => a.t - b.t);
  // The night before ends where this day begins.
  if (track.before) points.unshift(point(track.before, start));

  const found = places(points);
  waits(found, end);
  const nodes = merged(found);
  const steps = stepsOf(nodes);
  const last = nodes.at(-1);

  for (const n of nodes) {
    if (!n.stay) continue;
    const region = regionOf(n, track.regions) ?? regionOf(n, track.otherRegions ?? []);
    // Outside every circle kept here, the phone may still have said it was
    // in one of its regions then - for most of the stay, not at its edge.
    const visit = region
      ? undefined
      : track.visits.find((v) => {
          const shared = Math.min(n.leave, v.leftAt ? Date.parse(v.leftAt) : end) - Math.max(n.start, Date.parse(v.enteredAt));
          return shared >= Math.max(10 * 60_000, (n.leave - n.start) / 2);
        });
    n.name = region?.name ?? visit?.region;
    n.kind = region?.kind ?? visit?.kind;
  }

  const items: Item[] = [];
  let way: Step[] = [];
  let from: Node | undefined;
  nodes.forEach((n, k) => {
    if (n.stay) {
      if (way.length) items.push(moveOf(way, from, n));
      way = [];
      items.push({ type: "stay", start: n.start, end: n.leave, node: n, ongoing: today && n === last && n.leave >= end });
      from = n;
    }
    if (steps[k]) way.push(steps[k]);
  });
  if (way.length) items.push(moveOf(way, from, undefined));

  let bounds: Day["bounds"] = null;
  for (const n of nodes) {
    if (!bounds) bounds = [[n.lon, n.lat], [n.lon, n.lat]];
    bounds[0][0] = Math.min(bounds[0][0], n.lon);
    bounds[0][1] = Math.min(bounds[0][1], n.lat);
    bounds[1][0] = Math.max(bounds[1][0], n.lon);
    bounds[1][1] = Math.max(bounds[1][1], n.lat);
  }

  const reports = track.fixes.map((f) => Date.parse(f.at)).filter((t) => t >= start && t <= end);
  return {
    date: track.date,
    start,
    end,
    dayEnd,
    today,
    nodes,
    steps,
    items,
    bounds,
    lastReport: reports.length ? Math.max(...reports) : undefined,
    keepDays: track.keepDays,
    since: track.since,
    clock: clockFor(movesOf(items), start, end),
  };
}

function moveOf(steps: Step[], from: Node | undefined, to: Node | undefined): Move {
  const total = steps.reduce((s, x) => s + x.meters, 0);
  // The way is named by how most of it was gone; a gap only when it was all gap.
  const by = new Map<Mode, number>();
  for (const s of steps) if (s.mode !== "gap") by.set(s.mode, (by.get(s.mode) ?? 0) + s.meters);
  const mode = [...by.entries()].sort((a, b) => b[1] - a[1])[0]?.[0] ?? "gap";
  return { type: "move", start: steps[0].t0, end: steps.at(-1)!.t1, meters: total, mode, from, to, steps };
}

/** The stretches a day moved, [from, to]. */
const movesOf = (items: Item[]): [number, number][] =>
  items.filter((it) => it.type === "move").map((it) => [it.start, it.end]);

/**
 * The replay's clock. What moved takes most of the replay, in proportion to
 * how long it took; what stood still takes the rest by the square root of
 * its length - a short stop is seen, a night is not sat through. A day with
 * an hour on the road replays in about 13 seconds, a still one in 4. With two
 * people, a stretch either of them moved counts as moving.
 */
function clockFor(stretches: [number, number][], start: number, end: number): Clock {
  const runs = stretches
    .map(([a, b]): [number, number] => [Math.max(a, start), Math.min(b, end)])
    .filter(([a, b]) => b > a)
    .sort((x, y) => x[0] - y[0]);
  const merged: [number, number][] = [];
  for (const [a, b] of runs) {
    const last = merged.at(-1);
    if (last && a <= last[1]) last[1] = Math.max(last[1], b);
    else merged.push([a, b]);
  }
  const pieces: { t0: number; t1: number; moving: boolean }[] = [];
  let t = start;
  for (const [a, b] of merged) {
    if (a > t) pieces.push({ t0: t, t1: a, moving: false });
    pieces.push({ t0: a, t1: b, moving: true });
    t = b;
  }
  if (end > t) pieces.push({ t0: t, t1: end, moving: false });

  const moving = pieces.reduce((s, p) => s + (p.moving ? p.t1 - p.t0 : 0), 0);
  const still = pieces.reduce((s, p) => s + (p.moving ? 0 : Math.sqrt(p.t1 - p.t0)), 0);
  const length = moving === 0 ? 4000 : Math.min(28_000, Math.max(8000, 6000 + (moving / 60_000) * 110));
  const stillShare = moving === 0 ? 1 : still === 0 ? 0 : 0.2;

  const real = [start];
  const replay = [0];
  for (const p of pieces) {
    const share = p.moving
      ? (length * (1 - stillShare) * (p.t1 - p.t0)) / moving
      : (length * stillShare * Math.sqrt(p.t1 - p.t0)) / (still || 1);
    real.push(p.t1);
    replay.push(replay.at(-1)! + share);
  }
  const total = replay.at(-1)!;

  // Piecewise linear, both ways.
  const along = (xs: number[], ys: number[], x: number) => {
    if (x <= xs[0]) return ys[0];
    if (x >= xs.at(-1)!) return ys.at(-1)!;
    let lo = 0;
    let hi = xs.length - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (xs[mid] <= x) lo = mid;
      else hi = mid;
    }
    const span = xs[hi] - xs[lo];
    return span === 0 ? ys[hi] : ys[lo] + ((ys[hi] - ys[lo]) * (x - xs[lo])) / span;
  };
  return {
    length: total,
    realAt: (ms) => along(replay, real, ms),
    replayAt: (at) => along(real, replay, at),
  };
}

export type Head = LatLon & { node?: Node; step?: Step };

/** Where the operator was at t, or null before the day's first fix. */
export function headAt(day: Day, t: number): Head | null {
  const { nodes, steps } = day;
  if (nodes.length === 0 || t < nodes[0].start) return null;
  let lo = 0;
  let hi = nodes.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (nodes[mid].start <= t) lo = mid;
    else hi = mid - 1;
  }
  const n = nodes[lo];
  const s = steps[lo];
  if (t <= n.leave || !s) return { lat: n.lat, lon: n.lon, node: n };
  const f = s.t1 > s.t0 ? Math.min(1, (t - s.t0) / (s.t1 - s.t0)) : 1;
  return { lat: s.a.lat + (s.b.lat - s.a.lat) * f, lon: s.a.lon + (s.b.lon - s.a.lon) * f, step: s };
}

/** What was happening at t. */
export function itemAt(day: Day, t: number): Item | undefined {
  return day.items.find((it) => it.start <= t && t <= it.end);
}

/** A person on the screen: whose day it is, and whether it is the viewer's own. */
export type Person = { who: Who; me: boolean; day: Day };

/**
 * What the 위치 screen shows at once: one person's day, or both - the
 * viewer's first - on one map, one strip and one clock.
 */
export type Scene = {
  date: string;
  start: number;
  end: number;
  dayEnd: number;
  today: boolean;
  people: Person[];
  clock: Clock;
  bounds: Day["bounds"];
  keepDays: number;
  since?: string;
};

export function sceneOf(people: Person[]): Scene {
  const days = people.map((p) => p.day);
  const first = days[0];
  const end = Math.max(...days.map((d) => d.end));
  let bounds: Day["bounds"] = null;
  for (const d of days) {
    if (!d.bounds) continue;
    bounds = bounds
      ? [
          [Math.min(bounds[0][0], d.bounds[0][0]), Math.min(bounds[0][1], d.bounds[0][1])],
          [Math.max(bounds[1][0], d.bounds[1][0]), Math.max(bounds[1][1], d.bounds[1][1])],
        ]
      : d.bounds;
  }
  return {
    date: first.date,
    start: first.start,
    end,
    dayEnd: first.dayEnd,
    today: first.today,
    people,
    clock: days.length === 1 ? first.clock : clockFor(days.flatMap((d) => movesOf(d.items)), first.start, end),
    bounds,
    keepDays: first.keepDays,
    since: days.map((d) => d.since).filter(Boolean).sort()[0],
  };
}

// --- words ----------------------------------------------------------------

const ZONE = "Asia/Seoul";
const hmFormat = new Intl.DateTimeFormat("ko-KR", { timeZone: ZONE, hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

/** "14:32" on the operator's clock; the end of a day is 24:00, not the next day's 00:00. */
export function hm(t: number, dayEnd?: number): string {
  if (dayEnd !== undefined && t >= dayEnd) return "24:00";
  return hmFormat.format(t);
}

/** "37분", "9시간 42분". */
export function span(ms: number): string {
  const minutes = Math.max(0, Math.round(ms / 60_000));
  if (minutes < 60) return `${minutes}분`;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m ? `${h}시간 ${m}분` : `${h}시간`;
}

/** "850m", "14.2km". */
export function distance(m: number): string {
  if (m < 1000) return `${Math.round(m / 10) * 10}m`;
  return m < 100_000 ? `${(m / 1000).toFixed(1)}km` : `${Math.round(m / 1000)}km`;
}

/** "방금", "3분 전", "2시간 전". */
export function ago(ms: number): string {
  if (ms < 60_000) return "방금";
  return `${span(ms)} 전`;
}

/** 으로 after a closed syllable (집으로), 로 after an open one or ㄹ (회사로, 서울로). */
export function toward(word: string): string {
  const code = word.charCodeAt(word.length - 1) - 0xac00;
  if (code < 0 || code > 11171) return `${word}로`;
  const tail = code % 28;
  return tail === 0 || tail === 8 ? `${word}로` : `${word}으로`;
}

/** A word as the object of a sentence: 집을, 회사를. */
export function object(word: string): string {
  const code = word.charCodeAt(word.length - 1) - 0xac00;
  if (code < 0 || code > 11171) return `${word}을(를)`;
  return code % 28 === 0 ? `${word}를` : `${word}을`;
}

export const MODE_WORD: Record<Mode, string> = {
  drive: "차로 이동",
  walk: "걸어서 이동",
  move: "이동",
  gap: "기록 없이 이동",
};

/** "10월 22일 (목)". */
export function dateLabel(date: string): string {
  const [y, m, d] = date.split("-").map(Number);
  const weekday = new Date(Date.UTC(y, m - 1, d)).toLocaleDateString("ko-KR", { timeZone: "UTC", weekday: "short" });
  return `${m}월 ${d}일 (${weekday})`;
}

/** The operator's today, YYYY-MM-DD. */
export function todayOf(now: number): string {
  return new Date(now).toLocaleDateString("sv-SE", { timeZone: ZONE });
}

/** The day before or after a YYYY-MM-DD. */
export function shiftDay(date: string, by: number): string {
  const [y, m, d] = date.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d + by)).toISOString().slice(0, 10);
}

/** What a region made in the console comes back as: the region, and - when the phone's list is known - a link that puts it on the phone at once. */
export type Registered = { region: Region; link?: string };

/** Make where the viewer's phone is now one of its regions (50 m). */
export async function registerRegion(who: Who, name: string, at: LatLon): Promise<Registered> {
  const res = await fetch(`/api/context/owntracks/regions${who === "guest" ? "/guest" : ""}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ name, lat: at.lat, lon: at.lon }),
  });
  const body = (await res.json().catch(() => null)) as (Registered & { error?: string }) | null;
  if (!res.ok || !body) throw new Error(body?.error ?? `위치 서비스가 ${res.status}로 답했어요.`);
  return body;
}

export async function readTrack(date: string, who: Who): Promise<Track> {
  const res = await fetch(`/api/context/track?date=${encodeURIComponent(date)}&who=${who}`, { cache: "no-store" });
  const body = (await res.json().catch(() => null)) as (Track & { error?: string }) | null;
  if (!res.ok || !body) throw new Error(body?.error ?? `위치 서비스가 ${res.status}로 답했어요.`);
  return body;
}
