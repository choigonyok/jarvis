"use client";

import { useState } from "react";
import { type Holding, type ReturnPoint, WINDOW_LABEL, percent, type Point, type Window } from "@/lib/portfolio";
import { cn } from "@/lib/utils";

/**
 * How the whole basket has moved, over time.
 *
 * Plotted as percent from the start of the window rather than in won, because
 * the question is "is it up" and a won axis makes that depend on how much you
 * happen to hold. The zero line is where the window began, so anything above
 * it is ahead of where you started and anything below is not - the same
 * baseline grammar the rest of this console uses.
 *
 * Inline SVG, no chart library: one line, one baseline and one label is less
 * code than configuring a library to draw them, and it inherits the palette
 * instead of fighting it.
 */
export function Curve({
  series,
  rates,
  holdings = [],
  holdingSeries = {},
  subject,
  onSubject,
}: {
  series: Record<Window, Point[]>;
  rates: Record<Window, number | null>;
  holdings?: Holding[];
  /** Per holding: its price return over each window. */
  holdingSeries?: Record<string, Record<Window, ReturnPoint[]>>;
  /** A holding id, or null for the whole basket. */
  subject: string | null;
  onSubject: (id: string | null) => void;
}) {
  const [window, setWindow] = useState<Window>("month");
  const held = subject ? holdings.find((h) => h.id === subject) : undefined;
  // One shape for both: dates and the return since the window began.
  const points: ReturnPoint[] = held
    ? (holdingSeries[held.id]?.[window] ?? [])
    : (() => {
        const ps = series[window] ?? [];
        const base = ps[0]?.valueKrw ?? 0;
        return base > 0 ? ps.map((p) => ({ date: p.date, rate: (p.valueKrw - base) / base })) : [];
      })();
  const rate = held ? (points.length >= 2 ? points[points.length - 1].rate : null) : rates[window];
  const charted = holdings.filter((h) => (holdingSeries[h.id]?.month?.length ?? 0) >= 2);

  return (
    <section aria-label={held ? `${held.name} 수익률 변화` : "총자산 변화"}>
      {charted.length > 0 ? (
        <div role="tablist" aria-label="그래프 대상" className="-ms-1 mb-3 flex flex-wrap gap-1">
          {[{ id: null as string | null, name: "전체" }, ...charted.map((h) => ({ id: h.id as string | null, name: h.name }))].map((t) => {
            const on = (t.id ?? null) === (held?.id ?? null);
            return (
              <button
                key={t.id ?? "all"}
                type="button"
                role="tab"
                aria-selected={on}
                onClick={() => onSubject(t.id)}
                className={cn(
                  "max-w-[10rem] truncate rounded-md px-2.5 py-1 text-[12px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  on ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim",
                )}
              >
                {t.name}
              </button>
            );
          })}
        </div>
      ) : null}
      <div className="mb-3 flex items-center justify-between gap-3">
        <p className="tnum text-[13px]">
          {rate === null ? (
            <span className="text-faint">—</span>
          ) : (
            <span className={cn(rate >= 0 ? "text-approve/85" : "text-reject/85")}>
              {percent(rate, 2)}
            </span>
          )}
          <span className="ms-2 text-faint">
            {held ? `${held.name} 수익률 · ` : ""}
            {WINDOW_LABEL[window]}
          </span>
        </p>

        <div
          role="radiogroup"
          aria-label="기간"
          className="inline-flex items-center gap-0.5 rounded-lg border border-edge-soft bg-glass p-0.5"
        >
          {(["day", "month", "year"] as Window[]).map((value) => {
            const active = window === value;
            return (
              <button
                key={value}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setWindow(value)}
                className={cn(
                  "min-h-8 rounded-md px-2.5 text-[12px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-0 sm:py-1",
                  active ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim",
                )}
              >
                {WINDOW_LABEL[value]}
              </button>
            );
          })}
        </div>
      </div>

      <Plot points={points} label={held ? `${held.name} 수익률` : "총자산 변화"} />
    </section>
  );
}

function Plot({ points, label }: { points: ReturnPoint[]; label: string }) {
  if (points.length < 2) {
    return (
      <div className="flex h-28 items-center rounded-lg border border-edge-soft bg-glass px-4">
        <p className="text-[12.5px] leading-relaxed text-faint">
          이 기간을 그릴 만큼의 시세 기록이 없습니다. 보유 종목 중 상장이 짧은
          것이 있으면 그 구간은 비어 있습니다.
        </p>
      </div>
    );
  }

  // Percent from the window's start: the line is about movement, not size.
  const values = points.map((p) => p.rate);
  const top = Math.max(...values, 0);
  const bottom = Math.min(...values, 0);
  // A flat month should not be magnified into a mountain range.
  const span = Math.max(top - bottom, 0.02);

  const W = 100;
  const H = 34;
  const x = (i: number) => (i / (points.length - 1)) * W;
  const y = (v: number) => H - ((v - bottom) / span) * H;

  const line = values.map((v, i) => `${i === 0 ? "M" : "L"}${x(i)},${y(v)}`).join(" ");
  const area = `${line} L${W},${H} L0,${H} Z`;
  const zero = y(0);
  const up = values[values.length - 1] >= 0;

  return (
    <div className="rounded-lg border border-edge-soft bg-glass p-3">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="h-28 w-full"
        role="img"
        aria-label={`${label} ${percent(values[values.length - 1], 2)}`}
      >
        <defs>
          <linearGradient id="curve-fill" x1="0" y1="0" x2="0" y2="1">
            <stop
              offset="0%"
              stopColor={up ? "oklch(0.755 0.153 150)" : "oklch(0.687 0.176 25)"}
              stopOpacity="0.22"
            />
            <stop
              offset="100%"
              stopColor={up ? "oklch(0.755 0.153 150)" : "oklch(0.687 0.176 25)"}
              stopOpacity="0"
            />
          </linearGradient>
        </defs>

        {/* Where the window began. Everything above it is ahead of the start. */}
        <line
          x1="0"
          x2={W}
          y1={zero}
          y2={zero}
          stroke="oklch(1 0 0 / 14%)"
          strokeWidth="0.4"
          vectorEffect="non-scaling-stroke"
        />

        <path d={area} fill="url(#curve-fill)" />
        <path
          d={line}
          fill="none"
          stroke={up ? "oklch(0.755 0.153 150)" : "oklch(0.687 0.176 25)"}
          strokeWidth="1.4"
          strokeLinejoin="round"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
        />
      </svg>

      <div className="mt-1.5 flex justify-between text-[10.5px] text-faint">
        <span className="tnum">{points[0].date.slice(5).replace("-", ".")}</span>
        <span className="tnum">
          {points[points.length - 1].date.slice(5).replace("-", ".")}
        </span>
      </div>
    </div>
  );
}
