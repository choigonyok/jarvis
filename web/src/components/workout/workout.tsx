"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Trash2, X } from "lucide-react";
import { ExerciseHistory } from "@/components/workout/exercise-history";
import { Lifts } from "@/components/workout/lifts";
import { LiveSession } from "@/components/workout/live-session";
import { RoutineEditor } from "@/components/workout/routine-editor";
import { RoutinePicker } from "@/components/workout/routine-picker";
import { WorkoutCalendar } from "@/components/workout/workout-calendar";
import { VolumeBar } from "@/components/workout/volume-bar";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { isoDate } from "@/lib/month";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { useWorkout } from "@/lib/use-workout";
import {
  MUSCLE_GROUPS,
  clock,
  dayLabel,
  doneSets,
  durationOf,
  kg,
  load,
  previousVolume,
  sessionVolume,
  setLabel,
  streak,
  volumeByGroup,
  volumeOf,
  type Session,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

type Undo = { id: number; label: string; restore: () => void };

/** The three things the tab is opened for when nothing is running. */
const VIEWS = [
  { id: "today", label: "오늘" },
  { id: "log", label: "기록" },
  { id: "lifts", label: "종목" },
] as const;
type View = (typeof VIEWS)[number]["id"];

const PAGE = 10;

/**
 * Training, in two states.
 *
 * While a session is running the set you are about to lift takes the bottom
 * of the screen and everything is within a thumb's reach; the rest of the
 * time this is a plan (which day is next) over a log you read. One surface
 * that tried to be both at once would be a dashboard with a timer buried in
 * it, which is no use in either situation.
 */
export function Workout() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  const w = useWorkout();
  const { book, routines, live, loaded, error } = w;

  const [editing, setEditing] = useState(false);
  const [looking, setLooking] = useState<string | null>(null);
  const [day, setDay] = useState<string | null>(null);
  const [shown, setShown] = useState(PAGE);
  const [view, setView] = useState<View>("today");

  // The view lives in the URL hash, so 기록 or 종목 can be bookmarked and the
  // back button steps between them. Read once after mount (the server has no
  // hash), then kept in step as it changes.
  useEffect(() => {
    const read = () => {
      const h = window.location.hash.slice(1);
      setView(VIEWS.some((v) => v.id === h) ? (h as View) : "today");
    };
    read();
    window.addEventListener("hashchange", read);
    return () => window.removeEventListener("hashchange", read);
  }, []);
  function choose(next: View) {
    setView(next);
    history.replaceState(
      null,
      "",
      next === "today" ? window.location.pathname : `#${next}`,
    );
  }

  // One undo at a time, for five seconds - long enough to see what you did,
  // short enough that it is gone before it is in the way.
  const [undo, setUndo] = useState<Undo | null>(null);
  const undoTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const offerUndo = useCallback((label: string, restore: () => void) => {
    if (undoTimer.current) clearTimeout(undoTimer.current);
    const id = Date.now();
    setUndo({ id, label, restore });
    undoTimer.current = setTimeout(
      () => setUndo((u) => (u?.id === id ? null : u)),
      5000,
    );
  }, []);
  useEffect(
    () => () => void (undoTimer.current && clearTimeout(undoTimer.current)),
    [],
  );

  const past = useMemo(
    () =>
      book.sessions
        .filter((s) => s.endedAt)
        .sort((a, b) => b.startedAt - a.startedAt),
    [book.sessions],
  );

  // Reading the clock during render is impure: the server and the hydrating
  // client would disagree about where "최근 7일" starts. Taken once after
  // mount instead, which is also often enough for a window measured in days.
  const [now, setNow] = useState<number | null>(null);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setNow(Date.now());
  }, []);

  const week = useMemo(() => {
    if (now === null) return null;
    const today = new Date(now);
    const days = Array.from({ length: 7 }, (_, i) => {
      const d = new Date(
        today.getFullYear(),
        today.getMonth(),
        today.getDate() - 6 + i,
      );
      return isoDate(d);
    });
    const recent = past.filter((s) => s.date >= days[0]);
    return {
      days,
      byDate: new Map(days.map((d) => [d, recent.filter((s) => s.date === d)])),
      count: recent.length,
      volume: recent.reduce((sum, s) => sum + sessionVolume(s), 0),
      time: recent.reduce((sum, s) => sum + durationOf(s), 0),
      byGroup: volumeByGroup(recent),
      streak: streak(past, today),
    };
  }, [past, now]);

  const log = day ? past.filter((s) => s.date === day) : past.slice(0, shown);

  function removeSession(session: Session) {
    const removed = w.removeSession(session.id);
    if (removed)
      offerUndo(`${dayLabel(session.date)} 기록을 지웠어요`, () =>
        w.restoreSession(removed),
      );
  }

  const notice = undo ? (
    <div
      role="status"
      className="mb-2 flex items-center gap-2 rounded-xl bg-popover/95 py-1 ps-3.5 pe-1 text-[13px] shadow-lg ring-1 ring-edge"
    >
      <span className="min-w-0 flex-1 truncate text-foreground/90">
        {undo.label}
      </span>
      <button
        type="button"
        onClick={() => {
          undo.restore();
          setUndo(null);
        }}
        className="tap shrink-0 rounded-lg px-3 font-medium text-foreground underline underline-offset-4 outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        되돌리기
      </button>
    </div>
  ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-5 pb-12 sm:px-8 sm:pt-7 sm:pb-8">
          {error ? (
            <p
              role="status"
              className="mb-5 border-l-2 border-reject pl-3 text-[13.5px] leading-relaxed text-dim"
            >
              {error}
            </p>
          ) : null}

          {live ? (
            <LiveSession
              session={live}
              history={book.sessions}
              restTarget={book.restTarget ?? 90}
              onRestTarget={w.setRestTarget}
              onAddExercise={(name) => w.addExercise(live.id, name)}
              onInsertExercise={(exercise, at) =>
                w.insertExercise(live.id, exercise, at)
              }
              onPatchSets={(exerciseId, fn) =>
                w.patchSets(live.id, exerciseId, fn)
              }
              onRemoveExercise={(exerciseId) =>
                w.removeExercise(live.id, exerciseId)
              }
              onNote={(note) => w.setNote(live.id, note)}
              onBodyWeight={(weight) => w.setBodyWeight(live.id, weight)}
              onEnd={(at) => w.endSession(live.id, at)}
              onDiscard={() => {
                const removed = w.removeSession(live.id);
                if (removed)
                  offerUndo("운동 기록을 버렸어요", () =>
                    w.restoreSession(removed),
                  );
              }}
              onUndoable={offerUndo}
              notice={notice}
            />
          ) : loaded ? (
            <>
              <ViewTabs view={view} onChange={choose} />

              {view === "today" ? (
                <div
                  id="view-today"
                  role="tabpanel"
                  aria-labelledby="tab-today"
                >
                  <section aria-label="루틴" className="mb-10">
                    <RoutinePicker
                      routines={routines}
                      sessions={book.sessions}
                      onStart={(routine) => w.startRoutine(routine)}
                      onFree={() => w.startSession()}
                      // Same movements, maybe a little more weight - that is what
                      // most sessions are, and retyping them is what makes people
                      // stop logging.
                      onRepeat={past[0] ? () => w.startSession(past[0]) : null}
                      onEdit={() => setEditing(true)}
                    />
                  </section>

                  {week ? (
                    <section aria-label="최근 7일">
                      <div className="mb-3 flex items-baseline justify-between gap-3">
                        <h2 className="text-[13px] text-dim">최근 7일</h2>
                        <p className="tnum text-[12px] text-faint">
                          {week.count}회 · {kg(week.volume)} ·{" "}
                          {clock(week.time)}
                          {week.streak > 1 ? ` · ${week.streak}일 연속` : ""}
                        </p>
                      </div>

                      {/* The week as seven blocks, today on the right: whether you
                      are keeping it up is a pattern before it is a number. */}
                      <ol className="mb-4 grid grid-cols-7 gap-1">
                        {week.days.map((date) => {
                          const done = week.byDate.get(date) ?? [];
                          const d = new Date(`${date}T00:00:00`);
                          const isToday = date === week.days[6];
                          return (
                            <li
                              key={date}
                              aria-label={`${dayLabel(date)}${done.length ? `, ${done.map((s) => s.routineName ?? "운동").join(", ")}` : ", 쉼"}`}
                              className={cn(
                                "flex h-14 flex-col items-center justify-center gap-1 rounded-lg",
                                done.length ? "bg-approve/12" : "bg-glass",
                              )}
                            >
                              <span
                                className={cn(
                                  "text-[11px]",
                                  isToday
                                    ? "font-semibold text-foreground"
                                    : "text-faint",
                                )}
                              >
                                {"일월화수목금토"[d.getDay()]}
                              </span>
                              <span className="max-w-full truncate px-1 text-[9.5px] leading-none text-approve/80">
                                {done[0]?.routineName ??
                                  (done.length ? "운동" : "")}
                              </span>
                            </li>
                          );
                        })}
                      </ol>

                      {/* Which parts got worked. The question weekly volume is
                      for is "did I skip legs again", and a total in kilograms
                      cannot answer it - so every part is listed, a skipped one
                      as an empty track rather than a missing row. */}
                      {week.byGroup.size > 0 ? (
                        <ul className="space-y-1.5">
                          {(() => {
                            const top = Math.max(...week.byGroup.values(), 1);
                            return MUSCLE_GROUPS.filter(
                              (g) =>
                                g !== "기타" || (week.byGroup.get(g) ?? 0) > 0,
                            ).map((group) => {
                              const value = week.byGroup.get(group) ?? 0;
                              return (
                                <li
                                  key={group}
                                  className="flex items-center gap-3"
                                >
                                  <span
                                    className={cn(
                                      "w-8 shrink-0 text-[12px]",
                                      value ? "text-dim" : "text-faint",
                                    )}
                                  >
                                    {group}
                                  </span>
                                  <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-well">
                                    <span
                                      className="block h-full rounded-full bg-dim/55"
                                      style={{
                                        width: `${(value / top) * 100}%`,
                                      }}
                                    />
                                  </span>
                                  <span className="tnum w-16 shrink-0 text-right text-[11.5px] text-faint">
                                    {value ? kg(value) : "—"}
                                  </span>
                                </li>
                              );
                            });
                          })()}
                        </ul>
                      ) : (
                        <p className="text-[13px] text-faint">
                          이번 주는 아직 기록이 없어요.
                        </p>
                      )}
                    </section>
                  ) : null}
                </div>
              ) : null}

              {view === "log" ? (
                <section
                  id="view-log"
                  role="tabpanel"
                  aria-labelledby="tab-log"
                >
                  <WorkoutCalendar
                    sessions={book.sessions}
                    selected={day}
                    onSelect={setDay}
                  />

                  {day ? (
                    <div className="mt-4 flex items-center gap-2 text-[12px]">
                      <button
                        type="button"
                        onClick={() => setDay(null)}
                        className="flex min-h-11 items-center gap-1 rounded-full bg-glass-raised ps-3.5 pe-2.5 text-foreground/90 outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                      >
                        {dayLabel(day)}
                        <X aria-hidden className="size-3.5 text-faint" />
                        <span className="sr-only">날짜 필터 지우기</span>
                      </button>
                    </div>
                  ) : null}

                  {past.length === 0 ? (
                    <p className="mt-6 text-[13.5px] leading-relaxed text-faint">
                      아직 기록이 없어요. 위에서 루틴을 시작하면 종목과 세트가
                      여기에 쌓이고, 다음부터는 세트마다 지난번 기록이 옆에
                      붙어요.
                    </p>
                  ) : (
                    <div className="mt-6 space-y-8">
                      {log.map((session) => (
                        <SessionLog
                          key={session.id}
                          session={session}
                          all={book.sessions}
                          onLook={setLooking}
                          onRemove={() => removeSession(session)}
                        />
                      ))}
                      {!day && past.length > shown ? (
                        <button
                          type="button"
                          onClick={() => setShown((n) => n + PAGE)}
                          className="tap w-full rounded-lg text-[13px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
                        >
                          이전 기록 더 보기
                        </button>
                      ) : null}
                    </div>
                  )}
                </section>
              ) : null}

              {view === "lifts" ? (
                <div
                  id="view-lifts"
                  role="tabpanel"
                  aria-labelledby="tab-lifts"
                >
                  <Lifts sessions={book.sessions} onOpen={setLooking} />
                </div>
              ) : null}
            </>
          ) : null}
        </div>

        {!live && notice ? (
          <div
            className="pointer-events-none fixed inset-x-0 z-30 mx-auto max-w-[42rem] px-4 sm:px-8"
            style={{
              bottom:
                "calc(max(var(--tabbar-space), env(safe-area-inset-bottom)) + 0.75rem)",
            }}
          >
            <div className="pointer-events-auto">{notice}</div>
          </div>
        ) : null}
      </main>

      <RoutineEditor
        open={editing}
        onOpenChange={setEditing}
        routines={routines}
        sessions={book.sessions}
        onSave={w.saveRoutines}
      />
      <ExerciseHistory
        name={looking}
        sessions={book.sessions}
        onOpenChange={(o) => !o && setLooking(null)}
      />

      <TabBar pending={waiting.length} />
    </div>
  );
}

/**
 * 오늘, 기록, 종목 - a segmented control rather than three scrolls of one
 * long page. Arrow keys move between them, as a tab list should.
 */
function ViewTabs({
  view,
  onChange,
}: {
  view: View;
  onChange: (view: View) => void;
}) {
  return (
    <div
      role="tablist"
      aria-label="운동 화면"
      className="mb-7 grid grid-cols-3 gap-1 rounded-xl bg-glass p-1"
      onKeyDown={(e) => {
        if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
        const i = VIEWS.findIndex((v) => v.id === view);
        const next =
          VIEWS[
            (i + (e.key === "ArrowRight" ? 1 : VIEWS.length - 1)) % VIEWS.length
          ];
        onChange(next.id);
        document.getElementById(`tab-${next.id}`)?.focus();
      }}
    >
      {VIEWS.map((v) => {
        const on = v.id === view;
        return (
          <button
            key={v.id}
            id={`tab-${v.id}`}
            type="button"
            role="tab"
            aria-selected={on}
            aria-controls={`view-${v.id}`}
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(v.id)}
            className={cn(
              "min-h-11 rounded-lg text-[13.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              on
                ? "bg-glass-raised font-medium text-foreground"
                : "text-dim hover:text-foreground",
            )}
          >
            {v.label}
          </button>
        );
      })}
    </div>
  );
}

/** One finished session: what was lifted, set by set, against last time. */
function SessionLog({
  session,
  all,
  onLook,
  onRemove,
}: {
  session: Session;
  all: Session[];
  onLook: (name: string) => void;
  onRemove: () => void;
}) {
  return (
    <article
      aria-label={`${dayLabel(session.date)} ${session.routineName ?? "운동"}`}
    >
      <div className="mb-2 flex items-center gap-3">
        <h3 className="min-w-0 flex-1 truncate text-[13.5px] text-foreground/90">
          <span className="text-dim">{dayLabel(session.date)}</span>
          <span className="ms-2">{session.routineName ?? "운동"}</span>
        </h3>
        <button
          type="button"
          onClick={onRemove}
          aria-label={`${dayLabel(session.date)} 기록 지우기`}
          className="tap -me-2 flex shrink-0 items-center justify-center rounded-lg text-faint/70 transition-colors outline-none hover:text-reject focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <Trash2 aria-hidden className="size-3.5" />
        </button>
      </div>
      <p className="tnum -mt-2 mb-3 text-[11.5px] text-faint">
        {kg(sessionVolume(session))} · {doneSets(session)}세트 ·{" "}
        {clock(durationOf(session))}
        {session.bodyWeight ? ` · 체중 ${load(session.bodyWeight)}kg` : ""}
      </p>
      {session.note ? (
        <p className="mb-3 text-[12.5px] leading-relaxed text-dim">
          {session.note}
        </p>
      ) : null}

      <ul className="space-y-3.5">
        {session.exercises
          .filter((e) => e.sets.some((s) => s.done))
          .map((exercise) => {
            const working = exercise.sets.filter((s) => s.done && !s.warmup);
            const warm = exercise.sets.filter((s) => s.done && s.warmup).length;
            return (
              <li key={exercise.id}>
                <div className="flex items-baseline gap-3">
                  <button
                    type="button"
                    onClick={() => onLook(exercise.name)}
                    className="shrink-0 rounded text-[13.5px] text-foreground/90 underline decoration-edge underline-offset-4 outline-none hover:decoration-dim focus-visible:ring-3 focus-visible:ring-ring/50"
                  >
                    {exercise.name}
                  </button>
                  <span className="tnum min-w-0 truncate text-[12px] text-dim">
                    {working.map((s, i) => (
                      <span key={i} className="me-2.5">
                        {setLabel(s)}
                      </span>
                    ))}
                    {warm ? (
                      <span className="text-faint">워밍업 {warm}</span>
                    ) : null}
                  </span>
                </div>
                {volumeOf(exercise) > 0 ? (
                  <VolumeBar
                    volume={volumeOf(exercise)}
                    previous={previousVolume(
                      all.filter((s) => s.startedAt < session.startedAt),
                      exercise.name,
                      session.id,
                    )}
                    className="mt-1.5"
                  />
                ) : null}
              </li>
            );
          })}
      </ul>
    </article>
  );
}
