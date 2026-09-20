"use client";

import { useMemo, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { EventComposer } from "@/components/calendar/composer";
import { EventRow, GhostRow } from "@/components/calendar/event-row";
import { MonthGrid, type DayMark } from "@/components/calendar/month-grid";
import { Header } from "@/components/shell/header";
import { Button } from "@/components/ui/button";
import {
  cursorOf,
  isoDate,
  korDate,
  monthCells,
  sameMonth,
  shiftMonth,
  type Cursor,
} from "@/lib/month";
import { ghostsFor, type CreateGhost, type Ghost } from "@/lib/proposed";
import { useCalendar } from "@/lib/use-calendar";

/**
 * The month is the map; the selected day is the place. Tapping a date does
 * not open anything - the detail is always on screen under the grid, so the
 * calendar never costs a round trip to answer "what is on the 25th".
 */
export function Calendar() {
  const today = isoDate(new Date());
  const [selected, setSelected] = useState(today);
  // The month on screen follows the selected day - there is no second piece
  // of state that could disagree with it.
  const cursor: Cursor = useMemo(() => cursorOf(selected), [selected]);

  const { events, proposed, pendingCount, connection, error, save, remove, decide } =
    useCalendar(cursor);

  const ghosts = useMemo(() => ghostsFor(proposed, events), [proposed, events]);

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
        found = { events: 0, pending: false };
        out.set(date, found);
      }
      return found;
    };

    for (const event of events) mark(event.date).events += 1;
    for (const ghost of creates) mark(ghost.preview.date).pending = true;
    for (const [targetId, ghost] of attached) {
      const target = events.find((e) => e.id === targetId);
      if (target) mark(target.date).pending = true;
      if (ghost.op === "update") mark(ghost.preview.date).pending = true;
    }
    return out;
  }, [events, creates, attached]);

  const dayEvents = events.filter((e) => e.date === selected);
  const dayCreates = creates.filter((g) => g.preview.date === selected);

  // Anything waiting outside the six weeks on screen would otherwise be
  // invisible; the operator is told it exists and can jump to it.
  const visible = useMemo(() => new Set(monthCells(cursor).map((c) => c.date)), [cursor]);
  const elsewhere = creates.filter((g) => !visible.has(g.preview.date));

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
    <div className="flex h-full flex-col">
      <Header connection={connection} pending={pendingCount} />

      <main className="scrollbar-hairline relative flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-[34rem] px-5 pt-6 pb-6 sm:px-8">
          <div className="mb-4 flex items-center justify-between">
            <h2 className="text-[15px] font-medium tracking-tight text-foreground">
              {cursor.year}년 {cursor.month + 1}월
            </h2>
            <div className="flex items-center gap-1">
              {sameMonth(today, cursor) ? null : (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setSelected(today)}
                  className="h-7 px-2.5 text-[12.5px] text-dim hover:text-foreground"
                >
                  오늘
                </Button>
              )}
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => page(-1)}
                aria-label="이전 달"
                className="text-faint hover:text-foreground"
              >
                <ChevronLeft aria-hidden className="size-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => page(1)}
                aria-label="다음 달"
                className="text-faint hover:text-foreground"
              >
                <ChevronRight aria-hidden className="size-4" />
              </Button>
            </div>
          </div>

          <MonthGrid
            cursor={cursor}
            selected={selected}
            today={today}
            marks={marks}
            onSelect={setSelected}
          />

          {elsewhere.length > 0 ? (
            <p className="mt-4 flex items-center gap-2 text-[12.5px] text-dim">
              <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
              이 달 밖에 결재 대기 {elsewhere.length}건
              <Button
                variant="ghost"
                size="xs"
                onClick={() => setSelected(elsewhere[0].preview.date)}
                className="h-6 px-2 text-[12px] text-dim hover:text-foreground"
              >
                보기
              </Button>
            </p>
          ) : null}

          <section className="mt-7 border-t border-edge-soft pt-5" aria-live="polite">
            <h3 className="mb-3 px-2 text-[13px] text-dim">
              {korDate(selected)}
              {selected === today ? <span className="text-faint"> · 오늘</span> : null}
            </h3>

            {dayEvents.length === 0 && dayCreates.length === 0 ? (
              <p className="px-2 text-[13.5px] leading-relaxed text-faint">
                비어 있는 날입니다.
              </p>
            ) : null}

            <div className="space-y-1.5">
              {dayEvents.map((event) => {
                const ghost = attached.get(event.id);
                return (
                  <div key={event.id} className="space-y-1.5">
                    <EventRow
                      event={event}
                      pending={ghost}
                      onDelete={() => void remove(event.id)}
                    />
                    {ghost ? (
                      <GhostRow
                        ghost={ghost}
                        onDecide={(d) => void decide(ghost.proposal.id, d)}
                      />
                    ) : null}
                  </div>
                );
              })}
              {dayCreates.map((ghost) => (
                <GhostRow
                  key={ghost.proposal.id}
                  ghost={ghost}
                  onDecide={(d) => void decide(ghost.proposal.id, d)}
                />
              ))}
            </div>
          </section>

          {error ? (
            <p
              role="status"
              className="mt-6 border-l-2 border-reject pl-3 text-[13.5px] leading-relaxed text-dim"
            >
              {error}
            </p>
          ) : null}
        </div>
      </main>

      <footer className="relative shrink-0">
        {/* The month dissolves into the composer instead of stopping at a rule. */}
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 -top-10 h-10 bg-gradient-to-b from-transparent to-background"
        />
        <div className="mx-auto w-full max-w-[34rem] px-5 pb-6 sm:px-8">
          <EventComposer date={selected} onDateChange={setSelected} onSave={save} />
        </div>
      </footer>
    </div>
  );
}
