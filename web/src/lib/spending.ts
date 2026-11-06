/** The wire shape of the book (mind GET /api/spending/spending), and what the screen derives from it. */
import { ago as whenAgo, dayLabel as whenDayLabel } from "@/lib/when";

export type TxStatus = "ok" | "cancelled" | "cancel" | "refund" | "excluded";

export type Tx = {
  id: number;
  /** kakao: a card alert. manual: written by hand. asset: taken out of an investment account and said to be spent (자산 카드). */
  source: "kakao" | "manual" | "asset";
  issuer: string;
  cardTail: string;
  kind: "approval" | "cancel";
  amountKrw: number;
  currency: string;
  foreignAmount: number | null;
  estimated: boolean;
  installment: number;
  merchant: string;
  category: string;
  categorySource: "rule" | "learned" | "manual";
  approvedAt: string;
  memo: string;
  excluded: boolean;
  status: TxStatus;
  /** What this row adds to the month: 0 for a cancelled pair, negative for a refund. */
  signedKrw: number;
  /** What a marketplace charge bought, read from its order page in the background. */
  items: Item[] | null;
  enrichSource: "coupang" | "naverpay" | null;
  enrichStatus: "pending" | "done" | "not_found" | "login_required" | null;
  enrichNote: string;
};

export type Item = {
  id: number;
  name: string;
  quantity: number;
  listedKrw: number;
  /** This item's share of the card charge. */
  amountKrw: number;
  category: string;
  categorySource: "agent" | "manual";
};

export const SITE_LABEL: Record<string, string> = { coupang: "쿠팡", naverpay: "네이버페이" };

/** An opened-up order counts by its items, as the book's category totals do. */
const byItems = (t: Tx) => t.status === "ok" && (t.items?.length ?? 0) > 0;

export const inCategory = (t: Tx, category: string) =>
  byItems(t) ? t.items!.some((it) => it.category === category) : t.category === category;

/** What this row adds to one category: only its items in it, for an opened-up order. */
export const categoryKrw = (t: Tx, category: string) =>
  byItems(t)
    ? t.items!.reduce((sum, it) => sum + (it.category === category ? it.amountKrw : 0), 0)
    : t.signedKrw;

export type CategoryTotal = {
  name: string;
  totalKrw: number;
  count: number;
  budgetKrw: number | null;
  prevKrw: number;
};

export type Day = { date: string; totalKrw: number; count: number };

export type Recurring = {
  merchant: string;
  category: string;
  amountKrw: number;
  day: number;
  lastAt: string;
  nextAt: string;
  months: number;
};

export type Unparsed = {
  messageId: string;
  chatName: string;
  body: string;
  sentAt: string;
  /** Why an alert that could not even be recorded was set aside; absent
   *  for one whose format was not understood. */
  reason?: string;
};

export type Collector = {
  latestMessageAt: string | null;
  lastAlertAt: string | null;
  stale: boolean;
};

export type Book = {
  month: string;
  today: string;
  daysInMonth: number;
  elapsedDays: number;
  totalKrw: number;
  count: number;
  prev: { month: string; totalKrw: number; sameDayKrw: number };
  budgetKrw: number | null;
  categories: CategoryTotal[];
  daily: Day[];
  topMerchants: { name: string; totalKrw: number; count: number }[];
  transactions: Tx[];
  categoryNames: string[];
  recurring: Recurring[];
  unparsed: Unparsed[];
  collector: Collector;
  budgets: Record<string, number>;
  /** Sites whose order lookups wait on a fresh login in the agent's browser. */
  enrichBlocked: { source: string; label: string }[];
};

/** Seoul's wall clock, whatever timezone the phone is in. */
const seoul = (iso: string) =>
  new Date(new Date(iso).toLocaleString("en-US", { timeZone: "Asia/Seoul" }));

export function dayKey(iso: string): string {
  const d = seoul(iso);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export function clock(iso: string): string {
  const d = seoul(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** "오늘", "어제", "11월 6일 (목)" from YYYY-MM-DD (lib/when.ts). */
export function dayLabel(date: string): string {
  return whenDayLabel(date);
}

export function monthLabel(month: string, withYear = false): string {
  const [y, m] = month.split("-").map(Number);
  return withYear ? `${y}년 ${m}월` : `${m}월`;
}

export function shiftMonth(month: string, by: number): string {
  const [y, m] = month.split("-").map(Number);
  const d = new Date(y, m - 1 + by, 1);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

/** "방금", "3분 전", "2시간 전", then "어제 22:51", "11월 3일 14:02" (lib/when.ts). */
export function ago(iso: string, now = Date.now()): string {
  return whenAgo(iso, new Date(now));
}

/** Card and issuer, short: "하나 5*2*". */
export function cardLabel(t: Tx): string {
  if (t.source === "manual") return "직접 적음";
  if (t.source === "asset") return `투자금 ${t.issuer.replace(/증권$/, "")}`;
  const issuer = t.issuer.replace(/카드$/, "");
  return t.cardTail ? `${issuer} ${t.cardTail}` : t.issuer;
}

export function groupByDay(txs: Tx[]): { date: string; txs: Tx[]; totalKrw: number }[] {
  const out: { date: string; txs: Tx[]; totalKrw: number }[] = [];
  for (const t of txs) {
    const date = dayKey(t.approvedAt);
    let g = out[out.length - 1];
    if (!g || g.date !== date) {
      g = { date, txs: [], totalKrw: 0 };
      out.push(g);
    }
    g.txs.push(t);
    g.totalKrw += t.signedKrw;
  }
  return out;
}

export async function send(
  path: string,
  method: "POST" | "PATCH" | "PUT" | "DELETE",
  body?: unknown,
): Promise<string | null> {
  const res = await fetch(`/api/spending/${path}`, {
    method,
    headers: body ? { "content-type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  }).catch(() => null);
  if (res?.ok) return null;
  const err = (await res?.json().catch(() => null)) as { error?: string } | null;
  return err?.error ?? "저장하지 못했어요.";
}
