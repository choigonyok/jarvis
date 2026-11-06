"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Minus, Plus, TrendingUp, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { chime } from "@/lib/use-gym";
import {
  clock,
  isBarbell,
  load,
  platesPerSide,
  setLabel,
  weightStep,
  type Exercise,
  type SetRef,
  type WorkoutSet,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

import { type Rest, round2 } from "@/components/workout/live-shared";

/**
 * The set you are about to lift, under the thumb.
 *
 * Sticky to the bottom of the scroll area (above the tab bar on a phone), so
 * on a tablet or desktop it stays inside the column rather than spanning the
 * window. The rest countdown sits on its top edge while it runs.
 */
export function Dock({
  now,
  rest,
  onRest,
  exercise,
  focus,
  previous,
  empty,
  hint,
  onRaise,
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
  exercise: Exercise | undefined;
  focus: SetRef | null;
  previous: WorkoutSet | null;
  empty: boolean;
  /** Last time's working sets all reached the top of the range: one step heavier. */
  hint?: { from: number; to: number; reps: number } | null;
  onRaise?: (exerciseId: string, from: number, to: number) => void;
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
    // And where its top is, from the bottom of the screen: the 되돌리기 toast
    // stands over the dock while it is up (--dock-top, shell/toasts.tsx). It
    // is stuck to the bottom while there is more to scroll, and sits after
    // the last exercise when there is not.
    let frame = 0;
    const place = () => {
      frame = 0;
      root.style.setProperty("--dock-top", `${Math.ceil(window.innerHeight - el.getBoundingClientRect().top)}px`);
    };
    const later = () => {
      if (!frame) frame = requestAnimationFrame(place);
    };
    const observer = new ResizeObserver(([entry]) => {
      const h = Math.ceil(entry.borderBoxSize?.[0]?.blockSize ?? el.offsetHeight);
      root.style.setProperty("--dock-h", `${h}px`);
      later();
    });
    observer.observe(el);
    window.addEventListener("scroll", later, { capture: true, passive: true });
    window.addEventListener("resize", later);
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", later, { capture: true });
      window.removeEventListener("resize", later);
      root.style.removeProperty("--dock-h");
      root.style.removeProperty("--dock-top");
    };
  }, []);

  const set = exercise && focus ? exercise.sets[focus.index] : undefined;
  const nth =
    exercise && focus && set
      ? exercise.sets
          .slice(0, focus.index + 1)
          .filter((s) => Boolean(s.warmup) === Boolean(set.warmup)).length
      : 0;
  const plates =
    exercise && set && isBarbell(exercise.name)
      ? platesPerSide(set.weight)
      : null;

  return (
    // bottom-2, not tab bar + gap: the scroll area's own bottom padding
    // (pb-tabbar) already insets where a sticky child can sit.
    <div ref={measured} className="sticky bottom-2 z-20 mt-6">
      <div className="liquid-glass rounded-2xl p-3 sm:p-3.5">
        {rest ? <RestStrip now={now} rest={rest} onRest={onRest} /> : null}

        {exercise && focus && set ? (
          <>
            <div className="-mt-1 mb-1 flex items-center gap-2">
              <p className="min-w-0 flex-1 truncate text-body text-foreground">
                {exercise.name}
                <span className="ms-1.5 text-dim">
                  {set.warmup ? `워밍업 ${nth}` : `${nth}세트`}
                </span>
              </p>
              <button
                type="button"
                onClick={() => onWarmup(focus)}
                aria-pressed={Boolean(set.warmup)}
                className={cn(
                  "min-h-11 shrink-0 rounded-md px-2.5 text-aside transition-colors focus-ring",
                  set.warmup
                    ? "bg-glass-raised text-foreground"
                    : "text-faint hover:text-dim",
                )}
              >
                워밍업
              </button>
              <button
                type="button"
                onClick={() => onRemoveSet(focus)}
                className="min-h-11 shrink-0 rounded-md px-2.5 text-aside text-faint transition-colors hover:text-reject focus-ring"
              >
                세트 삭제
              </button>
            </div>

            {/* Progressive overload, offered at the moment it matters: on a
                working set still at last time's load. A tap raises this set
                and the ones after it; the stepper still says the last word. */}
            {hint && onRaise && !set.done && !set.warmup && set.weight === hint.from ? (
              <button
                type="button"
                onClick={() => onRaise(exercise.id, hint.from, hint.to)}
                className="mb-2 flex w-full items-center gap-2 rounded-lg bg-glass px-3 py-2 text-left text-aside transition-colors hover:bg-glass-raised focus-ring"
              >
                <TrendingUp aria-hidden className="size-4 shrink-0 text-approve" />
                <span className="min-w-0 flex-1 text-dim">
                  지난번 {load(hint.from)}kg로 모든 세트 {hint.reps}회 이상
                </span>
                <span className="tnum shrink-0 font-medium text-foreground">{load(hint.to)}kg 도전</span>
              </button>
            ) : null}

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
            <p className="tnum mt-1.5 flex justify-between gap-3 px-1 text-aside text-faint [@media(max-height:700px)]:hidden">
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
                "mt-2.5 flex h-12 w-full items-center justify-center gap-2 rounded-xl text-title font-medium transition-colors focus-ring",
                set.done
                  ? "bg-glass text-dim hover:text-foreground"
                  : "bg-approve-wash text-approve",
              )}
            >
              <Check aria-hidden className="size-[18px]" />
              {set.done ? "완료 취소" : "완료"}
            </button>
          </>
        ) : empty ? (
          <div className="flex items-center gap-3">
            <p className="min-w-0 flex-1 text-ui text-dim">
              종목을 추가하면 여기서 세트를 기록해요.
            </p>
            <Button variant="raised" size="touch" onClick={onAddExercise} className="flex-none">
              종목 추가
            </Button>
          </div>
        ) : (
          <div>
            <p className="mb-2 text-ui text-foreground">
              모든 세트를 마쳤어요.
            </p>
            <div className="flex gap-2">
              <Button variant="soft" size="touch-sm" onClick={onAddSet} className="flex-1">
                세트 추가
              </Button>
              <Button variant="soft" size="touch-sm" onClick={onAddExercise} className="flex-1">
                종목 추가
              </Button>
              <Button variant="raised" size="touch" onClick={onFinish} className="flex-1">
                끝내기
              </Button>
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
export function RestStrip({
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

  const nudge = (by: number) =>
    onRest({ ...rest, target: Math.max(15, rest.target + by) });

  return (
    <div className="mb-2.5 border-b border-edge-soft pb-2.5">
      <div className="-mt-1 flex items-center gap-2">
        <span className="text-aside text-faint">휴식</span>
        <span
          role="timer"
          aria-live="off"
          className={cn(
            "font-score tnum min-w-[3.5rem] text-[30px] leading-none font-semibold",
            over ? "text-approve" : "text-foreground",
          )}
        >
          {over ? `+${clock(-left)}` : clock(left + 999)}
        </span>
        <span className="flex-1" />
        <button
          type="button"
          onClick={() => nudge(-15)}
          className="tnum min-h-11 rounded-md px-2.5 text-aside text-dim hover:text-foreground focus-ring"
        >
          −15초
        </button>
        <button
          type="button"
          onClick={() => nudge(15)}
          className="tnum min-h-11 rounded-md px-2.5 text-aside text-dim hover:text-foreground focus-ring"
        >
          +15초
        </button>
        <button
          type="button"
          onClick={() => onRest(null)}
          aria-label="휴식 끝내기"
          className="flex size-11 items-center justify-center rounded-md text-faint hover:text-foreground focus-ring"
        >
          <X aria-hidden className="size-4" />
        </button>
      </div>
      <Progress
        aria-label="휴식"
        value={share * 100}
        className="mt-1.5 bg-well [@media(max-height:700px)]:hidden"
        indicatorClassName={over ? "bg-approve-fill" : "bg-foreground/60"}
      />
    </div>
  );
}

/**
 * −, the number, +. The steppers are what most changes are: 2.5kg more, one
 * rep fewer. The number is still a field for the jump a stepper would take
 * ten taps to make, and it opens the number pad rather than the keyboard.
 */
export function Stepper({
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
    "flex h-12 w-11 shrink-0 items-center justify-center rounded-lg bg-glass text-dim transition-colors hover:text-foreground active:bg-glass-raised focus-ring";

  return (
    <div className="flex items-center gap-1">
      <button
        type="button"
        onClick={() => onChange(Math.max(0, round2(value - step)))}
        aria-label={`${label} ${load(step)} 줄이기`}
        className={button}
      >
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
            const t = e.target.value.replace(
              decimal ? /[^\d.]/g : /[^\d]/g,
              "",
            );
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
        <span aria-hidden className="shrink-0 text-meta text-faint">
          {unit}
        </span>
      </label>
      <button
        type="button"
        onClick={() => onChange(round2(value + step))}
        aria-label={`${label} ${load(step)} 늘리기`}
        className={button}
      >
        <Plus aria-hidden className="size-4" />
      </button>
    </div>
  );
}
