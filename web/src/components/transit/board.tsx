"use client";

import type { Who } from "@/lib/track";
import { type Arrival, type Plan, type Route, clockOf, countdown, stageOf } from "@/lib/transit";
import { cn } from "@/lib/utils";

/** Each person's own face, as on the 위치 map. */
const FACE: Record<Who, string> = { owner: "/icons/location-cat.v4.png", guest: "/icons/location-cat-guest.v4.png" };

/**
 * Pixel drawings, a cell to a character: the little screen this board is
 * drawn after was 128×64 dots, and these keep its blockiness at any size.
 */
const BUS = ["..XXXXXXXXXXX.", ".X...........X", "X.XX.XX.XX.XXX", "X.XX.XX.XX.XXX", "X............X", "XXXXXXXXXXXXXX", ".XX.......XX.."];
const TRAIN = ["...XXXXXXXXXXXX", "..X............", ".X.XX.XX.XX.XX.", "X..XX.XX.XX.XX.", "X..............", "XXXXXXXXXXXXXXX", ".XX.XX....XX.XX"];
const FLAG = ["XXXX", "XXXXX", "XXXX", "X", "X", "X", "X", "X"];
const SIGN = ["XXXXXXX", "X.....X", "X.XXX.X", "X.....X", "XXXXXXX", "...X", "...X", "...X", "...X", "..XXX"];

function Pixels({ rows, cell, x, y, className }: { rows: string[]; cell: number; x: number; y: number; className?: string }) {
  return (
    <g className={className} shapeRendering="crispEdges">
      {rows.flatMap((row, j) =>
        [...row].map((c, i) => (c === "X" ? <rect key={`${i}-${j}`} x={x + i * cell} y={y + j * cell} width={cell} height={cell} /> : null)),
      )}
    </g>
  );
}

/**
 * The rail: a stretch of minutes ruled like a track, from twenty minutes
 * before one must leave to the moment the vehicle is at the stop. The face is
 * now - waiting at the left while there is time, then walking to the flag
 * (when to leave) and on to the sign, where the bus or the train is.
 */
function Rail({ who, plan, route, now }: { who: Who; plan: Plan; route: Route; now: number }) {
  const W = 340;
  const H = 52;
  const left = 14;
  const right = W - 24;
  const ground = 36;
  const board = plan.catch ? Date.parse(plan.catch.at) : NaN;
  const leave = plan.leave ? Date.parse(plan.leave) : NaN;
  const known = Number.isFinite(board) && Number.isFinite(leave);
  const start = known ? leave - 20 * 60_000 : 0;
  const span = known ? board - start : 1;
  const xOf = (t: number) => left + ((right - left) * Math.min(Math.max(t - start, 0), span)) / span;
  const flagX = known ? xOf(leave) : left + (right - left) * 0.65;
  const meX = known ? xOf(now) : left;
  const minutes = Math.round(span / 60_000);
  const ticks = known ? Array.from({ length: minutes + 1 }, (_, i) => i) : [];
  const vehicle = route.stop.kind === "subway" ? TRAIN : BUS;
  return (
    <div className="relative">
      <svg viewBox={`0 0 ${W} ${H}`} className="block w-full overflow-visible" role="img" aria-label={known ? `${clockOf(plan.leave!)}에 나가서 ${clockOf(plan.catch!.at)}에 타요` : "도착 정보 없음"}>
        {/* The track, ruled by the minute; every fifth a little taller. */}
        <line x1={0} x2={W} y1={ground} y2={ground} className="stroke-foreground/45" strokeWidth={1.5} />
        {ticks.map((i) => {
          const x = left + ((right - left) * i * 60_000) / span;
          return <line key={i} x1={x} x2={x} y1={ground} y2={ground + (i % 5 === 0 ? 6 : 3)} className="stroke-foreground/35" strokeWidth={1.2} />;
        })}
        {/* When to leave, and where it is boarded. */}
        <Pixels rows={FLAG} cell={2.5} x={flagX - 1.25} y={ground - 20} className={cn("fill-mine", plan.stage === "go" && "anim-breathe")} />
        <Pixels rows={SIGN} cell={2.5} x={right - 2} y={ground - 25} className="fill-mine" />
        <Pixels rows={vehicle} cell={2.5} x={right - 2 - vehicle[0].length * 2.5 - 6} y={ground - 18} className={cn(known ? "fill-foreground/80" : "fill-foreground/25")} />
      </svg>
      {/* Now: one's own face, walking the track. */}
      {/* eslint-disable-next-line @next/next/no-img-element -- a 26px icon from public/, nothing to optimise */}
      <img
        src={FACE[who]}
        alt=""
        draggable={false}
        className={cn(
          "pointer-events-none absolute h-[26px] w-auto -translate-x-1/2 select-none",
          plan.stage === "relax" && "opacity-70",
          plan.stage === "go" && "anim-breathe",
        )}
        style={{ left: `${(meX / W) * 100}%`, top: `${((ground - 27) / H) * 100}%` }}
      />
      {known ? (
        <div className="tnum relative mt-1 h-4 text-[11.5px] text-dim">
          <span className="absolute -translate-x-1/2 whitespace-nowrap" style={{ left: `${(flagX / W) * 100}%` }}>
            {clockOf(plan.leave!)} 나가기
          </span>
          <span className="absolute right-0 whitespace-nowrap">{clockOf(plan.catch!.at)}</span>
        </div>
      ) : null}
    </div>
  );
}

/** Room on a bus: the empty seats where they are counted, else how full it is. */
function Room({ arrival }: { arrival: Arrival }) {
  const { seats, crowd } = arrival;
  if (seats != null) {
    if (seats === 0) return <span className="text-reject">만석</span>;
    return (
      <>
        빈자리 <span className="font-score text-[19px] leading-none font-semibold">{seats}</span>
      </>
    );
  }
  if (!crowd) return null;
  return <span className={crowd === "만차" || crowd === "매우혼잡" ? "text-reject" : undefined}>{crowd}</span>;
}

/**
 * What is coming on this line, in the order it comes: the one to catch bright,
 * the ones after it, and first the ones too soon to walk to - the feeds tell
 * of only the next two buses, and those may be all there is to see.
 */
function Coming({ plan, now }: { plan: Plan; now: number }) {
  const rows = [
    ...(plan.missed ?? []).filter((a) => Date.parse(a.at) > now - 30_000).slice(-2).map((a) => ({ a, kind: "missed" })),
    ...(plan.catch ? [{ a: plan.catch, kind: "catch" }] : []),
    ...plan.next.slice(0, 3).map((a) => ({ a, kind: "next" })),
  ];
  if (rows.length === 0) return null;
  return (
    <ul aria-label="오는 차" className="mt-3 divide-y divide-edge-soft border-t border-edge-soft">
      {rows.map(({ a, kind }) => {
        const min = Math.round((Date.parse(a.at) - now) / 60_000);
        const about = [a.away ? `${a.away}번째 전` : "", a.note, a.rough ? "시각은 짐작" : "", kind === "missed" ? "못 타요" : ""].filter(Boolean).join(" · ");
        return (
          <li
            key={`${a.at}-${a.vehicle ?? ""}`}
            aria-current={kind === "catch" ? "true" : undefined}
            className={cn(
              "tnum flex min-h-10 items-center gap-3 py-1.5 text-[13px]",
              kind === "catch" ? "font-medium text-foreground" : kind === "missed" ? "text-faint" : "text-dim",
            )}
          >
            <span className="w-11 shrink-0">{clockOf(a.at)}</span>
            <span className="w-12 shrink-0">{min <= 0 ? "곧" : `${min}분 뒤`}</span>
            <span className="min-w-0 flex-1 truncate font-normal">{about}</span>
            <span className="shrink-0 font-normal">
              <Room arrival={a} />
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * The board for one route: one's face saying how it stands, how long until one
 * must leave, the rail, the line to catch - "18:09 7770 타기 · 도보 10분" - and
 * what is coming, with the seats left on each. Between reads the countdown and
 * the words go on with the clock.
 */
export function Board({ who, route, plan, now }: { who: Who; route: Route; plan?: Plan; now: number }) {
  const leave = plan?.leave ? Date.parse(plan.leave) : NaN;
  const last = plan?.last ? Date.parse(plan.last) : leave;
  const running = plan && plan.catch && Number.isFinite(leave) ? stageOf(leave, now, last) : null;
  // Run out between two reads: nothing to count down to until the next says what is.
  const live = running?.stage === "none" ? null : running;
  const stage = running?.stage ?? plan?.stage ?? "off";
  const say = running?.say ?? plan?.say ?? "불러오는 중이에요";
  const late = stage === "run";
  const pastGood = live !== null && now > leave;
  const shown: Plan = plan ? { ...plan, stage } : { route: route.id, now: "", next: [], stage: "off", say };
  const caption = live
    ? late
      ? "나갔어야 할 때가 지났어요"
      : pastGood
        ? "늦어도 이때까지 나가야 해요"
        : "나갈 때까지"
    : plan?.error
      ? plan.error
      : stage !== "none"
        ? " "
        : plan?.missed?.length
          ? `걸어서 ${route.walk}분이라 놓쳐요. 다음 차가 잡히면 세어 드려요.`
          : "지금 잡히는 차가 없어요. 아직 출발 전이거나 운행이 끝났을 수 있어요.";
  return (
    <section aria-label={`${route.name} 출발`} className="pt-1">
      <div className="flex items-start gap-3">
        {/* eslint-disable-next-line @next/next/no-img-element -- a 40px icon from public/, nothing to optimise */}
        <img src={FACE[who]} alt="" draggable={false} className="mt-1 h-10 w-auto shrink-0 select-none" />
        <p
          aria-live="polite"
          className={cn(
            "relative rounded-[12px] border-[1.5px] px-3.5 py-2 text-[16px] leading-snug font-medium break-keep",
            "before:absolute before:top-[14px] before:-left-[7px] before:size-3 before:rotate-45 before:border-b-[1.5px] before:border-l-[1.5px] before:bg-background",
            stage === "go" ? "border-foreground/80 text-foreground before:border-foreground/80" : late ? "border-reject/70 text-reject before:border-reject/70" : "border-foreground/40 text-foreground/90 before:border-foreground/40",
          )}
        >
          {say}
        </p>
      </div>

      <div className="mt-4">
        <p className={cn("font-score tnum text-[84px] leading-[0.95] font-semibold tracking-[-0.01em]", late ? "text-reject" : live ? "text-foreground" : "text-faint")}>
          {live ? countdown(Math.abs((pastGood ? last : leave) - now)) : "--:--"}
        </p>
        <p className="mt-1 text-[13px] text-dim">{caption}</p>
      </div>

      <div className="mt-6">
        <Rail who={who} plan={shown} route={route} now={now} />
      </div>

      <div className="mt-4 flex items-baseline justify-between gap-3">
        <p className="min-w-0 truncate text-[17px] font-medium text-foreground">
          {plan?.catch ? `${clockOf(plan.catch.at)} ${plan.catch.line.name || route.line.name} 타기` : `${route.line.name} 타기`}
        </p>
        <p className="shrink-0 text-[15px] text-dim">도보 {route.walk}분</p>
      </div>
      <p className="mt-0.5 truncate text-[12.5px] text-dim">
        {[route.stop.name, plan?.catch?.line.toward || route.line.toward].filter(Boolean).join(" · ")}
      </p>
      {plan ? <Coming plan={plan} now={now} /> : null}
    </section>
  );
}
