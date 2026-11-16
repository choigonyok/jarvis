"use client";

import { useRef, type KeyboardEvent } from "react";
import { RowBand } from "@/components/sleep/band";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  type Night,
  type Window,
  clockAt,
  clockRange,
  clockSinceEvening,
  daysBack,
  duration,
  hourTicks,
  shortDuration,
  span,
} from "@/lib/sleep";
import { cn } from "@/lib/utils";
import { dateLabel } from "@/lib/when";

/** The diary's lengths: a week reads row by row, a month as a field. */
export type Days = "7" | "30";

const WEEK = ["일", "월", "화", "수", "목", "금", "토"];

/**
 * The last week or month: what the nights came to - their mean length, the
 * usual bedtime and waking and how far a night strays from them - over the
 * diary itself.
 */
export function Recent({
  nights,
  last,
  days,
  onDays,
  summary,
  shown,
  onShow,
}: {
  nights: Night[];
  /** The newest day there is a row for: today. */
  last: string;
  days: Days;
  onDays: (d: Days) => void;
  summary: Window;
  /** The night the top of the screen is reading. */
  shown: string;
  onShow: (night: string) => void;
}) {
  const count = Number(days);
  return (
    <section aria-labelledby="sleep-recent">
      <div className="flex items-center justify-between gap-3">
        <h2 id="sleep-recent" className="text-title font-medium text-foreground">
          최근
        </h2>
        <ToggleGroup
          type="single"
          aria-label="기간"
          variant="segment"
          size="segment"
          spacing={0.5}
          value={days}
          onValueChange={(v) => {
            if (v) onDays(v as Days);
          }}
          className="rounded-lg border border-edge-soft bg-glass p-0.5"
        >
          <ToggleGroupItem value="7" className="min-h-9 mouse:min-h-0 mouse:px-2.5 mouse:py-1">
            7일
          </ToggleGroupItem>
          <ToggleGroupItem value="30" className="min-h-9 mouse:min-h-0 mouse:px-2.5 mouse:py-1">
            30일
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      {summary.nights > 0 ? (
        <dl className="mt-4 grid grid-cols-3 gap-3">
          <Figure label="평균 수면" value={duration(summary.asleepMinutes ?? 0)} />
          <Figure label="잠자리" value={summary.bedAt ?? "—"} spread={summary.bedSpreadMinutes} />
          <Figure label="기상" value={summary.wakeAt ?? "—"} spread={summary.wakeSpreadMinutes} />
        </dl>
      ) : (
        <p className="mt-4 text-ui text-dim">이 {count}일에는 기록이 없어요.</p>
      )}
      {summary.nights > 0 && summary.nights < count ? (
        <p className="mt-2 text-meta text-faint">
          {count}일 중 {summary.nights}일 기록
        </p>
      ) : null}

      <Diary nights={nights} last={last} count={count} summary={summary} shown={shown} onShow={onShow} className="mt-6" />
    </section>
  );
}

/** A figure over its name, as a list of terms reads; how far nights stray beside the clock ones. */
function Figure({ label, value, spread }: { label: string; value: string; spread?: number }) {
  return (
    <div className="flex min-w-0 flex-col-reverse">
      <dt className="mt-1.5 text-meta text-faint">{label}</dt>
      <dd className="tnum text-title leading-none font-medium whitespace-nowrap text-foreground">
        {value}
        {spread !== undefined && value !== "—" ? (
          <span className="ms-1 text-aside font-normal text-dim" aria-label={`, 밤마다 평균 ${spread}분 차이`}>
            ±{spread}분
          </span>
        ) : null}
      </dd>
    </div>
  );
}

/**
 * A sleep diary, the paper kind a sleep clinic hands out: a row a day, newest
 * at the top, the clock running left to right from the evening, each night a
 * band from bed to waking with its depth as ink. Its length is the night's
 * length; where it sits is when; how ragged the left edges are is how
 * regular the bedtimes are - the faint stripes are the usual bedtime and
 * waking, as wide as nights stray from them. A night pressed is read at the
 * top of the screen; the arrow keys move between nights.
 */
function Diary({
  nights,
  last,
  count,
  summary,
  shown,
  onShow,
  className,
}: {
  nights: Night[];
  last: string;
  count: number;
  summary: Window;
  shown: string;
  onShow: (night: string) => void;
  className?: string;
}) {
  const byDay = new Map(nights.map((n) => [n.night, n]));
  const rows = daysBack(last, count).map((day) => ({ day, night: byDay.get(day) }));
  const inRows = rows.flatMap((r) => (r.night ? [r.night] : []));
  const range = clockRange(inRows);
  const width = range.to - range.from;
  const at = (minute: number) => `${((minute - range.from) / width) * 100}%`;
  const week = count <= 7;
  const group = useRef<HTMLDivElement>(null);
  // The one row the keyboard lands on: the night being read, else the newest.
  const focusable = inRows.some((n) => n.night === shown) ? shown : inRows[0]?.night;
  const anchor = Math.max(0, rows.findIndex((r) => r.day === shown));

  const zones = [
    summary.bedAt ? { at: clockSinceEvening(summary.bedAt), spread: summary.bedSpreadMinutes } : null,
    summary.wakeAt ? { at: clockSinceEvening(summary.wakeAt), spread: summary.wakeSpreadMinutes } : null,
  ].flatMap((z) => (z && z.at >= range.from && z.at <= range.to ? [z] : []));

  function key(e: KeyboardEvent<HTMLDivElement>) {
    const i = inRows.findIndex((n) => n.night === shown);
    const to =
      e.key === "ArrowDown" ? i + 1 : e.key === "ArrowUp" ? i - 1 : e.key === "Home" ? 0 : e.key === "End" ? inRows.length - 1 : null;
    if (to === null) return;
    e.preventDefault();
    const next = inRows[Math.min(inRows.length - 1, Math.max(0, to))];
    if (!next) return;
    onShow(next.night);
    group.current?.querySelector<HTMLButtonElement>(`[data-night="${next.night}"]`)?.focus();
  }

  return (
    <div className={className}>
      <div ref={group} role="group" aria-label={`최근 ${count}일의 밤`} onKeyDown={key} className="relative">
        {/* The clock's hours and the usual bedtime and waking, behind the rows. */}
        <div aria-hidden className="pointer-events-none absolute inset-y-0 right-14 left-[4.25rem]">
          {hourTicks(range).map((t) => (
            <span key={t.at} className="absolute inset-y-0 border-l border-edge-soft" style={{ left: at(t.at) }} />
          ))}
          {zones.map((z) => (
            <span key={z.at}>
              <span
                className="absolute inset-y-0 bg-foreground/[0.06]"
                style={{ left: at(z.at - z.spread), width: `${((2 * z.spread) / width) * 100}%` }}
              />
              <span className="absolute inset-y-0 border-l border-dashed border-dim/60" style={{ left: at(z.at) }} />
            </span>
          ))}
        </div>
        {rows.map(({ day, night }, i) => {
          const weekday = WEEK[new Date(`${day}T00:00:00Z`).getUTCDay()];
          // A week names every day. A month names its Mondays and the night
          // being read (or today) - not a Monday right beside that one, whose
          // name would run into it.
          const named = week || i === anchor || (weekday === "월" && Math.abs(i - anchor) > 1);
          const label = (
            <span className={cn("tnum truncate text-meta leading-none text-faint", !named && "invisible")}>
              {Number(day.slice(5, 7))}/{Number(day.slice(8, 10))} {weekday}
            </span>
          );
          const row = cn("relative grid w-full grid-cols-[3.5rem_1fr_2.75rem] items-center gap-3 rounded-md", week ? "h-9" : "h-3.5");
          if (!night) {
            return (
              <div key={day} aria-hidden className={row}>
                {label}
                <span />
                <span />
              </div>
            );
          }
          const s = span(night);
          const picked = night.night === shown;
          return (
            <button
              key={day}
              type="button"
              data-night={night.night}
              tabIndex={night.night === focusable ? 0 : -1}
              aria-pressed={picked}
              aria-label={[dateLabel(day), `${clockAt(night.bedAt)}부터 ${clockAt(night.wakeAt)}까지`, night.bedOnly ? "침대에" : "", duration(night.asleepMinutes)]
                .filter(Boolean)
                .join(" ")}
              onClick={() => onShow(night.night)}
              className={cn(row, "text-left transition-colors focus-ring", picked ? "bg-glass-raised" : "hover:bg-glass")}
            >
              {label}
              <span className="relative h-full">
                <span
                  className={cn("absolute top-1/2 -translate-y-1/2 overflow-hidden rounded-full bg-well", week ? "h-3" : "h-2")}
                  style={{ left: at(s.from), width: `${((s.to - s.from) / width) * 100}%` }}
                >
                  <RowBand night={night} />
                </span>
              </span>
              <span className={cn("tnum text-right text-meta leading-none", picked ? "text-foreground" : "text-dim", !week && !picked && "invisible")}>
                {shortDuration(night.asleepMinutes)}
              </span>
            </button>
          );
        })}
      </div>
      <div aria-hidden className="relative mt-1.5 mr-14 ml-[4.25rem] h-4">
        {hourTicks(range).map((t) => (
          <span key={t.at} className="tnum absolute top-0 -translate-x-1/2 text-meta text-faint" style={{ left: at(t.at) }}>
            {t.label}
          </span>
        ))}
      </div>
    </div>
  );
}
