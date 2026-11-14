"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Minus } from "lucide-react";
import { Failed } from "@/components/shell/failed";
import { Button } from "@/components/ui/button";
import { type Goal, type Mark, type Stats, checkDay, tally, whenLabel } from "@/lib/goals";
import { onResume } from "@/lib/resume";
import type { GoalAsk } from "@/lib/thread";
import { HttpError } from "@/lib/use-resource";
import { cn } from "@/lib/utils";

/**
 * The days a notice asks about, answered where it asks: 했다 or 안 했다, no
 * trip to the 목표 tab. What was answered is read back, so the card says the same
 * on the next visit and on the other device. Of a goal of more than one a day,
 * 했다 is all of them, and taking it back goes back to how far the day went.
 */
export function GoalAsks({ asks, said }: { asks: GoalAsk[]; said?: string }) {
  return (
    // With every goal it asks about gone, no rule is left standing on its own.
    <ul className="mt-3 space-y-3 border-t border-edge-soft pt-3 empty:hidden">
      {asks.map((a) => (
        <Ask key={`${a.goal}:${a.day}`} ask={a} named={asks.length > 1 || !said?.includes(a.title)} />
      ))}
    </ul>
  );
}

/**
 * The goal an ask is about, read as lib/goals.ts reads it but with mind's
 * answer kept: a 404 is the goal gone - deleted, or not the asker's - the one
 * failure that leaves nothing to ask. Any other is a read that did not come.
 */
async function readAsked(id: string): Promise<{ goal: Goal; stats: Stats }> {
  const res = await fetch(`/api/mind/goals/${encodeURIComponent(id)}`, { cache: "no-store" });
  if (!res.ok) throw new HttpError(res.status);
  return (await res.json()) as { goal: Goal; stats: Stats };
}

/** One ask: the notice's own words say it when they name the goal; otherwise it is named here. */
function Ask({ ask, named }: { ask: GoalAsk; named: boolean }) {
  // Undefined until read; null is no word yet.
  const [said, setSaid] = useState<Mark | null | undefined>(undefined);
  // How many times the day went before it was answered, of a goal of more than one a day.
  const [count, setCount] = useState(0);
  const [today, setToday] = useState("");
  const [gone, setGone] = useState(false);
  // The read did not come: said where the answers would be, with 다시 시도.
  // Taken for the goal gone, the ask vanished, and the question a push
  // brought the operator to was left with nothing to answer it.
  const [unread, setUnread] = useState(false);
  const [failed, setFailed] = useState(false);

  // What a read brings is shown only if no newer one was asked for since and
  // the ask is still on screen.
  const asked = useRef(0);
  const read = useCallback(() => {
    const mine = ++asked.current;
    return readAsked(ask.goal).then(
      ({ goal, stats }) => {
        if (asked.current !== mine) return;
        setUnread(false);
        setToday(goal.progress.today);
        setSaid(stats.marks[ask.day] ?? null);
        if (!stats.marks[ask.day]) setCount(stats.counts?.[ask.day] ?? 0);
      },
      (err: unknown) => {
        if (asked.current !== mine) return;
        if (err instanceof HttpError && err.status === 404) setGone(true);
        else setUnread(true);
      },
    );
  }, [ask.goal, ask.day]);
  useEffect(() => {
    void read();
    return () => {
      asked.current += 1;
    };
  }, [read]);
  // Not read, it is read again with the screens: the app back on screen, the
  // network back, a pull to refresh.
  useEffect(() => (unread ? onResume(read) : undefined), [unread, read]);

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
        <p className="mb-1.5 text-ui leading-snug text-ink">
          {ask.title}{" "}
          <span className="text-aside text-faint">
            {today ? whenLabel(ask.day, today) : ""}
            {ask.perDay && count > 0 ? ` ${tally(count, ask.perDay)}` : ""}
          </span>
        </p>
      ) : null}
      {said ? (
        <p className="anim-settle flex min-h-9 items-center gap-1.5 text-aside text-dim">
          {said === "done" ? <Check aria-hidden className="size-3.5 text-approve" /> : <Minus aria-hidden className="size-3.5 text-reject" />}
          {said === "done" ? "했어요" : "안 했어요"}
          <button
            type="button"
            onClick={() => void say("")}
            className="tap-y ml-1 text-aside text-faint underline-offset-2 hover:text-dim hover:underline focus-ring"
          >
            되돌리기
          </button>
        </p>
      ) : unread ? (
        <Failed quiet onRetry={read}>
          목표를 불러오지 못했어요.
        </Failed>
      ) : (
        <div className={cn("grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5", said === undefined && "opacity-50")}>
          <Button
            variant="reject"
            size="pair-sm"
            disabled={said === undefined}
            onClick={() => void say("missed")}
          >
            안 했다
          </Button>
          <Button
            variant="approve"
            size="pair-sm"
            disabled={said === undefined}
            onClick={() => void say("done")}
          >
            했다
          </Button>
        </div>
      )}
      {failed ? (
        <p role="alert" className="mt-1 text-aside text-reject">
          기록하지 못했어요. 다시 눌러 주세요.
        </p>
      ) : null}
    </li>
  );
}
