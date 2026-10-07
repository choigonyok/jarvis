// Shapes of jobs-svc's JSON. The service is the source of truth.

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

/** What a job is doing, in the words the tab shows. */
export function stateLabel(j: Job): string {
  if (j.running) return "진행 중";
  if (j.blockedSites?.length) return "로그인 대기";
  switch (j.state) {
    case "active":
      return "예약됨";
    case "waiting":
      return "승인 대기";
    case "paused":
      return "멈춤";
    case "done":
      return "끝남";
    case "cancelled":
      return "그만둠";
    case "failed":
      return "멈춤(실패)";
  }
}

export function stateTone(j: Job): string {
  if (j.running) return "text-approve";
  if (j.blockedSites?.length || j.state === "waiting") return "text-mine";
  if (j.state === "failed") return "text-reject";
  if (j.state === "active") return "text-dim";
  return "text-faint";
}

/** "오늘 15:30", "내일 09:00", "10/12 09:00". */
export function when(iso: string, now = new Date()): string {
  const d = new Date(iso);
  const day = (x: Date) => x.toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" });
  const time = d.toLocaleTimeString("ko-KR", { timeZone: "Asia/Seoul", hour: "2-digit", minute: "2-digit", hour12: false });
  const tomorrow = new Date(now.getTime() + 86_400_000);
  if (day(d) === day(now)) return `오늘 ${time}`;
  if (day(d) === day(tomorrow)) return `내일 ${time}`;
  const [, m, dd] = day(d).split("-");
  return `${Number(m)}/${Number(dd)} ${time}`;
}

export async function send(path: string, body?: unknown): Promise<string | null> {
  const res = await fetch(`/api/jobs/${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body ?? {}),
  }).catch(() => null);
  if (res?.ok) return null;
  const err = (await res?.json().catch(() => null)) as { error?: string } | null;
  return err?.error ?? "요청하지 못했습니다.";
}
