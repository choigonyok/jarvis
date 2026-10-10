"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  carrySets,
  routinesOf,
  today,
  type Book,
  type Exercise,
  type Routine,
  type Session,
  type WorkoutSet,
} from "@/lib/workout";

/**
 * The training log, loaded once and written back whenever it changes.
 *
 * Saving is debounced because the thing that changes most is a set being
 * ticked off between breaths - a request per tap would be a request per
 * second with wet hands on hotel wifi. A pending write is flushed on unload
 * so nothing logged is lost by closing the tab.
 */
export function useWorkout() {
  const [book, setBook] = useState<Book>({ sessions: [] });
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latest = useRef<Book>(book);

  useEffect(() => {
    void (async () => {
      try {
        const res = await fetch("/api/workout", { cache: "no-store" });
        if (!res.ok) throw new Error(String(res.status));
        const data = (await res.json()) as Book;
        setBook(data);
        latest.current = data;
      } catch {
        setError("기록을 불러오지 못했습니다.");
      } finally {
        setLoaded(true);
      }
    })();
  }, []);

  const save = useCallback(async (next: Book) => {
    try {
      const res = await fetch("/api/workout", {
        method: "PUT",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(next),
      });
      if (!res.ok) throw new Error(String(res.status));
      setError(null);
    } catch {
      setError("기록을 저장하지 못했습니다.");
    }
  }, []);

  const update = useCallback(
    (fn: (book: Book) => Book) => {
      setBook((prev) => {
        const next = fn(prev);
        latest.current = next;
        if (timer.current) clearTimeout(timer.current);
        timer.current = setTimeout(() => void save(next), 600);
        return next;
      });
    },
    [save],
  );

  // A tab closed mid-set should not lose it.
  useEffect(() => {
    const flush = () => {
      if (!timer.current) return;
      clearTimeout(timer.current);
      timer.current = null;
      navigator.sendBeacon?.(
        "/api/workout",
        new Blob([JSON.stringify(latest.current)], { type: "application/json" }),
      );
    };
    window.addEventListener("pagehide", flush);
    return () => {
      window.removeEventListener("pagehide", flush);
      flush();
    };
  }, []);

  const live = book.sessions.find((s) => !s.endedAt) ?? null;

  /**
   * Start a session, optionally carrying over the shape of an earlier one.
   *
   * Repeating last time is how most training actually works - same movements,
   * maybe a little more weight - and typing the same five exercises back in
   * every session is the tax that makes people stop logging. The weights come
   * along as a starting point; the ticks do not, because nothing has been
   * lifted yet.
   */
  const startSession = useCallback(
    (from?: Session) => {
      const session: Session = {
        id: `w-${Date.now()}`,
        date: today(),
        routineId: from?.routineId,
        routineName: from?.routineName,
        exercises: (from?.exercises ?? []).map((e, i) => ({
          id: `e-${Date.now()}-${i}`,
          name: e.name,
          sets: e.sets.map((set) => ({ ...set, done: false })),
        })),
        startedAt: Date.now(),
      };
      update((b) => ({ ...b, sessions: [...b.sessions, session] }));
      return session.id;
    },
    [update],
  );

  /**
   * Start the named day of the split, with its movements already laid out.
   *
   * The loads are carried over from the last time each movement was trained,
   * so the first thing on screen is what you lifted last rather than a row of
   * zeroes to retype.
   */
  const startRoutine = useCallback(
    (routine: Routine) => {
      const stamp = Date.now();
      update((b) => {
        const session: Session = {
          id: `w-${stamp}`,
          date: today(),
          routineId: routine.id,
          routineName: routine.name,
          exercises: routine.exercises.map((name, i) => ({
            id: `e-${stamp}-${i}`,
            name,
            sets: carrySets(b.sessions, name),
          })),
          startedAt: stamp,
        };
        return { ...b, sessions: [...b.sessions, session] };
      });
      return `w-${stamp}`;
    },
    [update],
  );

  const saveRoutines = useCallback(
    (routines: Routine[]) => update((b) => ({ ...b, routines })),
    [update],
  );

  /**
   * Finish a session, at `at` if it ended earlier than now (a session left
   * open overnight ended at its last set, not at breakfast).
   *
   * Sets never ticked were planned, not lifted, so they are dropped rather
   * than kept as zeroes - next time's "지난번" is then what actually happened.
   * A session with nothing ticked was a false start, not a rest day with a
   * zero in it, and goes entirely.
   */
  const endSession = useCallback(
    (id: string, at?: number) => {
      update((b) => ({
        ...b,
        sessions: b.sessions.flatMap((s) => {
          if (s.id !== id) return [s];
          const exercises = s.exercises
            .map((e) => ({ ...e, sets: e.sets.filter((set) => set.done) }))
            .filter((e) => e.sets.length > 0);
          if (exercises.length === 0) return [];
          return [{ ...s, exercises, endedAt: Math.max(s.startedAt, at ?? Date.now()) }];
        }),
      }));
    },
    [update],
  );

  /** Take a session out of the log. Returns it, so the caller can offer undo. */
  const removeSession = useCallback(
    (id: string): Session | null => {
      const found = latest.current.sessions.find((s) => s.id === id) ?? null;
      update((b) => ({ ...b, sessions: b.sessions.filter((s) => s.id !== id) }));
      return found;
    },
    [update],
  );

  const restoreSession = useCallback(
    (session: Session) =>
      update((b) =>
        b.sessions.some((s) => s.id === session.id)
          ? b
          : { ...b, sessions: [...b.sessions, session] },
      ),
    [update],
  );

  const patchSession = useCallback(
    (id: string, fn: (session: Session) => Session) => {
      update((b) => ({
        ...b,
        sessions: b.sessions.map((s) => (s.id === id ? fn(s) : s)),
      }));
    },
    [update],
  );

  /** How long the rest timer counts down to, kept across sessions. */
  const setRestTarget = useCallback(
    (seconds: number) => update((b) => ({ ...b, restTarget: seconds })),
    [update],
  );

  const addExercise = useCallback(
    (id: string, name: string) => {
      patchSession(id, (s) => ({
        ...s,
        exercises: [
          ...s.exercises,
          {
            id: `e-${Date.now()}`,
            name,
            sets: carrySets(latest.current.sessions, name),
          },
        ],
      }));
    },
    [patchSession],
  );

  /** Put a removed movement back where it was. */
  const insertExercise = useCallback(
    (id: string, exercise: Exercise, at: number) => {
      patchSession(id, (s) => {
        if (s.exercises.some((e) => e.id === exercise.id)) return s;
        const exercises = [...s.exercises];
        exercises.splice(Math.min(at, exercises.length), 0, exercise);
        return { ...s, exercises };
      });
    },
    [patchSession],
  );

  const patchSets = useCallback(
    (id: string, exerciseId: string, fn: (sets: WorkoutSet[]) => WorkoutSet[]) => {
      patchSession(id, (s) => ({
        ...s,
        exercises: s.exercises.map((e) =>
          e.id === exerciseId ? { ...e, sets: fn(e.sets) } : e,
        ),
      }));
    },
    [patchSession],
  );

  const removeExercise = useCallback(
    (id: string, exerciseId: string) => {
      patchSession(id, (s) => ({
        ...s,
        exercises: s.exercises.filter((e) => e.id !== exerciseId),
      }));
    },
    [patchSession],
  );

  const setNote = useCallback(
    (id: string, note: string) => patchSession(id, (s) => ({ ...s, note })),
    [patchSession],
  );

  const setBodyWeight = useCallback(
    (id: string, bodyWeight: number) =>
      patchSession(id, (s) => ({ ...s, bodyWeight })),
    [patchSession],
  );

  const routines = routinesOf(book);

  return {
    book,
    routines,
    live,
    loaded,
    error,
    startSession,
    startRoutine,
    saveRoutines,
    endSession,
    removeSession,
    restoreSession,
    addExercise,
    insertExercise,
    patchSets,
    removeExercise,
    setNote,
    setBodyWeight,
    setRestTarget,
  };
}
