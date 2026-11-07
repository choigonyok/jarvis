import { readableTitle } from "@/lib/card-body";
import { dayLabel as whenDayLabel, dayOf, monthDay } from "@/lib/when";
import { API, type Proposal, type ProposalState } from "@/lib/thread";
import { HttpError } from "@/lib/use-resource";

/**
 * The record surface reads the same proposals the chat does, and the ones
 * the agent's store keeps in its archive - there is no second store and no
 * read/unread flag. A decision's state is the only state there is, so a
 * request cannot be "dismissed" into looking handled.
 */

/**
 * The cards in the agent's archive: decided, and unchanged for two weeks.
 * The thread's pages carry the live cards and those their turns point at,
 * so the record reads the rest here.
 */
export async function readArchive(signal: AbortSignal): Promise<Proposal[]> {
  const res = await fetch(`${API}/proposals?scope=archive`, { cache: "no-store", signal });
  if (!res.ok) throw new HttpError(res.status);
  return ((await res.json()) as { proposals?: Proposal[] }).proposals ?? [];
}

/** Every card once: the live ones, then the archive's not among them - the live copy is the one the stream keeps current. */
export function withArchive(live: Proposal[], archived: Proposal[] | undefined): Proposal[] {
  if (!archived?.length) return live;
  const have = new Set(live.map((p) => p.id));
  const old = archived.filter((p) => !have.has(p.id));
  return old.length > 0 ? [...live, ...old] : live;
}

export type Filter = "all" | "pending" | "approved" | "rejected";

export const filters: { value: Filter; label: string }[] = [
  { value: "all", label: "전체" },
  { value: "pending", label: "대기" },
  { value: "approved", label: "승인" },
  { value: "rejected", label: "반려" },
];

/** How each state reads, and which edge it draws down the margin. */
export const stateCopy: Record<ProposalState, { label: string; edge: string }> = {
  // The margin edges are held back from full strength on purpose. At full
  // chroma a column of them outshines the rows they belong to, and the point
  // of the column is to be readable at a glance *before* the words - not
  // instead of them.
  pending: { label: "대기 중", edge: "bg-dim/50" },
  approved: { label: "승인함", edge: "bg-approve-fill" },
  executed: { label: "실행함", edge: "bg-approve-fill" },
  rejected: { label: "반려함", edge: "bg-reject-fill" },
  failed: { label: "실패함", edge: "bg-reject-fill" },
};

export function matches(proposal: Proposal, filter: Filter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "pending":
      return proposal.state === "pending";
    case "approved":
      return proposal.state === "approved" || proposal.state === "executed";
    case "rejected":
      return proposal.state === "rejected" || proposal.state === "failed";
  }
}

/**
 * When a proposal was raised.
 *
 * The agent stamps `at` as a wall clock ("15:04") with no date on it, so a
 * week of records would otherwise collapse into one undated list. The id is
 * minted as `p-<epoch ms>-<seq>`, which carries the day the clock dropped.
 * Reading it here keeps this surface to the frontend; the agent should grow a
 * real timestamp field, and this falls back to undated when it does not match.
 */
export function raisedAt(proposal: Proposal): Date | null {
  const ms = Number(/^p-(\d{10,})-/.exec(proposal.id)?.[1]);
  if (!Number.isFinite(ms)) return null;
  const date = new Date(ms);
  return Number.isNaN(date.getTime()) ? null : date;
}

/** "오늘", "어제", "9월 22일 (화)" (lib/when.ts). */
export function dayLabel(date: Date | null, now = new Date()): string {
  if (!date) return "날짜 없음";
  return whenDayLabel(dayOf(date), now);
}

function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

export type Day = { key: string; label: string; entries: Proposal[] };

/**
 * Newest first, grouped by the day each request was raised. Sorting is by the
 * id's timestamp rather than the wall clock, so 23:59 and 00:01 do not swap
 * places across a midnight.
 */
export function groupByDay(proposals: Proposal[], now = new Date()): Day[] {
  const sorted = newestFirst(proposals);

  const days: Day[] = [];
  for (const proposal of sorted) {
    const date = raisedAt(proposal);
    const key = date ? startOfDay(date).toISOString().slice(0, 10) : "unknown";
    const last = days.at(-1);
    if (last?.key === key) last.entries.push(proposal);
    else days.push({ key, label: dayLabel(date, now), entries: [proposal] });
  }
  return days;
}

/** The day a card still waiting came, when not today: the 대기 rows have no day above them. */
export function waitingSince(proposal: Proposal, now = new Date()): string | undefined {
  const date = raisedAt(proposal);
  if (!date || startOfDay(date).getTime() === startOfDay(now).getTime()) return undefined;
  return monthDay(dayOf(date), now);
}

/** The order the record shows them in - and pages them in (lib/paging.ts). */
export function newestFirst(proposals: Proposal[]): Proposal[] {
  return proposals.slice().sort((a, b) => (raisedAt(b)?.getTime() ?? 0) - (raisedAt(a)?.getTime() ?? 0));
}

/**
 * The one-line version of what a request would do. The card's title is
 * written for a card with the body underneath it; in a scannable list it is
 * the whole row, so it carries the weight alone.
 */
export function summarize(proposal: Proposal): string {
  return readableTitle(proposal.card.title).trim() || proposal.action.kind;
}
