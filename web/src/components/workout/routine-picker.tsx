"use client";

import { Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  daysSince,
  lastDone,
  mostOverdue,
  type Routine,
  type Session,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * Which day of the split to train.
 *
 * The decision at the gym door is not "what exercises" - that was settled
 * when the split was written - it is "which day am I owed". So the routines
 * are ordered by how long it has been, and the one you have gone longest
 * without is marked. Nothing is hidden or auto-chosen: a split is a plan, not
 * a rule, and skipping legs again should be a decision you can see yourself
 * making.
 */
export function RoutinePicker({
  routines,
  sessions,
  onStart,
  onFree,
}: {
  routines: Routine[];
  sessions: Session[];
  onStart: (routine: Routine) => void;
  onFree: () => void;
}) {
  const overdue = mostOverdue(sessions, routines);

  const ranked = routines
    .map((routine) => {
      const last = lastDone(sessions, routine);
      return { routine, last, days: daysSince(last) };
    })
    .sort((a, b) => (b.days ?? Infinity) - (a.days ?? Infinity));

  return (
    <div>
      <ul className="space-y-1.5">
        {ranked.map(({ routine, days }) => {
          const due = routine.id === overdue?.id;
          return (
            <li key={routine.id}>
              <button
                type="button"
                onClick={() => onStart(routine)}
                className={cn(
                  "flex w-full items-center gap-3 rounded-xl border px-4 py-3 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  due
                    ? "border-approve/30 bg-approve/8 hover:bg-approve/12"
                    : "border-edge-soft bg-glass hover:bg-glass-raised",
                )}
              >
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[14.5px] text-foreground">
                    {routine.name}
                  </p>
                  <p className="mt-0.5 truncate text-[11.5px] text-faint">
                    {routine.exercises.join(" · ")}
                  </p>
                </div>

                <div className="shrink-0 text-right">
                  <p
                    className={cn(
                      "tnum text-[12.5px]",
                      due ? "text-approve/85" : "text-dim",
                    )}
                  >
                    {days === null
                      ? "한 적 없음"
                      : days === 0
                        ? "오늘"
                        : `${days}일 전`}
                  </p>
                  {due ? (
                    <p className="mt-0.5 text-[10.5px] text-approve/70">
                      가장 오래됨
                    </p>
                  ) : null}
                </div>

                <Play aria-hidden className="size-4 shrink-0 text-faint" />
              </button>
            </li>
          );
        })}
      </ul>

      <Button
        variant="ghost"
        onClick={onFree}
        className="mt-2 h-11 w-full text-[12.5px] text-faint hover:text-foreground"
      >
        루틴 없이 시작
      </Button>
    </div>
  );
}
