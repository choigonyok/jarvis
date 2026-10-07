"use client";

import { krw } from "@/lib/portfolio";
import { type Book, dayLabel } from "@/lib/spending";
import { cn } from "@/lib/utils";

/**
 * The month as one line: how much of the money is gone, against how much of
 * the month is gone.
 *
 * Both are fractions of the same bar. The fill is spending against the
 * budget; the hairline is today's place in the month. Fill ahead of the
 * hairline is spending faster than the month is passing - read before any
 * number. Without a budget the bar measures against last month's whole total
 * instead, and says so.
 *
 * Under it, one column per day on the same width, so the day under the
 * hairline is today. A column is a filter for the list below it.
 */
export function Pace({
  book,
  selectedDay,
  onSelectDay,
  onEditBudget,
}: {
  book: Book;
  selectedDay: string | null;
  onSelectDay: (date: string | null) => void;
  onEditBudget: () => void;
}) {
  const reference = book.budgetKrw ?? (book.prev.totalKrw > 0 ? book.prev.totalKrw : null);
  const spent = Math.max(0, book.totalKrw);
  const fill = reference ? Math.min(1, spent / reference) : 0;
  const time = book.daysInMonth > 0 ? book.elapsedDays / book.daysInMonth : 0;
  const running = book.elapsedDays > 0 && book.elapsedDays < book.daysInMonth;

  return (
    <div>
      {reference ? (
        <>
          <div className="relative h-2.5 w-full overflow-hidden rounded-full bg-glass-raised">
            <div
              className="h-full rounded-full bg-foreground/80 transition-[width] duration-500 motion-reduce:transition-none"
              style={{ width: `${fill * 100}%` }}
            />
          </div>
          {/* The hairline sits outside the clipped track so it can stand
              taller than the bar it measures. */}
          <div className="relative h-0">
            {running ? (
              <span
                aria-hidden
                className="absolute -top-4 h-5.5 w-px bg-foreground"
                style={{ left: `calc(${time * 100}% - 0.5px)` }}
              />
            ) : null}
          </div>
          <PaceLine book={book} reference={reference} onEditBudget={onEditBudget} />
        </>
      ) : (
        <button
          type="button"
          onClick={onEditBudget}
          className="tap -mx-1 rounded-md px-1 text-left text-[12.5px] text-dim underline decoration-edge underline-offset-4 outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          한 달 예산을 정하면 쓰는 속도를 보여 드려요
        </button>
      )}

      <DayStrip book={book} selectedDay={selectedDay} onSelectDay={onSelectDay} />
    </div>
  );
}

function PaceLine({
  book,
  reference,
  onEditBudget,
}: {
  book: Book;
  reference: number;
  onEditBudget: () => void;
}) {
  const hasBudget = book.budgetKrw !== null;
  const running = book.elapsedDays > 0 && book.elapsedDays < book.daysInMonth;
  const left = reference - book.totalKrw;
  const daysLeft = book.daysInMonth - book.elapsedDays + 1;
  const expected = (reference * book.elapsedDays) / book.daysInMonth;
  const ahead = book.totalKrw - expected;

  let sentence: string;
  if (!hasBudget) {
    sentence = `지난달 전체 ${krw(reference)} 기준`;
  } else if (left < 0) {
    sentence = `예산보다 ${krw(-left)} 더 썼어요`;
  } else if (running) {
    sentence = `${krw(left)} 남았어요 · 하루 ${krw(left / daysLeft)}씩`;
  } else {
    sentence = `예산에서 ${krw(left)} 남겼어요`;
  }

  return (
    <div className="mt-3 flex items-start justify-between gap-3">
      <div className="min-w-0">
        <p className="text-[13px] text-foreground/90">{sentence}</p>
        {hasBudget && running && left >= 0 ? (
          <p className="mt-0.5 text-[11.5px] text-faint">
            {Math.abs(ahead) < reference * 0.02
              ? "달이 지나는 만큼 쓰고 있어요"
              : ahead > 0
                ? `오늘까지 적정선보다 ${krw(ahead)} 빨라요`
                : `오늘까지 적정선보다 ${krw(-ahead)} 여유 있어요`}
          </p>
        ) : null}
      </div>
      <button
        type="button"
        onClick={onEditBudget}
        className="tap -my-2 shrink-0 rounded-md px-1 text-[12px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        {hasBudget ? `예산 ${krw(reference)}` : "예산 정하기"}
      </button>
    </div>
  );
}

function DayStrip({
  book,
  selectedDay,
  onSelectDay,
}: {
  book: Book;
  selectedDay: string | null;
  onSelectDay: (date: string | null) => void;
}) {
  const max = Math.max(1, ...book.daily.map((d) => d.totalKrw));
  return (
    <div className="mt-5">
      <div
        role="group"
        aria-label="날짜별 지출. 누르면 그날 내역만 봅니다"
        className="flex h-16 items-end gap-[2px] sm:h-20 sm:gap-[3px]"
      >
        {book.daily.map((d, i) => {
          const future = d.date > book.today;
          const today = d.date === book.today;
          const selected = selectedDay === d.date;
          const h = d.totalKrw > 0 ? Math.max(0.06, d.totalKrw / max) : 0;
          return (
            <button
              key={d.date}
              type="button"
              disabled={future}
              onClick={() => onSelectDay(selected ? null : d.date)}
              aria-pressed={selected}
              aria-label={`${dayLabel(d.date)} ${d.count ? `${krw(d.totalKrw)} ${d.count}건` : "지출 없음"}`}
              className="group relative flex h-full min-w-0 flex-1 items-end rounded-[2px] outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:cursor-default"
            >
              <span
                className={cn(
                  "block w-full rounded-[2px] transition-colors",
                  h === 0 && (future ? "h-px bg-edge-soft" : "h-px bg-edge"),
                  h > 0 &&
                    (selected
                      ? "bg-foreground"
                      : selectedDay
                        ? "bg-foreground/20 group-hover:bg-foreground/40"
                        : today
                          ? "bg-foreground/85"
                          : "bg-foreground/40 group-hover:bg-foreground/60"),
                )}
                style={h > 0 ? { height: `${h * 100}%` } : undefined}
              />
              {/* Week starts, as a tick under the strip: enough to find a
                  date without a row of 31 numbers. */}
              {i === 0 || weekday(d.date) === 1 ? (
                <span
                  aria-hidden
                  className={cn(
                    "tnum absolute top-full mt-1 left-0 text-[10px] leading-none",
                    today || selected ? "text-dim" : "text-faint",
                  )}
                >
                  {Number(d.date.slice(8))}
                </span>
              ) : null}
            </button>
          );
        })}
      </div>
      <div className="h-4" />
    </div>
  );
}

function weekday(date: string): number {
  const [y, m, d] = date.split("-").map(Number);
  return new Date(y, m - 1, d).getDay();
}
