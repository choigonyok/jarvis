"use client";

import { useEffect, useRef, useState } from "react";
import {
  Check,
  Flame,
  Minus,
  Pause,
  Play,
  Plus,
  RotateCcw,
  Trash2,
  X,
} from "lucide-react";
import { VolumeBar } from "@/components/workout/volume-bar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  COMMON_EXERCISES,
  bestSet,
  clock,
  doneSets,
  durationOf,
  estimate1RM,
  heaviest,
  kg,
  previousVolume,
  sessionVolume,
  volumeOf,
  type Session,
  type WorkoutSet,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

/** What people actually rest for, by what they are training. */
const REST_PRESETS = [60, 90, 120, 180];

/**
 * A workout in progress.
 *
 * Written for hands that are sweaty and a mind that is counting: the elapsed
 * clock is the largest thing on screen, the rest timer starts itself the
 * moment a set is ticked off, and every control clears 44px. Nothing here
 * needs a second tap to confirm - a mis-tick is undone by tapping again.
 */
export function LiveSession({
  session,
  history,
  restTarget,
  onRestTarget,
  onAddExercise,
  onPatchSets,
  onRemoveExercise,
  onNote,
  onBodyWeight,
  onEnd,
}: {
  session: Session;
  history: Session[];
  restTarget: number;
  onRestTarget: (seconds: number) => void;
  onAddExercise: (name: string) => void;
  onPatchSets: (exerciseId: string, fn: (sets: WorkoutSet[]) => WorkoutSet[]) => void;
  onRemoveExercise: (exerciseId: string) => void;
  onNote: (note: string) => void;
  onBodyWeight: (weight: number) => void;
  onEnd: () => void;
}) {
  const [elapsed, setElapsed] = useState(() => durationOf(session));
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");

  useEffect(() => {
    const id = setInterval(() => setElapsed(durationOf(session)), 1000);
    return () => clearInterval(id);
  }, [session]);

  const volume = sessionVolume(session);
  const sets = doneSets(session);
  const earlier = history.filter((s) => s.id !== session.id);

  function add(value: string) {
    const trimmed = value.trim();
    if (!trimmed) return;
    onAddExercise(trimmed);
    setName("");
    setAdding(false);
  }

  return (
    <div className="pb-4">
      {/* The clock is the hero while a session is running - it is the number
          you keep glancing at, and the totals underneath are the ones you
          check between exercises. */}
      <div className="mb-5 flex items-end justify-between gap-4">
        <div>
          <p className="text-[12px] text-faint">
            {session.routineName ?? "운동 시간"}
          </p>
          <p className="tnum mt-0.5 text-[38px] leading-none font-semibold tracking-tight text-foreground tabular-nums sm:text-[44px]">
            {clock(elapsed)}
          </p>
          <p className="tnum mt-2 text-[12.5px] text-dim">
            {kg(volume)} · {sets}세트
          </p>
        </div>
        <Button
          variant="outline"
          onClick={onEnd}
          className="h-11 shrink-0 border-edge bg-glass-raised px-4 text-[13.5px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve dark:bg-glass-raised dark:hover:bg-approve/12"
        >
          운동 끝내기
        </Button>
      </div>

      <RestTimer key={sets} target={restTarget} onTarget={onRestTarget} />

      <div className="mt-6 space-y-6">
        {session.exercises.map((exercise) => (
          <ExerciseBlock
            key={exercise.id}
            name={exercise.name}
            sets={exercise.sets}
            volume={volumeOf(exercise)}
            previous={previousVolume(history, exercise.name, session.id)}
            best={bestSet(earlier, exercise.name)}
            top={heaviest(earlier, exercise.name)}
            onPatch={(fn) => onPatchSets(exercise.id, fn)}
            onRemove={() => onRemoveExercise(exercise.id)}
          />
        ))}
      </div>

      {adding ? (
        <div className="mt-5">
          <div className="flex items-center gap-2">
            <Input
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.nativeEvent.isComposing) add(name);
                if (e.key === "Escape") setAdding(false);
              }}
              placeholder="종목 이름"
              aria-label="추가할 운동 종목"
              className="h-11 flex-1 border-edge bg-well/40"
            />
            <Button
              onClick={() => add(name)}
              disabled={!name.trim()}
              className="h-11 px-4 text-[13.5px]"
            >
              추가
            </Button>
            <Button
              variant="ghost"
              onClick={() => setAdding(false)}
              aria-label="취소"
              className="tap text-faint hover:text-dim"
            >
              <X aria-hidden className="size-4" />
            </Button>
          </div>

          {/* The lifts most people actually log, so the common case is a tap
              instead of typing Korean on a phone mid-set. */}
          <div className="mt-2.5 flex flex-wrap gap-1.5">
            {COMMON_EXERCISES.map((preset) => (
              <button
                key={preset.name}
                type="button"
                onClick={() => add(preset.name)}
                className="min-h-9 rounded-lg border border-edge-soft bg-glass px-3 text-[12.5px] text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                {preset.name}
              </button>
            ))}
          </div>
        </div>
      ) : (
        <Button
          variant="outline"
          onClick={() => setAdding(true)}
          className="mt-5 h-12 w-full border-dashed border-edge bg-transparent text-[13.5px] text-dim hover:text-foreground dark:bg-transparent"
        >
          <Plus aria-hidden className="size-4" />
          종목 추가
        </Button>
      )}

      <section className="mt-7 space-y-3 border-t border-edge-soft pt-5">
        <label className="block">
          <span className="mb-1.5 block text-[11.5px] text-faint">오늘 체중</span>
          <div className="flex items-center gap-2">
            <Input
              type="number"
              inputMode="decimal"
              defaultValue={session.bodyWeight || ""}
              onBlur={(e) => onBodyWeight(Number(e.target.value) || 0)}
              placeholder="0"
              className="tnum h-11 w-28 border-edge bg-well/40 text-center"
            />
            <span className="text-[12px] text-faint">kg</span>
          </div>
        </label>

        <label className="block">
          <span className="mb-1.5 block text-[11.5px] text-faint">메모</span>
          <Input
            defaultValue={session.note ?? ""}
            onBlur={(e) => onNote(e.target.value)}
            placeholder="컨디션, 통증, 바꾼 것"
            className="h-11 border-edge bg-well/40"
          />
        </label>
      </section>
    </div>
  );
}

/**
 * Rest between sets.
 *
 * Counts down to a target rather than up from zero: resting is something you
 * finish, and a number climbing forever cannot tell you when. It remounts
 * whenever a set is ticked off (the `key` upstream), which is what makes it
 * start on its own - having to press start after every set is what makes
 * people stop using a timer at all.
 */
function RestTimer({
  target,
  onTarget,
}: {
  target: number;
  onTarget: (seconds: number) => void;
}) {
  const [elapsed, setElapsed] = useState(0);
  const [running, setRunning] = useState(true);
  const rang = useRef(false);

  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => setElapsed((s) => s + 1), 1000);
    return () => clearInterval(id);
  }, [running]);

  const left = target - elapsed;
  const over = left <= 0;

  // A buzz when the rest is up, once. The phone is in a pocket or on the
  // bench and you are not watching the screen - which is the whole point of
  // setting a target.
  useEffect(() => {
    if (over && !rang.current) {
      rang.current = true;
      navigator.vibrate?.([120, 60, 120]);
    }
  }, [over]);

  return (
    <div className="rounded-xl border border-edge-soft bg-glass px-3 py-2.5">
      <div className="flex items-center gap-3">
        <span className="text-[11.5px] text-faint">휴식</span>
        <span
          className={cn(
            "tnum flex-1 text-[18px] leading-none tabular-nums",
            over ? "text-approve" : "text-foreground",
          )}
        >
          {over ? `+${clock(-left * 1000)}` : clock(left * 1000)}
        </span>
        <button
          type="button"
          onClick={() => setRunning((r) => !r)}
          aria-label={running ? "휴식 타이머 멈춤" : "휴식 타이머 시작"}
          className="tap flex items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          {running ? (
            <Pause aria-hidden className="size-4" />
          ) : (
            <Play aria-hidden className="size-4" />
          )}
        </button>
        <button
          type="button"
          onClick={() => {
            setElapsed(0);
            rang.current = false;
            setRunning(true);
          }}
          aria-label="휴식 타이머 처음으로"
          className="tap flex items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <RotateCcw aria-hidden className="size-4" />
        </button>
      </div>

      <div className="mt-2 flex gap-1.5" role="radiogroup" aria-label="휴식 목표">
        {REST_PRESETS.map((seconds) => (
          <button
            key={seconds}
            type="button"
            role="radio"
            aria-checked={target === seconds}
            onClick={() => onTarget(seconds)}
            className={cn(
              "min-h-8 flex-1 rounded-md text-[12px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              target === seconds
                ? "bg-glass-raised text-foreground"
                : "text-faint hover:text-dim",
            )}
          >
            {seconds < 60 ? `${seconds}초` : `${seconds / 60}분`}
          </button>
        ))}
      </div>
    </div>
  );
}

function ExerciseBlock({
  name,
  sets,
  volume,
  previous,
  best,
  top,
  onPatch,
  onRemove,
}: {
  name: string;
  sets: WorkoutSet[];
  volume: number;
  previous: number | null;
  best: { weight: number; reps: number; oneRm: number; date: string } | null;
  top: number;
  onPatch: (fn: (sets: WorkoutSet[]) => WorkoutSet[]) => void;
  onRemove: () => void;
}) {
  // A new set inherits the last working set's load: the common case is the
  // same weight again, and retyping it every time is the tax this removes.
  function addSet(warmup = false) {
    onPatch((current) => {
      const last = [...current].reverse().find((s) => !s.warmup) ?? current.at(-1);
      return [
        ...current,
        {
          weight: warmup ? 0 : (last?.weight ?? 0),
          reps: warmup ? 0 : (last?.reps ?? 0),
          done: false,
          ...(warmup ? { warmup: true } : {}),
        },
      ];
    });
  }

  // A working set that beats the heaviest ever is worth marking as it
  // happens - that is the moment, not a line in a report afterwards.
  const beatsTop = sets.some((s) => s.done && !s.warmup && top > 0 && s.weight > top);

  return (
    <section>
      {/* items-center, not baseline: a 44px target has no baseline worth
          aligning to and would ride up above the heading. */}
      <div className="mb-1.5 flex items-center justify-between gap-3">
        <h3 className="flex min-w-0 items-center gap-2 text-[14.5px] font-medium text-foreground">
          <span className="truncate">{name}</span>
          {beatsTop ? (
            <span className="flex shrink-0 items-center gap-1 text-[11px] text-approve">
              <Flame aria-hidden className="size-3" />
              최고 기록
            </span>
          ) : null}
        </h3>
        <button
          type="button"
          onClick={onRemove}
          aria-label={`${name} 빼기`}
          className="-mr-2 flex size-11 shrink-0 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-reject focus-visible:ring-3 focus-visible:ring-ring/50 sm:-mr-1 sm:size-8"
        >
          <Trash2 aria-hidden className="size-4" />
        </button>
      </div>

      {/* Your best with this movement, before you decide today's load. This
          is the line people open a second app to find. */}
      {best ? (
        <p className="tnum mb-2 text-[11.5px] text-faint">
          최고 {kg(best.weight)}×{best.reps}
          <span className="ms-1.5">추정 1RM {Math.round(best.oneRm)}kg</span>
        </p>
      ) : null}

      <VolumeBar volume={volume} previous={previous} className="mb-2.5" />

      <ul className="space-y-1.5">
        {sets.map((set, i) => {
          const oneRm = estimate1RM(set.weight, set.reps);
          return (
            <li key={i} className="flex items-center gap-2">
              {/* The row number doubles as the warm-up switch. A warm-up is
                  work you did but not work that counts, and every serious log
                  keeps the two apart - counting empty-bar sets into weekly
                  volume makes the number useless for deciding what to lift. */}
              <button
                type="button"
                onClick={() =>
                  onPatch((c) =>
                    c.map((s, j) => (j === i ? { ...s, warmup: !s.warmup } : s)),
                  )
                }
                aria-label={
                  set.warmup ? `${i + 1}번 워밍업 해제` : `${i + 1}번을 워밍업으로`
                }
                aria-pressed={set.warmup ?? false}
                className={cn(
                  "tnum flex h-11 w-7 shrink-0 items-center justify-center rounded-md text-[12px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  set.warmup ? "text-dim" : "text-faint",
                )}
              >
                {set.warmup ? "W" : i + 1}
              </button>

              <label className="flex min-w-0 flex-1 items-center gap-1.5">
                <span className="sr-only">{`${i + 1}세트 중량 (kg)`}</span>
                <Input
                  type="number"
                  inputMode="decimal"
                  value={set.weight || ""}
                  onChange={(e) =>
                    onPatch((c) =>
                      c.map((s, j) =>
                        j === i ? { ...s, weight: Number(e.target.value) || 0 } : s,
                      ),
                    )
                  }
                  placeholder="0"
                  className="tnum h-11 min-w-0 border-edge bg-well/40 text-center"
                />
                <span className="shrink-0 text-[12px] text-faint">kg</span>
              </label>

              <label className="flex min-w-0 flex-1 items-center gap-1.5">
                <span className="sr-only">{`${i + 1}세트 횟수`}</span>
                <Input
                  type="number"
                  inputMode="numeric"
                  value={set.reps || ""}
                  onChange={(e) =>
                    onPatch((c) =>
                      c.map((s, j) =>
                        j === i ? { ...s, reps: Number(e.target.value) || 0 } : s,
                      ),
                    )
                  }
                  placeholder="0"
                  className="tnum h-11 min-w-0 border-edge bg-well/40 text-center"
                />
                <span className="shrink-0 text-[12px] text-faint">회</span>
              </label>

              {/* Ticking a set is the most-pressed control in the app, so it is
                  the biggest and the only one that carries the approve hue. */}
              <button
                type="button"
                onClick={() =>
                  onPatch((c) =>
                    c.map((s, j) => (j === i ? { ...s, done: !s.done } : s)),
                  )
                }
                aria-pressed={set.done}
                aria-label={`${i + 1}세트 완료`}
                className={cn(
                  "flex size-11 shrink-0 items-center justify-center rounded-lg border transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  set.done
                    ? "border-approve/40 bg-approve/15 text-approve"
                    : "border-edge bg-glass text-faint hover:text-dim",
                )}
              >
                <Check aria-hidden className="size-[18px]" />
              </button>

              {/* Removing a mis-added set. Quiet, because it sits beside the
                  control you press every set and must not be the one your
                  thumb finds by accident. */}
              <button
                type="button"
                onClick={() => onPatch((c) => c.filter((_, j) => j !== i))}
                aria-label={`${i + 1}세트 삭제`}
                className="flex size-9 shrink-0 items-center justify-center rounded-lg text-faint/70 transition-colors outline-none hover:text-reject focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <Minus aria-hidden className="size-4" />
              </button>

              <span className="tnum hidden w-12 shrink-0 text-right text-[11px] text-faint sm:block">
                {oneRm && !set.warmup ? `${Math.round(oneRm)}kg` : ""}
              </span>
            </li>
          );
        })}
      </ul>

      <div className="mt-1.5 flex gap-1.5">
        <Button
          variant="ghost"
          onClick={() => addSet(false)}
          className="h-10 flex-1 text-[12.5px] text-faint hover:text-foreground"
        >
          <Plus aria-hidden className="size-3.5" />
          세트
        </Button>
        <Button
          variant="ghost"
          onClick={() => addSet(true)}
          className="h-10 flex-1 text-[12.5px] text-faint hover:text-foreground"
        >
          <Plus aria-hidden className="size-3.5" />
          워밍업
        </Button>
      </div>
    </section>
  );
}
