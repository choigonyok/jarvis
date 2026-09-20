"use client";

import { useEffect, useRef } from "react";
import { addDays, monthCells, WEEKDAYS, type Cursor } from "@/lib/month";
import { cn } from "@/lib/utils";

export type DayMark = {
  /** 확정된 일정 수. 점의 개수를 정한다. */
  events: number;
  /** 결재를 기다리는 제안이 있는 날. */
  pending: boolean;
};

/**
 * A whole month at once, the way a pocket calendar shows it: a number, and
 * underneath it the evidence that the day has something in it. The detail
 * lives below the grid, not inside the cells - a 7x6 grid has no room to say
 * anything true about an event, and a title chopped to four characters is
 * worse than a dot.
 */
export function MonthGrid({
  cursor,
  selected,
  today,
  marks,
  onSelect,
}: {
  cursor: Cursor;
  selected: string;
  today: string;
  marks: Map<string, DayMark>;
  onSelect: (date: string) => void;
}) {
  const cells = monthCells(cursor);
  const gridRef = useRef<HTMLDivElement>(null);
  const moveByKey = useRef(false);

  // Keyboard selection moves focus with it; a pointer selection must not, or
  // the page would yank focus around while someone is reading.
  useEffect(() => {
    if (!moveByKey.current) return;
    moveByKey.current = false;
    gridRef.current
      ?.querySelector<HTMLButtonElement>(`[data-date="${selected}"]`)
      ?.focus();
  }, [selected]);

  function onKeyDown(e: React.KeyboardEvent) {
    const steps: Record<string, number> = {
      ArrowLeft: -1,
      ArrowRight: 1,
      ArrowUp: -7,
      ArrowDown: 7,
    };
    const step = steps[e.key];
    if (step === undefined) return;
    e.preventDefault();
    moveByKey.current = true;
    onSelect(addDays(selected, step));
  }

  return (
    <div>
      <div
        className="mb-1.5 grid grid-cols-7 gap-px"
        aria-hidden
      >
        {WEEKDAYS.map((label) => (
          <div key={label} className="py-1 text-center text-[11px] text-faint">
            {label}
          </div>
        ))}
      </div>

      <div
        ref={gridRef}
        role="grid"
        aria-label="월 달력"
        onKeyDown={onKeyDown}
        className="grid grid-cols-7 gap-px"
      >
        {cells.map((cell) => {
          const mark = marks.get(cell.date);
          const isSelected = cell.date === selected;
          const isToday = cell.date === today;

          return (
            <button
              key={cell.date}
              type="button"
              role="gridcell"
              data-date={cell.date}
              tabIndex={isSelected ? 0 : -1}
              aria-selected={isSelected}
              aria-current={isToday ? "date" : undefined}
              aria-label={ariaLabel(cell.date, cell.day, cell.weekday, mark)}
              onClick={() => onSelect(cell.date)}
              className={cn(
                "flex h-12 flex-col items-center justify-center gap-1 rounded-lg transition-colors outline-none sm:h-14",
                "focus-visible:ring-3 focus-visible:ring-ring/50",
                isSelected ? "bg-glass-raised" : "hover:bg-glass",
              )}
            >
              <span
                className={cn(
                  "tnum text-[13px] leading-none",
                  !cell.inMonth && "text-faint/60",
                  cell.inMonth && (isSelected || isToday ? "text-foreground" : "text-dim"),
                  isToday && "font-semibold",
                )}
              >
                {cell.day}
              </span>

              {/* 한 줄뿐인 신호: 점이 있으면 뭔가 있는 날, 숨쉬면 결재 대기. */}
              <span aria-hidden className="flex h-[5px] items-center gap-[3px]">
                {mark?.pending ? (
                  <span className="anim-breathe size-[5px] rounded-full border border-dim" />
                ) : null}
                {Array.from({ length: Math.min(mark?.events ?? 0, 3) }).map((_, i) => (
                  <span
                    key={i}
                    className={cn(
                      "size-[3px] rounded-full",
                      cell.inMonth ? "bg-dim" : "bg-faint/60",
                    )}
                  />
                ))}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function ariaLabel(date: string, day: number, weekday: number, mark?: DayMark): string {
  const month = Number(date.slice(5, 7));
  const parts = [`${month}월 ${day}일 ${WEEKDAYS[weekday]}요일`];
  if (mark?.events) parts.push(`일정 ${mark.events}건`);
  if (mark?.pending) parts.push("결재 대기 있음");
  return parts.join(", ");
}
