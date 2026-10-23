/**
 * The 출발 screen's data: each person's routes - the bus or the train they
 * usually take, from which stop, after what walk - and for each, now, when to
 * leave for it (context-svc's /transit; the real-time feeds are its business).
 * Each person reads and keeps only their own: the operator's paths, or the
 * guest's (…/guest), which is all the guest's token opens.
 */
import type { Who } from "@/lib/track";

export type StopKind = "bus" | "subway";

export type Stop = {
  kind: StopKind;
  /** A bus stop's feed: 경기 or 서울. */
  region?: "gyeonggi" | "seoul";
  /** A bus stop's id in its feed; a station's name as the subway feed has it (강남). */
  id: string;
  name: string;
  /** The number a bus stop shows. */
  no?: string;
  lat?: number;
  lon?: number;
  /** Meters from where it was looked for. */
  dist?: number;
};

export type Line = { id: string; name: string; toward?: string };

export type Arrival = { line: Line; at: string; vehicle?: string; note?: string; rough?: boolean };

export type Route = {
  id: string;
  name: string;
  stop: Stop;
  line: Line;
  /** Minutes to walk to the stop, and to be there early. */
  walk: number;
  spare: number;
  /** 1 Monday … 7 Sunday; none, every day. */
  days?: number[];
  /** "HH:MM": the hours it is gone in, for the nudges. */
  from?: string;
  to?: string;
  push: boolean;
};

export type Stage = "relax" | "pack" | "soon" | "go" | "run" | "none" | "off";

export type Plan = {
  route: string;
  now: string;
  catch?: Arrival;
  leave?: string;
  next: Arrival[];
  stage: Stage;
  say: string;
  error?: string;
};

export type Departures = {
  now: string;
  routes: Route[];
  plans: Plan[];
  /** Which feeds have their keys. */
  feeds: { buses: boolean; subway: boolean };
};

const base = (who: Who) => `/api/context/transit${who === "guest" ? "/guest" : ""}`;

async function read<T>(res: Response): Promise<T> {
  const body = (await res.json().catch(() => null)) as (T & { error?: string }) | null;
  if (!res.ok || !body) throw new Error(body?.error ?? `출발 정보가 ${res.status}로 답했어요.`);
  return body;
}

export async function readDepartures(who: Who): Promise<Departures> {
  return read(await fetch(base(who), { cache: "no-store" }));
}

export async function saveRoutes(who: Who, routes: Route[]): Promise<Route[]> {
  const res = await fetch(`${base(who)}/routes`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ routes }),
  });
  return (await read<{ routes: Route[] }>(res)).routes;
}

/** Stops near where one's phone last was (near "me"), near a point, or by name. */
export async function findStops(
  who: Who,
  by: { near: "me" | { lat: number; lon: number } } | { q: string },
): Promise<{ stops: Stop[]; from?: { lat: number; lon: number } }> {
  const q =
    "q" in by
      ? `q=${encodeURIComponent(by.q)}`
      : `near=${by.near === "me" ? "me" : `${by.near.lat.toFixed(6)},${by.near.lon.toFixed(6)}`}`;
  return read(await fetch(`${base(who)}/stops?${q}`, { cache: "no-store" }));
}

export async function findLines(stop: Stop): Promise<Line[]> {
  const q = new URLSearchParams({ kind: stop.kind, id: stop.id, ...(stop.region ? { region: stop.region } : {}) });
  return (await read<{ lines: Line[] }>(await fetch(`/api/context/transit/lines?${q}`, { cache: "no-store" }))).lines;
}

/** Minutes a walk of meters takes (the server's own estimate: streets a quarter longer, 70 m a minute). */
export function walkMinutes(meters: number): number {
  return Math.max(1, Math.ceil((meters * 1.25) / 70));
}

/** "18:09", in Seoul. */
export function clockOf(iso: string): string {
  return new Date(iso).toLocaleTimeString("ko-KR", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "Asia/Seoul" });
}

/** A countdown: "13:11" (minutes:seconds), "1:02:05" past an hour. */
export function countdown(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${sec}` : `${m}:${sec}`;
}

export const DAY_NAMES = ["월", "화", "수", "목", "금", "토", "일"];

/** The words for a stage, said again on the client as time passes between reads. */
export function stageOf(leaveMs: number, nowMs: number): { stage: Stage; say: string } {
  const left = leaveMs - nowMs;
  if (left < -2 * 60_000) return { stage: "none", say: "이번 차는 놓쳤어요" };
  if (left < 0) return { stage: "run", say: "뛰면 탈 수 있어요" };
  if (left <= 3 * 60_000) return { stage: "go", say: "지금 나가요!" };
  if (left <= 10 * 60_000) return { stage: "soon", say: "곧 나가야 해요" };
  if (left <= 20 * 60_000) return { stage: "pack", say: "슬슬 짐 싸야 해요" };
  return { stage: "relax", say: "여유 있어요" };
}
