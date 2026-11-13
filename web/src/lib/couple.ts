/**
 * The couple's space in uniple, as the 일정 screens read it: who the two are,
 * since when, and the blocks of the home (calendar-svc couple.go speaks the
 * same shapes). Reads go to the adapter as it stands; what a screen changes
 * goes in at the way in, like the calendar's own changes.
 */
import { HttpError } from "@/lib/use-resource";
import { dayOf, shiftDay } from "@/lib/when";

export type Who = "me" | "partner";

export type Person = { id: string; name: string };

export type BlockType =
  | "dday"
  | "calendar"
  | "workout"
  | "bucket"
  | "map"
  | "free"
  | "log"
  | "worldmap"
  | "diary"
  | "budget"
  | "album"
  | "food";

export type DDay = { id: string; title: string; date: string; start?: boolean; repeat?: string; reminder?: number };
export type Bucket = { id: string; title: string; done: boolean; order: number; createdAt: string };
export type Free = { id: string; content: string; by: Who; order: number; createdAt: string };
export type Visit = {
  id: string;
  region: string;
  asset: string;
  rotation?: number;
  rep?: boolean;
  record?: string;
  note?: string;
  createdAt: string;
};
export type DateRecord = {
  id: string;
  region: string;
  title?: string;
  from?: string;
  to?: string;
  note?: string;
  order: number;
  createdAt: string;
};

export type Block = {
  id: string;
  type: BlockType;
  title: string;
  order: number;
  config?: Record<string, unknown>;
  dday?: DDay[];
  bucket?: Bucket[];
  free?: Free[];
  visits?: Visit[];
  records?: DateRecord[];
  /** When a block in 보관 was put there. */
  archived?: string;
};

export type Space = {
  me: Person;
  partner?: Person;
  /** The day they met, YYYY-MM-DD: that day is D+1. */
  anniversary?: string;
  weekStart: "sun" | "mon";
  blocks: Block[];
};

export type Check = { id: string; date: string; memo?: string; who: Who };

export type NewsItem = { title?: string; date?: string; time?: string; region?: string; note?: string };
export type News = {
  id: string;
  who: Who;
  block?: string;
  blockType?: BlockType;
  blockTitle?: string;
  kind: string;
  at: string;
  /** The thing the news is about, by its id (an entry, a photo, a wish). */
  itemId?: string;
  item?: NewsItem;
};

export type DayPhoto = { id: string; date: string; asset: string; event?: string; cover?: boolean };

/** What each block is, in a word: the home's add list and a block's page. */
export const BLOCK_NAME: Record<BlockType, string> = {
  dday: "디데이",
  calendar: "커플 캘린더",
  workout: "습관 체크",
  bucket: "버킷리스트",
  map: "데이트 지도",
  free: "자유 블록",
  log: "문화 기록장",
  worldmap: "세계 지도",
  diary: "커플 일기장",
  budget: "데이트 가계부",
  album: "공유 앨범",
  food: "맛집 기록장",
};

/** The blocks this console opens. The rest show on the home and open in uniple. */
export const OPENS: ReadonlySet<BlockType> = new Set(["dday", "calendar", "workout", "bucket", "map", "free"]);

/** The blocks a space may hold, how many of each, and what each is for. */
export const ADDABLE: { type: BlockType; cap: number; line: string }[] = [
  { type: "bucket", cap: 2, line: "같이 하고 싶은 것을 적고 하나씩 이뤄요" },
  { type: "workout", cap: 2, line: "둘이 매일 하는 것을 날마다 체크해요" },
  { type: "free", cap: 2, line: "무엇이든 짧게 적어 두는 목록" },
  { type: "dday", cap: 1, line: "만난 날부터 며칠, 기념일까지 며칠" },
  { type: "calendar", cap: 1, line: "둘의 일정을 한 달에" },
  { type: "map", cap: 1, line: "가 본 시/군/구를 사진으로 채우는 지도" },
  { type: "worldmap", cap: 1, line: "가 본 나라 - uniple 앱에서 열어요" },
  { type: "log", cap: 1, line: "영화·전시·공연 기록 - uniple 앱에서 열어요" },
  { type: "food", cap: 1, line: "맛집·카페 기록 - uniple 앱에서 열어요" },
  { type: "diary", cap: 1, line: "둘이 쓰는 일기 - uniple 앱에서 열어요" },
  { type: "budget", cap: 1, line: "데이트에 쓴 돈 - uniple 앱에서 열어요" },
  { type: "album", cap: 1, line: "둘이 올리는 사진 - uniple 앱에서 열어요" },
];

/* ───────────────────────────── reading and writing ───────────────────────────── */

const READ = "/api/couple";

async function failure(res: Response): Promise<string> {
  const body = (await res.json().catch(() => null)) as { error?: string } | null;
  return body?.error ?? `${res.status}`;
}

/** Reads one thing of the space; a refusal is not tried again (HttpError). */
export async function readCouple<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`${READ}${path}`, { cache: "no-store", signal });
  if (!res.ok) throw new HttpError(res.status, await failure(res));
  return (await res.json()) as T;
}

/** What a change came to: the saved thing, or why it did not go. */
export type Sent<T> = { ok: true; data: T } | { ok: false; error: string };

/** Changes something of the space at the way in, and answers as the adapter did. */
export async function writeCouple<T>(method: "POST" | "PATCH" | "DELETE", path: string, body?: unknown): Promise<Sent<T>> {
  try {
    const res = await fetch(`${READ}${path}`, {
      method,
      headers: body === undefined ? undefined : { "content-type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      keepalive: method === "DELETE",
    });
    if (res.status === 202) return { ok: false, error: "받았지만 아직 반영되지 않았어요. 잠시 뒤 다시 보세요." };
    if (!res.ok) return { ok: false, error: await failure(res) };
    return { ok: true, data: (await res.json().catch(() => ({}))) as T };
  } catch {
    return { ok: false, error: "연결하지 못했어요." };
  }
}

/**
 * A photo picked on the phone, made the size the app keeps (no side over
 * 2048px, JPEG) and uploaded for a block. Resolves to the asset to put
 * somewhere, or why it did not go. Nothing shows until something names it.
 */
export async function uploadPhoto(block: string, file: File): Promise<Sent<string>> {
  let blob: Blob;
  try {
    blob = await shrink(file);
  } catch {
    const heic = /hei[cf]/i.test(file.type) || /\.hei[cf]$/i.test(file.name);
    return {
      ok: false,
      error: heic
        ? "HEIC 사진은 이 브라우저에서 열 수 없어요. 사진 앱에서 JPEG로 내보내거나 휴대폰에서 올려 주세요."
        : "사진을 읽지 못했어요. 다른 사진을 골라 주세요.",
    };
  }
  if (blob.size > 4 * 1024 * 1024) return { ok: false, error: "사진이 너무 커요(4MB 까지)." };
  try {
    const res = await fetch(`${READ}/uploads?block=${encodeURIComponent(block)}`, {
      method: "POST",
      headers: { "content-type": blob.type },
      body: blob,
    });
    if (!res.ok) return { ok: false, error: await failure(res) };
    const body = (await res.json()) as { asset?: string };
    return body.asset ? { ok: true, data: body.asset } : { ok: false, error: "사진을 올리지 못했어요." };
  } catch {
    return { ok: false, error: "사진을 올리지 못했어요. 연결을 확인해 주세요." };
  }
}

/** A photo no side over 2048px, as a JPEG - what the app itself uploads is no bigger. */
async function shrink(file: File): Promise<Blob> {
  const bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  try {
    const scale = Math.min(1, 2048 / Math.max(bitmap.width, bitmap.height));
    const w = Math.max(1, Math.round(bitmap.width * scale));
    const h = Math.max(1, Math.round(bitmap.height * scale));
    const canvas = document.createElement("canvas");
    canvas.width = w;
    canvas.height = h;
    const g = canvas.getContext("2d");
    if (!g) throw new Error("canvas");
    g.imageSmoothingQuality = "high";
    g.drawImage(bitmap, 0, 0, w, h);
    const out = await new Promise<Blob | null>((ok) => canvas.toBlob(ok, "image/jpeg", 0.86));
    canvas.width = canvas.height = 0;
    if (!out) throw new Error("encode");
    return out;
  } finally {
    bitmap.close();
  }
}

/** A photo of the space, through the console: thumb for tiles, display to look at. */
export const photoSrc = (asset: string, size: "thumb" | "display" = "thumb") =>
  `${READ}/media/${encodeURIComponent(asset)}?size=${size}`;

/* ───────────────────────────── days ───────────────────────────── */

/** Today on Seoul's clock. */
export const today = () => dayOf(new Date());

function utc(day: string): number {
  const [y, m, d] = day.split("-").map(Number);
  return Date.UTC(y, m - 1, d);
}

/** Days from a to b (b - a). */
export function between(a: string, b: string): number {
  return Math.round((utc(b) - utc(a)) / 86_400_000);
}

/** The day count since they met: the first day is D+1. */
export function together(start: string, on = today()): number {
  return between(start, on) + 1;
}

/** "D-9", "D-DAY", "D+3": a day as counted from today. */
export function dMark(day: string, on = today()): string {
  const n = between(on, day);
  if (n === 0) return "D-DAY";
  return n > 0 ? `D-${n}` : `D+${-n}`;
}

/** The same month and day n years on; a 29 February lands on the 28th. */
function yearsOn(day: string, n: number): string {
  const [y, m, d] = day.split("-").map(Number);
  const last = new Date(Date.UTC(y + n, m, 0)).getUTCDate();
  return `${y + n}-${String(m).padStart(2, "0")}-${String(Math.min(d, last)).padStart(2, "0")}`;
}

/** An anniversary's next day on or after `on`: a yearly one comes round again. */
export function nextOn(day: string, repeat: string | undefined, on = today()): string {
  if (repeat !== "yearly" || day >= on) return day;
  const years = Number(on.slice(0, 4)) - Number(day.slice(0, 4));
  for (let n = Math.max(0, years - 1); ; n++) {
    const next = yearsOn(day, n);
    if (next >= on) return next;
  }
}

export type Milestone = { key: string; label: string; date: string };

/**
 * The days worth counting since they met, from `on` onward: every hundredth
 * day and every year, in the order they come.
 */
export function milestones(start: string, count: number, on = today()): Milestone[] {
  const out: Milestone[] = [];
  const passed = Math.max(0, together(start, on));
  let hundred = Math.max(1, Math.ceil(passed / 100));
  let year = 1;
  while (yearsOn(start, year) < on) year++;
  while (out.length < count) {
    const byDays = shiftDay(start, hundred * 100 - 1);
    const byYears = yearsOn(start, year);
    if (byDays <= byYears) {
      if (byDays >= on) out.push({ key: `d${hundred}`, label: `${hundred * 100}일`, date: byDays });
      hundred++;
      if (byDays === byYears) year++;
    } else {
      out.push({ key: `y${year}`, label: `${year}주년`, date: byYears });
      year++;
    }
  }
  return out;
}

/** What today is, when it is a day worth saying: "100일이에요", "1주년이에요". */
export function todayIs(start: string, on = today()): string | null {
  const n = together(start, on);
  if (n > 0 && n % 100 === 0) return `${n}일이에요`;
  const years = Number(on.slice(0, 4)) - Number(start.slice(0, 4));
  if (years > 0 && yearsOn(start, years) === on) return `${years}주년이에요`;
  return null;
}

/** The seven days of the week `day` is in, from the space's first weekday. */
export function weekOf(day: string, weekStart: Space["weekStart"]): string[] {
  const weekday = new Date(utc(day)).getUTCDay();
  const back = weekStart === "mon" ? (weekday + 6) % 7 : weekday;
  const first = shiftDay(day, -back);
  return Array.from({ length: 7 }, (_, i) => shiftDay(first, i));
}

/** The weekday names from the space's first weekday. */
export function weekdays(weekStart: Space["weekStart"]): string[] {
  const names = ["일", "월", "화", "수", "목", "금", "토"];
  return weekStart === "mon" ? [...names.slice(1), names[0]] : names;
}

/** A habit's days by who checked them: mine, theirs, and the ones we both did. */
export function checkedDays(checks: Check[]) {
  const me = new Set<string>();
  const partner = new Set<string>();
  for (const c of checks) (c.who === "me" ? me : partner).add(c.date);
  const both = new Set([...me].filter((d) => partner.has(d)));
  return { me, partner, both };
}

/** Reminder choices, in minutes before (-1: none). */
export const REMINDERS: { value: number; label: string }[] = [
  { value: -1, label: "없음" },
  { value: 0, label: "시작 시간" },
  { value: 10, label: "10분 전" },
  { value: 30, label: "30분 전" },
  { value: 60, label: "1시간 전" },
  { value: 120, label: "2시간 전" },
  { value: 1440, label: "1일 전" },
  { value: 2880, label: "2일 전" },
  { value: 10080, label: "1주 전" },
];

/** A reminder as words: "1시간 전". */
export function reminderLabel(minutes: number | undefined): string {
  if (minutes === undefined || minutes < 0) return "없음";
  return REMINDERS.find((r) => r.value === minutes)?.label ?? `${minutes}분 전`;
}

export const REPEATS: { value: string; label: string }[] = [
  { value: "none", label: "안 함" },
  { value: "daily", label: "매일" },
  { value: "weekly", label: "매주" },
  { value: "biweekly", label: "2주마다" },
  { value: "monthly", label: "매월" },
  { value: "yearly", label: "매년" },
];
