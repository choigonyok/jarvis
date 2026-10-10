"use client";

import { Panel } from "@/components/workout/panel";
import {
  bestSet,
  dayLabel,
  exerciseHistory,
  groupOf,
  heaviest,
  kg,
  load,
  setLabel,
  type Session,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * One movement over time.
 *
 * The question this answers is "is this lift going up", so the head of the
 * sheet is a row of bars - the estimated 1RM of each session, oldest on the
 * left - and under it the sessions themselves, set by set, for when the
 * trend raises a "what did I actually do that day".
 */
export function ExerciseHistory({
  name,
  sessions,
  onOpenChange,
}: {
  name: string | null;
  sessions: Session[];
  onOpenChange: (open: boolean) => void;
}) {
  const history = name ? exerciseHistory(sessions, name) : [];
  const best = name ? bestSet(sessions, name) : null;
  const top = name ? heaviest(sessions, name) : 0;
  const trend = history.filter((h) => h.oneRm !== null).slice(0, 12).reverse();
  const ceiling = Math.max(...trend.map((h) => h.oneRm ?? 0), 1);
  const floor = Math.min(...trend.map((h) => h.oneRm ?? 0)) * 0.85;

  return (
    <Panel
      open={name !== null}
      onOpenChange={onOpenChange}
      title={name ?? ""}
      description={name ? `${groupOf(name)} · ${history.length}회 기록` : undefined}
    >
      {history.length === 0 ? (
        <p className="py-4 text-[13px] leading-relaxed text-faint">
          아직 끝낸 기록이 없어요. 이 종목을 한 번 마치면 여기에 회차별로 쌓여요.
        </p>
      ) : (
        <>
          <div className="mb-4 flex gap-6">
            {best ? (
              <div>
                <p className="text-[11.5px] text-faint">추정 1RM</p>
                <p className="tnum text-[22px] leading-tight font-semibold text-foreground">
                  {Math.round(best.oneRm)}kg
                </p>
                <p className="tnum text-[11.5px] text-faint">
                  {load(best.weight)}×{best.reps} · {dayLabel(best.date)}
                </p>
              </div>
            ) : null}
            {top > 0 ? (
              <div>
                <p className="text-[11.5px] text-faint">최고 무게</p>
                <p className="tnum text-[22px] leading-tight font-semibold text-foreground">{load(top)}kg</p>
              </div>
            ) : null}
          </div>

          {trend.length > 1 ? (
            <div className="mb-5" role="img" aria-label={`추정 1RM ${trend.map((h) => Math.round(h.oneRm ?? 0)).join(", ")}kg`}>
              <div className="flex h-20 items-end justify-between gap-1">
                {trend.map((h, i) => {
                  const v = h.oneRm ?? 0;
                  const height = ((v - floor) / (ceiling - floor || 1)) * 100;
                  const latest = i === trend.length - 1;
                  return (
                    <span
                      key={h.id}
                      className={cn("max-w-10 flex-1 rounded-t-[2px]", latest ? "bg-foreground/70" : "bg-dim/40")}
                      style={{ height: `${Math.max(height, 6)}%` }}
                    />
                  );
                })}
              </div>
              <div className="tnum mt-1 flex justify-between text-[10.5px] text-faint">
                <span>{dayLabel(trend[0].date)}</span>
                <span>{dayLabel(trend[trend.length - 1].date)}</span>
              </div>
            </div>
          ) : null}

          <ul className="space-y-3">
            {history.map((h) => (
              <li key={h.id}>
                <div className="flex items-baseline justify-between gap-3">
                  <span className="text-[12.5px] text-dim">{dayLabel(h.date)}</span>
                  <span className="tnum text-[11.5px] text-faint">
                    {kg(h.volume)}
                    {h.oneRm ? ` · 1RM ${Math.round(h.oneRm)}` : ""}
                  </span>
                </div>
                <p className="tnum mt-0.5 text-[13px] leading-relaxed text-foreground/85">
                  {h.sets.map((s, i) => (
                    <span key={i} className={cn("me-2 inline-block", s.warmup && "text-faint")}>
                      {s.warmup ? "W " : ""}
                      {setLabel(s)}
                    </span>
                  ))}
                </p>
              </li>
            ))}
          </ul>
        </>
      )}
    </Panel>
  );
}
