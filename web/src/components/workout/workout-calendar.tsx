"use client";

import { useMemo, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  WEEKDAYS,
  cursorOf,
  isoDate,
  monthCells,
  sameMonth,
  shiftMonth,
  type Cursor,
} from "@/lib/month";
import { cellName, dayLabel, type Session } from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * The month, as training days only.
 *
 * Deliberately not the calendar tab. That one holds appointments - things you
 * have agreed to be somewhere for - and a workout is not an appointment; it
 * is something that either happened or did not. Mixing them would make the
 * calendar half diary and half plan, and neither half readable.
 *
 * So a day here is empty or it is not, and a trained day carries the
 * routine's name. Pressing a trained day narrows the log below to it, the
 * way the ledger's day bars narrow its list - the grid is how you find a
 * day, the log is how you read it.
 */
export function WorkoutCalendar({
  sessions,
  selected,
  onSelect,
}: {
  sessions: Session[];
  selected: string | null;
  onSelect: (date: string | null) => void;
}) {
  const today = isoDate(new Date());
  const [cursor, setCursor] = useState<Cursor>(() => cursorOf(today));

  const byDate = useMemo(() => {
    const out = new Map<string, Session[]>();
    for (const session of sessions) {
      if (!session.endedAt) continue;
      const list = out.get(session.date) ?? [];
      list.push(session);
      out.set(session.date, list);
    }
    return out;
  }, [sessions]);

  const cells = monthCells(cursor);
  const trained = cells.filter((c) => c.inMonth && byDate.has(c.date)).length;

  return (
    <section aria-label="운동 달력">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-ui text-dim">
          {cursor.year}년 {cursor.month + 1}월
          <span className="ms-2 tnum text-meta text-faint">{trained}일</span>
        </h2>
        <div className="flex items-center gap-1">
          {sameMonth(today, cursor) ? null : (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setCursor(cursorOf(today))}
              className="h-11 px-3 text-aside text-dim hover:text-foreground mouse:h-7"
            >
              이번 달
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setCursor(shiftMonth(cursor, -1))}
            aria-label="이전 달"
            className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
          >
            <ChevronLeft aria-hidden className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setCursor(shiftMonth(cursor, 1))}
            aria-label="다음 달"
            className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
          >
            <ChevronRight aria-hidden className="size-4" />
          </Button>
        </div>
      </div>

      <div className="mb-1 grid grid-cols-7 gap-px" aria-hidden>
        {WEEKDAYS.map((label) => (
          <div key={label} className="py-1 text-center text-meta text-faint">
            {label}
          </div>
        ))}
      </div>

      <div className="grid grid-cols-7 gap-px">
        {cells.map((cell) => {
          const done = byDate.get(cell.date);
          const isToday = cell.date === today;
          const chosen = selected === cell.date;
          // A day as the rest of the screen names one ("오늘", "11월 14일
          // (금)"), and what was trained: read out, the cells had been
          // "2026-11-14", or for a day without training only its number.
          const label = `${dayLabel(cell.date)}, ${
            done ? done.map((s) => s.routineName ?? "운동").join(", ") : "운동 없음"
          }`;
          // A trained day is filled; an untrained one is just a number. No
          // dots, no legend - the month reads as a pattern of blocks, which
          // is what "am I keeping it up" actually looks like.
          const body = (
            <>
              <span
                aria-hidden
                className={cn(
                  "tnum text-aside leading-none",
                  !cell.inMonth && "text-faint",
                  cell.inMonth && (done ? "text-approve" : "text-faint"),
                  isToday && "font-semibold underline decoration-2 underline-offset-[3px]",
                )}
              >
                {cell.day}
              </span>
              {done ? (
                <span aria-hidden className="flex max-w-full items-baseline px-0.5 text-meta leading-none text-approve">
                  <span className="truncate">{cellName(done[0])}</span>
                  {done.length > 1 ? <span className="shrink-0 text-tiny">+{done.length - 1}</span> : null}
                </span>
              ) : null}
            </>
          );
          const shape = cn(
            "flex h-12 flex-col items-center justify-center gap-0.5 rounded-lg sm:h-14",
            done ? "bg-approve-wash" : cell.inMonth ? "bg-glass" : "",
            chosen && "ring-2 ring-foreground/70",
          );
          return done && cell.inMonth ? (
            <button
              key={cell.date}
              type="button"
              aria-label={label}
              aria-pressed={chosen}
              onClick={() => onSelect(chosen ? null : cell.date)}
              className={cn(shape, "hover:bg-approve-wash focus-ring")}
            >
              {body}
            </button>
          ) : (
            // Words, not a label: a label on a plain box is not read.
            <div key={cell.date} className={shape}>
              <span className="sr-only">{label}</span>
              {body}
            </div>
          );
        })}
      </div>
    </section>
  );
}
