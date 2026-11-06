"use client";

import { Button } from "@/components/ui/button";
import { Play } from "lucide-react";
import { daysSince, lastDone, mostOverdue, type Routine, type Session } from "@/lib/workout";
import { cn } from "@/lib/utils";

const ago = (days: number | null) =>
  days === null ? "한 적 없음" : days === 0 ? "오늘" : days === 1 ? "어제" : `${days}일 전`;

/**
 * Which day of the split to train.
 *
 * The decision at the gym door is not "what exercises" - that was settled
 * when the split was written - it is "which day am I owed". So the day you
 * have gone longest without is the one thing on this screen set large, with
 * its start button under the thumb; the rest of the split follows as plain
 * rows. Nothing is auto-chosen: skipping legs again should be a decision you
 * can see yourself making.
 */
export function RoutinePicker({
  routines,
  sessions,
  onStart,
  onFree,
  onRepeat,
  onEdit,
}: {
  routines: Routine[];
  sessions: Session[];
  onStart: (routine: Routine) => void;
  onFree: () => void;
  onRepeat: (() => void) | null;
  onEdit: () => void;
}) {
  const overdue = mostOverdue(sessions, routines);
  const ranked = routines
    .map((routine) => ({ routine, days: daysSince(lastDone(sessions, routine)) }))
    .sort((a, b) => (b.days ?? Infinity) - (a.days ?? Infinity));
  const lead = ranked.find((r) => r.routine.id === overdue?.id) ?? ranked[0];
  const others = ranked.filter((r) => r !== lead);

  // The way to anything other than a routine: the faintest words here.
  const quiet = "px-2.5 text-aside text-faint";

  return (
    <div>
      {lead ? (
        <div className="mb-4">
          <p className="text-aside text-dim">
            {lead.days === null
              ? "아직 한 번도 안 한 루틴이에요"
              : lead.days === 0
                ? "오늘 이미 했어요"
                : `${lead.days}일째 쉬고 있어요`}
          </p>
          <h1 className="mt-1 text-display leading-[1.15] font-semibold tracking-tight text-foreground">
            {lead.routine.name}
          </h1>
          <p className="mt-1.5 text-ui leading-relaxed text-dim">
            {lead.routine.exercises.join(" · ") || "종목 없음"}
          </p>
          <button
            type="button"
            onClick={() => onStart(lead.routine)}
            className="mt-4 flex h-12 w-full items-center justify-center gap-2 rounded-xl bg-approve-wash text-title font-medium text-approve transition-colors focus-ring sm:w-auto sm:px-8"
          >
            <Play aria-hidden className="size-4" />
            시작
          </button>
        </div>
      ) : null}

      {others.length > 0 ? (
        <ul className="space-y-px" aria-label="다른 루틴">
          {others.map(({ routine, days }) => (
            <li key={routine.id}>
              <button
                type="button"
                onClick={() => onStart(routine)}
                className="flex min-h-12 w-full items-center gap-3 rounded-lg px-2 text-left transition-colors hover:bg-glass focus-ring"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-body text-ink">{routine.name}</span>
                  <span className="block truncate text-meta text-faint">{routine.exercises.join(" · ")}</span>
                </span>
                <span className={cn("tnum shrink-0 text-aside", days === null ? "text-faint" : "text-dim")}>
                  {ago(days)}
                </span>
                <Play aria-hidden className="size-3.5 shrink-0 text-faint" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      <div className="-ms-2.5 mt-1 flex flex-wrap items-center">
        <Button variant="quiet" size="touch-sm" onClick={onFree} className={quiet}>
          빈 운동
        </Button>
        {onRepeat ? (
          <Button variant="quiet" size="touch-sm" onClick={onRepeat} className={quiet}>
            지난 운동 반복
          </Button>
        ) : null}
        <Button variant="quiet" size="touch-sm" onClick={onEdit} className={quiet}>
          루틴 편집
        </Button>
      </div>
    </div>
  );
}
