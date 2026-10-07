"use client";

import { useState } from "react";
import { WINDOW_LABEL, percent, type Point, type Window } from "@/lib/portfolio";
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
}: {
  series: Record<Window, Point[]>;
  rates: Record<Window, number | null>;
}) {
  const [window, setWindow] = useState<Window>("month");
  const points = series[window] ?? [];
  const rate = rates[window];

  return (
    <section aria-label="총자산 변화">
      <div className="mb-3 flex items-center justify-between gap-3">
        <p className="tnum text-[13px]">
          {rate === null ? (
            <span className="text-faint">—</span>
          ) : (
            <span className={cn(rate >= 0 ? "text-approve/85" : "text-reject/85")}>
              {percent(rate, 2)}
            </span>
          )}
          <span className="ms-2 text-faint">{WINDOW_LABEL[window]}</span>
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

      <Plot points={points} />
    </section>
  );
}

function Plot({ points }: { points: Point[] }) {
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

  const base = points[0].valueKrw;
  // Percent from the window's start: the line is about movement, not size.
  const values = points.map((p) => (p.valueKrw - base) / base);
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
        aria-label={`총자산 변화 ${percent(values[values.length - 1], 2)}`}
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
