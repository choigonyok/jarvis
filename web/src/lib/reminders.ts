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
  /** When it was said, and how: "푸시", "카드", or "말하지 않음: …" (why it was not). */
  sentAt?: string;
  result?: string;
  updatedAt: string;
  /** mind's words for the moment: "10/29(수) 08:00", "집에 도착할 때 · 늦어도 11/11(화) 21:00". */
  when: string;
};

export const waiting = (r: Reminder) => r.status === "waiting";

const NOT_SAID = "말하지 않음";

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
  const how = (r.result ?? "").trim();
  if (how.startsWith(NOT_SAID)) return NOT_SAID;
  if (how === "푸시") return "푸시로 알림";
  if (how === "카드") return "카드로 알림";
  return "알림";
}

export function outcomeTone(r: Reminder): string {
  if (r.status === "sent" && !(r.result ?? "").startsWith(NOT_SAID)) return "text-approve";
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
  const why = (r.result ?? "").trim();
  if (r.status === "sent" && why.startsWith(NOT_SAID)) parts.push(why.slice(NOT_SAID.length).replace(/^[:：]\s*/, ""));
  return parts.filter(Boolean).join(" · ");
}
