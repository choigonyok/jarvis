"use client";

import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, Flame, Minus, Plus, Trash2, X } from "lucide-react";
import { ExerciseHistory } from "@/components/workout/exercise-history";
import { ExercisePicker } from "@/components/workout/exercise-picker";
import { Panel, quietButton, saveButton } from "@/components/workout/panel";
import { VolumeBar } from "@/components/workout/volume-bar";
import { Input } from "@/components/ui/input";
import { chime, local, primeChime, useNow, useWakeLock } from "@/lib/use-gym";
import {
  bestSet,
  clock,
  doneSets,
  heaviest,
  isBarbell,
  kg,
  load,
  nextOpen,
  platesPerSide,
  previousFor,
  previousSets,
  previousVolume,
  sessionRecords,
  sessionVolume,
  setLabel,
  signedKg,
  volumeOf,
  warmupRamp,
  weightStep,
  estimate1RM,
  type Exercise,
  type Session,
  type SetRef,
  type WorkoutSet,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

/** What people actually rest for, by what they are training. */
const REST_PRESETS = [60, 90, 120, 180];

/** Past this with nothing ticked, the session was most likely left open. */
const STALE_MS = 3 * 60 * 60 * 1000;

type Rest = { sessionId: string; startedAt: number; target: number };
const REST_KEY = "jarvis.workout.rest";
const activeKey = (id: string) => `jarvis.workout.active.${id}`;

const round2 = (n: number) => Math.round(n * 100) / 100;

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
  onPatchSets: (exerciseId: string, fn: (sets: WorkoutSet[]) => WorkoutSet[]) => void;
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
    (session.exercises.find((e) => e.id === ref.exerciseId)?.sets.length ?? 0) > ref.index;
  const focus = valid(chosen) ? chosen : nextOpen(session);
  const focusExercise = focus ? session.exercises.find((e) => e.id === focus.exerciseId) : undefined;

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
        document.getElementById(`set-${ref.exerciseId}-${ref.index}`)?.scrollIntoView({
          block: "nearest",
          behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
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
        document.getElementById(`set-${ref.exerciseId}-${ref.index}`)?.scrollIntoView({ block: "nearest" }),
      );
  });

  function toggle(ref: SetRef) {
    const exercise = session.exercises.find((e) => e.id === ref.exerciseId);
    const set = exercise?.sets[ref.index];
    if (!exercise || !set) return;
    const done = !set.done;
    onPatchSets(ref.exerciseId, (c) => c.map((s, j) => (j === ref.index ? { ...s, done } : s)));
    if (!done) {
      setChosen(ref);
      return;
    }
    primeChime();
    touch();
    // A warm-up is followed by the next warm-up, not by a rest.
    if (!set.warmup) setRest({ sessionId: session.id, startedAt: stamp(), target: restTarget });
    const after: Session = {
      ...session,
      exercises: session.exercises.map((e) =>
        e.id === ref.exerciseId
          ? { ...e, sets: e.sets.map((s, j) => (j === ref.index ? { ...s, done } : s)) }
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
        (j > ref.index && !s.done && Boolean(s.warmup) === warm && s[field] === old)
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
      const ramp = exercise.sets.some((s) => s.warmup) ? [] : warmupRamp(exercise.name, firstWork);
      onPatchSets(exercise.id, (c) => {
        const at = c.findIndex((s) => !s.warmup);
        const insert =
          ramp.length > 0
            ? ramp
            : [{ ...(c.filter((s) => s.warmup).at(-1) ?? { weight: 0, reps: 0 }), done: false, warmup: true }];
        const out = [...c];
        out.splice(at === -1 ? c.length : at, 0, ...insert);
        return out;
      });
      const firstWorkAt = exercise.sets.findIndex((s) => !s.warmup);
      setChosen({ exerciseId: exercise.id, index: firstWorkAt === -1 ? exercise.sets.length : firstWorkAt });
      return;
    }
    onPatchSets(exercise.id, (c) => [
      ...c,
      { weight: lastWorking?.weight ?? 0, reps: lastWorking?.reps ?? 0, done: false },
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
    onUndoable(`${exercise.name}을(를) 뺐어요`, () => onInsertExercise(exercise, at));
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
        behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
      }),
    );
  }

  const working = session.exercises.flatMap((e) => e.sets.filter((s) => !s.warmup));
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
        <SetMap exercises={session.exercises} focus={focus} onJump={openExercise} />
      ) : null}

      {stale ? (
        <div role="status" className="mt-3 mb-2 rounded-xl bg-glass px-3.5 py-3">
          <p className="text-[13.5px] leading-relaxed text-foreground/90">
            {lastActive
              ? `마지막 세트 뒤로 ${Math.floor((now - since) / 3_600_000)}시간이 지났어요. 그때 끝난 운동인가요?`
              : `시작한 지 ${Math.floor((now - since) / 3_600_000)}시간이 지났어요. 끝난 운동인가요?`}
          </p>
          <div className="-ms-3 mt-1.5 flex flex-wrap items-center">
            {lastActive ? (
              <button type="button" onClick={() => onEnd(lastActive)} className={quietButton}>
                마지막 세트 시각으로 끝내기
              </button>
            ) : (
              <button type="button" onClick={() => setFinishing(true)} className={quietButton}>
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
            <li key={exercise.id} id={`ex-${exercise.id}`} className="scroll-mt-4 border-b border-edge-soft">
              {open ? (
                <ExerciseBlock
                  exercise={exercise}
                  previous={previousSets(history, exercise.name, session.id)}
                  previousVolume={previousVolume(history, exercise.name, session.id)}
                  best={bestSet(earlier, exercise.name)}
                  top={heaviest(earlier, exercise.name)}
                  focus={focus?.index ?? null}
                  editing={editing === exercise.id}
                  onEditing={(on) => setEditing(on ? exercise.id : null)}
                  onFocus={(index) => setChosen({ exerciseId: exercise.id, index })}
                  onToggle={(index) => toggle({ exerciseId: exercise.id, index })}
                  onRemoveSet={(index) => removeSet({ exerciseId: exercise.id, index })}
                  onAddSet={(warmup) => addSet(exercise, warmup)}
                  onHistory={() => setLooking(exercise.name)}
                  onRemove={() => removeExercise(exercise)}
                />
              ) : (
                <ExerciseLine exercise={exercise} onOpen={() => openExercise(exercise)} />
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

      <section aria-label="오늘 기록" className="mt-8 grid gap-4 sm:grid-cols-[auto_1fr]">
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
          <span id="rest-default" className="mb-1.5 block text-[12px] text-faint">
            기본 휴식
          </span>
          <div className="flex gap-2" role="radiogroup" aria-labelledby="rest-default">
            {REST_PRESETS.map((seconds) => (
              <button
                key={seconds}
                type="button"
                role="radio"
                aria-checked={restTarget === seconds}
                onClick={() => onRestTarget(seconds)}
                className={cn(
                  "min-h-11 flex-1 rounded-lg text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  restTarget === seconds ? "bg-glass-raised text-foreground" : "bg-glass text-faint hover:text-dim",
                )}
              >
                {seconds % 60 ? `${Math.floor(seconds / 60)}분 ${seconds % 60}초` : `${seconds / 60}분`}
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
            ? previousFor(previousSets(history, focusExercise.name, session.id), focusExercise.sets, focus.index)
            : null
        }
        empty={session.exercises.length === 0}
        onField={setField}
        onToggle={toggle}
        onWarmup={(ref) =>
          onPatchSets(ref.exerciseId, (c) => c.map((s, j) => (j === ref.index ? { ...s, warmup: !s.warmup } : s)))
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
      <ExerciseHistory name={looking} sessions={history} onOpenChange={(o) => !o && setLooking(null)} />
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

/**
 * The whole session as bars - one per set, grouped by movement, warm-ups
 * narrower. Filled is lifted, white is the set the dock is on. It answers
 * "how far through am I" without a percentage, and each group is a button
 * to that movement.
 */
function SetMap({
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
function ExerciseLine({ exercise, onOpen }: { exercise: Exercise; onOpen: () => void }) {
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
          complete ? "text-approve" : done > 0 ? "text-foreground" : "text-faint",
        )}
      >
        {complete ? <Check aria-hidden className="size-[18px]" /> : `${done}/${working.length}`}
      </span>
      <span className="min-w-0 flex-1">
        <span className={cn("block truncate text-[15px]", complete ? "text-dim" : "text-foreground")}>
          {exercise.name}
        </span>
        <span className="tnum block truncate text-[12px] text-faint">
          {complete ? `${working.length}세트 · ${kg(volumeOf(exercise))}` : next ? `다음 ${setLabel(next)}` : "세트 없음"}
        </span>
      </span>
      <ChevronDown aria-hidden className="size-4 shrink-0 text-faint" />
    </button>
  );
}

function ExerciseBlock({
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
    (best !== null && working.some((s) => (estimate1RM(s.weight, s.reps) ?? 0) > best.oneRm + 0.05));

  // Warm-ups are W, working sets count 1, 2, 3 among themselves - a ramp
  // in front should not make the first real set "set 4".
  const labels = sets.map((set, i) => {
    const nth = sets.slice(0, i + 1).filter((s) => Boolean(s.warmup) === Boolean(set.warmup)).length;
    return set.warmup ? { short: "W", long: `워밍업 ${nth}` } : { short: String(nth), long: `${nth}세트` };
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
            editing ? "bg-glass-raised text-foreground" : "text-dim hover:text-foreground",
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
                  <span aria-hidden className="absolute inset-y-2 left-0 w-[2px] rounded-full bg-foreground/80" />
                ) : null}
                <button
                  type="button"
                  onClick={() => onFocus(i)}
                  aria-label={`${label.long} ${load(set.weight)}kg ${set.reps}회${set.done ? ", 완료" : ""}`}
                  aria-current={focused || undefined}
                  className="grid min-h-12 flex-1 grid-cols-[1.75rem_1fr_4.25rem_3rem] items-center gap-2 rounded-lg px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  <span className={cn("tnum text-center text-[13px]", set.warmup ? "text-dim" : "text-faint")}>
                    {label.short}
                  </span>
                  <span className="tnum truncate text-[12.5px] text-faint">{before ? setLabel(before) : "—"}</span>
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
                      set.done ? "bg-approve/15 text-approve" : "text-faint/70 hover:text-dim",
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
            className={cn(quietButton, "flex items-center gap-1.5 text-faint hover:text-reject")}
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
            <VolumeBar volume={volumeOf(exercise)} previous={previousVolume} className="mt-1" />
          ) : null}
        </>
      )}
    </section>
  );
}

/**
 * The set you are about to lift, under the thumb.
 *
 * Sticky to the bottom of the scroll area (above the tab bar on a phone), so
 * on a tablet or desktop it stays inside the column rather than spanning the
 * window. The rest countdown sits on its top edge while it runs.
 */
function Dock({
  now,
  rest,
  onRest,
  notice,
  exercise,
  focus,
  previous,
  empty,
  onField,
  onToggle,
  onWarmup,
  onRemoveSet,
  onAddExercise,
  onAddSet,
  onFinish,
}: {
  now: number;
  rest: Rest | null;
  onRest: (rest: Rest | null) => void;
  notice?: React.ReactNode;
  exercise: Exercise | undefined;
  focus: SetRef | null;
  previous: WorkoutSet | null;
  empty: boolean;
  onField: (ref: SetRef, field: "weight" | "reps", value: number) => void;
  onToggle: (ref: SetRef) => void;
  onWarmup: (ref: SetRef) => void;
  onRemoveSet: (ref: SetRef) => void;
  onAddExercise: () => void;
  onAddSet: () => void;
  onFinish: () => void;
}) {
  // The dock's height, published for the set rows' scroll margin: a row
  // scrolled into view has to clear the dock, and the dock is taller while a
  // rest is running than when it is not.
  const measured = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = measured.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const root = document.documentElement;
    const observer = new ResizeObserver(([entry]) =>
      root.style.setProperty("--dock-h", `${Math.ceil(entry.borderBoxSize?.[0]?.blockSize ?? el.offsetHeight)}px`),
    );
    observer.observe(el);
    return () => {
      observer.disconnect();
      root.style.removeProperty("--dock-h");
    };
  }, []);

  const set = exercise && focus ? exercise.sets[focus.index] : undefined;
  const nth =
    exercise && focus && set
      ? exercise.sets.slice(0, focus.index + 1).filter((s) => Boolean(s.warmup) === Boolean(set.warmup)).length
      : 0;
  const plates = exercise && set && isBarbell(exercise.name) ? platesPerSide(set.weight) : null;

  return (
    // bottom-2, not tab bar + gap: the scroll area's own bottom padding
    // (pb-tabbar) already insets where a sticky child can sit.
    <div ref={measured} className="sticky bottom-2 z-20 mt-6">
      {notice}
      <div className="liquid-glass rounded-2xl p-3 sm:p-3.5">
        {rest ? <RestStrip now={now} rest={rest} onRest={onRest} /> : null}

        {exercise && focus && set ? (
          <>
            <div className="-mt-1 mb-1 flex items-center gap-2">
              <p className="min-w-0 flex-1 truncate text-[14px] text-foreground">
                {exercise.name}
                <span className="ms-1.5 text-dim">{set.warmup ? `워밍업 ${nth}` : `${nth}세트`}</span>
              </p>
              <button
                type="button"
                onClick={() => onWarmup(focus)}
                aria-pressed={Boolean(set.warmup)}
                className={cn(
                  "min-h-11 shrink-0 rounded-md px-2.5 text-[12.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  set.warmup ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim",
                )}
              >
                워밍업
              </button>
              <button
                type="button"
                onClick={() => onRemoveSet(focus)}
                className="min-h-11 shrink-0 rounded-md px-2.5 text-[12.5px] text-faint transition-colors outline-none hover:text-reject focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                세트 삭제
              </button>
            </div>

            <div className="grid grid-cols-2 gap-2">
              <Stepper
                label="무게"
                unit="kg"
                decimal
                value={set.weight}
                step={weightStep(exercise.name)}
                onChange={(v) => onField(focus, "weight", v)}
              />
              <Stepper
                label="횟수"
                unit="회"
                value={set.reps}
                step={1}
                onChange={(v) => onField(focus, "reps", v)}
              />
            </div>

            {/* On a short phone the dock gives this line back: the row above
                already says what last time was. */}
            <p className="tnum mt-1.5 flex justify-between gap-3 px-1 text-[12px] text-faint [@media(max-height:700px)]:hidden">
              <span className="truncate">
                {exercise && isBarbell(exercise.name)
                  ? set.weight === 20
                    ? "빈 봉"
                    : plates
                      ? `한쪽 ${plates.map(load).join(" + ")}`
                      : ""
                  : ""}
              </span>
              <span className="shrink-0">
                {previous ? `지난번 ${setLabel(previous)}` : "첫 기록"}
              </span>
            </p>

            <button
              type="button"
              onClick={() => onToggle(focus)}
              className={cn(
                "mt-2.5 flex h-12 w-full items-center justify-center gap-2 rounded-xl text-[15px] font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                set.done
                  ? "bg-glass text-dim hover:text-foreground"
                  : "bg-approve/18 text-approve hover:bg-approve/25",
              )}
            >
              <Check aria-hidden className="size-[18px]" />
              {set.done ? "완료 취소" : "완료"}
            </button>
          </>
        ) : empty ? (
          <div className="flex items-center gap-3">
            <p className="min-w-0 flex-1 text-[13.5px] text-dim">종목을 추가하면 여기서 세트를 기록해요.</p>
            <button type="button" onClick={onAddExercise} className={cn(saveButton, "flex-none")}>
              종목 추가
            </button>
          </div>
        ) : (
          <div>
            <p className="mb-2 text-[13.5px] text-foreground">모든 세트를 마쳤어요.</p>
            <div className="flex gap-2">
              <button type="button" onClick={onAddSet} className={cn(quietButton, "flex-1 bg-glass")}>
                세트 추가
              </button>
              <button type="button" onClick={onAddExercise} className={cn(quietButton, "flex-1 bg-glass")}>
                종목 추가
              </button>
              <button type="button" onClick={onFinish} className={saveButton}>
                끝내기
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * Rest, counted down to a target.
 *
 * Resting is something you finish, so it counts down; past zero it keeps
 * counting up in the approve hue, because "how long over" is the next thing
 * you want to know. ±15s nudges this rest only - the default lives with the
 * session settings.
 */
function RestStrip({
  now,
  rest,
  onRest,
}: {
  now: number;
  rest: Rest;
  onRest: (rest: Rest | null) => void;
}) {
  const left = rest.target * 1000 - (now - rest.startedAt);
  const over = left <= 0;
  const share = Math.min(1, (now - rest.startedAt) / (rest.target * 1000));

  // One chime per rest, keyed to when it started, so a re-render or a nudge
  // after the end does not ring again.
  // A rest restored after a reload that had already run out does not ring.
  const rang = useRef<number | null>(over ? rest.startedAt : null);
  useEffect(() => {
    if (over && rang.current !== rest.startedAt) {
      rang.current = rest.startedAt;
      chime();
    }
  }, [over, rest.startedAt]);

  const nudge = (by: number) => onRest({ ...rest, target: Math.max(15, rest.target + by) });

  return (
    <div className="mb-2.5 border-b border-edge-soft pb-2.5">
      <div className="-mt-1 flex items-center gap-2">
        <span className="text-[12px] text-faint">휴식</span>
        <span
          role="timer"
          aria-live="off"
          className={cn("font-score tnum min-w-[3.5rem] text-[30px] leading-none font-semibold", over ? "text-approve" : "text-foreground")}
        >
          {over ? `+${clock(-left)}` : clock(left + 999)}
        </span>
        <span className="flex-1" />
        <button type="button" onClick={() => nudge(-15)} className="tnum min-h-11 rounded-md px-2.5 text-[12.5px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
          −15초
        </button>
        <button type="button" onClick={() => nudge(15)} className="tnum min-h-11 rounded-md px-2.5 text-[12.5px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
          +15초
        </button>
        <button
          type="button"
          onClick={() => onRest(null)}
          aria-label="휴식 끝내기"
          className="flex size-11 items-center justify-center rounded-md text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <X aria-hidden className="size-4" />
        </button>
      </div>
      <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-well [@media(max-height:700px)]:hidden">
        <div
          className={cn("h-full rounded-full", over ? "bg-approve/70" : "bg-foreground/60")}
          style={{ width: `${share * 100}%` }}
        />
      </div>
    </div>
  );
}

/**
 * −, the number, +. The steppers are what most changes are: 2.5kg more, one
 * rep fewer. The number is still a field for the jump a stepper would take
 * ten taps to make, and it opens the number pad rather than the keyboard.
 */
function Stepper({
  label,
  unit,
  value,
  step,
  decimal,
  onChange,
}: {
  label: string;
  unit: string;
  value: number;
  step: number;
  decimal?: boolean;
  onChange: (value: number) => void;
}) {
  const [text, setText] = useState<string | null>(null);
  const shown = text ?? load(value);
  const button =
    "flex h-12 w-11 shrink-0 items-center justify-center rounded-lg bg-glass text-dim transition-colors outline-none hover:text-foreground active:bg-glass-raised focus-visible:ring-3 focus-visible:ring-ring/50";

  return (
    <div className="flex items-center gap-1">
      <button type="button" onClick={() => onChange(Math.max(0, round2(value - step)))} aria-label={`${label} ${load(step)} 줄이기`} className={button}>
        <Minus aria-hidden className="size-4" />
      </button>
      <label className="flex min-w-0 flex-1 items-baseline justify-center gap-0.5">
        <span className="sr-only">{label}</span>
        <input
          inputMode={decimal ? "decimal" : "numeric"}
          value={shown}
          onFocus={(e) => {
            setText(load(value));
            e.currentTarget.select();
          }}
          onChange={(e) => {
            const t = e.target.value.replace(decimal ? /[^\d.]/g : /[^\d]/g, "");
            setText(t);
            const n = Number(t);
            if (!Number.isNaN(n)) onChange(n);
          }}
          onBlur={() => setText(null)}
          onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
          // Fixed to about five digits and set against its unit, so on a wide
          // dock the number does not drift off to the middle of the field.
          className="font-score tnum w-[3em] min-w-0 bg-transparent text-right text-[30px] leading-none font-semibold text-foreground outline-none"
        />
        <span aria-hidden className="shrink-0 text-[11.5px] text-faint">
          {unit}
        </span>
      </label>
      <button type="button" onClick={() => onChange(round2(value + step))} aria-label={`${label} ${load(step)} 늘리기`} className={button}>
        <Plus aria-hidden className="size-4" />
      </button>
    </div>
  );
}

/**
 * Finishing, as a look back rather than a button.
 *
 * The summary is what you would have opened the history to check, and it
 * comes with the decision so a mis-tap on 끝내기 costs a second, not a
 * session. Unticked sets are named before they are dropped.
 */
function FinishPanel({
  open,
  onOpenChange,
  session,
  history,
  elapsed,
  onEnd,
  onDiscard,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  session: Session;
  history: Session[];
  elapsed: number;
  onEnd: () => void;
  onDiscard: () => void;
}) {
  const sets = doneSets(session);
  const volume = sessionVolume(session);
  const open_ = session.exercises.reduce((n, e) => n + e.sets.filter((s) => !s.done).length, 0);
  const anything = session.exercises.some((e) => e.sets.some((s) => s.done));
  const records = sessionRecords(session, history);
  const lastSame = session.routineName
    ? history
        .filter(
          (s) =>
            s.id !== session.id &&
            s.endedAt &&
            (s.routineId === session.routineId || s.routineName === session.routineName),
        )
        .sort((a, b) => b.startedAt - a.startedAt)[0]
    : undefined;
  const delta = lastSame ? volume - sessionVolume(lastSame) : null;

  return (
    <Panel open={open} onOpenChange={onOpenChange} title="운동 끝내기">
      <dl className="mb-4 grid grid-cols-3 gap-3">
        {[
          ["시간", clock(elapsed)],
          ["볼륨", kg(volume)],
          ["세트", `${sets}`],
        ].map(([label, value]) => (
          <div key={label}>
            <dt className="text-[11.5px] text-faint">{label}</dt>
            <dd className="font-score tnum text-[30px] leading-tight font-semibold text-foreground">{value}</dd>
          </div>
        ))}
      </dl>

      {delta !== null && anything ? (
        <p className="mb-3 text-[13px] text-dim">
          지난 {session.routineName}보다{" "}
          <span className={cn("tnum", delta > 0 ? "text-approve" : "text-foreground/90")}>
            {delta === 0 ? "같은 볼륨" : signedKg(delta)}
          </span>
        </p>
      ) : null}

      {records.length > 0 ? (
        <ul className="mb-4 space-y-1.5">
          {records.map((r) => (
            <li key={r.name} className="flex items-center gap-2 text-[13px]">
              <Flame aria-hidden className="size-3.5 shrink-0 text-approve" />
              <span className="min-w-0 flex-1 truncate text-foreground/90">{r.name}</span>
              <span className="tnum shrink-0 text-faint">
                {r.kind === "무게" ? "최고 무게" : "추정 1RM"} {load(Math.round(r.before * 10) / 10)} → <span className="text-approve">{load(Math.round(r.value * 10) / 10)}kg</span>
              </span>
            </li>
          ))}
        </ul>
      ) : null}

      {!anything ? (
        <p className="mb-4 text-[13px] leading-relaxed text-dim">체크한 세트가 없어서 남길 기록이 없어요.</p>
      ) : open_ > 0 ? (
        <p className="mb-4 text-[13px] leading-relaxed text-dim">체크하지 않은 세트 {open_}개는 기록에서 빠져요.</p>
      ) : null}

      <div className="flex gap-2">
        <button type="button" onClick={() => onOpenChange(false)} className={cn(quietButton, "flex-1 bg-glass")}>
          계속하기
        </button>
        {anything ? (
          <button type="button" onClick={onEnd} className={saveButton}>
            기록하고 끝내기
          </button>
        ) : (
          <button type="button" onClick={onDiscard} className={saveButton}>
            운동 취소
          </button>
        )}
      </div>
      {anything ? (
        <div className="mt-2 flex justify-center">
          <button type="button" onClick={onDiscard} className={cn(quietButton, "text-faint hover:text-reject")}>
            기록 버리기
          </button>
        </div>
      ) : null}
    </Panel>
  );
}
