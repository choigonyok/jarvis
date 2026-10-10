"use client";

import { useEffect, useRef, useState } from "react";
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
 * Touching or hovering a day marks it in both and opens a small card at that
 * point with the day's numbers; touching anywhere else closes it.
 */
export function Track({ points }: { points: TrackPoint[] }) {
  const [at, setAt] = useState<number | null>(null);
  const [on, setOn] = useState<"size" | "rate">("size");
  const root = useRef<HTMLDivElement>(null);

  // A touch outside the charts closes the card; hovering away does the same.
  useEffect(() => {
    if (at === null) return;
    const close = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setAt(null);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [at]);

  if (points.length < 2) {
    return (
      <p className="rounded-lg border border-edge-soft bg-glass px-4 py-5 text-[12.5px] leading-relaxed text-faint">
        하루에 한 번 기록해요. 이틀치가 쌓이면 여기에 그려져요.
      </p>
    );
  }

  const rate = (p: TrackPoint) => (p.principalKrw > 0 ? (p.totalKrw - p.principalKrw) / p.principalKrw : 0);
  const dates = points.map((p) => p.date);
  const card =
    at === null ? null : (
      <Card point={points[at]} rate={rate(points[at])} />
    );
  const hover = (which: "size" | "rate") => ({
    onPick: (i: number) => {
      setOn(which);
      setAt(i);
    },
    onLeave: (e: React.PointerEvent) => {
      if (e.pointerType === "mouse") setAt(null);
    },
  });

  return (
    <div ref={root}>
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
        card={on === "size" ? card : null}
        dates={dates}
        {...hover("size")}
      />

      <Chart
        className="mt-5"
        label="원금 대비 수익률"
        legend={<Key line="solid">원금 대비 수익률</Key>}
        series={[{ values: points.map(rate), dashed: false }]}
        format={(v) => percent(v, 1)}
        zero
        at={at}
        card={on === "rate" ? card : null}
        dates={dates}
        {...hover("rate")}
      />
    </div>
  );
}

function Card({ point, rate }: { point: TrackPoint; rate: number }) {
  const gain = point.totalKrw - point.principalKrw;
  return (
    <div className="tnum w-max rounded-lg border border-edge bg-background/95 px-3 py-2 text-[12px] leading-relaxed shadow-lg backdrop-blur-sm">
      <p className="text-foreground/90">
        {Number(point.date.slice(5, 7))}월 {Number(point.date.slice(8, 10))}일
      </p>
      <p className="text-dim">총자산 {krw(point.totalKrw)}</p>
      <p className="text-faint">넣은 원금 {krw(point.principalKrw)}</p>
      <p className={gain >= 0 ? "text-approve/85" : "text-reject/85"}>
        {signedKrw(gain)} ({percent(rate, 2)})
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
  card,
  dates,
  onPick,
  onLeave,
  className,
}: {
  label: string;
  legend: React.ReactNode;
  series: { values: number[]; dashed: boolean; step?: boolean }[];
  format: (v: number) => string;
  /** Draw and include a zero line (for a rate). */
  zero?: boolean;
  at: number | null;
  /** The day's numbers, opened over this chart at the marked day. */
  card: React.ReactNode;
  dates: string[];
  onPick: (i: number) => void;
  onLeave: (e: React.PointerEvent) => void;
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
      .map((v, i) => (i === 0 ? `M${x(i)},${y(v)}` : step ? `H${x(i)} V${y(v)}` : `L${x(i)},${y(v)}`))
      .join(" ");
  const last = series[0].values[n - 1];

  // Pointer position to the nearest recorded day.
  const pick = (e: React.PointerEvent<HTMLDivElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    const f = Math.min(1, Math.max(0, (e.clientX - box.left) / box.width));
    onPick(Math.round(f * (n - 1)));
  };
  // Keep the card inside the chart: anchored left, centred, or right by where the day falls.
  const side = at === null ? 0 : at / (n - 1);

  return (
    <figure className={className}>
      <figcaption className="mb-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11.5px] text-faint">
        {legend}
      </figcaption>
      {/* Room above and below the plot for the scale's two labels. */}
      <div className="relative rounded-lg border border-edge-soft bg-glass px-3 pt-6 pb-6">
        <div className="relative touch-pan-y" onPointerDown={pick} onPointerMove={pick} onPointerLeave={onLeave}>
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
          {at !== null ? (
            // The marked day's point on the first series.
            <span
              aria-hidden
              className="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-foreground ring-2 ring-background"
              style={{ left: `${(x(at) / W) * 100}%`, top: `${(y(series[0].values[at]) / H) * 100}%` }}
            />
          ) : null}
          {card && at !== null ? (
            <div
              role="status"
              className={cn(
                "pointer-events-none absolute -top-4 z-10",
                side < 0.3 ? "translate-x-2" : side > 0.7 ? "-translate-x-[calc(100%+0.5rem)]" : "-translate-x-1/2",
              )}
              style={{ left: `${(x(at) / W) * 100}%` }}
            >
              {card}
            </div>
          ) : null}
        </div>
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
