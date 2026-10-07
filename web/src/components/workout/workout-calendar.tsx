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
import { clock, durationOf, type Session } from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * The month, as training days only.
 *
 * Deliberately not the calendar tab. That one holds appointments - things you
 * have agreed to be somewhere for - and a workout is not an appointment; it
 * is something that either happened or did not. Mixing them would make the
 * calendar half diary and half plan, and neither half readable.
 *
 * So a day here is empty or it is not. A day that was trained carries the
 * routine's name and how long it took, which together answer the two
 * questions you ask of a training month: what am I doing, and am I doing it.
 */
export function WorkoutCalendar({ sessions }: { sessions: Session[] }) {
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
        <h2 className="text-[13px] text-dim">
          {cursor.year}년 {cursor.month + 1}월
          <span className="ms-2 tnum text-[11.5px] text-faint">{trained}일</span>
        </h2>
        <div className="flex items-center gap-1">
          {sameMonth(today, cursor) ? null : (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setCursor(cursorOf(today))}
              className="h-9 px-2.5 text-[12.5px] text-dim hover:text-foreground sm:h-7"
            >
              이번 달
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setCursor(shiftMonth(cursor, -1))}
            aria-label="이전 달"
            className="tap text-faint hover:text-foreground sm:size-7 sm:min-h-0 sm:min-w-0"
          >
            <ChevronLeft aria-hidden className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setCursor(shiftMonth(cursor, 1))}
            aria-label="다음 달"
            className="tap text-faint hover:text-foreground sm:size-7 sm:min-h-0 sm:min-w-0"
          >
            <ChevronRight aria-hidden className="size-4" />
          </Button>
        </div>
      </div>

      <div className="mb-1 grid grid-cols-7 gap-px" aria-hidden>
        {WEEKDAYS.map((label) => (
          <div key={label} className="py-1 text-center text-[11px] text-faint">
            {label}
          </div>
        ))}
      </div>

      <div className="grid grid-cols-7 gap-px" role="grid" aria-label="운동한 날">
        {cells.map((cell) => {
          const done = byDate.get(cell.date);
          const isToday = cell.date === today;
          // A trained day is filled; an untrained one is just a number. No
          // dots, no legend - the month reads as a pattern of blocks, which
          // is what "am I keeping it up" actually looks like.
          return (
            <div
              key={cell.date}
              role="gridcell"
              aria-label={
                done
                  ? `${cell.date}, ${done.map((s) => s.routineName ?? "운동").join(", ")}`
                  : cell.date
              }
              className={cn(
                "flex h-12 flex-col items-center justify-center gap-0.5 rounded-lg sm:h-14",
                done ? "bg-approve/12" : cell.inMonth ? "bg-glass" : "",
              )}
            >
              <span
                className={cn(
                  "tnum text-[12px] leading-none",
                  !cell.inMonth && "text-faint/50",
                  cell.inMonth && (done ? "text-approve/90" : "text-faint"),
                  isToday && "font-semibold",
                )}
              >
                {cell.day}
              </span>
              {done ? (
                <span className="max-w-full truncate px-0.5 text-[9px] leading-none text-approve/70">
                  {done[0].routineName ?? "운동"}
                </span>
              ) : null}
            </div>
          );
        })}
      </div>

      {/* The list under the grid, because a 9px label in a cell can say which
          day it was but not how long it took. */}
      {trained > 0 ? (
        <ul className="mt-3 space-y-1">
          {cells
            .filter((c) => c.inMonth && byDate.has(c.date))
            .reverse()
            .map((cell) => {
              const done = byDate.get(cell.date) ?? [];
              const minutes = done.reduce((sum, s) => sum + durationOf(s), 0);
              return (
                <li
                  key={cell.date}
                  className="flex items-baseline gap-3 px-1 text-[12.5px]"
                >
                  <span className="tnum w-6 shrink-0 text-faint">{cell.day}</span>
                  <span className="min-w-0 flex-1 truncate text-dim">
                    {done.map((s) => s.routineName ?? "운동").join(", ")}
                  </span>
                  <span className="tnum shrink-0 text-faint">{clock(minutes)}</span>
                </li>
              );
            })}
        </ul>
      ) : null}
    </section>
  );
}
