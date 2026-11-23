// 상대에게 알릴 한 일을 모아 두는 장부와, 언제 보낼지. 장부는 둥지의 Durable Object(worker/live.ts)가 쥔다.
//
// 한 사람이 둥지에 머무는 동안 한 일은 한 통으로 간다. 보내는 때는 셋 중 먼저 오는 것:
// 그 사람이 둥지를 떠나고 GRACE 가 지났을 때(잠깐 다른 탭에 다녀오는 사이에는 보내지 않는다),
// 마지막으로 한 일에서 IDLE 이 지났을 때(창을 켜 둔 채 자리를 떴을 때), 처음 한 일에서 CAP 이 지났을 때.
// 보낼 길이 잠깐 막혔으면 RETRY 간격으로 다시 보낸다.

import type { Act } from "../shared/news";

/** jarvis 게이트웨이(edge/src/index.ts 의 Nest 엔트리포인트)가 받는 알림 하나 */
export interface NestNotice {
  /** 받는 사람: jarvis 의 운영자(owner)나 손님(guest) */
  to: "owner" | "guest";
  title: string;
  body: string;
  /** 같은 키는 한 번만 알린다: 다시 보내도 두 통이 되지 않는다. */
  key: string;
}

/** 서비스 바인딩 JARVIS 로 부르는 게이트웨이의 문 */
export interface JarvisDoor {
  tell(n: NestNotice): Promise<{ ok: boolean; status: number }>;
}

/** 한 사람이 머무는 동안 한 일 */
export interface Pending {
  acts: Act[];
  /** 처음, 마지막으로 한 때 */
  first: number;
  last: number;
  /** 둥지를 떠난 때(떠나 있는 동안만) */
  left?: number;
  /** 보내지 못해 다시 보낼 때와 지금까지 실패한 수 */
  retry?: number;
  tries?: number;
}

/** 사람(둥지 사용자 id)마다 */
export type Book = Record<string, Pending>;

export const GRACE = 30_000;
export const IDLE = 5 * 60_000;
export const CAP = 30 * 60_000;
export const RETRY = [60_000, 5 * 60_000, 15 * 60_000];
/** 한 통에 모으는 한 일의 수: 넘으면 오래된 것부터 뺀다(본문에는 어차피 몇 줄만 적힌다). */
const MAX_ACTS = 200;

/** 보낼 때 */
export function due(p: Pending, grace = GRACE): number {
  if (p.retry !== undefined) return p.retry;
  return Math.min(p.first + CAP, p.last + IDLE, p.left !== undefined ? p.left + grace : Infinity);
}

/** 한 일을 장부에 더한다. here: 그 사람이 지금 둥지를 보고 있나(아니면 떠난 것으로 센다). */
export function add(p: Pending | undefined, acts: Act[], now: number, here: boolean): Pending {
  const next: Pending = p ? { ...p, acts: [...p.acts] } : { acts: [], first: now, last: now };
  for (const act of acts) {
    // 방을 꾸민 것은 몇 번 옮겼든 한 줄이다.
    if (act.k === "decorate" && next.acts.some((a) => a.k === "decorate")) continue;
    next.acts.push(act);
  }
  if (next.acts.length > MAX_ACTS) next.acts = next.acts.slice(-MAX_ACTS);
  next.last = now;
  if (here) delete next.left;
  else next.left = now;
  return next;
}

/** 보내지 못한 한 통을 다시 장부에: 그사이 더 한 일이 있으면 합친다. 다 실패했으면 undefined(버린다). */
export function again(sent: Pending, since: Pending | undefined, now: number): Pending | undefined {
  const tries = (sent.tries ?? 0) + 1;
  if (tries > RETRY.length) return since;
  const merged = since ? { ...sent, acts: [...sent.acts, ...since.acts].slice(-MAX_ACTS), last: Math.max(sent.last, since.last) } : sent;
  return { ...merged, tries, retry: now + RETRY[tries - 1] };
}

/** 장부에서 지금 보낼 것을 떼어 낸다. */
export function take(book: Book, now: number, grace = GRACE): { ready: [string, Pending][]; rest: Book } {
  const ready: [string, Pending][] = [];
  const rest: Book = {};
  for (const [id, p] of Object.entries(book)) {
    if (due(p, grace) <= now) ready.push([id, p]);
    else rest[id] = p;
  }
  return { ready, rest };
}

/** 다음에 깨어날 때. 장부가 비었으면 null. */
export function nextWake(book: Book, grace = GRACE): number | null {
  let at = Infinity;
  for (const p of Object.values(book)) at = Math.min(at, due(p, grace));
  return Number.isFinite(at) ? at : null;
}
