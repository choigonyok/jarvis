"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, Minus, Plus } from "lucide-react";
import { GoalForm } from "@/components/goals/goal-form";
import { GoalSheet } from "@/components/goals/goal-sheet";
import { Header, TabBar } from "@/components/shell/header";
import { Retry } from "@/components/shell/retry";
import { StandingBar } from "@/components/shell/standing-bar";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { type Cell, type Check as DayCheck, type Goal, type Mark, checkDay, listGoals, perDayOf, tally, waitsToday, weekdayOf, WEEK_ROW, whenLabel } from "@/lib/goals";
import { onResume } from "@/lib/resume";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

/**
 * The register. Every goal is one row on one shared week, Monday to Sunday, so
 * a day reads down the page the way a teacher's attendance sheet does, and the
 * week so far reads across. It is a list you mark, not a set of cards: the marks
 * are the content, and the only colour on the page is what was done and what
 * was not. Under each row, today's two answers.
 */
export function Goals() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const [goals, setGoals] = useState<Goal[] | null>(null);
  const [ended, setEnded] = useState<Goal[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [making, setMaking] = useState(false);
  const [editing, setEditing] = useState<Goal | null>(null);

  const load = useCallback(async () => {
    try {
      const { goals: all } = await listGoals(true);
      setGoals(all.filter((g) => g.status !== "ended"));
      setEnded(all.filter((g) => g.status === "ended"));
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "목표를 불러오지 못했습니다.");
    }
  }, []);

  useEffect(() => {
    // load sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
  }, [load]);
  useEffect(() => onResume(load), [load]);
  // The name a notification gives: straight to that goal.
  useEffect(() => {
    const id = new URLSearchParams(window.location.search).get("goal");
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (id) setOpenId(id);
  }, []);

  const replace = useCallback((next: Goal) => {
    setGoals((cur) => cur?.map((g) => (g.id === next.id ? next : g)) ?? cur);
  }, []);

  const going = useMemo(() => (goals ?? []).filter((g) => g.status === "active"), [goals]);
  const resting = useMemo(() => (goals ?? []).filter((g) => g.status === "paused"), [goals]);
  const open = [...(goals ?? []), ...ended].find((g) => g.id === openId) ?? null;
  const today = going[0]?.progress.today ?? resting[0]?.progress.today ?? "";
  const left = going.filter(waitsToday).length;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-5 pb-12 sm:px-8 sm:pt-7 sm:pb-8">
          {error && !goals ? (
            <div className="py-16 text-center">
              <p className="text-[13px] text-dim">{error}</p>
              <Retry onRetry={load} />
            </div>
          ) : null}

          {goals && goals.length === 0 && ended.length === 0 ? (
            <NoGoals onMake={() => setMaking(true)} />
          ) : null}

          {goals && goals.length + ended.length > 0 ? (
            <div className="mb-4 flex items-center justify-between gap-3">
              <h2 className="text-[15px] leading-snug font-medium text-foreground">
                {going.length === 0 ? "지금 이어 가는 목표가 없어요" : left === 0 ? "오늘 할 일은 다 적었어요" : `오늘 ${left}개 남았어요`}
              </h2>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setMaking(true)}
                className="tap h-11 shrink-0 gap-1 px-2 text-[13px] text-dim hover:text-foreground mouse:h-8 mouse:min-h-0 mouse:min-w-0"
              >
                <Plus aria-hidden className="size-4" />
                목표
              </Button>
            </div>
          ) : null}

          {going.length > 0 ? (
            <>
              <WeekHead today={today} />
              <ol>
                {going.map((g) => (
                  <Row key={g.id} goal={g} onOpen={() => setOpenId(g.id)} onChanged={replace} />
                ))}
              </ol>
            </>
          ) : null}

          {resting.length > 0 ? (
            <section className="mt-8">
              <h2 className="mb-1 text-[12.5px] text-dim">쉬는 중</h2>
              <ul>
                {resting.map((g) => (
                  <Plain key={g.id} goal={g} onOpen={() => setOpenId(g.id)} />
                ))}
              </ul>
            </section>
          ) : null}

          {ended.length > 0 ? (
            <Collapsible className="mt-8">
              <CollapsibleTrigger className="tap-y cursor-pointer text-[12.5px] text-dim outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
                끝낸 목표 {ended.length}개
              </CollapsibleTrigger>
              <CollapsibleContent>
                <ul className="mt-1">
                  {ended.map((g) => (
                    <Plain key={g.id} goal={g} onOpen={() => setOpenId(g.id)} />
                  ))}
                </ul>
              </CollapsibleContent>
            </Collapsible>
          ) : null}
        </div>
      </main>

      <TabBar pending={waiting.length} />

      <GoalSheet
        goal={open}
        onOpenChange={(v) => {
          if (!v) setOpenId(null);
        }}
        onChanged={() => void load()}
        onEdit={(g) => setEditing(g)}
      />
      <GoalForm
        open={making || editing !== null}
        goal={editing}
        onOpenChange={(v) => {
          if (!v) {
            setMaking(false);
            setEditing(null);
          }
        }}
        onSaved={() => void load()}
      />
    </div>
  );
}

/** Nothing here yet: what a goal is, and the one thing to do about it. */
function NoGoals({ onMake }: { onMake: () => void }) {
  return (
    <Empty align="start" className="max-w-[30rem] py-10">
      <EmptyHeader className="gap-2">
        <EmptyTitle asChild className="text-[17px] text-foreground">
          <h2>되풀이해서 하려는 일을 적어 보세요</h2>
        </EmptyTitle>
        <EmptyDescription>
          매일, 정한 요일마다, 주 몇 번으로 세우고 날마다 했다·안 했다만 누르면 달성률은 알아서 쌓여요. 대화에서 “주 3회 운동 목표 만들어줘”라고
          해도 돼요.
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button variant="raised" size="touch" onClick={onMake}>
          <Plus aria-hidden />
          목표 만들기
        </Button>
      </EmptyContent>
    </Empty>
  );
}

/** The row every goal's cells stand under. */
const WEEK_GRID = "grid grid-cols-7 w-[11.5rem] shrink-0 sm:w-[15.75rem]";

/** The week's heads, over the column of cells. Today's stands out: the eye starts there. */
function WeekHead({ today }: { today: string }) {
  const at = today ? (weekdayOf(today) + 6) % 7 : -1;
  return (
    <div className="flex items-end justify-end border-b border-edge-soft pb-1.5" aria-hidden>
      <div className={WEEK_GRID}>
        {WEEK_ROW.map((d, i) => (
          <span key={d} className={cn("text-center text-[11.5px]", i === at ? "font-medium text-foreground" : "text-faint")}>
            {d}
          </span>
        ))}
      </div>
    </div>
  );
}

/** How a day stands while it is being said: its mark, and of a goal of more than one a day how many times. */
type DayState = { mark?: Mark; count: number };

/**
 * One goal: its name and how it stands, its week, and under them the answer for
 * the day picked - today, until another day's cell is tapped. A day is changed
 * here and not by tapping a cell through its states, so a thumb brushing a row
 * marks nothing. A goal of more than one a day answers a time at a time: one
 * more tap for each, and the day is done when they are all there.
 */
function Row({ goal, onOpen, onChanged }: { goal: Goal; onOpen: () => void; onChanged: (g: Goal) => void }) {
  const p = goal.progress;
  const n = perDayOf(goal);
  const many = n > 1;
  const [picked, setPicked] = useState<string | null>(null);
  // Said at once, then as mind has it: a thumb does not wait on a round trip.
  const [saying, setSaying] = useState<DayState | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const day = picked ?? p.today;
  const cell = p.week.find((c) => c.day === day);
  const now: DayState = saying ?? { mark: cell?.mark, count: cell?.count ?? 0 };
  const quietToday = !picked && !p.asks && goal.cadence !== "weekly" && goal.cadence !== "monthly";
  const when = whenLabel(day, p.today);

  async function say(check: DayCheck, next: DayState) {
    setFailed(null);
    setSaying(next);
    try {
      const { goal: got } = await checkDay(goal.id, day, check);
      onChanged(got);
      // Once a time at a time, the day stays picked until it is said.
      if (!many || next.mark) setPicked(null);
    } catch (e) {
      setFailed(e instanceof Error ? e.message : "기록하지 못했어요. 다시 눌러 주세요.");
    } finally {
      setSaying(null);
    }
  }

  const once = () => say({ add: 1 }, now.count + 1 >= n ? { mark: "done", count: n } : { count: now.count + 1 });
  const fewer = () => say({ add: -1 }, { count: Math.max(now.count - 1, 0) });
  // Back from 안 했다 to what was done of the day, or to nothing.
  const unsay = () => (many && now.count > 0 ? say({ count: now.count }, { count: now.count }) : say({ mark: "" }, { count: 0 }));

  return (
    <li data-focus={`goal:${goal.id}`} className="border-b border-edge-soft py-3">
      <div className="flex items-start justify-between gap-3">
        <button
          type="button"
          onClick={onOpen}
          className="-mx-1 min-h-11 min-w-0 flex-1 rounded-md px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-0"
        >
          <span className="block text-[14.5px] leading-snug font-medium break-keep text-foreground">{goal.title}</span>
          <span className={cn("mt-0.5 block text-[12px] leading-snug", p.tight ? "text-foreground/90" : "text-dim")}>
            {p.says || goal.rule}
          </span>
        </button>
        <div className={WEEK_GRID} role="group" aria-label={`${goal.title} 이번 주`}>
          {p.week.map((c) => (
            <Dot
              key={c.day}
              cell={c.day === day && saying ? { ...c, mark: saying.mark, count: saying.count } : c}
              of={n}
              today={p.today}
              picked={c.day === picked}
              onPick={() => setPicked(picked === c.day || c.day === p.today ? null : c.day)}
            />
          ))}
        </div>
      </div>

      {quietToday ? null : now.mark ? (
        <p className="anim-settle mt-1.5 flex min-h-9 items-center gap-1.5 text-[12.5px] text-dim">
          {now.mark === "done" ? <Check aria-hidden className="size-3.5 text-approve" /> : <Minus aria-hidden className="size-3.5 text-reject" />}
          {when}{" "}
          {now.mark === "done" ? (many ? `${n}번 다 했어요` : "했어요") : now.count > 0 ? `안 했어요 (${tally(now.count, n)})` : "안 했어요"}
          <button
            type="button"
            disabled={saying !== null}
            onClick={() => void (many && now.mark === "done" ? fewer() : unsay())}
            className="tap-y ml-1 text-[12px] text-faint underline-offset-2 outline-none hover:text-dim hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            {many && now.mark === "done" ? "하나 빼기" : "되돌리기"}
          </button>
        </p>
      ) : (
        // On a wide screen the day's line and its answers share a row.
        <div className="mt-2 sm:flex sm:items-center sm:gap-3">
          {many ? (
            <p className="mb-1.5 flex min-h-6 items-center gap-1.5 text-[12px] text-dim sm:mb-0">
              <span className="tnum">
                {when} {tally(now.count, n)}
              </span>
              {now.count > 0 ? (
                <button
                  type="button"
                  disabled={saying !== null}
                  onClick={() => void fewer()}
                  className="tap-y ml-1 text-[12px] text-faint underline-offset-2 outline-none hover:text-dim hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  하나 빼기
                </button>
              ) : null}
            </p>
          ) : picked ? (
            <p className="mb-1.5 text-[12px] text-dim sm:mb-0">{when} 기록</p>
          ) : null}
          {/* The same pair as a card's: the answer that counts on the thumb's side. */}
          <div className="grid grid-cols-2 gap-2 sm:ml-auto sm:flex sm:justify-end sm:gap-1.5">
            <Button
              variant="reject"
              size="pair-sm"
              disabled={saying !== null}
              onClick={() => void say({ mark: "missed" }, { mark: "missed", count: now.count })}
            >
              안 했다
            </Button>
            <Button
              variant="approve"
              size="pair-sm"
              disabled={saying !== null}
              onClick={() => void (many ? once() : say({ mark: "done" }, { mark: "done", count: 0 }))}
            >
              {many ? "한 번 했다" : "했다"}
            </Button>
          </div>
        </div>
      )}
      {failed ? (
        <p role="alert" className="mt-1.5 text-[12px] text-reject">
          {failed}
        </p>
      ) : null}
    </li>
  );
}

/**
 * One day of a row. Done is the approve colour with a tick and missed the
 * reject colour with a dash, so neither rests on colour alone; a day asked of
 * and left unsaid is an empty ring, and a day that asks nothing a speck. A goal
 * of more than one a day splits the ring into its times, and each time done
 * fills one: the ring filling is the day going, and full it is the tick.
 */
function Dot({ cell, of, today, picked, onPick }: { cell: Cell; of: number; today: string; picked: boolean; onPick: () => void }) {
  const mark = cell.mark;
  const count = cell.count ?? 0;
  const going = !mark && of > 1 && (count > 0 || (cell.asks && cell.open));
  const label = `${whenLabel(cell.day, today)}${
    mark === "done" ? " 했음" : mark === "missed" ? " 안 했음" : going ? ` ${tally(count, of)}` : cell.asks ? " 기록 없음" : ""
  }`;
  return (
    <button
      type="button"
      disabled={!cell.open}
      aria-label={label}
      aria-pressed={picked}
      onClick={onPick}
      className={cn(
        "tap-y flex h-9 items-center justify-center rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
        cell.day === today && "bg-glass-raised",
        picked && "ring-1 ring-edge",
      )}
    >
      {going ? (
        <Times count={count} of={of} />
      ) : (
        <span
          aria-hidden
          className={cn(
            "flex size-[1.125rem] items-center justify-center rounded-full",
            mark === "done" && "bg-approve/85 text-background",
            mark === "missed" && "bg-reject/15 text-reject",
            !mark && cell.asks && cell.open && "border border-edge",
            !mark && !(cell.asks && cell.open) && "size-[3px] bg-faint/40",
          )}
        >
          {mark === "done" ? <Check className="size-3" strokeWidth={3} /> : mark === "missed" ? <Minus className="size-3" strokeWidth={3} /> : null}
        </span>
      )}
    </button>
  );
}

/** The ring of a day asked more than once: one arc for each time, from the top, those done filled. Past eight, one arc. */
function Times({ count, of }: { count: number; of: number }) {
  const r = 7.25;
  const around = 2 * Math.PI * r;
  const gap = of > 8 ? 0 : 2;
  const arc = (around - gap * of) / of;
  // The first `filled` arcs of the ring, then nothing for the rest of the way round.
  const dash = (filled: number) =>
    gap === 0 ? `${(around * filled) / of} ${around}` : [...Array.from({ length: filled }, () => `${arc} ${gap}`), `0 ${around}`].join(" ");
  return (
    <svg aria-hidden viewBox="0 0 18 18" className="size-[1.125rem] -rotate-90">
      {/* Offset by half a gap, so the gaps sit across the top and not after it. */}
      <circle cx="9" cy="9" r={r} fill="none" strokeWidth="1.75" className="stroke-dim/35" strokeDasharray={dash(of)} strokeDashoffset={-gap / 2} />
      {count > 0 ? (
        <circle cx="9" cy="9" r={r} fill="none" strokeWidth="1.75" className="stroke-approve" strokeDasharray={dash(count)} strokeDashoffset={-gap / 2} />
      ) : null}
    </svg>
  );
}

/** A goal resting or ended: its name and how it stands. */
function Plain({ goal, onOpen }: { goal: Goal; onOpen: () => void }) {
  return (
    <li className="border-b border-edge-soft">
      <button
        type="button"
        onClick={onOpen}
        className="flex min-h-11 w-full items-baseline justify-between gap-3 rounded-md py-2.5 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <span className="min-w-0 truncate text-[14px] text-foreground/85">{goal.title}</span>
        <span className="shrink-0 text-[12px] text-faint">{goal.progress.says}</span>
      </button>
    </li>
  );
}
