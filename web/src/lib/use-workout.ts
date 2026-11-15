"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  carrySets,
  routinesOf,
  setGroups,
  today,
  type Book,
  type Exercise,
  type Routine,
  type Session,
  type WorkoutSet,
} from "@/lib/workout";
import { onResume } from "@/lib/resume";
import { local } from "@/lib/use-gym";

/**
 * The training log, loaded once and written back whenever it changes.
 *
 * Saving is debounced because the thing that changes most is a set being
 * ticked off between breaths - a request per tap would be a request per
 * second with wet hands on hotel wifi. A pending write is flushed on unload
 * so nothing logged is lost by closing the tab.
 *
 * Only what changed since the last save goes over the wire - sessions by id,
 * deletions by id. The guest and the operator share this book, and sending
 * the whole thing would delete whatever the other had added since.
 *
 * Those same changes are kept on the device until the server has them. A gym
 * in a basement has no signal, and a phone puts a home-screen app away without
 * a word - an hour logged there had lived only in the page, and went with it.
 * They go back over the book each time it is read, and are sent again
 * whenever the page comes back, the network with it.
 */
type Changes = {
  sessions?: Session[];
  deleted?: string[];
  restTarget?: number;
  routines?: Routine[];
};

const UNSENT = "jarvis.workout.unsent";

// Which running session is this device's. A session does not say whose it is
// (the book is one for the two of us), so the first one not yet ended was
// opened as this phone's own: the partner's workout under this thumb, no 시작
// for one's own, and a set pressed on it sent this phone's copy over theirs.
// A device knows the one it started, or the one it was asked to carry on.
const MINE = "jarvis.workout.mine";

function diff(from: Book, to: Book): Changes | null {
  const before = new Map(from.sessions.map((s) => [s.id, JSON.stringify(s)]));
  const ids = new Set(to.sessions.map((s) => s.id));
  const out: Changes = {};
  const sessions = to.sessions.filter((s) => before.get(s.id) !== JSON.stringify(s));
  if (sessions.length) out.sessions = sessions;
  const deleted = from.sessions.map((s) => s.id).filter((id) => !ids.has(id));
  if (deleted.length) out.deleted = deleted;
  if (to.restTarget !== undefined && to.restTarget !== from.restTarget) out.restTarget = to.restTarget;
  if (to.routines && JSON.stringify(to.routines) !== JSON.stringify(from.routines)) {
    out.routines = to.routines;
  }
  return Object.keys(out).length ? out : null;
}

/** Changes laid over a book as it was read - sessions by id, deletions by id. */
function overlay(book: Book, changes: Changes): Book {
  const changed = new Map((changes.sessions ?? []).map((s) => [s.id, s]));
  const gone = new Set(changes.deleted);
  const known = new Set(book.sessions.map((s) => s.id));
  const sessions = book.sessions
    .filter((s) => !gone.has(s.id))
    .map((s) => changed.get(s.id) ?? s);
  for (const s of changed.values()) if (!known.has(s.id)) sessions.push(s);
  return {
    ...book,
    sessions,
    restTarget: changes.restTarget ?? book.restTarget,
    routines: changes.routines ?? book.routines,
  };
}

export function useWorkout() {
  const [book, setBook] = useState<Book>({ sessions: [] });
  // When the book was last read, null until it first has been: an empty book
  // that was never read is not an empty log, and is not shown as one.
  const [readAt, setReadAt] = useState<Date | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  // A save that did not go, and none has since: what was logged since is on
  // this device only.
  const [unsent, setUnsent] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latest = useRef<Book>(book);
  // What the server is known to hold. Changes are measured against this.
  const saved = useRef<Book>(book);
  const sending = useRef(false);
  const again = useRef(false);
  const [mine, setMine] = useState<string | null>(() => local.get<string>(MINE));
  const own = useCallback((id: string) => {
    setMine(id);
    local.set(MINE, id);
  }, []);

  // One save at a time, and whatever came while it was on its way right after
  // it: two at once could land in either order, the older one last.
  const save = useCallback(async () => {
    if (sending.current) {
      again.current = true;
      return;
    }
    sending.current = true;
    try {
      do {
        again.current = false;
        const next = latest.current;
        const changes = diff(saved.current, next);
        if (!changes) break;
        const res = await fetch("/api/workout/changes", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(changes),
        });
        if (!res.ok) throw new Error(String(res.status));
        saved.current = next;
      } while (again.current);
      setUnsent(false);
    } catch {
      setUnsent(true);
    } finally {
      sending.current = false;
      local.set(UNSENT, diff(saved.current, latest.current));
    }
  }, []);

  const read = useCallback(async () => {
    const asked = latest.current;
    try {
      const res = await fetch("/api/workout", { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const data = (await res.json()) as Book;
      // A press made while this was on its way is newer than the answer: it
      // stands, and the next time the page comes back reads again.
      if (latest.current !== asked) return;
      setGroups(data.groups);
      // What had not reached the server - this page's, or one a phone put
      // away with it - goes back over what has, and is sent.
      const kept = local.get<Changes>(UNSENT);
      const next = kept ? overlay(data, kept) : data;
      setBook(next);
      // Another tab of this browser may have started one since. Storage that
      // keeps nothing leaves this page's own as it was.
      const stored = local.get<string>(MINE);
      if (stored) setMine(stored);
      latest.current = next;
      saved.current = data;
      setReadAt(new Date());
      setReadError(null);
      if (kept) void save();
    } catch {
      setReadError(
        local.get<Changes>(UNSENT)
          ? "기록을 불러오지 못했어요. 아직 보내지 못한 기록은 이 기기에 있어요."
          : "기록을 불러오지 못했어요.",
      );
    }
  }, [save]);

  useEffect(() => {
    // read sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void read();
  }, [read]);

  // Coming back to the app, the network back, a pull to refresh: what has not
  // reached the server goes first - this copy is the newer one until it has,
  // and a read would undo it - and then the book is read again, since another
  // device may have logged.
  const reload = useCallback(async () => {
    if (timer.current) return;
    if (diff(saved.current, latest.current)) {
      await save();
      if (diff(saved.current, latest.current)) return;
    }
    return read();
  }, [read, save]);
  useEffect(() => onResume(reload), [reload]);

  const update = useCallback(
    (fn: (book: Book) => Book) => {
      setBook((prev) => {
        const next = fn(prev);
        latest.current = next;
        // On the device at once, not when the save goes: a phone can put the
        // page away in between.
        local.set(UNSENT, diff(saved.current, next));
        if (timer.current) clearTimeout(timer.current);
        // Cleared as it fires: a timer left set read as a save always on its
        // way, and the book was never read again after the first change.
        timer.current = setTimeout(() => {
          timer.current = null;
          void save();
        }, 600);
        return next;
      });
    },
    [save],
  );

  // A tab closed mid-set should not lose it - nor one closed after a save
  // that did not go.
  useEffect(() => {
    const flush = () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
      const changes = diff(saved.current, latest.current);
      if (!changes) return;
      navigator.sendBeacon?.(
        "/api/workout/changes",
        new Blob([JSON.stringify(changes)], { type: "application/json" }),
      );
    };
    window.addEventListener("pagehide", flush);
    return () => {
      window.removeEventListener("pagehide", flush);
      flush();
    };
  }, []);

  const running = book.sessions.filter((s) => !s.endedAt);
  const live = running.find((s) => s.id === mine) ?? null;
  // Running and not this device's: the partner's, or one started on another
  // phone. Not opened here unless asked, and never ended or written over.
  const elsewhere = live
    ? null
    : running.reduce<Session | null>((a, s) => (!a || s.startedAt > a.startedAt ? s : a), null);

  /**
   * Carry on here a session started elsewhere - this person's own, begun on
   * another device. Read again on the way, for the sets logged there since.
   */
  const continueHere = useCallback(
    (id: string) => {
      own(id);
      void reload();
    },
    [own, reload],
  );

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
      own(session.id);
      return session.id;
    },
    [update, own],
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
        // From finished sessions only: one running on another phone is
        // somebody's sets in the making, not this one's last time.
        const done = b.sessions.filter((s) => s.endedAt);
        const session: Session = {
          id: `w-${stamp}`,
          date: today(),
          routineId: routine.id,
          routineName: routine.name,
          exercises: routine.exercises.map((name, i) => ({
            id: `e-${stamp}-${i}`,
            name,
            sets: carrySets(done, name),
          })),
          startedAt: stamp,
        };
        return { ...b, sessions: [...b.sessions, session] };
      });
      own(`w-${stamp}`);
      return `w-${stamp}`;
    },
    [update, own],
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
   * zero in it, and goes entirely. Returns it as it was, so the caller can
   * offer undo - the sets dropped here exist nowhere else.
   */
  const endSession = useCallback(
    (id: string, at?: number): Session | null => {
      const found = latest.current.sessions.find((s) => s.id === id) ?? null;
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
      return found;
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

  /** Put a session back as it was: taken out, or finished (its unticked sets and all). */
  const restoreSession = useCallback(
    (session: Session) =>
      update((b) =>
        b.sessions.some((s) => s.id === session.id)
          ? { ...b, sessions: b.sessions.map((s) => (s.id === session.id ? session : s)) }
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
            sets: carrySets(
              latest.current.sessions.filter((other) => other.endedAt || other.id === id),
              name,
            ),
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
    elsewhere,
    continueHere,
    readAt,
    readError,
    unsent,
    reload,
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
