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

export function useThread() {
  const [turns, setTurns] = useState<Turn[]>([]);
  const [proposals, setProposals] = useState<Proposal[]>([]);
  const [thinking, setThinking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(false);

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
  }, []);

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
  }, []);

  // Hydrate first, then follow. A reconnect re-hydrates so dropped frames heal.
  useEffect(() => {
    // Not a cascading render: hydrate only sets state once the fetch it
    // starts has resolved. The lint rule cannot see past the async call.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void hydrate();
  }, [hydrate]);

  const connection = useAgentStream(onEvent, () => void hydrate());

  const send = useCallback(async (text: string) => {
    setError(null);
    try {
      const res = await fetch(`${API}/messages`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ text }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        setError(body?.error ?? "메시지를 보내지 못했습니다.");
      }
    } catch {
      setError("에이전트에 연결하지 못했습니다.");
    }
  }, []);

  const decide = useCallback(
    async (proposalId: string, decision: Decision) => {
      setError(null);
      const message = await decideProposal(proposalId, decision);
      if (message) {
        setError(message);
        void hydrate();
      }
    },
    [hydrate],
  );

  const byId = new Map(proposals.map((p) => [p.id, p]));

  return { turns, proposals, byId, thinking, connection, error, hydrated, send, decide };
}
