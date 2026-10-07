import type { Proposal, ProposalState } from "@/lib/thread";

/**
 * The record surface reads the same proposals the chat does - there is no
 * second store and no read/unread flag. A decision's state is the only state
 * there is, so a request cannot be "dismissed" into looking handled.
 */

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
  approved: { label: "승인함", edge: "bg-approve/65" },
  executed: { label: "실행함", edge: "bg-approve/65" },
  rejected: { label: "반려함", edge: "bg-reject/65" },
  failed: { label: "실패함", edge: "bg-reject/65" },
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

const WEEKDAYS = ["일", "월", "화", "수", "목", "금", "토"];

/** "9월 22일 (화)", and today and yesterday by name. */
export function dayLabel(date: Date | null, now = new Date()): string {
  if (!date) return "날짜 없음";
  const days = Math.round(
    (startOfDay(now).getTime() - startOfDay(date).getTime()) / 86_400_000,
  );
  if (days === 0) return "오늘";
  if (days === 1) return "어제";
  return `${date.getMonth() + 1}월 ${date.getDate()}일 (${WEEKDAYS[date.getDay()]})`;
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
  const sorted = proposals
    .slice()
    .sort((a, b) => (raisedAt(b)?.getTime() ?? 0) - (raisedAt(a)?.getTime() ?? 0));

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

/**
 * The one-line version of what a request would do. The card's title is
 * written for a card with the body underneath it; in a scannable list it is
 * the whole row, so it carries the weight alone.
 */
export function summarize(proposal: Proposal): string {
  return proposal.card.title.trim() || proposal.action.kind;
}
