/**
 * A workout is a list of exercises, each a list of sets. That is the whole
 * model - everything the surface shows (volume, totals, comparisons against
 * last time) is derived from it rather than stored, so there is no second
 * number that can disagree with the sets you actually logged.
 */

export type WorkoutSet = {
  /** kg. Bodyweight movements are logged as 0 and still count their reps. */
  weight: number;
  reps: number;
  done: boolean;
  /**
   * A warm-up set is work you did but not work that counts. Every serious
   * log separates the two, because counting your empty-bar sets into weekly
   * volume makes the number useless for deciding whether to add weight.
   */
  warmup?: boolean;
};

export type Exercise = {
  id: string;
  name: string;
  sets: WorkoutSet[];
  note?: string;
};

/**
 * A named day of a split.
 *
 * Most people train the same few days on rotation, so the movements are
 * decided long before the gym - what is decided at the door is only which day
 * it is. Keeping that list here means starting a session is one tap rather
 * than typing six exercises back in.
 */
export type Routine = {
  id: string;
  name: string;
  /** Movement names, in the order they are trained. */
  exercises: string[];
};

export type Session = {
  /** YYYY-MM-DD, local. */
  date: string;
  id: string;
  /** Which routine this was, if it came from one. Kept by name too, so
   *  renaming or deleting a routine does not erase what you actually did. */
  routineId?: string;
  routineName?: string;
  exercises: Exercise[];
  /** Epoch ms. `endedAt` missing means it is still running. */
  startedAt: number;
  endedAt?: number;
  note?: string;
  /** kg, logged at most once a day. */
  bodyWeight?: number;
};

export type Book = {
  sessions: Session[];
  /** Seconds. What the rest timer counts down to. */
  restTarget?: number;
  routines?: Routine[];
};

/**
 * The three-day split this log is set up for, used until routines are edited.
 *
 * Seeded rather than left blank because an empty routine list makes the
 * feature look broken on the first visit, and these three are the ones the
 * log is actually for.
 */
export const DEFAULT_ROUTINES: Routine[] = [
  {
    id: "r-push",
    name: "가슴/삼두",
    exercises: ["벤치프레스", "인클라인 덤벨프레스", "딥스", "케이블 푸시다운"],
  },
  {
    id: "r-pull",
    name: "등/이두",
    exercises: ["데드리프트", "바벨로우", "랫풀다운", "덤벨컬"],
  },
  {
    id: "r-legs",
    name: "어깨/하체",
    exercises: ["오버헤드프레스", "사이드 레터럴 레이즈", "스쿼트", "레그프레스"],
  },
];

export function routinesOf(book: Book): Routine[] {
  return book.routines && book.routines.length > 0
    ? book.routines
    : DEFAULT_ROUTINES;
}

/** When a routine was last trained, or null if never. */
export function lastDone(sessions: Session[], routine: Routine): Session | null {
  const done = sessions
    .filter(
      (s) =>
        s.endedAt &&
        (s.routineId === routine.id || s.routineName === routine.name),
    )
    .sort((a, b) => b.startedAt - a.startedAt);
  return done[0] ?? null;
}

/**
 * Whole days since a routine was last trained. Null means never - which sorts
 * ahead of everything, because a day you have not touched is more overdue than
 * one you did a fortnight ago.
 */
export function daysSince(session: Session | null, now = new Date()): number | null {
  if (!session) return null;
  const [y, m, d] = session.date.split("-").map(Number);
  const then = new Date(y, m - 1, d);
  const today0 = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  return Math.round((today0.getTime() - then.getTime()) / 86_400_000);
}

/** The routine you have gone longest without, never-trained first. */
export function mostOverdue(
  sessions: Session[],
  routines: Routine[],
  now = new Date(),
): Routine | null {
  if (routines.length === 0) return null;
  let worst: { routine: Routine; days: number } | null = null;
  for (const routine of routines) {
    const days = daysSince(lastDone(sessions, routine), now);
    const score = days === null ? Number.POSITIVE_INFINITY : days;
    if (!worst || score > worst.days) worst = { routine, days: score };
  }
  return worst?.routine ?? null;
}

/** kg × reps, over the working sets actually completed. Warm-ups excluded. */
export function volumeOf(exercise: Exercise): number {
  return exercise.sets.reduce(
    (sum, set) => (set.done && !set.warmup ? sum + set.weight * set.reps : sum),
    0,
  );
}

/**
 * Epley: weight × (1 + reps/30).
 *
 * An estimate, and only honest in the 1-10 rep range - past that it flatters
 * you badly. Shown as "추정" for that reason, and not at all above 12 reps.
 */
export function estimate1RM(weight: number, reps: number): number | null {
  if (weight <= 0 || reps <= 0 || reps > 12) return null;
  return weight * (1 + reps / 30);
}

/** The best single set ever logged for a movement, by estimated 1RM. */
export function bestSet(
  sessions: Session[],
  name: string,
): { weight: number; reps: number; oneRm: number; date: string } | null {
  const normalized = name.trim().toLowerCase();
  let best: { weight: number; reps: number; oneRm: number; date: string } | null =
    null;

  for (const session of sessions) {
    for (const exercise of session.exercises) {
      if (exercise.name.trim().toLowerCase() !== normalized) continue;
      for (const set of exercise.sets) {
        if (!set.done || set.warmup) continue;
        const oneRm = estimate1RM(set.weight, set.reps);
        if (oneRm === null) continue;
        if (!best || oneRm > best.oneRm) {
          best = { weight: set.weight, reps: set.reps, oneRm, date: session.date };
        }
      }
    }
  }
  return best;
}

/** Heaviest single working set ever, regardless of reps. */
export function heaviest(sessions: Session[], name: string): number {
  const normalized = name.trim().toLowerCase();
  let top = 0;
  for (const session of sessions) {
    for (const exercise of session.exercises) {
      if (exercise.name.trim().toLowerCase() !== normalized) continue;
      for (const set of exercise.sets) {
        if (set.done && !set.warmup && set.weight > top) top = set.weight;
      }
    }
  }
  return top;
}

export function sessionVolume(session: Session): number {
  return session.exercises.reduce((sum, e) => sum + volumeOf(e), 0);
}

export function doneSets(session: Session): number {
  return session.exercises.reduce(
    (sum, e) => sum + e.sets.filter((s) => s.done && !s.warmup).length,
    0,
  );
}

/**
 * Consecutive days trained, counting back from today.
 *
 * Yesterday still counts as alive - a streak that dies at midnight before
 * you have had a chance to train punishes you for the clock rather than for
 * skipping.
 */
export function streak(sessions: Session[], now = new Date()): number {
  const days = new Set(sessions.filter((s) => s.endedAt).map((s) => s.date));
  const cursor = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const key = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;

  if (!days.has(key(cursor))) cursor.setDate(cursor.getDate() - 1);

  let count = 0;
  while (days.has(key(cursor))) {
    count += 1;
    cursor.setDate(cursor.getDate() - 1);
  }
  return count;
}

/** Milliseconds. A session still running is measured to now. */
export function durationOf(session: Session, now = Date.now()): number {
  return Math.max(0, (session.endedAt ?? now) - session.startedAt);
}

/** 1:05:30 while long, 5:30 while short - an hour marker that appears when earned. */
export function clock(ms: number): string {
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

export function kg(value: number): string {
  return `${Math.round(value).toLocaleString("ko-KR")}kg`;
}

export function signedKg(value: number): string {
  const sign = value > 0 ? "+" : value < 0 ? "−" : "";
  return `${sign}${kg(Math.abs(value))}`;
}

export function today(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(
    d.getDate(),
  ).padStart(2, "0")}`;
}

const WEEKDAYS = ["일", "월", "화", "수", "목", "금", "토"];

export function dayLabel(date: string, now = new Date()): string {
  const [y, m, d] = date.split("-").map(Number);
  const when = new Date(y, m - 1, d);
  const days = Math.round(
    (new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() -
      when.getTime()) /
      86_400_000,
  );
  if (days === 0) return "오늘";
  if (days === 1) return "어제";
  return `${m}월 ${d}일 (${WEEKDAYS[when.getDay()]})`;
}

/**
 * The last time this exercise was trained, before `exclude`.
 *
 * This is what the whole comparison rests on: "더 들었나"는 지난번과 견줘야만
 * 답이 되고, 지난번은 같은 종목의 직전 세션이다.
 */
export function previousVolume(
  sessions: Session[],
  name: string,
  excludeId: string,
): number | null {
  const normalized = name.trim().toLowerCase();
  const earlier = sessions
    .filter((s) => s.id !== excludeId)
    .sort((a, b) => b.startedAt - a.startedAt);

  for (const session of earlier) {
    const match = session.exercises.find(
      (e) => e.name.trim().toLowerCase() === normalized,
    );
    if (match) {
      const volume = volumeOf(match);
      if (volume > 0) return volume;
    }
  }
  return null;
}

/**
 * Which part a movement trains.
 *
 * Enough to answer "did I do legs this week", which is the question weekly
 * volume is actually for. Anything not listed falls under 기타 rather than
 * being guessed at.
 */
export const MUSCLE_GROUPS = [
  "가슴",
  "등",
  "어깨",
  "하체",
  "팔",
  "코어",
  "기타",
] as const;

export type MuscleGroup = (typeof MUSCLE_GROUPS)[number];

/** Starters, so the first tap is not an empty text field. */
export const COMMON_EXERCISES: { name: string; group: MuscleGroup }[] = [
  { name: "벤치프레스", group: "가슴" },
  { name: "인클라인 덤벨프레스", group: "가슴" },
  { name: "딥스", group: "가슴" },
  { name: "스쿼트", group: "하체" },
  { name: "레그프레스", group: "하체" },
  { name: "루마니안 데드리프트", group: "하체" },
  { name: "데드리프트", group: "등" },
  { name: "바벨로우", group: "등" },
  { name: "랫풀다운", group: "등" },
  { name: "풀업", group: "등" },
  { name: "오버헤드프레스", group: "어깨" },
  { name: "사이드 레터럴 레이즈", group: "어깨" },
  { name: "덤벨컬", group: "팔" },
  { name: "케이블 푸시다운", group: "팔" },
  { name: "플랭크", group: "코어" },
  { name: "행잉 레그레이즈", group: "코어" },
];

const GROUP_OF = new Map(COMMON_EXERCISES.map((e) => [e.name, e.group]));

export function groupOf(name: string): MuscleGroup {
  return GROUP_OF.get(name.trim()) ?? "기타";
}

/** Volume per body part across the given sessions. */
export function volumeByGroup(sessions: Session[]): Map<MuscleGroup, number> {
  const out = new Map<MuscleGroup, number>();
  for (const session of sessions) {
    for (const exercise of session.exercises) {
      const group = groupOf(exercise.name);
      out.set(group, (out.get(group) ?? 0) + volumeOf(exercise));
    }
  }
  return out;
}
