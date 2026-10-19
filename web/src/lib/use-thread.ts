"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useAgentStream } from "@/lib/stream";
import {
  API,
  type AgentEvent,
  type Decision,
  type Proposal,
  type Queued,
  type Turn,
} from "@/lib/thread";
import { decideProposal } from "@/lib/decide";

/**
 * Upsert by id: an updated turn or proposal arrives the way the first did.
 * One given a newer time (a job's card with its latest word) moves to where
 * that time puts it - the bottom - rather than being rewritten in place.
 */
function upsert<T extends { id: string; atIso?: string }>(list: T[], next: T): T[] {
  const at = list.findIndex((item) => item.id === next.id);
  if (at === -1) return [...list, next];
  const copy = list.slice();
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

/** How many turns a page of the thread holds: the newest on opening, the ones above on scrolling up. */
const PAGE = 60;

export function useThread() {
  const [turns, setTurnsState] = useState<Turn[]>(() => cached?.turns ?? []);
  // Whether the thread goes on above the oldest loaded turn, and whether
  // that page is on its way.
  const [hasMore, setHasMore] = useState(() => cached?.hasMore ?? true);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [proposals, setProposalsState] = useState<Proposal[]>(() => cached?.proposals ?? []);
  const [thinking, setThinking] = useState(() => cached?.thinking ?? false);
  const [error, setError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(() => cached !== null);
  const [queue, setQueueState] = useState<Queued[]>(() => cachedQueue);
  const setQueue = useCallback((next: Queued[]) => {
    cachedQueue = next;
    setQueueState(next);
  }, []);

  const setTurns = useCallback((next: Turn[] | ((prev: Turn[]) => Turn[])) => {
    setTurnsState((prev) => {
      const value = typeof next === "function" ? next(prev) : next;
      cached = { ...(cached ?? { proposals: [], thinking: false }), turns: value };
      return value;
    });
  }, []);
  const setProposals = useCallback((next: Proposal[] | ((prev: Proposal[]) => Proposal[])) => {
    setProposalsState((prev) => {
      const value = typeof next === "function" ? next(prev) : next;
      cached = { ...(cached ?? { turns: [], thinking: false }), proposals: value };
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
      };
      setTurns(data.turns ?? []);
      setMore(Boolean(data.hasMore));
      setProposals(data.proposals ?? []);
      setThinking(Boolean(data.thinking));
      setQueue(data.queue ?? []);
    } catch {
      setError("스레드를 불러오지 못했습니다.");
    } finally {
      setHydrated(true);
    }
  }, [setTurns, setMore, setProposals, setQueue]);

  // The page above the oldest turn on screen, put in front of it. Resolves
  // once the turns are in (the caller keeps the view where it was).
  const loadOlder = useCallback(async () => {
    const oldest = cached?.turns[0];
    if (!oldest?.atIso || loadingOlder) return;
    setLoadingOlder(true);
    try {
      const res = await fetch(`${API}/thread?limit=${PAGE}&before=${encodeURIComponent(oldest.atIso)}`, { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const data = (await res.json()) as { turns: Turn[]; hasMore?: boolean };
      const older = data.turns ?? [];
      setTurns((prev) => {
        const have = new Set(prev.map((t) => t.id));
        return [...older.filter((t) => !have.has(t.id)), ...prev];
      });
      setMore(Boolean(data.hasMore));
    } catch {
      setError("이전 대화를 불러오지 못했습니다.");
    } finally {
      setLoadingOlder(false);
    }
  }, [loadingOlder, setTurns, setMore]);

  const onEvent = useCallback((event: AgentEvent) => {
    switch (event.type) {
      case "turn":
        setTurns((prev) => upsert(prev, event.turn));
        break;
      case "proposal":
        setProposals((prev) => upsert(prev, event.proposal));
        break;
      case "status":
        setThinking(Boolean(event.thinking));
        break;
      case "queue":
        setQueue(event.queue ?? []);
        break;
      case "error":
        setError(event.message);
        setThinking(false);
        break;
    }
  }, [setTurns, setProposals, setQueue]);

  // Hydrate first, then follow. A reconnect re-hydrates so dropped frames heal.
  useEffect(() => {
    // Not a cascading render: hydrate only sets state once the fetch it
    // starts has resolved. The lint rule cannot see past the async call.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void hydrate();
  }, [hydrate]);

  const connection = useAgentStream(onEvent, () => void hydrate());

  /** Resolves true once the agent took the message - to answer now, or queued behind the running turn. */
  const send = useCallback(async (text: string, images?: string[], refId?: string) => {
    setError(null);
    try {
      const res = await fetch(`${API}/messages`, {
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
        setError(body?.error ?? "메시지를 보내지 못했습니다.");
        return false;
      }
      return true;
    } catch {
      setError("에이전트에 연결하지 못했습니다.");
      return false;
    }
  }, []);

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
        setError(body?.error ?? "대기 중인 메시지를 바꾸지 못했습니다.");
        void hydrate();
      }
    } catch {
      setError("에이전트에 연결하지 못했습니다.");
    }
  }, [hydrate]);

  const decide = useCallback(
    async (proposalId: string, decision: Decision, edits?: Record<string, string>, reason?: string) => {
      setError(null);
      const message = await decideProposal(proposalId, decision, edits, reason);
      if (message) {
        setError(message);
        void hydrate();
      } else {
        // The card's notification is marked read behind this (notify-svc on
        // the agent's event); the inbox badge is asked to look again.
        window.setTimeout(() => window.dispatchEvent(new Event("jarvis:decided")), 1500);
      }
      return message;
    },
    [hydrate],
  );

  // The same map until a card changes: the transcript is memoized on it.
  const byId = useMemo(() => new Map(proposals.map((p) => [p.id, p])), [proposals]);

  return {
    turns,
    proposals,
    byId,
    thinking,
    queue,
    connection,
    error,
    hydrated,
    hasMore,
    loadingOlder,
    loadOlder,
    send,
    steer: (id: string) => onQueued(id, "steer"),
    drop: (id: string) => onQueued(id, "drop"),
    decide,
  };
}
