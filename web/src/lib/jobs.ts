// Shapes of the jobs' JSON (mind). Mind is the source of truth.
import { dateTime } from "@/lib/when";

export type JobState = "active" | "waiting" | "paused" | "done" | "cancelled" | "failed";

export type Job = {
  id: number;
  title: string;
  goal: string;
  instructions: string;
  sites: string[];
  photos: string[];
  state: JobState;
  nextAt?: string;
  nextReason?: string;
  waitingCard?: string;
  summary?: string;
  attempts: number;
  lastRunAt?: string;
  finishedAt?: string;
  photosGone?: boolean;
  createdAt: string;
  /** A background run of this job is in flight right now. */
  running: boolean;
  /** The last thing that run did. */
  currentStep?: string;
  blockedSites?: string[];
  records: number;
};

export type JobEvent = {
  id: number;
  runId?: number;
  /** created, run_start, step, note, progress, report, schedule, card, answer, instruction, control, finish, error, run_end */
  kind: string;
  text: string;
  at: string;
};

export type JobRecord = {
  key: string;
  title: string;
  status: string;
  amount?: number;
  metrics: Record<string, number>;
  image?: string;
  url?: string;
  note?: string;
  updatedAt: string;
};

export type JobDetail = {
  job: Job;
  events: JobEvent[];
  records: JobRecord[];
  memory: Record<string, string>;
  inbox: { id: number; kind: string; text: string; at: string }[];
};

export type BlockedSite = { site: string; loginRequired: boolean; note?: string; updatedAt: string };

export const LIVE: JobState[] = ["active", "waiting", "paused"];

/**
 * What a job is doing, in the words the tab shows. Paused, it rests as a goal
 * does (잠깐 쉬기 · 쉬는 중 · 다시 시작): 멈추기 beside 그만두기 read as two
 * ways to stop for good, and 멈추기 in the conversation stops an answer. A
 * failure is said as one, not as a kind of pause.
 */
export function stateLabel(j: Job): string {
  if (j.running) return "진행 중";
  if (j.blockedSites?.length) return "로그인 대기";
  switch (j.state) {
    case "active":
      return "예약됨";
    case "waiting":
      return "승인 대기";
    case "paused":
      return "쉬는 중";
    case "done":
      return "끝남";
    case "cancelled":
      return "그만둠";
    case "failed":
      return "실패";
  }
}

/** Live and held until its person answers a card or logs in again: the one thing the tab asks of anyone. */
export function waitsOnPerson(j: Job): boolean {
  return !j.running && LIVE.includes(j.state) && (j.state === "waiting" || Boolean(j.blockedSites?.length));
}

export function stateTone(j: Job): string {
  if (j.running) return "text-approve";
  // Waiting on the operator is not the calendar's violet: the row carries the
  // breathing dot a waiting card has (waitsOnPerson).
  if (j.blockedSites?.length || j.state === "waiting") return "text-ink";
  if (j.state === "failed") return "text-reject";
  if (j.state === "active") return "text-dim";
  return "text-faint";
}

/** "오늘 15:30", "내일 09:00", "10월 12일 09:00" (lib/when.ts). */
export function when(iso: string, now = new Date()): string {
  return dateTime(iso, now);
}

export async function send(path: string, body?: unknown): Promise<string | null> {
  const res = await fetch(`/api/jobs/${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body ?? {}),
  }).catch(() => null);
  if (res?.ok) return null;
  const err = (await res?.json().catch(() => null)) as { error?: string } | null;
  return err?.error ?? "요청하지 못했어요.";
}
