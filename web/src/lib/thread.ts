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
  /** multi: several options, kept comma-joined ("소음·매너,청결"). secret: typed hidden (a password). */
  kind: "text" | "textarea" | "number" | "select" | "multi" | "secret";
  value: string;
  options?: string[];
  /** Approval stays off while this is empty. */
  required?: boolean;
  /** A line under the field: a price reference, a format. */
  hint?: string;
  /**
   * Never kept with the card nor told to Jarvis: at approval the agent hands
   * it to the module (a login to the vault) and drops it. A secret always is.
   */
  private?: boolean;
};

/** Whether a field's value bypasses the card's record (a login). */
export function isPrivateField(f: CardField): boolean {
  return Boolean(f.private) || f.kind === "secret";
}

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
  /** Full timestamp (input-svc); `at` is only the wall clock. */
  atIso?: string;
  /** The earlier message this one quotes. */
  ref?: TurnRef;
  paragraphs?: string[];
  text?: string;
  /** Photos the operator attached, by upload name (see lib/uploads.ts). */
  images?: string[];
  /** Points at a proposal; the card itself lives in the proposal store. */
  proposalId?: string;
  /**
   * Set when the turn is not the conversation's own words: news from a
   * background job's run (drawn beside the thread; a later word from the
   * same run replaces it - same id, newer atIso), something told at its
   * moment (a notice), or a checklist shown.
   */
  origin?: TurnOrigin;
};

export type TurnOrigin = {
  kind: "job" | "notice" | "checklist";
  job?: number;
  run?: number;
  /** A job's name; a notice's why-now ("집에 도착하셔서 알려 드려요"); a list's name. */
  title?: string;
  /** What a notice was sent as. */
  ref?: string;
  /** The checklist the card shows, by id. */
  checklist?: string;
};

/** One line of a checklist. */
export type ChecklistItem = {
  id: string;
  text: string;
  done: boolean;
  doneAt?: string;
  /** Who last ticked it: "card", "chat", "mind". */
  by?: string;
};

/** A checklist as mind keeps it: what its cards draw, checks and all. */
export type Checklist = {
  id: string;
  title: string;
  note?: string;
  /** The day the list is for ("2026-10-24"). */
  date?: string;
  items: ChecklistItem[];
  status: "active" | "archived";
  updatedAt?: string;
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

/** A message sent while Jarvis was busy, waiting behind the running turn. */
export type Queued = {
  id: string;
  text: string;
  images?: string[];
  at: string;
};

/**
 * One frame of an answer still being written: `text` goes at `at` (a string
 * index) of what it says so far, `at` 0 being the whole of it. `gone` says it
 * will not become a turn. The turn it becomes arrives with `replaces` set.
 */
export type DraftFrame = {
  id: string;
  at: number;
  text?: string;
  gone?: boolean;
};

/**
 * An answer as far as it has been written. `joined` says this screen came to
 * it midway - the stream (re)opened and was sent what there was so far - so
 * that is shown at once rather than typed out. `turn` is the turn it became,
 * once that has come: the screen lets out the last of its words before the
 * turn takes its place.
 */
export type Draft = { id: string; text: string; joined?: boolean; turn?: string };

/**
 * Words as the runner stores them - split on blank lines, each part trimmed -
 * so a draft drawn this way reads the same as the turn that replaces it.
 */
export function storedForm(text: string): string {
  return text
    .split("\n\n")
    .map((p) => p.trim())
    .filter(Boolean)
    .join("\n\n");
}

/**
 * A frame applied to what is on screen. One that does not follow on - it
 * starts past the end, a frame having been missed - leaves the draft as it
 * is until a whole one (`at` 0) or the turn comes.
 */
export function applyDraft(cur: Draft | null, f: DraftFrame): Draft | null {
  if (f.gone) return cur?.id === f.id ? null : cur;
  // A whole: the first words of a new answer (a few characters), or - for a
  // draft this screen has not seen - what a stream opened midway was sent.
  // Judged by the frame, not by how long the text is when first drawn: two
  // frames landing together are drawn as one, and that read as joined.
  if (f.at === 0) return { id: f.id, text: f.text ?? "", joined: cur?.id !== f.id && (f.text?.length ?? 0) >= 60 };
  if (cur?.id !== f.id || f.at > cur.text.length) return cur;
  // What was known of it stays (joined, turn): frames landing before a
  // render are applied together, and the first render must still see them.
  return { ...cur, text: cur.text.slice(0, f.at) + (f.text ?? "") };
}

export type AgentEvent =
  | { type: "queue"; queue?: Queued[] }
  | { type: "turn"; turn: Turn; replaces?: string }
  | { type: "draft"; draft: DraftFrame }
  | { type: "status"; thinking?: boolean }
  | { type: "error"; message: string }
  | { type: "proposal"; proposal: Proposal }
  | { type: "calendar"; calendar: CalendarChange }
  | { type: "checklist"; checklist: Checklist };

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
    .replace(/\*\*|__/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** What a turn says, as a quote of it shows: its words, or its photos. */
export function quoteText(turn: Pick<Turn, "paragraphs" | "text" | "images">): string {
  const text = plainText(turn.paragraphs?.length ? turn.paragraphs.join("\n\n") : (turn.text ?? ""));
  if (text) return text;
  return turn.images?.length ? `사진 ${turn.images.length}장` : "";
}
