"use client";

import { useEffect, useRef, useState } from "react";
import { Plus } from "lucide-react";
import { ExerciseHistory } from "@/components/workout/exercise-history";
import { ExercisePicker } from "@/components/workout/exercise-picker";
import { quietButton } from "@/components/workout/panel";
import { Input } from "@/components/ui/input";
import { local, primeChime, useNow, useWakeLock } from "@/lib/use-gym";
import {
  bestSet,
  clock,
  heaviest,
  kg,
  nextOpen,
  overloadHint,
  previousFor,
  previousSets,
  previousVolume,
  sessionVolume,
  warmupRamp,
  type Exercise,
  type Session,
  type SetRef,
  type WorkoutSet,
} from "@/lib/workout";
import { type Rest } from "@/components/workout/live-shared";
import { cn } from "@/lib/utils";
import {
  SetMap,
  ExerciseLine,
  ExerciseBlock,
} from "@/components/workout/exercise-block";
import { Dock } from "@/components/workout/dock";
import { FinishPanel } from "@/components/workout/finish-panel";

/** What people actually rest for, by what they are training. */
const REST_PRESETS = [60, 90, 120, 180];

/** Past this with nothing ticked, the session was most likely left open. */
const STALE_MS = 3 * 60 * 60 * 1000;

const REST_KEY = "jarvis.workout.rest";
const activeKey = (id: string) => `jarvis.workout.active.${id}`;


/** Only ever called from handlers - kept out of the component so the purity lint can see that. */
const stamp = () => Date.now();

/**
 * A workout in progress.
 *
 * The list is for reading - what is planned, what is done, what last time
 * was. Everything you do between sets happens in the dock at the bottom,
 * under the thumb: it always holds the set you are about to lift, with the
 * load already filled in from last time, so a set is logged with one press.
 * After that press the rest countdown takes the top of the dock and the
 * next set moves in underneath it.
 */
export function LiveSession({
  session,
  history,
  restTarget,
  onRestTarget,
  onAddExercise,
  onInsertExercise,
  onPatchSets,
  onRemoveExercise,
  onNote,
  onBodyWeight,
  onEnd,
  onDiscard,
  onUndoable,
  notice,
}: {
  session: Session;
  history: Session[];
  restTarget: number;
  onRestTarget: (seconds: number) => void;
  onAddExercise: (name: string) => void;
  onInsertExercise: (exercise: Exercise, at: number) => void;
  onPatchSets: (
    exerciseId: string,
    fn: (sets: WorkoutSet[]) => WorkoutSet[],
  ) => void;
  onRemoveExercise: (exerciseId: string) => void;
  onNote: (note: string) => void;
  onBodyWeight: (weight: number) => void;
  onEnd: (at?: number) => void;
  onDiscard: () => void;
  onUndoable: (label: string, restore: () => void) => void;
  /** The undo toast, shown on top of the dock rather than under it. */
  notice?: React.ReactNode;
}) {
  const now = useNow();
  useWakeLock(true);

  const [picking, setPicking] = useState(false);
  const [looking, setLooking] = useState<string | null>(null);
  const [finishing, setFinishing] = useState(false);
  const [chosen, setChosen] = useState<SetRef | null>(null);
  const [editing, setEditing] = useState<string | null>(null);

  const [rest, setRest] = useState<Rest | null>(() => {
    const saved = local.get<Rest>(REST_KEY);
    return saved?.sessionId === session.id ? saved : null;
  });
  const [lastActive, setLastActive] = useState<number | null>(() =>
    local.get<number>(activeKey(session.id)),
  );
  const [staleDismissed, setStaleDismissed] = useState(false);

  useEffect(() => local.set(REST_KEY, rest), [rest]);

  const valid = (ref: SetRef | null) =>
    ref !== null &&
    (session.exercises.find((e) => e.id === ref.exerciseId)?.sets.length ?? 0) >
      ref.index;
  const focus = valid(chosen) ? chosen : nextOpen(session);
  const focusExercise = focus
    ? session.exercises.find((e) => e.id === focus.exerciseId)
    : undefined;

  const volume = sessionVolume(session);
  const earlier = history.filter((s) => s.id !== session.id);
  const since = lastActive ?? session.startedAt;
  const stale = !staleDismissed && now - since > STALE_MS;

  function touch() {
    const at = stamp();
    local.set(activeKey(session.id), at);
    setLastActive(at);
  }

  function reveal(ref: SetRef | null) {
    if (!ref) return;
    // Two frames: a tick also starts a rest, which makes the dock taller, and
    // the row has to clear the dock as it will be - its new height is only
    // published after the frame that lays it out.
    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        document
          .getElementById(`set-${ref.exerciseId}-${ref.index}`)
          ?.scrollIntoView({
            block: "nearest",
            behavior: window.matchMedia("(prefers-reduced-motion: reduce)")
              .matches
              ? "auto"
              : "smooth",
          });
      }),
    );
  }

  // Coming back to a running session, the set you are on is the first thing
  // to see - not the top of the list with that set somewhere under the dock.
  const opened = useRef(false);
  useEffect(() => {
    if (opened.current) return;
    opened.current = true;
    const ref = valid(chosen) ? chosen : nextOpen(session);
    if (ref)
      requestAnimationFrame(() =>
        document
          .getElementById(`set-${ref.exerciseId}-${ref.index}`)
          ?.scrollIntoView({ block: "nearest" }),
      );
  });

  function toggle(ref: SetRef) {
    const exercise = session.exercises.find((e) => e.id === ref.exerciseId);
    const set = exercise?.sets[ref.index];
    if (!exercise || !set) return;
    const done = !set.done;
    onPatchSets(ref.exerciseId, (c) =>
      c.map((s, j) => (j === ref.index ? { ...s, done } : s)),
    );
    if (!done) {
      setChosen(ref);
      return;
    }
    primeChime();
    touch();
    // A warm-up is followed by the next warm-up, not by a rest.
    if (!set.warmup)
      setRest({
        sessionId: session.id,
        startedAt: stamp(),
        target: restTarget,
      });
    const after: Session = {
      ...session,
      exercises: session.exercises.map((e) =>
        e.id === ref.exerciseId
          ? {
              ...e,
              sets: e.sets.map((s, j) =>
                j === ref.index ? { ...s, done } : s,
              ),
            }
          : e,
      ),
    };
    const next = nextOpen(after, ref);
    setChosen(next);
    reveal(next);
  }

  /**
   * Change one field of a set, and carry the change to the sets after it that
   * still held the old value. Going from 60 to 62.5 on set 2 almost always
   * means sets 3 and 4 are 62.5 too; a set already lifted, or one typed
   * differently on purpose, is left alone.
   */
  function setField(ref: SetRef, field: "weight" | "reps", value: number) {
    onPatchSets(ref.exerciseId, (c) => {
      const target = c[ref.index];
      if (!target) return c;
      const old = target[field];
      const warm = Boolean(target.warmup);
      return c.map((s, j) =>
        j === ref.index ||
        (j > ref.index &&
          !s.done &&
          Boolean(s.warmup) === warm &&
          s[field] === old)
          ? { ...s, [field]: value }
          : s,
      );
    });
  }

  function addSet(exercise: Exercise, warmup: boolean) {
    const working = exercise.sets.filter((s) => !s.warmup);
    const lastWorking = working.at(-1);
    if (warmup) {
      // The first warm-up asked for on a loaded lift is the whole ramp.
      const firstWork = working[0]?.weight ?? 0;
      const ramp = exercise.sets.some((s) => s.warmup)
        ? []
        : warmupRamp(exercise.name, firstWork);
      onPatchSets(exercise.id, (c) => {
        const at = c.findIndex((s) => !s.warmup);
        const insert =
          ramp.length > 0
            ? ramp
            : [
                {
                  ...(c.filter((s) => s.warmup).at(-1) ?? {
                    weight: 0,
                    reps: 0,
                  }),
                  done: false,
                  warmup: true,
                },
              ];
        const out = [...c];
        out.splice(at === -1 ? c.length : at, 0, ...insert);
        return out;
      });
      const firstWorkAt = exercise.sets.findIndex((s) => !s.warmup);
      setChosen({
        exerciseId: exercise.id,
        index: firstWorkAt === -1 ? exercise.sets.length : firstWorkAt,
      });
      return;
    }
    onPatchSets(exercise.id, (c) => [
      ...c,
      {
        weight: lastWorking?.weight ?? 0,
        reps: lastWorking?.reps ?? 0,
        done: false,
      },
    ]);
    setChosen({ exerciseId: exercise.id, index: exercise.sets.length });
    reveal({ exerciseId: exercise.id, index: exercise.sets.length });
  }

  function removeSet(ref: SetRef) {
    const exercise = session.exercises.find((e) => e.id === ref.exerciseId);
    const set = exercise?.sets[ref.index];
    if (!exercise || !set) return;
    onPatchSets(ref.exerciseId, (c) => c.filter((_, j) => j !== ref.index));
    setChosen(null);
    onUndoable(`${exercise.name} 세트를 지웠어요`, () =>
      onPatchSets(ref.exerciseId, (c) => {
        const out = [...c];
        out.splice(Math.min(ref.index, out.length), 0, set);
        return out;
      }),
    );
  }

  function removeExercise(exercise: Exercise) {
    const at = session.exercises.findIndex((e) => e.id === exercise.id);
    onRemoveExercise(exercise.id);
    onUndoable(`${exercise.name}을(를) 뺐어요`, () =>
      onInsertExercise(exercise, at),
    );
  }

  // A movement just added (from the picker, or put back by undo) is where you
  // are going next, so it opens - adjusted during render rather than in an
  // effect, so there is no frame where the old exercise is still open.
  const ids = session.exercises.map((e) => e.id).join(",");
  const [knownIds, setKnownIds] = useState(ids);
  if (ids !== knownIds) {
    const before = new Set(knownIds.split(","));
    const added = session.exercises.find((e) => !before.has(e.id));
    if (added) {
      const open = added.sets.findIndex((s) => !s.done);
      setChosen({ exerciseId: added.id, index: Math.max(open, 0) });
    }
    setKnownIds(ids);
  }

  /** Open a movement at its first set still to do (or its first set, if all are). */
  function openExercise(exercise: Exercise) {
    const open = exercise.sets.findIndex((s) => !s.done);
    const ref = { exerciseId: exercise.id, index: Math.max(open, 0) };
    setChosen(ref);
    setEditing(null);
    requestAnimationFrame(() =>
      document.getElementById(`ex-${exercise.id}`)?.scrollIntoView({
        block: "start",
        behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
          ? "auto"
          : "smooth",
      }),
    );
  }

  const working = session.exercises.flatMap((e) =>
    e.sets.filter((s) => !s.warmup),
  );
  const workingDone = working.filter((s) => s.done).length;

  return (
    <div className="pb-2">
      <div className="mb-3 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="truncate text-[15px] font-medium text-foreground">
            {session.routineName ?? "운동"}
          </h1>
          <p
            role="timer"
            aria-label={`운동 시간 ${clock(now - session.startedAt)}`}
            className="font-score tnum mt-0.5 text-[48px] leading-none font-semibold tracking-tight text-foreground sm:text-[56px]"
          >
            {clock(now - session.startedAt)}
          </p>
          <p className="tnum mt-1.5 text-[12.5px] text-dim">
            {workingDone}/{working.length}세트 · {kg(volume)}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setFinishing(true)}
          className="tap mt-1 shrink-0 rounded-lg bg-glass-raised px-4 text-[13.5px] text-foreground transition-colors outline-none hover:bg-approve/12 hover:text-approve focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          끝내기
        </button>
      </div>

      {session.exercises.length > 0 ? (
        <SetMap
          exercises={session.exercises}
          focus={focus}
          onJump={openExercise}
        />
      ) : null}

      {stale ? (
        <div
          role="status"
          className="mt-3 mb-2 rounded-xl bg-glass px-3.5 py-3"
        >
          <p className="text-[13.5px] leading-relaxed text-foreground/90">
            {lastActive
              ? `마지막 세트 뒤로 ${Math.floor((now - since) / 3_600_000)}시간이 지났어요. 그때 끝난 운동인가요?`
              : `시작한 지 ${Math.floor((now - since) / 3_600_000)}시간이 지났어요. 끝난 운동인가요?`}
          </p>
          <div className="-ms-3 mt-1.5 flex flex-wrap items-center">
            {lastActive ? (
              <button
                type="button"
                onClick={() => onEnd(lastActive)}
                className={quietButton}
              >
                마지막 세트 시각으로 끝내기
              </button>
            ) : (
              <button
                type="button"
                onClick={() => setFinishing(true)}
                className={quietButton}
              >
                끝내기
              </button>
            )}
            <button
              type="button"
              onClick={() => {
                setStaleDismissed(true);
                touch();
              }}
              className={quietButton}
            >
              계속하기
            </button>
          </div>
        </div>
      ) : null}

      {/* One movement open at a time - the one the dock is on. The rest are a
          line each, so the whole session fits a phone screen and the next
          movement is one tap away rather than a scroll. */}
      <ol className="mt-2 border-t border-edge-soft">
        {session.exercises.map((exercise) => {
          const open = focus?.exerciseId === exercise.id;
          return (
            <li
              key={exercise.id}
              id={`ex-${exercise.id}`}
              className="scroll-mt-4 border-b border-edge-soft"
            >
              {open ? (
                <ExerciseBlock
                  exercise={exercise}
                  previous={previousSets(history, exercise.name, session.id)}
                  previousVolume={previousVolume(
                    history,
                    exercise.name,
                    session.id,
                  )}
                  best={bestSet(earlier, exercise.name)}
                  top={heaviest(earlier, exercise.name)}
                  focus={focus?.index ?? null}
                  editing={editing === exercise.id}
                  onEditing={(on) => setEditing(on ? exercise.id : null)}
                  onFocus={(index) =>
                    setChosen({ exerciseId: exercise.id, index })
                  }
                  onToggle={(index) =>
                    toggle({ exerciseId: exercise.id, index })
                  }
                  onRemoveSet={(index) =>
                    removeSet({ exerciseId: exercise.id, index })
                  }
                  onAddSet={(warmup) => addSet(exercise, warmup)}
                  onHistory={() => setLooking(exercise.name)}
                  onRemove={() => removeExercise(exercise)}
                />
              ) : (
                <ExerciseLine
                  exercise={exercise}
                  onOpen={() => openExercise(exercise)}
                />
              )}
            </li>
          );
        })}
      </ol>

      <button
        type="button"
        onClick={() => setPicking(true)}
        className="mt-4 flex h-12 w-full items-center justify-center gap-2 rounded-xl border border-dashed border-edge text-[13.5px] text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <Plus aria-hidden className="size-4" />
        종목 추가
      </button>

      <section
        aria-label="오늘 기록"
        className="mt-8 grid gap-4 sm:grid-cols-[auto_1fr]"
      >
        <label className="block">
          <span className="mb-1.5 block text-[12px] text-faint">오늘 체중</span>
          <span className="flex items-center gap-2">
            <Input
              type="number"
              inputMode="decimal"
              defaultValue={session.bodyWeight || ""}
              onBlur={(e) => onBodyWeight(Number(e.target.value) || 0)}
              placeholder="0"
              className="tnum h-11 w-28 border-edge bg-well/40 text-center text-[16px] sm:text-sm"
            />
            <span className="text-[12px] text-faint">kg</span>
          </span>
        </label>
        <label className="block">
          <span className="mb-1.5 block text-[12px] text-faint">메모</span>
          <Input
            defaultValue={session.note ?? ""}
            onBlur={(e) => onNote(e.target.value)}
            placeholder="컨디션, 통증, 바꾼 것"
            className="h-11 border-edge bg-well/40 text-[16px] sm:text-sm"
          />
        </label>
        <div className="sm:col-span-2">
          <span
            id="rest-default"
            className="mb-1.5 block text-[12px] text-faint"
          >
            기본 휴식
          </span>
          <div
            className="flex gap-2"
            role="radiogroup"
            aria-labelledby="rest-default"
          >
            {REST_PRESETS.map((seconds) => (
              <button
                key={seconds}
                type="button"
                role="radio"
                aria-checked={restTarget === seconds}
                onClick={() => onRestTarget(seconds)}
                className={cn(
                  "min-h-11 flex-1 rounded-lg text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  restTarget === seconds
                    ? "bg-glass-raised text-foreground"
                    : "bg-glass text-faint hover:text-dim",
                )}
              >
                {seconds % 60
                  ? `${Math.floor(seconds / 60)}분 ${seconds % 60}초`
                  : `${seconds / 60}분`}
              </button>
            ))}
          </div>
        </div>
      </section>

      <Dock
        now={now}
        rest={rest}
        onRest={setRest}
        notice={notice}
        exercise={focusExercise}
        focus={focus}
        previous={
          focusExercise && focus
            ? previousFor(
                previousSets(history, focusExercise.name, session.id),
                focusExercise.sets,
                focus.index,
              )
            : null
        }
        empty={session.exercises.length === 0}
        hint={focusExercise ? overloadHint(history, focusExercise.name, session.id) : null}
        // One step heavier for every working set of this movement still to
        // lift at last time's load - the ones already ticked are history.
        onRaise={(exerciseId, from, to) =>
          onPatchSets(exerciseId, (c) => c.map((s) => (!s.done && !s.warmup && s.weight === from ? { ...s, weight: to } : s)))
        }
        onField={setField}
        onToggle={toggle}
        onWarmup={(ref) =>
          onPatchSets(ref.exerciseId, (c) =>
            c.map((s, j) =>
              j === ref.index ? { ...s, warmup: !s.warmup } : s,
            ),
          )
        }
        onRemoveSet={removeSet}
        onAddExercise={() => setPicking(true)}
        onAddSet={() => {
          const last = session.exercises.at(-1);
          if (last) addSet(last, false);
        }}
        onFinish={() => setFinishing(true)}
      />

      <ExercisePicker
        open={picking}
        onOpenChange={setPicking}
        sessions={history}
        taken={session.exercises.map((e) => e.name)}
        onPick={onAddExercise}
      />
      <ExerciseHistory
        name={looking}
        sessions={history}
        onOpenChange={(o) => !o && setLooking(null)}
      />
      <FinishPanel
        open={finishing}
        onOpenChange={setFinishing}
        session={session}
        history={history}
        elapsed={now - session.startedAt}
        onEnd={() => {
          setFinishing(false);
          setRest(null);
          onEnd();
        }}
        onDiscard={() => {
          setFinishing(false);
          setRest(null);
          onDiscard();
        }}
      />
    </div>
  );
}
