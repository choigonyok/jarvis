"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, ChevronLeft, ChevronRight, Minus, Plus } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { BottomSheetContent, BottomSheetDescription, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Sheet } from "@/components/ui/sheet";
import {
  type Bar,
  type Check as DayCheck,
  type Goal,
  type Mark,
  type Stats,
  changeGoal,
  checkDay,
  dayLabel,
  monthWeeks,
  perDayOf,
  percent,
  readGoal,
  removeGoal,
  tally,
  WEEK_ROW,
  whenLabel,
} from "@/lib/goals";
import { SAY } from "@/lib/say";
import { monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";


/**
 * One goal, all the way back: how much of it was done, how it went week by
 * week, each day on a calendar - and any day there can still be corrected.
 */
export function GoalSheet({
  goal,
  onOpenChange,
  onChanged,
  onEdit,
}: {
  goal: Goal | null;
  onOpenChange: (open: boolean) => void;
  onChanged: () => void;
  onEdit: (g: Goal) => void;
}) {
  return (
    <Sheet open={goal !== null} onOpenChange={onOpenChange}>
      <BottomSheetContent width="lg" height="full">
        {goal ? <Body key={goal.id} goal={goal} onChanged={onChanged} onClose={() => onOpenChange(false)} onEdit={onEdit} /> : null}
      </BottomSheetContent>
    </Sheet>
  );
}

function Body({ goal, onChanged, onClose, onEdit }: { goal: Goal; onChanged: () => void; onClose: () => void; onEdit: (g: Goal) => void }) {
  const [now, setNow] = useState(goal);
  const [stats, setStats] = useState<Stats | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const got = await readGoal(goal.id);
      setNow(got.goal);
      setStats(got.stats);
    } catch (e) {
      setFailed(e instanceof Error ? e.message : SAY.couldNotLoad);
    }
  }, [goal.id]);
  useEffect(() => {
    // load sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
  }, [load]);

  const p = now.progress;

  async function act(change: { pause?: boolean; finish?: boolean }) {
    try {
      const { goal: next } = await changeGoal(now.id, change);
      setNow(next);
      onChanged();
    } catch (e) {
      setFailed(e instanceof Error ? e.message : "바꾸지 못했어요.");
    }
  }

  async function remove() {
    try {
      await removeGoal(now.id);
      onChanged();
      onClose();
    } catch (e) {
      setFailed(e instanceof Error ? e.message : SAY.couldNotDelete);
    }
  }

  async function mark(day: string, check: DayCheck) {
    try {
      const got = await checkDay(now.id, day, check);
      setNow(got.goal);
      setStats(got.stats);
      onChanged();
    } catch (e) {
      setFailed(e instanceof Error ? e.message : "기록하지 못했어요.");
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Stays put over what scrolls: the sheet's close sits on this row. */}
      <BottomSheetHeader className="border-b border-edge-soft pb-3 sm:px-6">
        <BottomSheetTitle className="text-heading leading-snug break-keep">{now.title}</BottomSheetTitle>
        <BottomSheetDescription className="text-aside text-dim">
          {now.rule}
          {now.remind ? `, ${now.remind}에 물어봐요` : ""}
          {now.end ? `, ${dayLabel(now.end)}까지` : ""}
        </BottomSheetDescription>
      </BottomSheetHeader>

      <div className="scrollbar-hairline min-h-0 flex-1 space-y-7 overflow-y-auto overscroll-contain px-4 pt-4 pb-8 sm:px-6">
        {now.note ? <p className="text-ui leading-relaxed text-dim">{now.note}</p> : null}

        <section aria-label="달성률" className="flex items-end justify-between gap-4">
          <div>
            <p className="tnum text-display-lg leading-none font-semibold tracking-tight text-foreground">{percent(p.rate)}</p>
            <p className="mt-1.5 text-aside text-dim">{p.rate === null ? "아직 지난 기록이 없어요" : "지금까지 달성률"}</p>
          </div>
          <dl className="tnum grid grid-cols-3 gap-5 text-right">
            <Figure label="연속" value={`${p.streak}${p.unit}`} />
            <Figure label="최고" value={`${p.best}${p.unit}`} />
            <Figure label="해낸 날" value={`${p.times}일`} />
          </dl>
        </section>

        {stats ? (
          <>
            <Bars goal={now} bars={stats.bars} />
            <Month goal={now} stats={stats} onMark={mark} />
            <Weekdays goal={now} stats={stats} />
          </>
        ) : null}

        {failed ? (
          <p role="alert" className="text-aside text-reject">
            {failed}
          </p>
        ) : null}

        <div className="flex flex-wrap gap-2">
          {now.status === "ended" ? (
            <Button variant="soft" size="touch-sm" onClick={() => void act({ finish: false })}>
              다시 시작
            </Button>
          ) : (
            <>
              <Button variant="soft" size="touch-sm" onClick={() => onEdit(now)}>
                고치기
              </Button>
              <Button variant="soft" size="touch-sm" onClick={() => void act({ pause: now.status !== "paused" })}>
                {now.status === "paused" ? "다시 시작" : "잠깐 쉬기"}
              </Button>
              <Button variant="soft" size="touch-sm" onClick={() => void act({ finish: true })}>
                끝내기
              </Button>
            </>
          )}
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="soft" size="touch-sm">
                지우기
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{now.title} 목표를 지울까요?</AlertDialogTitle>
                <AlertDialogDescription>
                  지금까지의 기록도 함께 지워져요. 기록을 남기려면 지우지 말고 끝내기를 누르세요. 되돌릴 수 없어요.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>두기</AlertDialogCancel>
                <AlertDialogAction onClick={() => void remove()}>기록까지 지우기</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
        {now.status === "active" || now.status === "paused" ? (
          <p className="text-aside leading-relaxed text-faint">쉬는 날은 달성률에 세지 않아요. 끝내면 기록은 남고 목록에서만 내려가요.</p>
        ) : null}
      </div>
    </div>
  );
}

function Figure({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dd className="text-title leading-none font-medium text-foreground">{value}</dd>
      <dt className="mt-1.5 text-meta text-faint">{label}</dt>
    </div>
  );
}

/** "11월 2일" (lib/when.ts). */
const shortDay = (day: string) => monthDay(day);

/**
 * How it went, bar by bar: what was done against what was asked. One series, so
 * no legend: the bar is what was done and the tick over it what was asked, and a
 * bar reaching its tick turns the colour of done. The week still going is hollow
 * - it is not a result yet. The figures are not on the bars but in the line
 * above: tap a bar and it says that one.
 */
function Bars({ goal, bars }: { goal: Goal; bars: Bar[] }) {
  const [at, setAt] = useState<number | null>(null);
  const shown = at ?? bars.length - 1;
  const top = Math.max(1, ...bars.map((b) => Math.max(b.target, b.done)));
  const monthly = goal.cadence === "monthly";
  const b = bars[shown];
  if (!b) return null;
  const name = b.open ? (monthly ? "이번 달" : "이번 주") : monthly ? `${Number(b.from.slice(5, 7))}월` : `${shortDay(b.from)} 주`;
  return (
    <section aria-label={monthly ? "달마다" : "주마다"}>
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="text-ui font-medium text-foreground">{monthly ? "달마다" : "주마다"}</h3>
        <p className="tnum text-aside text-dim" aria-live="polite">
          {name} {b.done}/{b.target}
        </p>
      </div>
      <div className="mt-3 flex h-28 items-end gap-1 border-b border-edge" role="group" aria-label="달성 막대">
        {bars.map((bar, i) => {
          const hit = bar.target > 0 && bar.done >= bar.target;
          return (
            <button
              key={bar.from}
              type="button"
              onClick={() => setAt(i)}
              aria-label={`${shortDay(bar.from)}부터 ${bar.done}/${bar.target}`}
              aria-pressed={i === shown}
              className="group relative flex h-full min-w-0 flex-1 items-end justify-center rounded-t-sm focus-ring"
            >
              {i === shown ? <span aria-hidden className="absolute inset-x-0 inset-y-0 rounded-t-sm bg-glass" /> : null}
              {/* The tick: what this one asked. */}
              <span aria-hidden className="absolute inset-x-[18%] h-px bg-dim/60" style={{ bottom: `${(bar.target / top) * 100}%` }} />
              <span
                aria-hidden
                className={cn(
                  "relative w-full max-w-3.5 rounded-t-[4px]",
                  bar.open && !hit ? "border border-b-0 border-dim/60" : hit ? "bg-approve-fill" : "bg-foreground/30",
                )}
                style={{ height: `${(bar.done / top) * 100}%`, minHeight: bar.done > 0 ? 3 : 0 }}
              />
            </button>
          );
        })}
      </div>
      <div className="tnum mt-1 flex justify-between text-meta text-faint" aria-hidden>
        <span>{monthly ? `${Number(bars[0].from.slice(5, 7))}월` : shortDay(bars[0].from)}</span>
        <span>{monthly ? `${Number(bars.at(-1)!.from.slice(5, 7))}월` : shortDay(bars.at(-1)!.from)}</span>
      </div>
    </section>
  );
}

/**
 * Each day of a month, the same marks as the register. A past day can be
 * changed here: tap it, and its two answers open under the grid.
 */
function Month({ goal, stats, onMark }: { goal: Goal; stats: Stats; onMark: (day: string, check: DayCheck) => Promise<void> }) {
  const today = goal.progress.today;
  const n = perDayOf(goal);
  const [ym, setYm] = useState(() => ({ y: Number(today.slice(0, 4)), m: Number(today.slice(5, 7)) }));
  const [picked, setPicked] = useState<string | null>(null);
  const weeks = useMemo(() => monthWeeks(ym.y, ym.m), [ym]);
  const counted = (d: string) => d >= goal.start && d <= today && (!goal.end || d <= goal.end);
  const first = `${String(ym.y)}-${String(ym.m).padStart(2, "0")}-01`;
  const move = (n: number) => {
    setPicked(null);
    setYm(({ y, m }) => {
      const at = y * 12 + (m - 1) + n;
      return { y: Math.floor(at / 12), m: (at % 12) + 1 };
    });
  };
  const said = picked ? stats.marks[picked] : undefined;
  const count = picked ? (stats.counts?.[picked] ?? 0) : 0;
  return (
    <section aria-label="달력">
      <div className="flex items-center justify-between">
        <h3 className="tnum text-ui font-medium text-foreground">
          {ym.y}년 {ym.m}월
        </h3>
        <div className="flex">
          <button type="button" aria-label="이전 달" disabled={first <= goal.start.slice(0, 8) + "01"} onClick={() => move(-1)} className={stepper}>
            <ChevronLeft aria-hidden className="size-4" />
          </button>
          <button type="button" aria-label="다음 달" disabled={first >= today.slice(0, 8) + "01"} onClick={() => move(1)} className={stepper}>
            <ChevronRight aria-hidden className="size-4" />
          </button>
        </div>
      </div>
      <div className="mt-2 grid grid-cols-7 text-center" aria-hidden>
        {WEEK_ROW.map((d) => (
          <span key={d} className="text-meta text-faint">
            {d}
          </span>
        ))}
      </div>
      <div className="mt-1 space-y-1">
        {weeks.map((week, i) => (
          <div key={i} className="grid grid-cols-7 gap-1">
            {week.map((d, j) =>
              d ? (
                <Day
                  key={d}
                  day={d}
                  mark={stats.marks[d]}
                  share={n > 1 ? (stats.counts?.[d] ?? 0) / n : 0}
                  open={counted(d)}
                  today={d === today}
                  picked={d === picked}
                  onPick={() => setPicked(d === picked ? null : d)}
                />
              ) : (
                <span key={j} />
              ),
            )}
          </div>
        ))}
      </div>
      <p className="mt-2 flex items-center gap-3 text-meta text-faint">
        <span className="flex items-center gap-1">
          <Check aria-hidden className="size-3 text-approve" />
          했다
        </span>
        <span className="flex items-center gap-1">
          <Minus aria-hidden className="size-3 text-reject" />
          안 했다
        </span>
        {n > 1 ? (
          <span className="flex items-center gap-1">
            <span aria-hidden className="h-[3px] w-3 rounded-full bg-approve-fill" />
            한 만큼
          </span>
        ) : null}
        <span>빈칸은 기록 없음</span>
      </p>
      {picked && n > 1 ? (
        // A time at a time, or the whole day at once: a day gone by is put right here.
        <div className="anim-settle mt-2 flex flex-wrap items-center justify-between gap-2 rounded-lg bg-well px-3 py-2">
          <span className="tnum text-aside text-dim">
            {whenLabel(picked, today)} {said === "done" ? `${n}번 다 했어요` : said === "missed" ? `안 했어요 (${tally(count, n)})` : tally(count, n)}
          </span>
          <span className="flex gap-1.5">
            <button
              type="button"
              className={choice(said === "missed")}
              onClick={() => void onMark(picked, said === "missed" ? (count > 0 ? { count } : { mark: "" }) : { mark: "missed" })}
            >
              안 했다
            </button>
            <button type="button" aria-label="하나 빼기" disabled={count === 0} className={choice(false)} onClick={() => void onMark(picked, { add: -1 })}>
              <Minus aria-hidden className="size-3.5" />
            </button>
            <button type="button" aria-label="한 번 더" disabled={said === "done"} className={choice(false)} onClick={() => void onMark(picked, { add: 1 })}>
              <Plus aria-hidden className="size-3.5" />
            </button>
            <button type="button" className={choice(said === "done")} onClick={() => void onMark(picked, said === "done" ? { mark: "" } : { mark: "done" })}>
              다 했다
            </button>
          </span>
        </div>
      ) : picked ? (
        <div className="anim-settle mt-2 flex items-center justify-between gap-2 rounded-lg bg-well px-3 py-2">
          <span className="text-aside text-dim">{whenLabel(picked, today)}</span>
          <span className="flex gap-1.5">
            <button type="button" className={choice(said === "missed")} onClick={() => void onMark(picked, { mark: said === "missed" ? "" : "missed" })}>
              안 했다
            </button>
            <button type="button" className={choice(said === "done")} onClick={() => void onMark(picked, { mark: said === "done" ? "" : "done" })}>
              했다
            </button>
          </span>
        </div>
      ) : null}
    </section>
  );
}

const stepper =
  "flex size-11 items-center justify-center rounded-lg text-faint hover:text-foreground focus-ring disabled:opacity-30 mouse:size-8";

const choice = (on: boolean) =>
  cn(
    "flex min-h-10 min-w-10 items-center justify-center rounded-lg border px-3 text-ui focus-ring disabled:opacity-35 mouse:min-h-8 mouse:min-w-8",
    on ? "border-edge bg-glass-raised text-foreground" : "border-edge-soft text-dim hover:bg-glass",
  );

/** A day of the month. Of a goal of more than one a day, the line along its foot is how much of the day was done (share). */
function Day({
  day,
  mark,
  share,
  open,
  today,
  picked,
  onPick,
}: {
  day: string;
  mark?: Mark;
  share: number;
  open: boolean;
  today: boolean;
  picked: boolean;
  onPick: () => void;
}) {
  const going = !mark && share > 0;
  return (
    <button
      type="button"
      disabled={!open}
      onClick={onPick}
      aria-pressed={picked}
      aria-label={`${dayLabel(day)}${mark === "done" ? " 했음" : mark === "missed" ? " 안 했음" : going ? ` ${Math.round(share * 100)}% 함` : ""}`}
      className={cn(
        "tnum relative flex h-10 items-center justify-center overflow-hidden rounded-md text-aside focus-ring disabled:cursor-default mouse:h-8",
        mark === "done" ? "bg-approve-wash text-foreground" : mark === "missed" ? "bg-reject-wash text-dim" : open ? "bg-glass text-dim" : "text-faint",
        today && "ring-1 ring-dim/60",
        picked && "ring-2 ring-foreground/60",
      )}
    >
      {Number(day.slice(8, 10))}
      {(going || mark === "missed") && share > 0 ? (
        <span aria-hidden className="absolute inset-x-1.5 bottom-1 h-[3px] rounded-full bg-well">
          <span className="block h-full rounded-full bg-approve-fill" style={{ width: `${share * 100}%` }} />
        </span>
      ) : null}
    </button>
  );
}

/** Which days of the week it holds up on, when there is enough to say. */
function Weekdays({ goal, stats }: { goal: Goal; stats: Stats }) {
  const days = stats.weekdays.filter((d) => d.asks > 0);
  if (days.reduce((n, d) => n + d.asks, 0) < 8) return null;
  const free = goal.cadence === "weekly" || goal.cadence === "monthly";
  const top = free ? Math.max(1, ...days.map((d) => d.done)) : 1;
  return (
    <section aria-label="요일별">
      <h3 className="text-ui font-medium text-foreground">요일별</h3>
      <ul className="mt-2 space-y-1.5">
        {days.map((d) => {
          const share = free ? d.done / top : d.done / d.asks;
          return (
            <li key={d.day} className="flex items-center gap-2.5">
              <span className="w-4 text-aside text-dim">{WEEK_ROW[(d.day + 6) % 7]}</span>
              <span className="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-well">
                <span className="block h-full rounded-full bg-foreground/45" style={{ width: `${share * 100}%` }} />
              </span>
              <span className="tnum w-12 text-right text-aside text-dim">
                {free ? `${d.done}번` : `${d.done}/${d.asks}`}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
