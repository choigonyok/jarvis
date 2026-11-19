// 시각에 따라 바뀌는 방의 하늘.

export type Phase = "dawn" | "day" | "dusk" | "night";

export function phaseOf(d: Date): Phase {
  const h = d.getHours();
  if (h >= 5 && h < 7) return "dawn";
  if (h >= 7 && h < 17) return "day";
  if (h >= 17 && h < 19) return "dusk";
  return "night";
}

export const PHASE_NAME: Record<Phase, string> = { dawn: "새벽", day: "낮", dusk: "저녁", night: "밤" };

/** 한국 시각으로 오늘(YYYY-MM-DD) */
export const todayKey = (ms = Date.now()) => new Date(ms + 9 * 3_600_000).toISOString().slice(0, 10);

export function daysSince(day: string, now = Date.now()) {
  const start = Date.parse(`${day}T00:00:00+09:00`);
  return Math.floor((now - start) / 86_400_000) + 1;
}

export function ago(ms: number, now = Date.now()) {
  const m = Math.floor((now - ms) / 60_000);
  if (m < 1) return "방금";
  if (m < 60) return `${m}분 전`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}시간 전`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}일 전`;
  return new Date(ms).toLocaleDateString("ko-KR", { month: "long", day: "numeric" });
}

export function stamp(ms: number) {
  return new Date(ms).toLocaleString("ko-KR", { month: "numeric", day: "numeric", hour: "numeric", minute: "2-digit" });
}
