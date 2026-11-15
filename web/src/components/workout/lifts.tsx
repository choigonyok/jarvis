"use client";

import { useMemo } from "react";
import { ChevronRight } from "lucide-react";
import {
  MUSCLE_GROUPS,
  bestSet,
  dayLabel,
  exerciseHistory,
  groupOf,
  heaviest,
  load,
  recentExercises,
  type Session,
} from "@/lib/workout";

/**
 * Every movement you have logged, by body part.
 *
 * The question this view answers is "which lifts are going up", so each row
 * carries its best estimated 1RM and a line of the last sessions' 1RMs - a
 * rising line and a flat one look different at a glance, which a column of
 * numbers does not. The row opens the full history.
 */
export function Lifts({ sessions, onOpen }: { sessions: Session[]; onOpen: (name: string) => void }) {
  const rows = useMemo(
    () =>
      recentExercises(sessions).map((r) => {
        const history = exerciseHistory(sessions, r.name);
        // The best ever, as the movement's sheet says it - the line is the
        // recent sessions, the number is not only theirs.
        const best = bestSet(sessions, r.name)?.oneRm ?? null;
        const top = heaviest(sessions, r.name);
        // Past 12 reps every time there is no honest 1RM: a lift done light
        // for many reps is read by its heaviest load. It had been called 맨몸.
        const trend = (
          best !== null
            ? history.map((h) => h.oneRm).filter((v): v is number => v !== null)
            : top > 0
              ? history.map((h) => Math.max(0, ...h.sets.filter((s) => !s.warmup).map((s) => s.weight)))
              : []
        )
          .slice(0, 8)
          .reverse();
        return { ...r, group: groupOf(r.name), trend, best, top };
      }),
    [sessions],
  );

  if (rows.length === 0) {
    return (
      <p className="text-ui leading-relaxed text-faint">
        아직 기록한 종목이 없어요. 운동을 한 번 마치면 종목마다 추정 1RM이 여기에 쌓여요.
      </p>
    );
  }

  return (
    <div className="space-y-7">
      {MUSCLE_GROUPS.map((group) => {
        const mine = rows.filter((r) => r.group === group);
        if (mine.length === 0) return null;
        return (
          <section key={group} aria-labelledby={`lift-${group}`}>
            <h2 id={`lift-${group}`} className="mb-1 text-ui text-dim">
              {group}
            </h2>
            <ul className="border-t border-edge-soft">
              {mine.map((row) => (
                <li key={row.name} className="border-b border-edge-soft">
                  <button
                    type="button"
                    onClick={() => onOpen(row.name)}
                    className="flex min-h-16 w-full items-center gap-3 px-1 py-2 text-left hover:bg-glass focus-ring"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-title text-foreground">{row.name}</span>
                      <span className="block truncate text-aside text-faint">마지막 {dayLabel(row.date)}</span>
                    </span>
                    <Trend values={row.trend} of={row.best !== null ? "1RM" : "무게"} />
                    <span className="w-16 shrink-0 text-right">
                      {row.best !== null ? (
                        <>
                          <span className="font-score tnum block text-[22px] leading-none font-semibold text-foreground">
                            {Math.round(row.best)}
                          </span>
                          <span className="block text-meta text-faint">1RM kg</span>
                        </>
                      ) : row.top > 0 ? (
                        <>
                          <span className="font-score tnum block text-[22px] leading-none font-semibold text-foreground">
                            {load(row.top)}
                          </span>
                          <span className="block text-meta text-faint">최고 kg</span>
                        </>
                      ) : (
                        <span className="text-aside text-faint">맨몸</span>
                      )}
                    </span>
                    <ChevronRight aria-hidden className="size-4 shrink-0 text-faint" />
                  </button>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

/** The last few sessions' 1RM (or top load) as a line, the latest end marked. Nothing for a single point. */
function Trend({ values, of }: { values: number[]; of: "1RM" | "무게" }) {
  if (values.length < 2) return <span aria-hidden className="w-16 shrink-0" />;
  const w = 64;
  const h = 22;
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const span = hi - lo || 1;
  const pts = values.map((v, i) => [(i / (values.length - 1)) * (w - 4) + 2, h - 3 - ((v - lo) / span) * (h - 6)]);
  const up = values[values.length - 1] > values[0];
  const [lx, ly] = pts[pts.length - 1];
  return (
    <svg
      width={w}
      height={h}
      viewBox={`0 0 ${w} ${h}`}
      role="img"
      aria-label={
        of === "1RM"
          ? `추정 1RM ${values.map((v) => Math.round(v)).join(", ")}kg`
          : `최고 무게 ${values.map((v) => load(v)).join(", ")}kg`
      }
      className="shrink-0"
    >
      <polyline
        points={pts.map(([x, y]) => `${x},${y}`).join(" ")}
        fill="none"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
        className={up ? "stroke-approve-fill" : "stroke-dim/60"}
      />
      <circle cx={lx} cy={ly} r="2.25" className={up ? "fill-approve" : "fill-dim"} />
    </svg>
  );
}
