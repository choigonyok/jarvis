"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useAgentStream } from "@/lib/stream";
import {
  API,
  applyDraft,
  isPrivateField,
  storedForm,
  type AgentEvent,
  type Checklist,
  type Draft,
  type Decision,
  type Proposal,
  type Queued,
  type Step,
  type Turn,
} from "@/lib/thread";
import { DECIDE_ECHO_MS, decideProposal } from "@/lib/decide";
import { diag } from "@/lib/diag";
import { requestResume } from "@/lib/resume";

/** The lists mind keeps: read there, changed at the way in (the route says which). */
const LISTS = "/api/mind/checklists";
const byList = (lists?: Checklist[] | null) => Object.fromEntries((lists ?? []).map((l) => [l.id, l]));

/**
 * Upsert by id: an updated turn or proposal arrives the way the first did.
 * One given a newer time (a job's card with its latest word) moves to where
 * that time puts it - the bottom - rather than being rewritten in place.
 */
/**
 * A turn the server sends again keeps the row this page drew it in (rowKey):
 * the server does not know it, and without it the row is built again.
 */
function keepRows(prev: Turn[], next: Turn[]): Turn[] {
  const rows = new Map<string, string>();
  for (const t of prev) if (t.rowKey) rows.set(t.id, t.rowKey);
  if (rows.size === 0) return next;
  return next.map((t) => (t.rowKey || !rows.has(t.id) ? t : { ...t, rowKey: rows.get(t.id) }));
}

/**
 * The cards opened here - a checklist, a job's news unfolded - by turn, for as
 * long as the app is open: leaving for another tab and coming back finds them
 * as they were, and one being read stays where it is (upsert).
 */
export const openedTurns = new Set<string>();

function upsert<T extends { id: string; atIso?: string }>(list: T[], next: T, inPlace?: (id: string) => boolean): T[] {
  const at = list.findIndex((item) => item.id === next.id);
  if (at === -1) return [...list, next];
  const copy = list.slice();
  // A newer word on a card open in front of the reader is written where it
  // is, marked as news: moved down to its time, the card being read vanished
  // and came back at the bottom.
  if (inPlace?.(next.id) && next.atIso && copy[at].atIso && next.atIso > copy[at].atIso) {
    copy[at] = { ...next, freshNews: true };
    return copy;
  }
  if (next.atIso && copy[at].atIso && next.atIso > copy[at].atIso) {
    copy.splice(at, 1);
    let to = copy.length;
    while (to > 0 && (copy[to - 1].atIso ?? "") > next.atIso) to -= 1;
    copy.splice(to, 0, next);
    return copy;
  }
  copy[at] = next;
  return copy;
}

// The last thread this page saw. Coming back to a tab renders it at once -
// already scrolled to the newest turn - and the fetch then only corrects it.
let cached: { turns: Turn[]; proposals: Proposal[]; thinking: boolean; hasMore?: boolean } | null = null;
let cachedQueue: Queued[] = [];
// The lists the thread's checklist cards show, by id, as they stood last.
let cachedLists: Record<string, Checklist> = {};
// Every card that was waiting while this page was open: one decided here
// stays on screen even when no turn points at it (thread.tsx).
const waited = new Set<string>();

/** The checklists the turns' cards show that are not known yet. */
function missingLists(turns: Turn[], known: Record<string, Checklist>): string[] {
  const ids = new Set<string>();
  for (const t of turns) {
    const id = t.origin?.checklist;
    if (id && !known[id]) ids.add(id);
  }
  return [...ids];
}

/** How many turns a page of the thread holds: the newest on opening, the ones above on scrolling up. */
const PAGE = 60;

/** How long a sent message may take to come back over the stream before the thread is re-read. */
const SEND_ECHO_MS = 4000;

export function useThread() {
  const [turns, setTurnsState] = useState<Turn[]>(() => cached?.turns ?? []);
  // Whether the thread goes on above the oldest loaded turn, and whether
  // that page is on its way.
  const [hasMore, setHasMore] = useState(() => cached?.hasMore ?? true);
  const [loadingOlder, setLoadingOlder] = useState(false);
  // The last page above could not be read: not asked again on its own (a
  // scroll would only fail again), but on a tap.
  const [olderFailed, setOlderFailed] = useState(false);
  const [proposals, setProposalsState] = useState<Proposal[]>(() => cached?.proposals ?? []);
  const [thinking, setThinking] = useState(() => cached?.thinking ?? false);
  // The answer being written, shown as it grows until its turn takes its
  // place (draftShown). Not cached: a stream that opens mid-answer is sent
  // it whole first.
  const [draft, setDraft] = useState<Draft | null>(null);
  // What the turn under way has done since the last thing said. Not cached:
  // a run is not under way on a fresh page until the thread says so.
  const [steps, setSteps] = useState<Step[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(() => cached !== null);
  const [queue, setQueueState] = useState<Queued[]>(() => cachedQueue);
  const setQueue = useCallback((next: Queued[]) => {
    cachedQueue = next;
    setQueueState(next);
  }, []);
  const [checklists, setChecklistsState] = useState<Record<string, Checklist>>(() => cachedLists);
  const setChecklists = useCallback((next: (prev: Record<string, Checklist>) => Record<string, Checklist>) => {
    setChecklistsState((prev) => {
      cachedLists = next(prev);
      return cachedLists;
    });
  }, []);
  const putLists = useCallback(
    (lists?: Record<string, Checklist> | null) => {
      if (lists && Object.keys(lists).length > 0) setChecklists((prev) => ({ ...prev, ...lists }));
    },
    [setChecklists],
  );
  // A card whose list has not come with the page (laid while this page was
  // open, before its list's own frame): asked for once.
  const asking = useRef(new Set<string>());
  const fetchLists = useCallback(
    async (ids: string[]) => {
      const want = ids.filter((id) => !asking.current.has(id));
      if (want.length === 0) return;
      for (const id of want) asking.current.add(id);
      try {
        const res = await fetch(`${LISTS}?ids=${encodeURIComponent(want.join(","))}`, { cache: "no-store" });
        if (res.ok) putLists(byList(((await res.json()) as { checklists?: Checklist[] }).checklists));
      } catch {
        // The card says it could not be read; a reload asks again.
      } finally {
        for (const id of want) asking.current.delete(id);
      }
    },
    [putLists],
  );

  const setTurns = useCallback((next: Turn[] | ((prev: Turn[]) => Turn[])) => {
    setTurnsState((prev) => {
      const value = keepRows(prev, typeof next === "function" ? next(prev) : next);
      cached = { ...(cached ?? { proposals: [], thinking: false }), turns: value };
      return value;
    });
  }, []);
  const setProposals = useCallback((next: Proposal[] | ((prev: Proposal[]) => Proposal[])) => {
    setProposalsState((prev) => {
      const value = typeof next === "function" ? next(prev) : next;
      cached = { ...(cached ?? { turns: [], thinking: false }), proposals: value };
      for (const p of value) if (p.state === "pending") waited.add(p.id);
      return value;
    });
  }, []);
  const setMore = useCallback((more: boolean) => {
    cached = { ...(cached ?? { turns: [], proposals: [], thinking: false }), hasMore: more };
    setHasMore(more);
  }, []);

  // The newest page - or, on a reconnect, as many turns as are on screen,
  // so what the reader scrolled up to does not vanish under them.
  const hydrate = useCallback(async () => {
    try {
      const limit = Math.max(PAGE, cached?.turns.length ?? 0);
      const res = await fetch(`${API}/thread?limit=${limit}`, { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const data = (await res.json()) as {
        turns: Turn[];
        thinking: boolean;
        hasMore?: boolean;
        proposals: Proposal[];
        queue?: Queued[];
        checklists?: Record<string, Checklist>;
        steps?: Step[];
      };
      setTurns(data.turns ?? []);
      setMore(Boolean(data.hasMore));
      setProposals(data.proposals ?? []);
      setThinking(Boolean(data.thinking));
      setSteps(data.thinking ? (data.steps ?? []) : []);
      setQueue(data.queue ?? []);
      putLists(data.checklists);
      // Read: a failure said before this (a first load that did not come) is
      // over, not left at the bottom of the thread for good.
      setError(null);
    } catch {
      setError("대화를 불러오지 못했어요.");
    } finally {
      setHydrated(true);
    }
  }, [setTurns, setMore, setProposals, setQueue, putLists]);

  // The page above the oldest turn on screen, put in front of it. Resolves
  // once the turns are in (the caller keeps the view where it was).
  const loadOlder = useCallback(async () => {
    const oldest = cached?.turns[0];
    if (!oldest?.id || loadingOlder) return;
    setLoadingOlder(true);
    try {
      // The cursor is the turn's id, not its time: a "+09:00" in the query
      // came out as a space on the far side of the proxies.
      const res = await fetch(`${API}/thread?limit=${PAGE}&before=${encodeURIComponent(oldest.id)}`, { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const data = (await res.json()) as { turns: Turn[]; hasMore?: boolean; checklists?: Record<string, Checklist> };
      const older = data.turns ?? [];
      putLists(data.checklists);
      setTurns((prev) => {
        const have = new Set(prev.map((t) => t.id));
        return [...older.filter((t) => !have.has(t.id)), ...prev];
      });
      setMore(Boolean(data.hasMore));
      setOlderFailed(false);
    } catch {
      setOlderFailed(true);
    } finally {
      setLoadingOlder(false);
    }
  }, [loadingOlder, setTurns, setMore, putLists]);

  // When the stream last carried anything at all - see send.
  const heardAt = useRef(0);

  const onEvent = useCallback((event: AgentEvent) => {
    heardAt.current = Date.now();
    if (event.type === "status" || event.type === "turn" || event.type === "error") {
      diag("voice", { ev: "thread." + event.type, thinking: event.type === "status" ? (event.thinking ? 1 : 0) : -1, role: event.type === "turn" ? event.turn.role : "" });
    }
    switch (event.type) {
      case "turn":
        setTurns((prev) => upsert(prev, event.turn, (id) => openedTurns.has(id)));
        if (event.replaces) {
          const { replaces, turn } = event;
          // In the same render as the turn, so the words never show twice:
          // the draft holds its place while the screen lets out its last
          // words - unless it missed some, when the turn shows at once.
          setDraft((cur) => {
            if (!cur || cur.id !== replaces) return cur;
            return storedForm(cur.text) === storedForm(turn.paragraphs?.join("\n\n") ?? "") ? { ...cur, turn: turn.id } : null;
          });
        }
        if (event.turn.origin?.checklist && !cachedLists[event.turn.origin.checklist]) {
          // Its list usually follows on the stream; if not by then, ask.
          const id = event.turn.origin.checklist;
          window.setTimeout(() => {
            if (!cachedLists[id]) void fetchLists([id]);
          }, 1200);
        }
        break;
      case "checklist":
        if (event.checklist?.id) setChecklists((prev) => ({ ...prev, [event.checklist.id]: event.checklist }));
        break;
      case "proposal":
        setProposals((prev) => upsert(prev, event.proposal));
        break;
      case "draft":
        setDraft((cur) => applyDraft(cur, event.draft));
        break;
      case "steps":
        setSteps(event.steps ?? []);
        break;
      case "status":
        setThinking(Boolean(event.thinking));
        if (!event.thinking) setSteps([]);
        // A run that is over is writing nothing - but a finished answer may
        // still be coming out on screen.
        if (!event.thinking) setDraft((cur) => (cur?.turn ? cur : null));
        break;
      case "queue":
        setQueue(event.queue ?? []);
        break;
      case "error":
        setError(event.message);
        setThinking(false);
        setDraft(null);
        setSteps([]);
        break;
    }
  }, [setTurns, setProposals, setQueue, setChecklists, fetchLists]);

  // A page of turns came with cards whose lists did not come along (mind did
  // not answer the agent in time): asked for once more.
  useEffect(() => {
    if (!hydrated) return;
    const ids = missingLists(turns, cachedLists);
    if (ids.length > 0) void fetchLists(ids);
  }, [hydrated, turns, fetchLists]);

  // Hydrate first, then follow. A reconnect re-hydrates so dropped frames heal.
  useEffect(() => {
    // Not a cascading render: hydrate only sets state once the fetch it
    // starts has resolved. The lint rule cannot see past the async call.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void hydrate();
  }, [hydrate]);

  // A reopened stream sends the answer being written whole as its first
  // frame (after the open); what was on screen may be one since finished.
  const connection = useAgentStream(onEvent, () => {
    setDraft(null);
    void hydrate();
  });

  // The screen has let out a finished draft's last word: its turn shows.
  // The last word out: the turn shows itself, in the row that typed it.
  const draftShown = useCallback((id: string, turn?: string) => {
    setDraft((cur) => (cur?.id === id ? null : cur));
    if (turn) setTurns((prev) => prev.map((t) => (t.id === turn ? { ...t, rowKey: id } : t)));
  }, [setTurns]);

  // Re-read the thread and reopen the stream - for a screen that sent and
  // heard nothing back, which is what a quietly dead stream looks like.
  const resync = useCallback(() => {
    diag("voice", { ev: "thread.resync" });
    void hydrate();
    requestResume();
  }, [hydrate]);

  /** Resolves true once the agent took the message - to answer now, or queued behind the running turn. */
  const send = useCallback(async (text: string, images?: string[], refId?: string) => {
    setError(null);
    try {
      const res = await fetch("/api/chat/messages", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          text,
          ...(images?.length ? { images } : {}),
          ...(refId ? { ref: { id: refId } } : {}),
        }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        setError(body?.error ?? "메시지를 보내지 못했어요.");
        return false;
      }
      // What was sent comes back over the stream within a moment - as a turn,
      // or in the queue. Nothing at all back means the stream is dead without
      // having said so, and the message would not show until the app was
      // restarted: read the thread now and open a fresh stream.
      const asked = Date.now();
      setTimeout(() => {
        if (heardAt.current < asked) {
          diag("voice", { ev: "thread.echo-miss" });
          resync();
        }
      }, SEND_ECHO_MS);
      return true;
    } catch {
      setError("에이전트에 연결하지 못했어요.");
      return false;
    }
  }, [resync]);

  /**
   * Acts on a waiting message: "steer" hands it to the running turn, "drop"
   * takes it back unsent. The queue itself arrives over the stream.
   */
  const onQueued = useCallback(async (id: string, action: "steer" | "drop") => {
    setError(null);
    try {
      const res = await fetch(`${API}/messages/${encodeURIComponent(id)}/${action}`, { method: "POST" });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        setError(body?.error ?? "대기 중인 메시지를 바꾸지 못했어요.");
        void hydrate();
      }
    } catch {
      setError("에이전트에 연결하지 못했어요.");
    }
  }, [hydrate]);
  // Stable, so the composer's queue is not drawn again on every frame.
  const steer = useCallback((id: string) => onQueued(id, "steer"), [onQueued]);
  const drop = useCallback((id: string) => onQueued(id, "drop"), [onQueued]);

  /**
   * Stops the answer being written: what it wrote so far stays, marked where it
   * stopped, and what waits behind it goes on. The end arrives over the
   * stream; an answer that had just ended has nothing to stop.
   */
  const stop = useCallback(async () => {
    try {
      const res = await fetch(`${API}/thread/stop`, { method: "POST" });
      if (!res.ok && res.status !== 409) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        setError(body?.error ?? "답을 멈추지 못했어요.");
      }
    } catch {
      setError("에이전트에 연결하지 못했어요.");
    }
  }, []);

  /**
   * Marks an answer good or wrong ("" takes the mark off), with why when it is
   * wrong. Resolves to what to say when it could not be kept; the mark shows
   * once it is. Jarvis hears of it before its next turn.
   */
  const mark = useCallback(
    async (turnId: string, value: "good" | "bad" | "", note = ""): Promise<string | null> => {
      try {
        const res = await fetch(`/api/chat/turns/${encodeURIComponent(turnId)}/feedback`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ mark: value, ...(value === "bad" && note.trim() ? { note: note.trim() } : {}) }),
        });
        if (!res.ok) {
          const body = (await res.json().catch(() => null)) as { error?: string } | null;
          return body?.error ?? "표시하지 못했어요.";
        }
        const saved = (await res.json()) as Turn;
        setTurns((prev) => prev.map((t) => (t.id === turnId ? { ...t, feedback: saved.feedback } : t)));
        return null;
      } catch {
        return "표시하지 못했어요. 연결을 확인해 주세요.";
      }
    },
    [setTurns],
  );

  const decide = useCallback(
    async (proposalId: string, decision: Decision, edits?: Record<string, string>, reason?: string) => {
      setError(null);
      const secret = proposals.find((p) => p.id === proposalId)?.card.fields?.some(isPrivateField);
      const posted = await decideProposal(proposalId, decision, edits, reason, secret);
      if (!posted.ok) {
        setError(posted.error);
        void hydrate();
        return posted.error;
      }
      if (posted.already) {
        // Decided before this reached it: the card as it now stands.
        void hydrate();
        return null;
      }
      // The card's notification is marked read behind this (output-svc on
      // the agent's event); the inbox badge is asked to look again.
      window.setTimeout(() => window.dispatchEvent(new Event("jarvis:decided")), 1500);
      // The card comes back decided over the stream within a moment. Still
      // waiting by then is a stream gone quiet: the thread is read again, as
      // a sent message's missing echo is (SEND_ECHO_MS).
      window.setTimeout(() => {
        if (cached?.proposals.find((p) => p.id === proposalId)?.state === "pending") void hydrate();
      }, DECIDE_ECHO_MS);
      return null;
    },
    [hydrate, proposals],
  );

  /**
   * Ticks (or unticks) a checklist item: at once on screen, then saved. A
   * save that fails puts it back and resolves to what to say.
   */
  const tick = useCallback(
    async (listId: string, itemId: string, done: boolean): Promise<string | null> => {
      const before = cachedLists[listId];
      if (before) {
        setChecklists((prev) => ({
          ...prev,
          [listId]: { ...before, items: before.items.map((it) => (it.id === itemId ? { ...it, done } : it)) },
        }));
      }
      try {
        const res = await fetch(`${LISTS}/${encodeURIComponent(listId)}/items/${encodeURIComponent(itemId)}`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ done }),
        });
        if (res.ok) {
          const body = (await res.json().catch(() => null)) as { checklist?: Checklist } | null;
          if (body?.checklist?.id) setChecklists((prev) => ({ ...prev, [listId]: body.checklist! }));
          return null;
        }
      } catch {
        // put back below
      }
      if (before) setChecklists((prev) => ({ ...prev, [listId]: before }));
      return "저장하지 못했어요. 다시 눌러 주세요.";
    },
    [setChecklists],
  );

  /**
   * Takes an item off a list - one swiped away as not needed: off the screen
   * at once, then off the list in mind. A failure puts it back.
   */
  const dropItem = useCallback(
    async (listId: string, itemId: string): Promise<string | null> => {
      const before = cachedLists[listId];
      if (before) {
        setChecklists((prev) => ({ ...prev, [listId]: { ...before, items: before.items.filter((it) => it.id !== itemId) } }));
      }
      try {
        const res = await fetch(`${LISTS}/${encodeURIComponent(listId)}`, {
          method: "PATCH",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ remove: [itemId], by: "card" }),
        });
        if (res.ok) {
          const body = (await res.json().catch(() => null)) as { checklist?: Checklist } | null;
          if (body?.checklist?.id) setChecklists((prev) => ({ ...prev, [listId]: body.checklist! }));
          return null;
        }
      } catch {
        // put back below
      }
      if (before) setChecklists((prev) => ({ ...prev, [listId]: before }));
      return "빼지 못했어요. 다시 밀어 주세요.";
    },
    [setChecklists],
  );

  /** Closes a list: it leaves the 작업 tab and mind's open lists. */
  const closeList = useCallback(
    async (listId: string): Promise<string | null> => {
      try {
        const res = await fetch(`${LISTS}/${encodeURIComponent(listId)}`, { method: "DELETE" });
        if (res.ok) {
          const body = (await res.json().catch(() => null)) as { checklist?: Checklist } | null;
          if (body?.checklist?.id) setChecklists((prev) => ({ ...prev, [listId]: body.checklist! }));
          return null;
        }
      } catch {
        // said below
      }
      return "목록을 닫지 못했어요.";
    },
    [setChecklists],
  );

  /** The lists still open, read from mind into the map too (null when they could not be). */
  const loadOpenLists = useCallback(async (): Promise<Checklist[] | null> => {
    try {
      const res = await fetch(LISTS, { cache: "no-store" });
      if (!res.ok) return null;
      const open = ((await res.json()) as { checklists?: Checklist[] }).checklists ?? [];
      putLists(byList(open));
      return open;
    } catch {
      return null;
    }
  }, [putLists]);

  // The same map until a card changes: the transcript is memoized on it.
  const byId = useMemo(() => new Map(proposals.map((p) => [p.id, p])), [proposals]);

  return {
    turns,
    proposals,
    byId,
    checklists,
    tick,
    dropItem,
    closeList,
    loadOpenLists,
    waited: waited as ReadonlySet<string>,
    thinking,
    draft,
    steps,
    draftShown,
    queue,
    connection,
    error,
    hydrated,
    hasMore,
    loadingOlder,
    olderFailed,
    loadOlder,
    resync,
    send,
    steer,
    drop,
    stop,
    mark,
    decide,
  };
}
