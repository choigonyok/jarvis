// These shapes mirror the agent's JSON exactly; the agent is the source of truth.

export type ProposalState = "pending" | "approved" | "rejected" | "executed" | "failed";

export type Decision = "approved" | "rejected";

/** What a module was asked to do. `input` is the tool's own arguments. */
export type ProposalAction = {
  kind: string;
  input: Record<string, unknown>;
};

/** One value on a card the operator may change before approving. */
export type CardField = {
  key: string;
  label: string;
  /** multi: several options, kept comma-joined ("소음·매너,청결"). */
  kind: "text" | "textarea" | "number" | "select" | "multi";
  value: string;
  options?: string[];
  /** Approval stays off while this is empty. */
  required?: boolean;
  /** A line under the field: a price reference, a format. */
  hint?: string;
};

export type ProposalCardBody = {
  title: string;
  body: string;
  consequence: string;
  /** Upload names shown on the card. */
  images?: string[];
  /** Present when the card is a form (a Joongna draft). */
  fields?: CardField[];
};

/**
 * A proposal is a stored object, not a property of a chat turn: it can be
 * raised with no conversation behind it, which is how a detector will
 * eventually suggest something nobody asked for.
 */
export type Proposal = {
  id: string;
  origin: string;
  action: ProposalAction;
  card: ProposalCardBody;
  confidence?: number;
  state: ProposalState;
  note?: string;
  at: string;
  decidedAt?: string;
  /** What the operator said when deciding - why it was turned down. */
  reason?: string;
  /** Every change the operator made to a form card before deciding. */
  edits?: { at: string; fields: Record<string, string> }[];
};

/** An earlier turn a message quotes - swiped on the thread before sending. */
export type TurnRef = {
  id: string;
  role: "agent" | "user";
  at?: string;
  text: string;
};

export type Turn = {
  id: string;
  role: "agent" | "user";
  at: string;
  /** Full timestamp (chat-svc); `at` is only the wall clock. */
  atIso?: string;
  /** The earlier message this one quotes. */
  ref?: TurnRef;
  paragraphs?: string[];
  text?: string;
  /** Photos the operator attached, by upload name (see lib/uploads.ts). */
  images?: string[];
  /** Points at a proposal; the card itself lives in the proposal store. */
  proposalId?: string;
};

export type CalendarEvent = {
  id: string;
  date: string; // YYYY-MM-DD
  start?: string; // HH:MM
  end?: string;
  title: string;
  place?: string;
  memo?: string;
  source: "jarvis" | "me" | "partner";
  updatedAt: string;
  /** Last day of a multi-day entry. Absent for a single day. */
  endDate?: string;
  /** Whose entry it is on the shared couple calendar. Labels, never hides. */
  owner?: "me" | "partner" | "shared";
  /** Set on a repeating entry; its days carry ids "<series>#<date>". */
  recurrence?: string;
};

/** True when the entry is on `date`, counting every day of a multi-day one. */
export function covers(e: Pick<CalendarEvent, "date" | "endDate">, date: string): boolean {
  return e.date <= date && date <= (e.endDate || e.date);
}

export type CalendarChange = {
  op: "upsert" | "delete";
  event: CalendarEvent;
};

export type AgentEvent =
  | { type: "turn"; turn: Turn }
  | { type: "status"; thinking?: boolean }
  | { type: "error"; message: string }
  | { type: "proposal"; proposal: Proposal }
  | { type: "calendar"; calendar: CalendarChange };

export type Connection = "connecting" | "open" | "closed";

export const API = "/api/agent";

export function isPending(p: Proposal): boolean {
  return p.state === "pending";
}

/** Markdown as one plain line, for a quote: marks dropped, links as their words. */
export function plainText(md: string): string {
  return md
    .replace(/```[^\n]*\n?/g, "")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s{0,3}(#{1,6}|>|[-*+]|\d+\.)\s+/gm, "")
    .replace(/\*\*|__|~~/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** What a turn says, as a quote of it shows: its words, or its photos. */
export function quoteText(turn: Pick<Turn, "paragraphs" | "text" | "images">): string {
  const text = plainText(turn.paragraphs?.length ? turn.paragraphs.join("\n\n") : (turn.text ?? ""));
  if (text) return text;
  return turn.images?.length ? `사진 ${turn.images.length}장` : "";
}
