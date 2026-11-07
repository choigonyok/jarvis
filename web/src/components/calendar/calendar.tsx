"use client";

import { useKept } from "@/lib/keep";
import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { EventRow, GhostRow } from "@/components/calendar/event-row";
import { EventSheet } from "@/components/calendar/event-sheet";
import { type DayMark } from "@/components/calendar/month-grid";
import { MonthSwipe, type MonthSwipeHandle } from "@/components/calendar/month-swipe";
import { OWNER_LABEL, OWNERS, OwnerDot, ownerOf, type Owner } from "@/components/calendar/owner";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { useUndo } from "@/components/shell/undo";
import { Button } from "@/components/ui/button";
import {
  addDays,
  cursorOf,
  isoDate,
  korDate,
  monthCells,
  sameMonth,
  shiftMonth,
  type Cursor,
} from "@/lib/month";
import { onOpenParams } from "@/lib/focus";
import { ghostsFor, type CreateGhost, type Ghost } from "@/lib/proposed";
import { covers, type CalendarEvent } from "@/lib/thread";
import { useCalendar } from "@/lib/use-calendar";
import { cn } from "@/lib/utils";


/**
 * The month is the map; the selected day is the place. Tapping a date does
 * not open anything - the detail is always on screen under the grid, so the
 * calendar never costs a round trip to answer "what is on the 25th".
 */
export function Calendar() {
  const today = isoDate(new Date());
  // The day picked stays picked across a tab switch (lib/keep.ts).
  const [selected, setSelected] = useKept("calendar:selected", today);
  // A notification about an entry opens its day (?date=, lib/focus.ts); the
  // header then brings the entry itself into view.
  useEffect(() => {
    const open = (params: URLSearchParams) => {
      const date = params.get("date");
      if (date && /^\d{4}-\d{2}-\d{2}$/.test(date)) setSelected(date);
    };
    open(new URLSearchParams(window.location.search));
    return onOpenParams(open);
  }, [setSelected]);
  // The month on screen follows the selected day - there is no second piece
  // of state that could disagree with it.
  const cursor: Cursor = useMemo(() => cursorOf(selected), [selected]);

  // Whose entries are on screen. All three by default; tapping a legend chip
  // narrows the month to one person's days and back.
  const [hidden, setHidden] = useState<ReadonlySet<Owner>>(new Set());

  const { events: fetched, proposed, waiting, pendingCount, connection, error, loaded, reload, save, remove, decide } =
    useCalendar(cursor);

  // One of your own entries being changed in place (event-sheet.tsx). The
  // partner's are not: changing someone else's day is a conversation. Nor
  // one day of a repeating entry: the shared calendar takes that only in its
  // own app (calendar-svc refuses it).
  const [editing, setEditing] = useState<CalendarEvent | null>(null);
  const editable = (e: CalendarEvent) => ownerOf(e) !== "partner" && !e.id.includes("#");

  // A removal waits five seconds with 되돌리기 on screen before it is sent:
  // the row is gone at once, and an undo means the shared calendar never
  // heard of it (components/shell/undo.tsx).
  const { offer } = useUndo();
  const [leaving, setLeaving] = useState<ReadonlySet<string>>(new Set());
  const allEvents = useMemo(() => fetched.filter((e) => !leaving.has(e.id)), [fetched, leaving]);
  function erase(event: CalendarEvent) {
    const keep = (on: boolean) =>
      setLeaving((prev) => {
        const next = new Set(prev);
        if (on) next.add(event.id);
        else next.delete(event.id);
        return next;
      });
    keep(true);
    offer(
      event.recurrence ? `${event.title} - 반복 중 이 날만 지웠어요` : `${event.title} 일정을 지웠어요`,
      () => keep(false),
      () => void remove(event.id).then((gone) => !gone && keep(false)),
    );
  }

  const events = useMemo(
    () => allEvents.filter((e) => !hidden.has(ownerOf(e))),
    [allEvents, hidden],
  );

  // Counted before the filter, so a hidden chip still says what it hides.
  const monthCounts = useMemo(() => {
    const counts = new Map<Owner, number>();
    for (const e of allEvents) {
      if (!sameMonth(e.date, cursor)) continue;
      counts.set(ownerOf(e), (counts.get(ownerOf(e)) ?? 0) + 1);
    }
    return counts;
  }, [allEvents, cursor]);

  function toggle(owner: Owner) {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(owner)) next.delete(owner);
      else next.add(owner);
      return next;
    });
  }

  // Ghosts look at every event: a proposal to change a hidden entry must not
  // lose its target and reappear as something new.
  const ghosts = useMemo(() => ghostsFor(proposed, allEvents), [proposed, allEvents]);

  // An edit or a removal belongs on the event it would change; only a new
  // event needs a place of its own in the day it would land in.
  const { attached, creates } = useMemo(() => {
    const onEvents = new Map<string, Ghost>();
    const standalone: CreateGhost[] = [];
    for (const ghost of ghosts) {
      if (ghost.op === "create") standalone.push(ghost);
      else onEvents.set(ghost.targetId, ghost);
    }
    return { attached: onEvents, creates: standalone };
  }, [ghosts]);

  // One pass builds every cell's dots: what is settled, and what is waiting.
  const marks = useMemo(() => {
    const out = new Map<string, DayMark>();
    const mark = (date: string): DayMark => {
      let found = out.get(date);
      if (!found) {
        found = { owners: [], pending: false };
        out.set(date, found);
      }
      return found;
    };

    // A trip from the 3rd to the 5th is a dot on all three days, or the 4th
    // would read as free.
    for (const event of events) {
      const owner = ownerOf(event);
      for (let d = event.date; d <= (event.endDate || event.date); d = addDays(d, 1)) {
        mark(d).owners.push(owner);
      }
    }
    // Same order in every cell, so a row of days reads as a pattern.
    for (const m of out.values()) m.owners.sort((a, b) => OWNERS.indexOf(a) - OWNERS.indexOf(b));
    for (const ghost of creates) mark(ghost.preview.date).pending = true;
    for (const [targetId, ghost] of attached) {
      const target = allEvents.find((e) => e.id === targetId);
      if (target) mark(target.date).pending = true;
      if (ghost.op === "update") mark(ghost.preview.date).pending = true;
    }
    return out;
  }, [events, allEvents, creates, attached]);

  const dayEvents = events.filter((e) => covers(e, selected));
  const dayCreates = creates.filter((g) => g.preview.date === selected);

  // Anything waiting outside the six weeks on screen would otherwise be
  // invisible; the operator is told it exists and can jump to it.
  const visible = useMemo(() => new Set(monthCells(cursor).map((c) => c.date)), [cursor]);
  const elsewhere = creates.filter((g) => !visible.has(g.preview.date));

  // The month's page: turned by a swipe, a trackpad, the arrows or PageUp/PageDown.
  const swipe = useRef<MonthSwipeHandle>(null);

  // Paging months keeps a selection: the same weekday-ish slot, clamped.
  function page(by: number) {
    const next = shiftMonth(cursor, by);
    const day = Number(selected.slice(8, 10));
    const lastDay = new Date(next.year, next.month + 1, 0).getDate();
    setSelected(
      isoDate(new Date(next.year, next.month, Math.min(day, lastDay))),
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={pendingCount} />
        <StandingBar pending={waiting} />
      </PageTop>

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        {/* The month and the day it selects are one view, so on a screen wide
            enough to hold both side by side they stop taking turns down the
            page: the grid keeps its place while the day's detail changes
            beside it. Below that width the detail follows underneath, which is
            what a phone can show honestly. */}
        <div className="mx-auto grid w-full max-w-page-narrow grid-cols-1 gap-x-10 px-4 pt-5 pb-12 sm:px-8 sm:pt-6 sm:pb-6 lg:max-w-page-wide lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
          <div>
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-title font-medium tracking-tight text-foreground">
                {cursor.year}년 {cursor.month + 1}월
              </h2>
              <div className="flex items-center gap-1">
                {sameMonth(today, cursor) ? null : (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setSelected(today)}
                    className="h-10 px-3 text-ui text-dim hover:text-foreground mouse:h-7 mouse:px-2.5 mouse:text-aside"
                  >
                    오늘
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => (swipe.current ? swipe.current.go(-1) : page(-1))}
                  aria-label="이전 달"
                  className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
                >
                  <ChevronLeft aria-hidden className="size-5 mouse:size-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => (swipe.current ? swipe.current.go(1) : page(1))}
                  aria-label="다음 달"
                  className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
                >
                  <ChevronRight aria-hidden className="size-5 mouse:size-4" />
                </Button>
              </div>
            </div>

            <MonthSwipe
              ref={swipe}
              cursor={cursor}
              selected={selected}
              today={today}
              marks={marks}
              onSelect={setSelected}
              onPage={page}
            />

            {/* The legend is also the filter: the key to the colors is the
                place you would reach for to see only one person's days. */}
            <div className="mt-3 flex flex-wrap gap-1.5" role="group" aria-label="누구의 일정을 볼지">
              {OWNERS.map((owner) => {
                const on = !hidden.has(owner);
                const count = monthCounts.get(owner) ?? 0;
                return (
                  <button
                    key={owner}
                    type="button"
                    aria-pressed={on}
                    onClick={() => toggle(owner)}
                    className={cn(
                      "tap-y flex h-9 items-center gap-2 rounded-full border px-3 text-aside transition-colors outline-none mouse:h-7 mouse:px-2.5",
                      "focus-ring",
                      on
                        ? "border-edge bg-glass text-ink hover:bg-glass-raised"
                        : "border-edge-soft text-faint hover:text-dim",
                    )}
                  >
                    <OwnerDot owner={owner} className={cn(!on && "opacity-30")} />
                    {OWNER_LABEL[owner]}
                    <span className="tnum text-faint">{count}</span>
                  </button>
                );
              })}
            </div>

            {elsewhere.length > 0 ? (
              <p className="mt-4 flex items-center gap-2 text-aside text-dim">
                <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
                이 달 밖에 결재 대기 {elsewhere.length}건
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => setSelected(elsewhere[0].preview.date)}
                  className="h-8 px-2.5 text-aside text-dim hover:text-foreground mouse:h-6 mouse:px-2"
                >
                  보기
                </Button>
              </p>
            ) : null}
          </div>

          <section
            className="mt-7 border-t border-edge-soft pt-5 lg:mt-0 lg:border-t-0 lg:border-l lg:border-edge-soft lg:pt-0 lg:pl-10"
            aria-live="polite"
            data-focus="day"
          >
            {/* A row is a time, a title and a way to remove it. Let the column
                run to 1000px and those three sit a screen apart. */}
            <div className="lg:max-w-[26rem]">
            <h3 className="mb-3 px-2 text-ui text-dim">
              {korDate(selected)}
              {selected === today ? <span className="text-faint"> · 오늘</span> : null}
            </h3>

            {dayEvents.length === 0 && dayCreates.length === 0 ? (
              <p className="px-2 text-ui leading-relaxed text-faint">
                {/* The calendar is written by the assistant now, so an empty
                    day is an instruction rather than a dead end. */}
                비어 있는 날이에요. 일정은 대화로 Jarvis에게 맡기세요.
              </p>
            ) : null}

            <div className="space-y-1.5">
              {dayEvents.map((event) => {
                const ghost = attached.get(event.id);
                return (
                  // A repeating entry's days are "<series>#<date>"; a
                  // notification names the series.
                  <div key={event.id} className="space-y-1.5" data-focus={`event:${event.id.split("#")[0]}`}>
                    <EventRow
                      event={event}
                      pending={ghost}
                      onEdit={editable(event) && !ghost ? () => setEditing(event) : undefined}
                      onDelete={() => erase(event)}
                    />
                    {ghost ? (
                      <GhostRow
                        ghost={ghost}
                        onDecide={(d, reason) => decide(ghost.proposal.id, d, reason)}
                      />
                    ) : null}
                  </div>
                );
              })}
              {dayCreates.map((ghost) => (
                <GhostRow
                  key={ghost.proposal.id}
                  ghost={ghost}
                  onDecide={(d, reason) => decide(ghost.proposal.id, d, reason)}
                />
              ))}
            </div>
            </div>
          </section>

          {error ? (
            // The month that could not be read, read again on the spot - the
            // way back was leaving the tab.
            <Failed onRetry={loaded ? reload : undefined} className="mt-6 lg:col-span-2">
              {error}
            </Failed>
          ) : null}
        </div>

      </main>

      <EventSheet
        event={editing}
        onOpenChange={(open) => {
          if (!open) setEditing(null);
        }}
        // The whole entry with what was changed: the calendar takes an edit as
        // the entry it is to be, and what is left out would be dropped. The
        // place is in the memo's first line now (the sheet put it there).
        onSave={(patch) => (editing ? save({ ...editing, ...patch, place: undefined }) : Promise.resolve(false))}
      />

      <TabBar pending={pendingCount} />
    </div>
  );
}
