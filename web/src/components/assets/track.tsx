"use client";

import { useEffect, useRef, useState } from "react";
import { type TrackPoint, krw, percent, signedKrw } from "@/lib/portfolio";
import { cn } from "@/lib/utils";

/**
 * The whole pot, day by day, in one chart: its size as a line, and its return
 * against principal as bars underneath.
 *
 * Two measures of different units in one frame, so they are drawn as two
 * different marks with their own scales - won on the left, percent on the
 * right - rather than as two lines that would lie on top of each other. While
 * nothing is deposited the rate is the total over a fixed principal and the
 * two shapes are the same; a deposit is where they part, and the dashed
 * principal step under the total shows it.
 *
 * Touching or hovering a day marks it and opens a card there with that day's
 * numbers; touching anywhere else closes it.
 */
export function Track({ points }: { points: TrackPoint[] }) {
  const [at, setAt] = useState<number | null>(null);
  const root = useRef<HTMLDivElement>(null);

  // A touch outside the chart closes the card.
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

  const n = points.length;
  const rateOf = (p: TrackPoint) => (p.principalKrw > 0 ? (p.totalKrw - p.principalKrw) / p.principalKrw : 0);
  const rates = points.map(rateOf);

  // Won: the upper band, total and principal together.
  const won = points.flatMap((p) => [p.totalKrw, p.principalKrw]);
  let top = Math.max(...won);
  let bottom = Math.min(...won);
  if (top - bottom < top * 0.02) {
    const mid = (top + bottom) / 2;
    top = mid + top * 0.01;
    bottom = mid - top * 0.01;
  }
  // Percent: the lower band, always including zero.
  const rTop = Math.max(...rates, 0);
  const rBottom = Math.min(...rates, 0);
  const rSpan = Math.max(rTop - rBottom, 0.02);

  const W = 100;
  const H = 60;
  const LINE_H = 36; // upper band for the won lines
  const GAP = 4;
  const BAR_TOP = LINE_H + GAP;
  const BAR_H = H - BAR_TOP;
  const x = (i: number) => (i / (n - 1)) * W;
  const y = (v: number) => LINE_H - ((v - bottom) / (top - bottom)) * LINE_H;
  const yr = (r: number) => BAR_TOP + ((rTop - r) / rSpan) * BAR_H;
  const zero = yr(0);
  const line = (values: number[], step?: boolean) =>
    values
      .map((v, i) => (i === 0 ? `M${x(i)},${y(v)}` : step ? `H${x(i)} V${y(v)}` : `L${x(i)},${y(v)}`))
      .join(" ");
  // Bars a little narrower than a day, so neighbours keep a gap.
  const barW = Math.min(6, (W / n) * 0.6);

  const pick = (e: React.PointerEvent<HTMLDivElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    const f = Math.min(1, Math.max(0, (e.clientX - box.left) / box.width));
    setAt(Math.round(f * (n - 1)));
  };
  const side = at === null ? 0 : at / (n - 1);
  const manwon = (v: number) => `${Math.round(v / 10_000).toLocaleString("ko-KR")}만`;
  const shown = at === null ? null : points[at];

  return (
    <figure ref={root}>
      <figcaption className="mb-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11.5px] text-faint">
        <Key mark="line">총자산</Key>
        <Key mark="dashed">넣은 원금</Key>
        <Key mark="bar">원금 대비 수익률</Key>
      </figcaption>

      <div className="relative rounded-lg border border-edge-soft bg-glass px-12 pt-5 pb-5">
        <div
          className="relative touch-pan-y"
          onPointerDown={pick}
          onPointerMove={pick}
          onPointerLeave={(e) => {
            if (e.pointerType === "mouse") setAt(null);
          }}
        >
          <svg
            viewBox={`0 0 ${W} ${H}`}
            preserveAspectRatio="none"
            className="h-52 w-full overflow-visible"
            role="img"
            aria-label={`총자산 ${points[0].date} ${krw(points[0].totalKrw)}에서 ${points[n - 1].date} ${krw(points[n - 1].totalKrw)}, 수익률 ${percent(rates[0], 1)}에서 ${percent(rates[n - 1], 1)}`}
          >
            {/* The rate's zero line. */}
            <line
              x1="0"
              x2={W}
              y1={zero}
              y2={zero}
              stroke="oklch(1 0 0 / 14%)"
              strokeWidth="1"
              vectorEffect="non-scaling-stroke"
            />
            {rates.map((r, i) => (
              <rect
                key={points[i].date}
                x={Math.min(W - barW, Math.max(0, x(i) - barW / 2))}
                y={Math.min(zero, yr(r))}
                width={barW}
                height={Math.max(0.3, Math.abs(yr(r) - zero))}
                rx="0.6"
                className={r >= 0 ? "fill-approve" : "fill-reject"}
                opacity={at === null || at === i ? 0.7 : 0.35}
              />
            ))}
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
            <path
              d={line(points.map((p) => p.principalKrw), true)}
              fill="none"
              stroke="oklch(1 0 0 / 38%)"
              strokeWidth="2"
              strokeDasharray="4 3"
              vectorEffect="non-scaling-stroke"
            />
            <path
              d={line(points.map((p) => p.totalKrw))}
              fill="none"
              stroke="oklch(1 0 0 / 85%)"
              strokeWidth="2"
              strokeLinejoin="round"
              strokeLinecap="round"
              vectorEffect="non-scaling-stroke"
            />
          </svg>

          {/* Each scale's ends, on its own side: won left, percent right,
              placed at the plot's own heights. */}
          <span
            className="tnum pointer-events-none absolute right-full mr-2 -translate-y-1/2 text-[10.5px] whitespace-nowrap text-faint"
            style={{ top: "0%" }}
          >
            {manwon(top)}
          </span>
          <span
            className="tnum pointer-events-none absolute right-full mr-2 -translate-y-1/2 text-[10.5px] whitespace-nowrap text-faint"
            style={{ top: `${(LINE_H / H) * 100}%` }}
          >
            {manwon(bottom)}
          </span>
          <span
            className="tnum pointer-events-none absolute left-full ml-2 -translate-y-1/2 text-[10.5px] whitespace-nowrap text-faint"
            style={{ top: `${(BAR_TOP / H) * 100}%` }}
          >
            {percent(rTop, 1)}
          </span>
          <span
            className="tnum pointer-events-none absolute left-full ml-2 -translate-y-1/2 text-[10.5px] whitespace-nowrap text-faint"
            style={{ top: "100%" }}
          >
            {percent(rBottom, 1)}
          </span>

          {shown && at !== null ? (
            <>
              <span
                aria-hidden
                className="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-foreground ring-2 ring-background"
                style={{ left: `${(x(at) / W) * 100}%`, top: `${(y(shown.totalKrw) / H) * 100}%` }}
              />
              <div
                role="status"
                className={cn(
                  "pointer-events-none absolute -top-3 z-10",
                  side < 0.3 ? "translate-x-2" : side > 0.7 ? "-translate-x-[calc(100%+0.5rem)]" : "-translate-x-1/2",
                )}
                style={{ left: `${(x(at) / W) * 100}%` }}
              >
                <Card point={shown} rate={rates[at]} />
              </div>
            </>
          ) : null}
        </div>

      </div>

      <div className="mt-1.5 flex justify-between px-12 text-[10.5px] text-faint">
        <span className="tnum">{points[0].date.slice(5).replace("-", ".")}</span>
        <span className="tnum">{points[n - 1].date.slice(5).replace("-", ".")}</span>
      </div>
    </figure>
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
        수익 {signedKrw(gain)} · {percent(rate, 2)}
      </p>
    </div>
  );
}

function Key({ mark, children }: { mark: "line" | "dashed" | "bar"; children: React.ReactNode }) {
  return (
    <span className="flex items-center gap-1.5">
      {mark === "bar" ? (
        <span aria-hidden className="h-2.5 w-1.5 rounded-[1px] bg-approve/70" />
      ) : (
        <svg aria-hidden viewBox="0 0 16 4" className="h-1 w-4">
          <line
            x1="0"
            x2="16"
            y1="2"
            y2="2"
            stroke="currentColor"
            strokeWidth="2"
            strokeDasharray={mark === "dashed" ? "3 2" : undefined}
            className={mark === "dashed" ? "text-faint" : "text-foreground/80"}
          />
        </svg>
      )}
      {children}
    </span>
  );
}
