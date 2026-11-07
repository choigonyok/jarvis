/**
 * The kind strings the console reads, in one place. None of them is the
 * console's own: each is written by a service, in Go apart from assets-svc,
 * and the code that writes it is named beside it. A kind renamed there is
 * renamed here, or the screen that reads it quietly stops matching.
 *
 * The agent's stream frames (turn, draft, steps, ...) are not here: their
 * types are the AgentEvent union in lib/thread.ts.
 */

/**
 * What a card asks to run (Proposal.action.kind). The part before the first
 * dot is the module that runs it (agent/internal/core/action Action.Module).
 */
export const ACTION = {
  /** agent/internal/module/calendar/module.go KindCreate. */
  calendarCreate: "calendar.create_event",
  /** agent/internal/module/calendar/module.go KindUpdate. */
  calendarUpdate: "calendar.update_event",
  /** agent/internal/module/calendar/module.go KindDelete. */
  calendarDelete: "calendar.delete_event",
  /** agent/internal/module/jobs/module.go KindAsk: a job's question, approval or form. */
  jobAsk: "job.ask",
  /** agent/internal/module/jobs/module.go KindLogin: a site to log in to in the 화면 tab. */
  jobLogin: "job.login",
  /** agent/internal/module/jobs/module.go KindFirst: whether a job goes before the one on the browser. */
  jobFirst: "job.first",
  /** agent/internal/module/mind/mind.go Approve: an asked card of mind's. */
  mindApprove: "mind.approve",
  /** agent/internal/httpapi/intercept.go: a checkout the payment guard stopped. */
  browserPayment: "browser.payment",
} as const;

/** The modules whose actions the console tells apart by their prefix. */
export const MODULE = {
  /** agent/internal/module/calendar/module.go Name. */
  calendar: "calendar",
  /** agent/internal/module/mind/mind.go Name. */
  mind: "mind",
  /**
   * A tool call the CLI asked permission for: "claude." and the tool's name
   * (agent/internal/adapter/permission/mcp.go).
   */
  claude: "claude",
} as const;

/** Whether an action is one of a module's. */
export const ofModule = (kind: string, module: string) => kind.startsWith(`${module}.`);

/** A permission request for one of the agent's own tools, which are MCP tools ("mcp__<server>__<tool>"). */
export const CLAUDE_MCP = `${MODULE.claude}.mcp__`;

/**
 * Inbox entries (output-svc keeps them; each sender's gocommon/notify Event
 * Kind): the sender's name, a dot, and what happened.
 */
export const NOTE = {
  /** mind-svc/internal/gate/gate.go: a notice of mind's, sent under MIND_KEY. */
  mindInfo: "mind.info",
  /** mind-svc/internal/spending/watch/watch.go. */
  spendingUnparsed: "spending.unparsed",
  spendingBudget: "spending.budget",
  spendingDaily: "spending.daily",
  /** assets-svc/src/notify.ts (read by mind-svc/internal/mind/ledgers.go AssetsBand). */
  assetsBand: "assets.band",
  /** assets-svc/src/notify.ts. */
  assetsFlow: "assets.flow",
  assetsLedger: "assets.ledger",
  assetsTax: "assets.tax",
} as const;

/** The key a notice of mind's is sent under, its intent after it (mind-svc/internal/gate/gate.go). */
export const MIND_KEY = "mind:";

/**
 * The senders, as the start of a kind: spending (mind-svc), assets
 * (assets-svc), status (status-svc/main.go), calendar (calendar-svc),
 * location (mind-svc/internal/mind/moments.go), job (agent/cmd/agent/main.go).
 */
export const SENDER = {
  spending: "spending.",
  assets: "assets.",
  status: "status.",
  calendar: "calendar.",
  location: "location.",
  job: "job.",
} as const;

/**
 * The lines of a job's run: mind-svc/internal/jobs/store/store.go writes
 * run_start, agent/internal/module/jobs/runtools.go the steps and the
 * screens a run took.
 */
export const JOB_EVENT = {
  runStart: "run_start",
  step: "step",
  shot: "shot",
} as const;
