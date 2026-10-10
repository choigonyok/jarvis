"use client";

import { useMemo } from "react";
import { ChevronRight } from "lucide-react";
import {
  MUSCLE_GROUPS,
  dayLabel,
  exerciseHistory,
  groupOf,
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
        const trend = exerciseHistory(sessions, r.name)
          .map((h) => h.oneRm)
          .filter((v): v is number => v !== null)
          .slice(0, 8)
          .reverse();
        return { ...r, group: groupOf(r.name), trend, best: trend.length ? Math.max(...trend) : null };
      }),
    [sessions],
  );

  if (rows.length === 0) {
    return (
      <p className="text-[13.5px] leading-relaxed text-faint">
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
            <h2 id={`lift-${group}`} className="mb-1 text-[13px] text-dim">
              {group}
            </h2>
            <ul className="border-t border-edge-soft">
              {mine.map((row) => (
                <li key={row.name} className="border-b border-edge-soft">
                  <button
                    type="button"
                    onClick={() => onOpen(row.name)}
                    className="flex min-h-16 w-full items-center gap-3 px-1 py-2 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[15px] text-foreground">{row.name}</span>
                      <span className="block truncate text-[12px] text-faint">마지막 {dayLabel(row.date)}</span>
                    </span>
                    <Trend values={row.trend} />
                    <span className="w-16 shrink-0 text-right">
                      {row.best !== null ? (
                        <>
                          <span className="font-score tnum block text-[22px] leading-none font-semibold text-foreground">
                            {Math.round(row.best)}
                          </span>
                          <span className="block text-[11px] text-faint">1RM kg</span>
                        </>
                      ) : (
                        <span className="text-[12px] text-faint">맨몸</span>
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

/** The last few sessions' 1RM as a line, the latest end marked. Nothing for a single point. */
function Trend({ values }: { values: number[] }) {
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
      aria-label={`추정 1RM ${values.map((v) => Math.round(v)).join(", ")}kg`}
      className="shrink-0"
    >
      <polyline
        points={pts.map(([x, y]) => `${x},${y}`).join(" ")}
        fill="none"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
        className={up ? "stroke-approve/70" : "stroke-dim/60"}
      />
      <circle cx={lx} cy={ly} r="2.25" className={up ? "fill-approve" : "fill-dim"} />
    </svg>
  );
}
