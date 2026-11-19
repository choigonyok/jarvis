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

const SEOUL = "Asia/Seoul";
const hm = (ms: number) => new Date(ms).toLocaleTimeString("ko-KR", { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: SEOUL });
const monthDay = (ms: number) => new Date(ms).toLocaleDateString("ko-KR", { month: "long", day: "numeric", timeZone: SEOUL });

/** 얼마 전인지(콘솔의 ago 와 같다): 방금, 3분 전, 2시간 전, 12시간이 지나면 그때(어제 21:04, 11월 7일). */
export function ago(ms: number, now = Date.now()) {
  const m = Math.floor((now - ms) / 60_000);
  if (m < 1) return "방금";
  if (m < 60) return `${m}분 전`;
  if (m < 12 * 60) return `${Math.floor(m / 60)}시간 전`;
  return stamp(ms, now);
}

/** 그때(콘솔의 날짜 모양): 오늘 14:02, 어제 21:04, 11월 7일 09:30 */
export function stamp(ms: number, now = Date.now()) {
  const day = todayKey(ms);
  if (day === todayKey(now)) return `오늘 ${hm(ms)}`;
  if (day === todayKey(now - 86_400_000)) return `어제 ${hm(ms)}`;
  return `${monthDay(ms)} ${hm(ms)}`;
}
