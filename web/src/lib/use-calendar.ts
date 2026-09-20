"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useAgentStream } from "@/lib/stream";
import { gridBounds, type Cursor } from "@/lib/month";
import { decideProposal } from "@/lib/decide";
import {
  API,
  isPending,
  type AgentEvent,
  type CalendarEvent,
  type Decision,
  type Proposal,
} from "@/lib/thread";

function byStart(a: CalendarEvent, b: CalendarEvent): number {
  if (a.date !== b.date) return a.date < b.date ? -1 : 1;
  // 시각 없는 종일 일정이 그날의 맨 위에 온다.
  if (a.start !== b.start) return (a.start || "").localeCompare(b.start || "");
  return a.id.localeCompare(b.id);
}

export function useCalendar(cursor: Cursor) {
  const [events, setEvents] = useState<CalendarEvent[]>([]);
  const [proposals, setProposals] = useState<Proposal[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);

  // The grid shows the tail of the previous month and the head of the next,
  // so the fetch covers every cell that can carry a dot - not just the month.
  const { from, to } = useMemo(() => gridBounds(cursor), [cursor]);

  const hydrate = useCallback(async () => {
    try {
      const [calendar, pending] = await Promise.all([
        fetch(`${API}/calendar?from=${from}&to=${to}`, { cache: "no-store" }),
        fetch(`${API}/proposals`, { cache: "no-store" }),
      ]);
      if (!calendar.ok || !pending.ok) throw new Error("bad status");
      const days = (await calendar.json()) as { events: CalendarEvent[] };
      const props = (await pending.json()) as { proposals: Proposal[] };
      setEvents((days.events ?? []).slice().sort(byStart));
      setProposals(props.proposals ?? []);
      setError(null);
    } catch {
      setError("캘린더를 불러오지 못했습니다.");
    } finally {
      setLoaded(true);
    }
  }, [from, to]);

  useEffect(() => {
    // Not a cascading render: hydrate only sets state once the fetch it
    // starts has resolved. The lint rule cannot see past the async call.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void hydrate();
  }, [hydrate]);

  const onEvent = useCallback(
    (event: AgentEvent) => {
      if (event.type === "proposal") {
        setProposals((prev) => {
          const at = prev.findIndex((p) => p.id === event.proposal.id);
          if (at === -1) return [...prev, event.proposal];
          const copy = prev.slice();
          copy[at] = event.proposal;
          return copy;
        });
        return;
      }
      if (event.type !== "calendar") return;

      const { op, event: changed } = event.calendar;
      setEvents((prev) => {
        const without = prev.filter((e) => e.id !== changed.id);
        // An event that moved out of the month on screen simply leaves it.
        if (op === "delete" || changed.date < from || changed.date > to) {
          return without;
        }
        return [...without, changed].sort(byStart);
      });
    },
    [from, to],
  );

  const connection = useAgentStream(onEvent, () => void hydrate());

  /** Calendar proposals still waiting on a decision, in the order raised. */
  const proposed = useMemo(
    () => proposals.filter((p) => isPending(p) && p.action.kind.startsWith("calendar.")),
    [proposals],
  );

  // The header's count covers every pending proposal, not only this module's:
  // one queue, one number, wherever the operator happens to be standing.
  const pendingCount = useMemo(() => proposals.filter(isPending).length, [proposals]);

  const save = useCallback(
    async (event: Partial<CalendarEvent>) => {
      setError(null);
      try {
        const res = await fetch(`${API}/calendar`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(event),
        });
        if (!res.ok) {
          const body = (await res.json().catch(() => null)) as { error?: string } | null;
          setError(body?.error ?? "일정을 저장하지 못했습니다.");
          return false;
        }
        return true;
      } catch {
        setError("에이전트에 연결하지 못했습니다.");
        return false;
      }
    },
    [],
  );

  const remove = useCallback(async (id: string) => {
    setError(null);
    try {
      const res = await fetch(`${API}/calendar/${id}`, { method: "DELETE" });
      if (!res.ok && res.status !== 204) {
        setError("일정을 지우지 못했습니다.");
      }
    } catch {
      setError("에이전트에 연결하지 못했습니다.");
    }
  }, []);

  const decide = useCallback(
    async (proposalId: string, decision: Decision) => {
      const message = await decideProposal(proposalId, decision);
      if (message) {
        setError(message);
        void hydrate();
      }
    },
    [hydrate],
  );

  return { events, proposed, pendingCount, connection, error, loaded, save, remove, decide };
}
