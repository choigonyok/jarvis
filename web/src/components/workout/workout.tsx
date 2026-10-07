"use client";

import { useEffect, useMemo, useState } from "react";
import { RotateCcw } from "lucide-react";
import { LiveSession } from "@/components/workout/live-session";
import { RoutinePicker } from "@/components/workout/routine-picker";
import { WorkoutCalendar } from "@/components/workout/workout-calendar";
import { VolumeBar } from "@/components/workout/volume-bar";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { Button } from "@/components/ui/button";
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
  previousVolume,
  sessionVolume,
  streak,
  volumeByGroup,
  volumeOf,
} from "@/lib/workout";

/**
 * Training, in two states.
 *
 * While a session is running the clock takes the screen and everything is
 * within a thumb's reach; the rest of the time this is a log you read. One
 * surface that tried to be both at once would be a dashboard with a timer
 * buried in it, which is no use in either situation.
 */
export function Workout() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  const {
    book,
    routines,
    live,
    loaded,
    error,
    startSession,
    startRoutine,
    endSession,
    addExercise,
    patchSets,
    removeExercise,
    setNote,
    setBodyWeight,
    setRestTarget,
  } = useWorkout();

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
    const since = (now ?? 0) - 7 * 86_400_000;
    if (now === null)
      return { count: 0, volume: 0, time: 0, byGroup: new Map(), days: 0 };
    const recent = past.filter((s) => s.startedAt >= since);
    return {
      count: recent.length,
      volume: recent.reduce((sum, s) => sum + sessionVolume(s), 0),
      time: recent.reduce((sum, s) => sum + durationOf(s), 0),
      byGroup: volumeByGroup(recent),
      days: streak(past, new Date(now)),
    };
  }, [past, now]);

  const last = past[0];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-5 pb-12 sm:px-8 sm:pt-7 sm:pb-6">
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
              onRestTarget={setRestTarget}
              onAddExercise={(name) => addExercise(live.id, name)}
              onPatchSets={(exerciseId, fn) => patchSets(live.id, exerciseId, fn)}
              onRemoveExercise={(exerciseId) => removeExercise(live.id, exerciseId)}
              onNote={(note) => setNote(live.id, note)}
              onBodyWeight={(weight) => setBodyWeight(live.id, weight)}
              onEnd={() => endSession(live.id)}
            />
          ) : (
            <>
              <div className="mb-6 flex items-end justify-between gap-4">
                <div>
                  <p className="text-[12px] text-faint">최근 7일</p>
                  <p className="tnum mt-0.5 text-[26px] leading-none font-semibold tracking-tight text-foreground sm:text-[30px]">
                    {week.count}회
                  </p>
                  <p className="tnum mt-2 text-[12.5px] text-dim">
                    {kg(week.volume)} · {clock(week.time)}
                    {week.days > 1 ? (
                      <span className="ms-2 text-faint">{week.days}일 연속</span>
                    ) : null}
                  </p>
                </div>
                {/* Same movements, maybe a little more weight - that is what
                    most sessions are, and retyping them is what makes people
                    stop logging. */}
                {last ? (
                  <Button
                    variant="ghost"
                    onClick={() => startSession(last)}
                    className="h-10 shrink-0 px-3 text-[12.5px] text-faint hover:text-foreground"
                  >
                    <RotateCcw aria-hidden className="size-3.5" />
                    지난 운동 반복
                  </Button>
                ) : null}
              </div>

              <section className="mb-7" aria-label="루틴">
                <RoutinePicker
                  routines={routines}
                  sessions={book.sessions}
                  onStart={(routine) => startRoutine(routine)}
                  onFree={() => startSession()}
                />
              </section>

              {/* Which parts got worked this week. The question weekly volume
                  is actually for is "did I skip legs again", and a total in
                  kilograms cannot answer it. */}
              {week.byGroup.size > 0 ? (
                <section className="mb-7" aria-label="부위별 주간 볼륨">
                  {(() => {
                    const top = Math.max(...week.byGroup.values(), 1);
                    return (
                      <ul className="space-y-1.5">
                        {MUSCLE_GROUPS.filter((g) => (week.byGroup.get(g) ?? 0) > 0).map(
                          (group) => {
                            const value = week.byGroup.get(group) ?? 0;
                            return (
                              <li key={group} className="flex items-center gap-3">
                                <span className="w-8 shrink-0 text-[12px] text-dim">
                                  {group}
                                </span>
                                <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-well">
                                  <span
                                    className="block h-full rounded-full bg-dim/55"
                                    style={{ width: `${(value / top) * 100}%` }}
                                  />
                                </span>
                                <span className="tnum w-16 shrink-0 text-right text-[11.5px] text-faint">
                                  {kg(value)}
                                </span>
                              </li>
                            );
                          },
                        )}
                      </ul>
                    );
                  })()}
                </section>
              ) : null}

              {loaded && past.length === 0 ? (
                <p className="text-[13.5px] leading-relaxed text-faint">
                  아직 기록이 없습니다. 운동을 시작하면 종목과 세트가 여기에
                  쌓이고, 다음부터는 지난 회차와 견줘 보여줍니다.
                </p>
              ) : null}

              <section className="mb-7 border-t border-edge-soft pt-5">
                <WorkoutCalendar sessions={book.sessions} />
              </section>

              <div className="space-y-7">
                {past.map((session) => (
                  <section key={session.id}>
                    <div className="mb-2.5 flex items-center gap-3">
                      <h2 className="text-[12px] text-faint">
                        {dayLabel(session.date)}
                        {session.routineName ? (
                          <span className="ms-2 text-dim">{session.routineName}</span>
                        ) : null}
                      </h2>
                      <span aria-hidden className="h-px flex-1 bg-edge-soft" />
                      <span className="tnum text-[11.5px] text-faint">
                        {kg(sessionVolume(session))} · {doneSets(session)}세트 ·{" "}
                        {clock(durationOf(session))}
                      </span>
                    </div>

                    {session.note || session.bodyWeight ? (
                      <p className="mb-2 text-[12px] text-dim">
                        {session.bodyWeight ? (
                          <span className="tnum me-2 text-faint">
                            체중 {session.bodyWeight}kg
                          </span>
                        ) : null}
                        {session.note}
                      </p>
                    ) : null}

                    <div className="space-y-3">
                      {session.exercises
                        .filter((e) => volumeOf(e) > 0)
                        .map((exercise) => (
                          <div key={exercise.id}>
                            <p className="mb-1 text-[13.5px] text-foreground/90">
                              {exercise.name}
                              <span className="tnum ms-2 text-[11.5px] text-faint">
                                {exercise.sets.filter((s) => s.done).length}세트
                              </span>
                            </p>
                            <VolumeBar
                              volume={volumeOf(exercise)}
                              previous={previousVolume(
                                book.sessions,
                                exercise.name,
                                session.id,
                              )}
                            />
                          </div>
                        ))}
                    </div>
                  </section>
                ))}
              </div>
            </>
          )}
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}
