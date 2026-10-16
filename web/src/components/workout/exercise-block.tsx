"use client";

import { Check, ChevronDown, Flame, Minus, Plus, Trash2 } from "lucide-react";
import { quietButton } from "@/components/workout/panel";
import { VolumeBar } from "@/components/workout/volume-bar";
import {
  kg,
  load,
  previousFor,
  setLabel,
  volumeOf,
  estimate1RM,
  type Exercise,
  type SetRef,
  type WorkoutSet,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * The whole session as bars - one per set, grouped by movement, warm-ups
 * narrower. Filled is lifted, white is the set the dock is on. It answers
 * "how far through am I" without a percentage, and each group is a button
 * to that movement.
 */
export function SetMap({
  exercises,
  focus,
  onJump,
}: {
  exercises: Exercise[];
  focus: SetRef | null;
  onJump: (exercise: Exercise) => void;
}) {
  return (
    <nav aria-label="세트 진행" className="-mx-1.5 flex flex-wrap">
      {exercises.map((exercise) => {
        const working = exercise.sets.filter((s) => !s.warmup);
        const done = working.filter((s) => s.done).length;
        const here = focus?.exerciseId === exercise.id;
        return (
          <button
            key={exercise.id}
            type="button"
            onClick={() => onJump(exercise)}
            aria-label={`${exercise.name}, ${working.length}세트 중 ${done}세트 완료`}
            aria-current={here ? "step" : undefined}
            className="flex h-11 items-center gap-[3px] rounded-lg px-1.5 outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            {exercise.sets.map((set, i) => (
              <span
                key={i}
                aria-hidden
                className={cn(
                  "h-2.5 rounded-[2px] transition-colors motion-reduce:transition-none",
                  set.warmup ? "w-1.5" : "w-4",
                  here && focus?.index === i
                    ? "bg-foreground"
                    : set.done
                      ? "bg-approve/75"
                      : "bg-foreground/14",
                )}
              />
            ))}
          </button>
        );
      })}
    </nav>
  );
}

/** A movement that is not open: how far along it is, and what is next on it. */
export function ExerciseLine({
  exercise,
  onOpen,
}: {
  exercise: Exercise;
  onOpen: () => void;
}) {
  const working = exercise.sets.filter((s) => !s.warmup);
  const done = working.filter((s) => s.done).length;
  const complete = working.length > 0 && done === working.length;
  const next = exercise.sets.find((s) => !s.done);
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-expanded={false}
      className="flex min-h-14 w-full items-center gap-3 px-1 py-2 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span
        className={cn(
          "font-score tnum flex w-9 shrink-0 justify-center text-[18px] leading-none font-semibold",
          complete
            ? "text-approve"
            : done > 0
              ? "text-foreground"
              : "text-faint",
        )}
      >
        {complete ? (
          <Check aria-hidden className="size-[18px]" />
        ) : (
          `${done}/${working.length}`
        )}
      </span>
      <span className="min-w-0 flex-1">
        <span
          className={cn(
            "block truncate text-[15px]",
            complete ? "text-dim" : "text-foreground",
          )}
        >
          {exercise.name}
        </span>
        <span className="tnum block truncate text-[12px] text-faint">
          {complete
            ? `${working.length}세트 · ${kg(volumeOf(exercise))}`
            : next
              ? `다음 ${setLabel(next)}`
              : "세트 없음"}
        </span>
      </span>
      <ChevronDown aria-hidden className="size-4 shrink-0 text-faint" />
    </button>
  );
}

export function ExerciseBlock({
  exercise,
  previous,
  previousVolume,
  best,
  top,
  focus,
  editing,
  onEditing,
  onFocus,
  onToggle,
  onRemoveSet,
  onAddSet,
  onHistory,
  onRemove,
}: {
  exercise: Exercise;
  previous: WorkoutSet[] | null;
  previousVolume: number | null;
  best: { weight: number; reps: number; oneRm: number; date: string } | null;
  top: number;
  focus: number | null;
  editing: boolean;
  onEditing: (on: boolean) => void;
  onFocus: (index: number) => void;
  onToggle: (index: number) => void;
  onRemoveSet: (index: number) => void;
  onAddSet: (warmup: boolean) => void;
  onHistory: () => void;
  onRemove: () => void;
}) {
  const { sets } = exercise;
  // A record is worth marking as it happens - that is the moment, not a line
  // in a report afterwards. A first-ever session has nothing to beat.
  const working = sets.filter((s) => s.done && !s.warmup);
  const beats =
    (top > 0 && working.some((s) => s.weight > top)) ||
    (best !== null &&
      working.some(
        (s) => (estimate1RM(s.weight, s.reps) ?? 0) > best.oneRm + 0.05,
      ));

  // Warm-ups are W, working sets count 1, 2, 3 among themselves - a ramp
  // in front should not make the first real set "set 4".
  const labels = sets.map((set, i) => {
    const nth = sets
      .slice(0, i + 1)
      .filter((s) => Boolean(s.warmup) === Boolean(set.warmup)).length;
    return set.warmup
      ? { short: "W", long: `워밍업 ${nth}` }
      : { short: String(nth), long: `${nth}세트` };
  });

  return (
    <section aria-label={exercise.name} className="pt-2 pb-4">
      <div className="flex items-center justify-between gap-2">
        <button
          type="button"
          onClick={onHistory}
          className="-ms-1 flex min-h-11 min-w-0 items-center gap-2 rounded-lg px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <span className="truncate text-[17px] font-semibold text-foreground underline decoration-edge underline-offset-4">
            {exercise.name}
          </span>
          {beats ? (
            <span className="flex shrink-0 items-center gap-1 text-[12px] text-approve">
              <Flame aria-hidden className="size-3.5" />
              최고 기록
            </span>
          ) : null}
        </button>
        {/* Deleting is a mode, not a row of bins beside every tick: the
            control you press every set should not sit next to one that
            throws a set away. */}
        <button
          type="button"
          onClick={() => onEditing(!editing)}
          aria-pressed={editing}
          className={cn(
            "tap -me-2 shrink-0 rounded-lg px-3 text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
            editing
              ? "bg-glass-raised text-foreground"
              : "text-dim hover:text-foreground",
          )}
        >
          {editing ? "마침" : "편집"}
        </button>
      </div>

      {best ? (
        <p className="tnum mb-2 text-[12px] text-faint">
          최고 {setLabel(best)} · 추정 1RM {Math.round(best.oneRm)}kg
        </p>
      ) : null}

      {/* Column heads, once, so the numbers below need no units of their own. */}
      <div
        aria-hidden
        className="grid grid-cols-[1.75rem_1fr_4.25rem_3rem_2.75rem] items-center gap-2 px-1 pb-1 text-[11.5px] text-faint"
      >
        <span className="text-center">세트</span>
        <span>지난번</span>
        <span className="text-right">kg</span>
        <span className="text-right">회</span>
        <span />
      </div>

      <ul className="space-y-0.5">
        {sets.map((set, i) => {
          const label = labels[i];
          const before = previousFor(previous, sets, i);
          const focused = focus === i;
          return (
            <li
              key={i}
              id={`set-${exercise.id}-${i}`}
              className="scroll-mt-20 scroll-mb-[calc(var(--tabbar-space)+var(--dock-h,16rem)+1rem)]"
            >
              <div
                className={cn(
                  "relative flex items-center gap-2 rounded-lg transition-colors",
                  focused && !editing ? "bg-glass-raised" : "",
                )}
              >
                {focused && !editing ? (
                  <span
                    aria-hidden
                    className="absolute inset-y-2 left-0 w-[2px] rounded-full bg-foreground/80"
                  />
                ) : null}
                <button
                  type="button"
                  onClick={() => onFocus(i)}
                  aria-label={`${label.long} ${load(set.weight)}kg ${set.reps}회${set.done ? ", 완료" : ""}`}
                  aria-current={focused || undefined}
                  className="grid min-h-12 flex-1 grid-cols-[1.75rem_1fr_4.25rem_3rem] items-center gap-2 rounded-lg px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  <span
                    className={cn(
                      "tnum text-center text-[13px]",
                      set.warmup ? "text-dim" : "text-faint",
                    )}
                  >
                    {label.short}
                  </span>
                  <span className="tnum truncate text-[12.5px] text-faint">
                    {before ? setLabel(before) : "—"}
                  </span>
                  <span
                    className={cn(
                      "font-score tnum text-right text-[20px] leading-none font-semibold",
                      set.done ? "text-dim" : "text-foreground",
                    )}
                  >
                    {set.weight > 0 ? load(set.weight) : "—"}
                  </span>
                  <span
                    className={cn(
                      "font-score tnum text-right text-[20px] leading-none font-semibold",
                      set.done ? "text-dim" : "text-foreground",
                    )}
                  >
                    {set.reps}
                  </span>
                </button>
                {editing ? (
                  <button
                    type="button"
                    onClick={() => onRemoveSet(i)}
                    aria-label={`${label.long} 삭제`}
                    className="flex size-11 shrink-0 items-center justify-center rounded-lg bg-reject/12 text-reject transition-colors outline-none hover:bg-reject/20 focus-visible:ring-3 focus-visible:ring-ring/50"
                  >
                    <Minus aria-hidden className="size-[18px]" />
                  </button>
                ) : (
                  // Ticking a set is the most-pressed control in the app, so
                  // it is the only one in the list that carries the approve hue.
                  <button
                    type="button"
                    onClick={() => onToggle(i)}
                    aria-pressed={set.done}
                    aria-label={`${label.long} 완료`}
                    className={cn(
                      "flex size-11 shrink-0 items-center justify-center rounded-lg transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                      set.done
                        ? "bg-approve/15 text-approve"
                        : "text-faint/70 hover:text-dim",
                    )}
                  >
                    <Check aria-hidden className="size-[18px]" />
                  </button>
                )}
              </div>
            </li>
          );
        })}
      </ul>

      {editing ? (
        <div className="mt-2 flex justify-end">
          <button
            type="button"
            onClick={onRemove}
            className={cn(
              quietButton,
              "flex items-center gap-1.5 text-faint hover:text-reject",
            )}
          >
            <Trash2 aria-hidden className="size-4" />
            {exercise.name} 빼기
          </button>
        </div>
      ) : (
        <>
          <div className="mt-1 flex gap-2">
            <button
              type="button"
              onClick={() => onAddSet(false)}
              className="tap flex flex-1 items-center justify-center gap-1.5 rounded-lg text-[13px] text-faint transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <Plus aria-hidden className="size-3.5" />
              세트
            </button>
            <button
              type="button"
              onClick={() => onAddSet(true)}
              className="tap flex flex-1 items-center justify-center gap-1.5 rounded-lg text-[13px] text-faint transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <Plus aria-hidden className="size-3.5" />
              워밍업
            </button>
          </div>
          {/* A bodyweight movement has no kilograms to compare. */}
          {volumeOf(exercise) > 0 || previousVolume ? (
            <VolumeBar
              volume={volumeOf(exercise)}
              previous={previousVolume}
              className="mt-1"
            />
          ) : null}
        </>
      )}
    </section>
  );
}
