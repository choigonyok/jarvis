"use client";

import { useCallback, useEffect, useState } from "react";
import { useAgentStream } from "@/lib/stream";
import {
  API,
  type AgentEvent,
  type Decision,
  type Proposal,
  type Turn,
} from "@/lib/thread";
import { decideProposal } from "@/lib/decide";

/** Upsert by id: an updated turn or proposal arrives the way the first did. */
function upsert<T extends { id: string }>(list: T[], next: T): T[] {
  const at = list.findIndex((item) => item.id === next.id);
  if (at === -1) return [...list, next];
  const copy = list.slice();
  copy[at] = next;
  return copy;
}

// The last thread this page saw. Coming back to a tab renders it at once -
// already scrolled to the newest turn - and the fetch then only corrects it.
let cached: { turns: Turn[]; proposals: Proposal[]; thinking: boolean } | null = null;

export function useThread() {
  const [turns, setTurnsState] = useState<Turn[]>(() => cached?.turns ?? []);
  const [proposals, setProposalsState] = useState<Proposal[]>(() => cached?.proposals ?? []);
  const [thinking, setThinking] = useState(() => cached?.thinking ?? false);
  const [error, setError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(() => cached !== null);

  const setTurns = useCallback((next: Turn[] | ((prev: Turn[]) => Turn[])) => {
    setTurnsState((prev) => {
      const value = typeof next === "function" ? next(prev) : next;
      cached = { turns: value, proposals: cached?.proposals ?? [], thinking: cached?.thinking ?? false };
      return value;
    });
  }, []);
  const setProposals = useCallback((next: Proposal[] | ((prev: Proposal[]) => Proposal[])) => {
    setProposalsState((prev) => {
      const value = typeof next === "function" ? next(prev) : next;
      cached = { turns: cached?.turns ?? [], proposals: value, thinking: cached?.thinking ?? false };
      return value;
    });
  }, []);

  const hydrate = useCallback(async () => {
    try {
      const res = await fetch(`${API}/thread`, { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const data = (await res.json()) as {
        turns: Turn[];
        thinking: boolean;
        proposals: Proposal[];
      };
      setTurns(data.turns ?? []);
      setProposals(data.proposals ?? []);
      setThinking(Boolean(data.thinking));
    } catch {
      setError("스레드를 불러오지 못했습니다.");
    } finally {
      setHydrated(true);
    }
  }, [setTurns, setProposals]);

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
      case "error":
        setError(event.message);
        setThinking(false);
        break;
    }
  }, [setTurns, setProposals]);

  // Hydrate first, then follow. A reconnect re-hydrates so dropped frames heal.
  useEffect(() => {
    // Not a cascading render: hydrate only sets state once the fetch it
    // starts has resolved. The lint rule cannot see past the async call.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void hydrate();
  }, [hydrate]);

  const connection = useAgentStream(onEvent, () => void hydrate());

  /** Resolves true once the agent took the message. */
  const send = useCallback(async (text: string, images?: string[]) => {
    setError(null);
    try {
      const res = await fetch(`${API}/messages`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(images?.length ? { text, images } : { text }),
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

  const decide = useCallback(
    async (proposalId: string, decision: Decision, edits?: Record<string, string>) => {
      setError(null);
      const message = await decideProposal(proposalId, decision, edits);
      if (message) {
        setError(message);
        void hydrate();
      }
      return message;
    },
    [hydrate],
  );

  const byId = new Map(proposals.map((p) => [p.id, p]));

  return { turns, proposals, byId, thinking, connection, error, hydrated, send, decide };
}
