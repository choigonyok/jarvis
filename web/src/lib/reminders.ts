import { when } from "@/lib/jobs";

/**
 * A reminder mind keeps: a line to be said at a moment - a time, or getting
 * home or leaving a place - as the conversation set it ("8시에 알려줘",
 * "집에 가면 알려줘"), or as mind did (rebalancing after work, a checklist).
 * The 작업 tab lists the waiting ones and the last month's (agent GET
 * /reminders, mind's own answer for this person).
 */
export type Reminder = {
  id: string;
  /** What is said: "내일 캠핑, 장갑 챙기세요". */
  text: string;
  /** A line more under it. */
  body?: string;
  /** Who set it: chat, checklist, rebalance. */
  source: string;
  status: "waiting" | "sent" | "cancelled" | "expired";
  /** When it was said. */
  sentAt?: string;
  /**
   * How it went once its moment came: mind's code, read here instead of its
   * words - "목록이 닫혀 말하지 않음" read as said. silent: nothing
   * went out, for `unsaid`.
   */
  how?: "push" | "card" | "digest" | "silent" | "failed";
  /** Why nothing went out, or what failed. */
  unsaid?: string;
  /** The same in mind's words. */
  result?: string;
  updatedAt: string;
  /** mind's words for the moment: "10/29(수) 08:00", "집에 도착할 때 · 늦어도 11/11(화) 21:00". */
  when: string;
};

export const waiting = (r: Reminder) => r.status === "waiting";

const HOW: Record<NonNullable<Reminder["how"]>, string> = {
  push: "푸시로 알림",
  card: "카드로 알림",
  digest: "저녁 요약으로",
  silent: "말하지 않음",
  failed: "보내지 못함",
};

/** Nothing went out. */
const unsaid = (r: Reminder) => r.how === "silent" || r.how === "failed";

/** What came of it, short - the row's right side. "" while it waits. */
export function outcome(r: Reminder): string {
  switch (r.status) {
    case "waiting":
      return "";
    case "cancelled":
      return "취소함";
    case "expired":
      return "기한 지남";
  }
  return (r.how && HOW[r.how]) || "알림";
}

export function outcomeTone(r: Reminder): string {
  if (r.status === "sent" && !unsaid(r)) return "text-approve";
  return "text-faint";
}

const SOURCES: Record<string, string> = { checklist: "체크리스트", rebalance: "리밸런싱" };

/** Who set it, when it was not the conversation. */
export const sourceLabel = (r: Reminder) => SOURCES[r.source] ?? "";

/**
 * The row's last line: the moment it waits for, or when it ended - then who
 * set it, when not the conversation, and why it was not said.
 */
export function detail(r: Reminder): string {
  const parts: string[] = [];
  if (r.status === "sent" && r.sentAt) parts.push(when(r.sentAt));
  else if (r.status === "cancelled") parts.push(`${when(r.updatedAt)} 취소`);
  else parts.push(r.when);
  parts.push(sourceLabel(r));
  if (r.status === "sent" && unsaid(r)) parts.push(r.unsaid ?? "");
  return parts.filter(Boolean).join(" · ");
}
