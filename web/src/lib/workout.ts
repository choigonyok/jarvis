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

const sameName = (a: string, b: string) =>
  a.trim().toLowerCase() === b.trim().toLowerCase();

/** Newest first, the one in progress (`excludeId`) left out. */
function earlierThan(sessions: Session[], excludeId?: string): Session[] {
  return sessions
    .filter((s) => s.id !== excludeId)
    .sort((a, b) => b.startedAt - a.startedAt);
}

/**
 * The sets of the last time this movement was actually trained.
 *
 * This is the "지난번" column every serious log has: the number you are
 * trying to beat, on the row you are about to lift. Only sessions where the
 * movement had a set ticked off count - a row added and abandoned is not a
 * last time.
 */
export function previousSets(
  sessions: Session[],
  name: string,
  excludeId?: string,
): WorkoutSet[] | null {
  for (const session of earlierThan(sessions, excludeId)) {
    const match = session.exercises.find((e) => sameName(e.name, name));
    const done = match?.sets.filter((s) => s.done) ?? [];
    if (done.length > 0) return done;
  }
  return null;
}

/**
 * Which earlier set row `index` should be compared with.
 *
 * Warm-ups against warm-ups and working sets against working sets, each by
 * its own order - otherwise adding one warm-up today would shift every
 * comparison down a row and set 1 would be read against last time's ramp.
 */
export function previousFor(
  previous: WorkoutSet[] | null,
  sets: WorkoutSet[],
  index: number,
): WorkoutSet | null {
  if (!previous) return null;
  const warm = Boolean(sets[index]?.warmup);
  const nth = sets.slice(0, index).filter((s) => Boolean(s.warmup) === warm).length;
  return previous.filter((s) => Boolean(s.warmup) === warm)[nth] ?? null;
}

/**
 * The sets a movement starts with: last time's, unticked.
 *
 * The first thing on screen is what you lifted last rather than a row of
 * zeroes to retype - logging is then a tick, and progress is one stepper tap.
 */
export function carrySets(sessions: Session[], name: string): WorkoutSet[] {
  const previous = previousSets(sessions, name);
  return previous
    ? previous.map((s) => ({ ...s, done: false }))
    : [{ weight: 0, reps: 0, done: false }];
}

/** Every movement ever logged, most recently trained first, with how it went. */
export function recentExercises(
  sessions: Session[],
): { name: string; date: string; top: WorkoutSet | null }[] {
  const seen = new Map<string, { name: string; date: string; top: WorkoutSet | null }>();
  for (const session of earlierThan(sessions)) {
    for (const exercise of session.exercises) {
      const key = exercise.name.trim().toLowerCase();
      if (seen.has(key)) continue;
      const working = exercise.sets.filter((s) => s.done && !s.warmup);
      if (working.length === 0) continue;
      const top = working.reduce((a, b) => (b.weight > a.weight ? b : a));
      seen.set(key, { name: exercise.name.trim(), date: session.date, top });
    }
  }
  return [...seen.values()];
}

/** One movement across sessions, newest first - what the history sheet lists. */
export function exerciseHistory(
  sessions: Session[],
  name: string,
): { id: string; date: string; sets: WorkoutSet[]; volume: number; oneRm: number | null }[] {
  const out = [];
  for (const session of earlierThan(sessions)) {
    if (!session.endedAt) continue;
    const match = session.exercises.find((e) => sameName(e.name, name));
    if (!match) continue;
    const sets = match.sets.filter((s) => s.done);
    if (sets.length === 0) continue;
    let oneRm: number | null = null;
    for (const set of sets) {
      if (set.warmup) continue;
      const e = estimate1RM(set.weight, set.reps);
      if (e !== null && (oneRm === null || e > oneRm)) oneRm = e;
    }
    out.push({ id: session.id, date: session.date, sets, volume: volumeOf(match), oneRm });
  }
  return out;
}

/** Barbell lifts - the ones a plate breakdown means anything for. */
const BARBELL = new Set([
  "벤치프레스",
  "스쿼트",
  "데드리프트",
  "루마니안 데드리프트",
  "바벨로우",
  "오버헤드프레스",
]);

export function isBarbell(name: string): boolean {
  const n = name.trim();
  return BARBELL.has(n) || n.includes("바벨");
}

/** How much one tap of the stepper moves the load. Dumbbells jump by 2kg racks. */
export function weightStep(name: string): number {
  return name.includes("덤벨") ? 2 : 2.5;
}

const BAR = 20;
const PLATES = [20, 15, 10, 5, 2.5, 1.25];

/**
 * The plates on each side of a 20kg bar, heaviest first, or null if the load
 * cannot be built from a standard set (or is the bar alone).
 *
 * Adding up 62.5 - 20 = 42.5, halved, in your head between sets is exactly
 * the arithmetic that goes wrong when you are out of breath.
 */
export function platesPerSide(weight: number): number[] | null {
  let side = (weight - BAR) / 2;
  if (side <= 0) return null;
  const out: number[] = [];
  for (const plate of PLATES) {
    while (side >= plate - 1e-9) {
      out.push(plate);
      side -= plate;
    }
  }
  return Math.abs(side) < 1e-6 ? out : null;
}

function roundTo(value: number, step: number): number {
  return Math.round(value / step) * step;
}

/**
 * Warm-up sets leading up to a working load.
 *
 * The usual ramp - the empty bar, then about half, 70% and 85% - with the
 * reps falling as the load climbs, so the warm-up primes rather than tires.
 * Steps that round onto each other or onto the working load are dropped.
 */
export function warmupRamp(name: string, work: number): WorkoutSet[] {
  if (work <= 0) return [];
  const step = weightStep(name);
  const barbell = isBarbell(name);
  const plan: [number, number][] = barbell
    ? [
        [BAR, 10],
        [roundTo(work * 0.5, step), 5],
        [roundTo(work * 0.7, step), 3],
        [roundTo(work * 0.85, step), 1],
      ]
    : [
        [roundTo(work * 0.5, step), 10],
        [roundTo(work * 0.75, step), 5],
      ];
  const out: WorkoutSet[] = [];
  let last = 0;
  for (const [weight, reps] of plan) {
    if (weight <= last || weight >= work || (barbell && weight < BAR)) continue;
    out.push({ weight, reps, done: false, warmup: true });
    last = weight;
  }
  return out;
}

/** A place in a session: which movement, which row. */
export type SetRef = { exerciseId: string; index: number };

/**
 * The next set not yet ticked, reading on from `after` and wrapping round.
 *
 * This is what the dock points at - after a tick it moves on by itself, so
 * the hand that just racked the bar does not also have to find the next row.
 */
export function nextOpen(session: Session, after?: SetRef | null): SetRef | null {
  const flat: SetRef[] = [];
  for (const exercise of session.exercises) {
    exercise.sets.forEach((_, index) => flat.push({ exerciseId: exercise.id, index }));
  }
  const isOpen = (ref: SetRef) =>
    !session.exercises.find((e) => e.id === ref.exerciseId)?.sets[ref.index]?.done;
  const start = after
    ? flat.findIndex((r) => r.exerciseId === after.exerciseId && r.index === after.index) + 1
    : 0;
  for (let i = 0; i < flat.length; i++) {
    const ref = flat[(start + i) % flat.length];
    if (isOpen(ref)) return ref;
  }
  return null;
}

/**
 * What this session beat: a heavier working set than ever, or a better
 * estimated 1RM. A movement's first time is not a record - there was nothing
 * to beat - so it is left out rather than celebrated for showing up.
 */
export function sessionRecords(
  session: Session,
  sessions: Session[],
): { name: string; kind: "무게" | "1RM"; value: number; before: number }[] {
  const earlier = sessions.filter((s) => s.id !== session.id && s.startedAt < session.startedAt);
  const out: { name: string; kind: "무게" | "1RM"; value: number; before: number }[] = [];
  for (const exercise of session.exercises) {
    const working = exercise.sets.filter((s) => s.done && !s.warmup);
    if (working.length === 0) continue;
    const top = heaviest(earlier, exercise.name);
    const now = Math.max(...working.map((s) => s.weight));
    if (top > 0 && now > top) {
      out.push({ name: exercise.name, kind: "무게", value: now, before: top });
      continue;
    }
    const best = bestSet(earlier, exercise.name);
    const mine = Math.max(...working.map((s) => estimate1RM(s.weight, s.reps) ?? 0));
    if (best && mine > best.oneRm + 0.05) {
      out.push({ name: exercise.name, kind: "1RM", value: mine, before: best.oneRm });
    }
  }
  return out;
}

/** 62.5×8, or 12회 for a bodyweight set - "0kg" is not how anyone says a dip. */
export function setLabel(set: Pick<WorkoutSet, "weight" | "reps">): string {
  return set.weight > 0 ? `${load(set.weight)}×${set.reps}` : `${set.reps}회`;
}

/** 62.5, 60, 1.25 - a load as people say it, without a trailing ".0". */
export function load(value: number): string {
  return Number.isInteger(value) ? String(value) : String(Math.round(value * 100) / 100);
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
