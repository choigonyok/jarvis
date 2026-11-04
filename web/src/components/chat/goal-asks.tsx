"use client";

import { useEffect, useState } from "react";
import { Check, Minus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { type Mark, checkDay, readGoal, tally, whenLabel } from "@/lib/goals";
import type { GoalAsk } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * The days a notice asks about, answered where it asks: 했다 or 안 했다, no
 * trip to the 목표 tab. What was answered is read back, so the card says the same
 * on the next visit and on the other device. Of a goal of more than one a day,
 * 했다 is all of them, and taking it back goes back to how far the day went.
 */
export function GoalAsks({ asks, said }: { asks: GoalAsk[]; said?: string }) {
  return (
    <ul className="mt-3 space-y-3 border-t border-edge-soft pt-3">
      {asks.map((a) => (
        <Ask key={`${a.goal}:${a.day}`} ask={a} named={asks.length > 1 || !said?.includes(a.title)} />
      ))}
    </ul>
  );
}

/** One ask: the notice's own words say it when they name the goal; otherwise it is named here. */
function Ask({ ask, named }: { ask: GoalAsk; named: boolean }) {
  // Undefined until read; null is no word yet.
  const [said, setSaid] = useState<Mark | null | undefined>(undefined);
  // How many times the day went before it was answered, of a goal of more than one a day.
  const [count, setCount] = useState(0);
  const [today, setToday] = useState("");
  const [gone, setGone] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    readGoal(ask.goal)
      .then(({ goal, stats }) => {
        if (!alive) return;
        setToday(goal.progress.today);
        setSaid(stats.marks[ask.day] ?? null);
        if (!stats.marks[ask.day]) setCount(stats.counts?.[ask.day] ?? 0);
      })
      .catch(() => {
        if (alive) setGone(true);
      });
    return () => {
      alive = false;
    };
  }, [ask.goal, ask.day]);

  async function say(mark: Mark | "") {
    const before = said;
    setFailed(false);
    setSaid(mark || null);
    try {
      await checkDay(ask.goal, ask.day, mark === "" && count > 0 ? { count } : { mark });
    } catch {
      setSaid(before);
      setFailed(true);
    }
  }

  if (gone) return null;
  return (
    <li>
      {named ? (
        <p className="mb-1.5 text-[13.5px] leading-snug text-foreground/90">
          {ask.title}{" "}
          <span className="text-[12px] text-faint">
            {today ? whenLabel(ask.day, today) : ""}
            {ask.perDay && count > 0 ? ` ${tally(count, ask.perDay)}` : ""}
          </span>
        </p>
      ) : null}
      {said ? (
        <p className="anim-settle flex min-h-9 items-center gap-1.5 text-[12.5px] text-dim">
          {said === "done" ? <Check aria-hidden className="size-3.5 text-approve" /> : <Minus aria-hidden className="size-3.5 text-reject" />}
          {said === "done" ? "했어요" : "안 했어요"}
          <button
            type="button"
            onClick={() => void say("")}
            className="tap-y ml-1 text-[12px] text-faint underline-offset-2 outline-none hover:text-dim hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            되돌리기
          </button>
        </p>
      ) : (
        <div className={cn("grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5", said === undefined && "opacity-50")}>
          <Button
            variant="ghost"
            size="sm"
            disabled={said === undefined}
            onClick={() => void say("missed")}
            className="h-11 text-[13.5px] text-dim hover:bg-reject/10 hover:text-reject mouse:h-8 mouse:px-3 mouse:text-[0.8rem]"
          >
            안 했다
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={said === undefined}
            onClick={() => void say("done")}
            className="h-11 border-edge bg-glass-raised text-[13.5px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve mouse:h-8 mouse:px-3.5 mouse:text-[0.8rem] dark:bg-glass-raised dark:hover:bg-approve/12"
          >
            했다
          </Button>
        </div>
      )}
      {failed ? (
        <p role="alert" className="mt-1 text-[12px] text-reject">
          기록하지 못했어요. 다시 눌러 주세요.
        </p>
      ) : null}
    </li>
  );
}
