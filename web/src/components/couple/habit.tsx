"use client";

import { useMemo, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { BlockTitle } from "@/components/couple/screen";
import { useChecks } from "@/components/couple/use-space";
import { Failed } from "@/components/shell/failed";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { checkedDays, today, weekdays, writeCouple, type Block, type Check, type Space } from "@/lib/couple";
import { SAY } from "@/lib/say";
import type { Resource } from "@/lib/use-resource";
import { dateLabel, shiftDay } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * A habit the two keep: each checks their own days. A day is coloured by who
 * did it - mine purple, theirs amber, both the two halves, the calendar's
 * colours - and a day neither did stays the page's grey.
 */

/** How a day looks, by who checked it. */
function fillOf(me: boolean, partner: boolean): string {
  if (me && partner) return "owner-shared text-background";
  if (me) return "bg-mine text-background";
  if (partner) return "bg-partner text-background";
  return "";
}

/** The week on the home, a day a cell. */
export function WeekStrip({ days, checks, today: now, weekStart }: { days: string[]; checks: Check[]; today: string; weekStart: Space["weekStart"] }) {
  const { me, partner } = checkedDays(checks);
  const names = weekdays(weekStart);
  return (
    <ol className="grid grid-cols-7 gap-1.5" aria-label="이번 주">
      {days.map((d, i) => {
        const future = d > now;
        const who = me.has(d) && partner.has(d) ? "둘 다" : me.has(d) ? "나" : partner.has(d) ? "상대" : "아무도 안 함";
        return (
          <li key={d} className="flex flex-col items-center gap-1" aria-label={`${dateLabel(d)} · ${future ? "아직" : who}`}>
            <span aria-hidden className="text-tiny text-faint">
              {names[i]}
            </span>
            <span
              aria-hidden
              className={cn(
                "flex aspect-square w-full max-w-11 items-center justify-center rounded-lg text-ui tabular-nums",
                fillOf(me.has(d), partner.has(d)) || (future ? "text-faint/60" : "bg-glass-raised text-dim"),
                d === now && "ring-2 ring-foreground ring-offset-2 ring-offset-background",
              )}
            >
              {Number(d.slice(8))}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

/** Today's own check on a habit, and the one tap that sets or takes it off. */
export function useCheckToday(block: string, checks: Resource<Check[]>) {
  const now = today();
  const mine = checks.data?.find((c) => c.who === "me" && c.date === now);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function toggle() {
    setBusy(true);
    setError(null);
    const sent = mine
      ? await writeCouple("DELETE", `/blocks/${block}/checks/${mine.id}`)
      : await writeCouple<Check>("POST", `/blocks/${block}/checks`, { date: now });
    setBusy(false);
    if (!sent.ok) {
      setError(sent.error);
      return;
    }
    await checks.reload();
  }
  return { mine, busy, error, toggle };
}

/** The habit's page: a month of days, and the day picked under it. */
export function HabitPage({ block, space }: { block: Block; space: Space }) {
  const now = today();
  const [month, setMonth] = useState(now.slice(0, 7));
  const [picked, setPicked] = useState(now);
  const first = `${month}-01`;
  const last = shiftDay(`${shiftDay(first, 32).slice(0, 7)}-01`, -1);
  const checks = useChecks(block.id, first, last);
  const days = checkedDays(checks.data ?? []);
  const names = weekdays(space.weekStart);
  const lead = (new Date(`${first}T00:00:00Z`).getUTCDay() - (space.weekStart === "mon" ? 1 : 0) + 7) % 7;
  const cells = useMemo(() => {
    const out: string[] = [];
    for (let d = first; d <= last; d = shiftDay(d, 1)) out.push(d);
    return out;
  }, [first, last]);
  const [y, m] = month.split("-").map(Number);

  function page(by: number) {
    const next = shiftDay(by > 0 ? shiftDay(last, 1) : shiftDay(first, -1), 0).slice(0, 7);
    setMonth(next);
    setPicked(next === now.slice(0, 7) ? now : `${next}-01` <= now ? `${next}-01` : `${next}-01`);
  }

  return (
    <div>
      <BlockTitle title={block.title} />
      <div className="mt-5 flex items-center justify-between">
        <h2 className="text-title font-medium tracking-tight text-foreground tabular-nums">
          {y}년 {m}월
        </h2>
        <div className="flex items-center gap-1">
          {month !== now.slice(0, 7) ? (
            <Button variant="ghost" size="sm" onClick={() => (setMonth(now.slice(0, 7)), setPicked(now))} className="h-10 px-3 text-ui text-dim hover:text-foreground mouse:h-7">
              오늘
            </Button>
          ) : null}
          <Button variant="ghost" size="icon-sm" onClick={() => page(-1)} aria-label="이전 달" className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0">
            <ChevronLeft aria-hidden className="size-5 mouse:size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => page(1)}
            disabled={month >= now.slice(0, 7)}
            aria-label="다음 달"
            className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
          >
            <ChevronRight aria-hidden className="size-5 mouse:size-4" />
          </Button>
        </div>
      </div>

      <dl className="mt-3 grid grid-cols-3 gap-2">
        {[
          { dot: "bg-mine", label: "나", n: days.me.size },
          { dot: "bg-partner", label: "상대", n: days.partner.size },
          { dot: "owner-shared", label: "함께", n: days.both.size },
        ].map((s) => (
          <div key={s.label} className="rounded-xl bg-glass px-3 py-2.5">
            <dt className="flex items-center gap-1.5 text-aside text-dim">
              <span aria-hidden className={cn("size-[7px] rounded-full", s.dot)} />
              {s.label}
            </dt>
            <dd className="mt-0.5 text-lead font-semibold tracking-tight text-foreground tabular-nums">
              {s.n}
              <span className="ml-0.5 text-aside font-normal text-faint">일</span>
            </dd>
          </div>
        ))}
      </dl>

      <div className="mt-4 grid grid-cols-7 gap-1.5 text-center">
        {names.map((n) => (
          <span key={n} className="text-meta text-faint">
            {n}
          </span>
        ))}
        {Array.from({ length: lead }, (_, i) => (
          <span key={`p${i}`} />
        ))}
        {cells.map((d) => {
          const future = d > now;
          const fill = fillOf(days.me.has(d), days.partner.has(d));
          return (
            <button
              key={d}
              type="button"
              disabled={future}
              onClick={() => setPicked(d)}
              aria-pressed={picked === d}
              aria-label={`${dateLabel(d)}${days.me.has(d) ? " · 나" : ""}${days.partner.has(d) ? " · 상대" : ""}`}
              className={cn(
                "flex aspect-square items-center justify-center rounded-lg text-ui tabular-nums transition-colors focus-ring",
                fill || (future ? "text-faint/50" : "bg-glass text-dim hover:bg-glass-raised"),
                picked === d && "ring-2 ring-foreground ring-offset-2 ring-offset-background",
                d === now && !fill && "font-semibold text-foreground",
              )}
            >
              {Number(d.slice(8))}
            </button>
          );
        })}
      </div>

      {checks.error && !checks.data ? (
        <Failed onRetry={checks.reload} className="mt-5">
          {SAY.couldNotLoad}
        </Failed>
      ) : (
        <Day key={picked} block={block} day={picked} checks={(checks.data ?? []).filter((c) => c.date === picked)} space={space} onChanged={checks.reload} loading={!checks.data} />
      )}
    </div>
  );
}

/** The day picked: my check with its memo, and theirs as it stands. */
function Day({
  block,
  day,
  checks,
  space,
  onChanged,
  loading,
}: {
  block: Block;
  day: string;
  checks: Check[];
  space: Space;
  onChanged: () => Promise<void>;
  loading: boolean;
}) {
  const mine = checks.find((c) => c.who === "me");
  const theirs = checks.find((c) => c.who === "partner");
  const [memo, setMemo] = useState(mine?.memo ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [asking, setAsking] = useState(false);
  // The memo as read last: one read back after a save takes the field's place.
  const [read, setRead] = useState(mine?.memo);
  if (read !== mine?.memo) {
    setRead(mine?.memo);
    setMemo(mine?.memo ?? "");
  }

  async function check() {
    setBusy(true);
    setError(null);
    const sent = await writeCouple<Check>("POST", `/blocks/${block.id}/checks`, { date: day, memo });
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    await onChanged();
  }
  async function uncheck() {
    if (!mine) return;
    setAsking(false);
    setBusy(true);
    setError(null);
    const sent = await writeCouple("DELETE", `/blocks/${block.id}/checks/${mine.id}`);
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    setMemo("");
    await onChanged();
  }
  async function saveMemo() {
    if (!mine || memo.trim() === (mine.memo ?? "")) return;
    setError(null);
    const sent = await writeCouple("PATCH", `/blocks/${block.id}/checks/${mine.id}`, { memo: memo.trim() });
    if (!sent.ok) return setError(sent.error);
    await onChanged();
  }

  return (
    <section className="mt-6 border-t border-edge-soft pt-5" aria-live="polite">
      <h3 className="text-ui text-dim">
        {dateLabel(day)}
        {day === today() ? <span className="text-faint"> · 오늘</span> : null}
      </h3>
      {loading ? null : (
        <>
          <Button
            variant={mine ? "soft" : "raised"}
            size="touch"
            disabled={busy}
            onClick={() => (mine ? (mine.memo ? setAsking(true) : void uncheck()) : void check())}
            className="mt-3 w-full"
          >
            {busy ? "보내는 중…" : mine ? "체크 취소하기" : "체크하기"}
          </Button>
          {mine ? (
            <Input
              surface="glass"
              value={memo}
              maxLength={200}
              placeholder="메모"
              aria-label={`${dateLabel(day)} 메모`}
              onChange={(e) => setMemo(e.target.value)}
              onBlur={() => void saveMemo()}
              onKeyDown={(e) => {
                if (e.key === "Enter") (e.target as HTMLInputElement).blur();
              }}
              className="mt-2"
            />
          ) : null}
          {theirs ? (
            <p className="mt-3 flex items-start gap-2 text-ui text-ink">
              <span aria-hidden className="mt-[7px] size-[7px] shrink-0 rounded-full bg-partner" />
              <span>
                {space.partner?.name || "상대"}도 했어요{theirs.memo ? <span className="text-dim"> · {theirs.memo}</span> : null}
              </span>
            </p>
          ) : null}
          {error ? (
            <p role="alert" className="mt-2 text-aside text-reject">
              {error}
            </p>
          ) : null}
        </>
      )}
      <AlertDialog open={asking} onOpenChange={setAsking}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>체크를 취소할까요?</AlertDialogTitle>
            <AlertDialogDescription>이 날의 메모도 함께 지워져요. 되돌릴 수 없어요.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>두기</AlertDialogCancel>
            <AlertDialogAction variant="refuse" onClick={() => void uncheck()}>
              취소하기
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
