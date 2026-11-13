"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useAgentStream } from "@/lib/stream";
import { gridBounds, shiftMonth, type Cursor } from "@/lib/month";
import { DECIDE_ECHO_MS, decideProposal } from "@/lib/decide";
import { MODULE, ofModule } from "@/lib/kinds";
import {
  API,
  isPending,
  type AgentEvent,
  type CalendarEvent,
  type Decision,
  type Proposal,
} from "@/lib/thread";

/** The calendar: read at its adapter, changed at the way in (the route says which). */
const CALENDAR = "/api/calendar";

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
        fetch(`${CALENDAR}/events?from=${from}&to=${to}`, { cache: "no-store" }),
        fetch(`${API}/proposals`, { cache: "no-store" }),
      ]);
      if (!calendar.ok || !pending.ok) throw new Error("bad status");
      const days = (await calendar.json()) as { events: CalendarEvent[] };
      const props = (await pending.json()) as { proposals: Proposal[] };
      setEvents((days.events ?? []).slice().sort(byStart));
      setProposals(props.proposals ?? []);
      setError(null);
    } catch {
      setError("캘린더를 불러오지 못했어요.");
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
    () => proposals.filter((p) => isPending(p) && ofModule(p.action.kind, MODULE.calendar)),
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
        // Only what is already on the calendar: a new one is create's.
        // Encoded: see remove.
        const res = await fetch(`${CALENDAR}/${encodeURIComponent(event.id ?? "")}`, {
          method: "PATCH",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(event),
        });
        if (!res.ok) {
          const body = (await res.json().catch(() => null)) as { error?: string } | null;
          setError(body?.error ?? "일정을 저장하지 못했어요.");
          return false;
        }
        return true;
      } catch {
        setError("에이전트에 연결하지 못했어요.");
        return false;
      }
    },
    [],
  );

  /**
   * A new entry a person filled in, in at the way in. Resolves to why it did
   * not go, or null; the stream brings it onto the month, and a read makes sure.
   */
  const create = useCallback(
    async (entry: Partial<CalendarEvent> & { repeat?: string; reminder?: number }): Promise<string | null> => {
      try {
        const res = await fetch(CALENDAR, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ ...entry, id: undefined }),
        });
        if (res.status === 202) return "받았지만 아직 반영되지 않았어요. 잠시 뒤 다시 보세요.";
        if (!res.ok) {
          const body = (await res.json().catch(() => null)) as { error?: string } | null;
          return body?.error ?? "일정을 저장하지 못했어요.";
        }
        void hydrate();
        return null;
      } catch {
        return "에이전트에 연결하지 못했어요.";
      }
    },
    [hydrate],
  );

  /** True when the entry is gone. */
  const remove = useCallback(async (id: string) => {
    setError(null);
    try {
      // Encoded: a repeating entry's day is "<series>#<date>", and an unencoded
      // # is where a URL's fragment starts - the request went out as the whole
      // series. keepalive: a removal's undo window can close as the page goes.
      const res = await fetch(`${CALENDAR}/${encodeURIComponent(id)}`, { method: "DELETE", keepalive: true });
      if (!res.ok && res.status !== 204) {
        setError("일정을 지우지 못했어요.");
        return false;
      }
      return true;
    } catch {
      setError("에이전트에 연결하지 못했어요.");
      return false;
    }
  }, []);

  // The cards as they stand now, for a check made a moment after deciding.
  const latest = useRef(proposals);
  useEffect(() => {
    latest.current = proposals;
  }, [proposals]);

  /** Resolves to what to say when the decision did not go; the row says it is on its way when it did. */
  const decide = useCallback(
    async (proposalId: string, decision: Decision, reason?: string): Promise<string | null> => {
      const posted = await decideProposal(proposalId, decision, undefined, reason);
      if (!posted.ok) {
        setError(posted.error);
        void hydrate();
        return posted.error;
      }
      if (posted.already) {
        void hydrate();
        return null;
      }
      // Back over the stream in a moment; still waiting then, read again.
      window.setTimeout(() => {
        if (latest.current.some((p) => p.id === proposalId && isPending(p))) void hydrate();
      }, DECIDE_ECHO_MS);
      return null;
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
    /** Reads the month again, by hand - under "불러오지 못했어요". */
    reload: hydrate,
    save,
    create,
    remove,
    decide,
  };
}
