"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useAgentStream } from "@/lib/stream";
import { gridBounds, shiftMonth, type Cursor } from "@/lib/month";
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
  // And the months either side whole: a swipe pulls the next page into view
  // before it is let go, and that page should already carry its dots.
  const { from, to } = useMemo(
    () => ({ from: gridBounds(shiftMonth(cursor, -1)).from, to: gridBounds(shiftMonth(cursor, 1)).to }),
    [cursor],
  );

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
  // The whole queue, not just its size: the standing bar names what is
  // waiting, and a count cannot be named.
  const waiting = useMemo(() => proposals.filter(isPending), [proposals]);
  const pendingCount = waiting.length;

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

  /** True when the entry is gone. */
  const remove = useCallback(async (id: string) => {
    setError(null);
    try {
      // Encoded: a repeating entry's day is "<series>#<date>", and an unencoded
      // # is where a URL's fragment starts - the request went out as the whole
      // series. keepalive: a removal's undo window can close as the page goes.
      const res = await fetch(`${API}/calendar/${encodeURIComponent(id)}`, { method: "DELETE", keepalive: true });
      if (!res.ok && res.status !== 204) {
        setError("일정을 지우지 못했습니다.");
        return false;
      }
      return true;
    } catch {
      setError("에이전트에 연결하지 못했습니다.");
      return false;
    }
  }, []);

  const decide = useCallback(
    async (proposalId: string, decision: Decision, reason?: string) => {
      const message = await decideProposal(proposalId, decision, undefined, reason);
      if (message) {
        setError(message);
        void hydrate();
      }
    },
    [hydrate],
  );

  return {
    events,
    proposed,
    waiting,
    pendingCount,
    connection,
    error,
    loaded,
    save,
    remove,
    decide,
  };
}
