"use client";

import { useState } from "react";
import { type TrackPoint, krw, percent, signedKrw } from "@/lib/portfolio";
import { cn } from "@/lib/utils";

/**
 * The whole pot, day by day: how big it was, and how far ahead of the money
 * put in. Two charts, not one with two axes - won and percent do not share a
 * scale, and a second axis makes the crossing of two lines mean nothing.
 *
 * The size chart draws the principal as a dashed step under the total: the
 * gap between them is the return, so a deposit (both lines jump) reads
 * differently from a gain (only the total moves). The rate chart is that gap
 * over the principal, against a zero line.
 *
 * One cursor serves both: hovering or touching a day marks it in each and
 * says that day's numbers once, above them.
 */
export function Track({ points }: { points: TrackPoint[] }) {
  const [at, setAt] = useState<number | null>(null);

  if (points.length < 2) {
    return (
      <p className="rounded-lg border border-edge-soft bg-glass px-4 py-5 text-[12.5px] leading-relaxed text-faint">
        하루에 한 번 기록해요. 이틀치가 쌓이면 여기에 그려져요.
      </p>
    );
  }

  const rate = (p: TrackPoint) => (p.principalKrw > 0 ? (p.totalKrw - p.principalKrw) / p.principalKrw : 0);
  const shown = points[at ?? points.length - 1];
  const md = (d: string) => `${Number(d.slice(5, 7))}.${Number(d.slice(8, 10))}`;

  // Pointer position to the nearest recorded day.
  const pick = (e: React.PointerEvent<HTMLDivElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    const f = Math.min(1, Math.max(0, (e.clientX - box.left) / box.width));
    setAt(Math.round(f * (points.length - 1)));
  };

  return (
    <div
      onPointerMove={pick}
      onPointerDown={pick}
      onPointerLeave={() => setAt(null)}
      className="touch-pan-y"
    >
      <p className="tnum mb-3 text-[12.5px] text-dim" aria-live="polite">
        <span className="text-foreground/90">{md(shown.date)}</span>
        <span className="ms-2">총자산 {krw(shown.totalKrw)}</span>
        <span className="ms-2 text-faint">원금 {krw(shown.principalKrw)}</span>
        <span className={cn("ms-2", rate(shown) >= 0 ? "text-approve/85" : "text-reject/85")}>
          {percent(rate(shown), 2)}
        </span>
      </p>

      <Chart
        label="총자산"
        legend={
          <>
            <Key line="solid">총자산</Key>
            <Key line="dashed">넣은 원금</Key>
          </>
        }
        series={[
          { values: points.map((p) => p.totalKrw), dashed: false },
          { values: points.map((p) => p.principalKrw), dashed: true, step: true },
        ]}
        format={(v) => `${Math.round(v / 10_000).toLocaleString("ko-KR")}만`}
        at={at}
        dates={points.map((p) => p.date)}
      />

      <Chart
        className="mt-5"
        label="원금 대비 수익률"
        legend={<Key line="solid">원금 대비 수익률</Key>}
        series={[{ values: points.map(rate), dashed: false }]}
        format={(v) => percent(v, 1)}
        zero
        at={at}
        dates={points.map((p) => p.date)}
      />

      <p className="tnum mt-3 text-[11.5px] text-faint">
        {md(points[0].date)}부터 {points.length}일 · 수익 {signedKrw(points[0].totalKrw - points[0].principalKrw)} →{" "}
        {signedKrw(points[points.length - 1].totalKrw - points[points.length - 1].principalKrw)}
      </p>
    </div>
  );
}

function Key({ line, children }: { line: "solid" | "dashed"; children: React.ReactNode }) {
  return (
    <span className="flex items-center gap-1.5">
      <svg aria-hidden viewBox="0 0 16 4" className="h-1 w-4">
        <line
          x1="0"
          x2="16"
          y1="2"
          y2="2"
          stroke="currentColor"
          strokeWidth="2"
          strokeDasharray={line === "dashed" ? "3 2" : undefined}
          className={line === "dashed" ? "text-faint" : "text-foreground/80"}
        />
      </svg>
      {children}
    </span>
  );
}

function Chart({
  label,
  legend,
  series,
  format,
  zero = false,
  at,
  dates,
  className,
}: {
  label: string;
  legend: React.ReactNode;
  series: { values: number[]; dashed: boolean; step?: boolean }[];
  format: (v: number) => string;
  /** Draw and include a zero line (for a rate). */
  zero?: boolean;
  at: number | null;
  dates: string[];
  className?: string;
}) {
  const all = series.flatMap((s) => s.values);
  let top = Math.max(...all, ...(zero ? [0] : []));
  let bottom = Math.min(...all, ...(zero ? [0] : []));
  // A flat stretch should not be magnified into a mountain range.
  const minSpan = zero ? 0.02 : Math.max(top * 0.02, 1);
  if (top - bottom < minSpan) {
    const mid = (top + bottom) / 2;
    top = mid + minSpan / 2;
    bottom = mid - minSpan / 2;
  }
  const W = 100;
  const H = 30;
  const n = dates.length;
  const x = (i: number) => (i / (n - 1)) * W;
  const y = (v: number) => H - ((v - bottom) / (top - bottom)) * H;
  const path = (values: number[], step?: boolean) =>
    values
      .map((v, i) =>
        i === 0 ? `M${x(i)},${y(v)}` : step ? `H${x(i)} V${y(v)}` : `L${x(i)},${y(v)}`,
      )
      .join(" ");
  const last = series[0].values[n - 1];

  return (
    <figure className={className}>
      <figcaption className="mb-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11.5px] text-faint">
        {legend}
      </figcaption>
      {/* Room above and below the plot for the scale's two labels. */}
      <div className="relative rounded-lg border border-edge-soft bg-glass px-3 pt-6 pb-6">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="none"
          className="h-28 w-full overflow-visible"
          role="img"
          aria-label={`${label}: ${dates[0]} ${format(series[0].values[0])}에서 ${dates[n - 1]} ${format(last)}`}
        >
          {zero ? (
            <line
              x1="0"
              x2={W}
              y1={y(0)}
              y2={y(0)}
              stroke="oklch(1 0 0 / 14%)"
              strokeWidth="1"
              vectorEffect="non-scaling-stroke"
            />
          ) : null}
          {at !== null ? (
            <line
              x1={x(at)}
              x2={x(at)}
              y1="0"
              y2={H}
              stroke="oklch(1 0 0 / 22%)"
              strokeWidth="1"
              vectorEffect="non-scaling-stroke"
            />
          ) : null}
          {series.map((s, i) => (
            <path
              key={i}
              d={path(s.values, s.step)}
              fill="none"
              stroke={s.dashed ? "oklch(1 0 0 / 38%)" : "oklch(1 0 0 / 82%)"}
              strokeWidth="2"
              strokeDasharray={s.dashed ? "4 3" : undefined}
              strokeLinejoin="round"
              strokeLinecap="round"
              vectorEffect="non-scaling-stroke"
            />
          ))}
        </svg>
        {/* The scale's two ends, in words, so the line can be read without a grid. */}
        <span className="tnum pointer-events-none absolute top-1.5 right-3 text-[10.5px] text-faint">
          {format(top)}
        </span>
        <span className="tnum pointer-events-none absolute right-3 bottom-1.5 text-[10.5px] text-faint">
          {format(bottom)}
        </span>
      </div>
      <div className="mt-1.5 flex justify-between text-[10.5px] text-faint">
        <span className="tnum">{dates[0].slice(5).replace("-", ".")}</span>
        <span className="tnum">{dates[n - 1].slice(5).replace("-", ".")}</span>
      </div>
    </figure>
  );
}
