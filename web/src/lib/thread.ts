// These shapes mirror the agent's JSON exactly; the agent is the source of truth.

export type ProposalState = "pending" | "approved" | "rejected" | "executed" | "failed";

export type Decision = "approved" | "rejected";

/** What a module was asked to do. `input` is the tool's own arguments. */
export type ProposalAction = {
  kind: string;
  input: Record<string, unknown>;
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
  card: { title: string; body: string; consequence: string };
  confidence?: number;
  state: ProposalState;
  note?: string;
  at: string;
  decidedAt?: string;
};

export type Turn = {
  id: string;
  role: "agent" | "user";
  at: string;
  paragraphs?: string[];
  text?: string;
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
  source: "jarvis" | "me";
  updatedAt: string;
};

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
