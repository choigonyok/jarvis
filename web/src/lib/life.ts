/**
 * What mind keeps about the operator (mind-svc /api, through /api/mind):
 * the notes it believes about them, and the ledgers it reads from their days
 * - the places reviewed, the things bought, the meals paid for. Read-only,
 * apart from the operator's word on a note.
 */

export type NoteStatus = "hypothesis" | "confirmed" | "rejected";

export type Note = {
  id: string;
  text: string;
  /** What it is about: 캠핑, 운동, 이동, 사람:지수 … */
  scope?: string;
  status: NoteStatus;
  /** 0-1. */
  confidence: number;
  /** What it rests on, a line each: counts, days, the purchases it saw. */
  evidence?: string[] | null;
  version: number;
  /** Why this version: the night that found it, a card approved, the operator. */
  reason?: string;
  createdAt: string;
  updatedAt: string;
};

export type Place = {
  /** 캠핑장 | 숙소 | 식당 | 카페 */
  type: string;
  name: string;
  /** The reviews' average, 1-5. */
  score: number;
  reviews: number;
  /** Stays in its region and payments there. */
  visits: number;
  spent: number;
  good?: string[] | null;
  bad?: string[] | null;
  /** The last review, YYYY-MM-DD. */
  last: string;
};

export type Thing = {
  class: string;
  items: string[];
  /** Days bought, oldest first. */
  bought: string[];
  /** How often it comes round, for a consumable bought three times or more. */
  cycleDays?: number;
  /** When it usually runs out next, YYYY-MM-DD. */
  next?: string;
  last: string;
  sizes?: string[] | null;
};

export type Slot = "아침" | "점심" | "저녁" | "밤";

export type Meal = {
  at: string;
  merchant: string;
  amount: number;
  delivery: boolean;
  /** Paid while at home (a stay in the home region). */
  home: boolean;
  slot: Slot;
};

export const SLOTS: Slot[] = ["아침", "점심", "저녁", "밤"];

/** How a meal was had: delivered, at home otherwise, or out. */
export type MealWay = "delivery" | "home" | "out";

export const WAYS: { way: MealWay; label: string }[] = [
  { way: "delivery", label: "배달" },
  { way: "home", label: "집에서" },
  { way: "out", label: "밖에서" },
];

export function wayOf(m: Meal): MealWay {
  return m.delivery ? "delivery" : m.home ? "home" : "out";
}

export type Loaded<T> = { data: T; error?: undefined } | { data?: undefined; error: string };

/** One of the ledgers, by its path under /api/mind and the key its list comes under; the error is what the way there said. */
export async function read<T>(path: string, key: string): Promise<Loaded<T>> {
  try {
    const res = await fetch(`/api/mind/${path}`, { cache: "no-store" });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) return { error: body.error ?? `응답이 ${res.status} 이었어요.` };
    return { data: (body[key] ?? []) as T };
  } catch {
    return { error: "콘솔 서버에 닿지 못했어요." };
  }
}

/** The operator's word on a note: so, not so (with why), or reworded. The note as it now stands, or why not. */
export async function decide(
  id: string,
  change: { status?: NoteStatus; text?: string; reason?: string },
): Promise<{ note: Note; error?: undefined } | { note?: undefined; error: string }> {
  try {
    const res = await fetch(`/api/mind/notes/${encodeURIComponent(id)}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ status: change.status ?? "", text: change.text ?? "", reason: change.reason ?? "" }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) return { error: body.error ?? "저장하지 못했어요. 잠시 뒤 다시 하세요." };
    return { note: body as Note };
  } catch {
    return { error: "저장하지 못했어요. 잠시 뒤 다시 하세요." };
  }
}

export async function versions(id: string): Promise<Note[] | null> {
  const got = await read<Note[]>(`notes/${encodeURIComponent(id)}/versions`, "versions");
  return got.data ?? null;
}

/** "운동", or for a note about someone, their name. */
export function scopeLabel(scope: string | undefined): string {
  if (!scope) return "";
  return scope.startsWith("사람:") ? scope.slice(3) : scope;
}

export const pct = (x: number) => `${Math.round(x * 100)}%`;

const ZONE = "Asia/Seoul";
const today = () => new Date().toLocaleDateString("sv-SE", { timeZone: ZONE });

/** Whole days from today to a YYYY-MM-DD, on the operator's calendar. */
export function daysUntil(date: string, from = today()): number {
  const a = Date.UTC(+from.slice(0, 4), +from.slice(5, 7) - 1, +from.slice(8, 10));
  const b = Date.UTC(+date.slice(0, 4), +date.slice(5, 7) - 1, +date.slice(8, 10));
  return Math.round((b - a) / 86_400_000);
}

/** "10/31", the year only when it is not this one. */
export function shortDate(date: string): string {
  const [y, m, d] = date.slice(0, 10).split("-").map(Number);
  const md = `${m}/${d}`;
  return String(y) === today().slice(0, 4) ? md : `${y}. ${md}`;
}

/** When a consumable is due next, as a person says it. */
export function dueLabel(next: string): string {
  const n = daysUntil(next);
  if (n < 0) return `${-n}일 지남`;
  if (n === 0) return "오늘";
  if (n === 1) return "내일";
  if (n === 2) return "모레";
  return `${n}일 뒤`;
}

/** A moment as the day and the clock: "10/18 19:42". */
export function moment(iso: string): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return "";
  const day = t.toLocaleDateString("sv-SE", { timeZone: ZONE });
  const hm = t.toLocaleTimeString("ko-KR", { timeZone: ZONE, hour: "2-digit", minute: "2-digit", hour12: false });
  return `${shortDate(day)} ${hm}`;
}

/** Notes in the order they want attention: the ones asking first, then the ones held, then the ones let go; newest first within. */
export function byAttention(notes: Note[]): Note[] {
  const rank: Record<NoteStatus, number> = { hypothesis: 0, confirmed: 1, rejected: 2 };
  return [...notes].sort((a, b) => rank[a.status] - rank[b.status] || b.updatedAt.localeCompare(a.updatedAt));
}

/** Things in the order they are wanted: the next to run out first, then the rest by the last bought. */
export function byNeed(things: Thing[]): Thing[] {
  return [...things].sort((a, b) => {
    if (a.next && b.next) return a.next.localeCompare(b.next);
    if (a.next) return -1;
    if (b.next) return 1;
    return b.last.localeCompare(a.last);
  });
}
