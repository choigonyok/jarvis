"use client";

import { useState } from "react";
import { Minus, Plus } from "lucide-react";
import { useRole } from "@/components/shell/role";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { type Cadence, type Draft, type Goal, changeGoal, makeGoal, WEEK_ROW } from "@/lib/goals";
import { cn } from "@/lib/utils";

const field =
  "w-full rounded-lg border border-edge-soft bg-well px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:border-edge focus-visible:ring-3 focus-visible:ring-ring/50 mouse:text-[13.5px]";

const RULES: { value: Cadence; label: string }[] = [
  { value: "daily", label: "매일" },
  { value: "days", label: "요일마다" },
  { value: "weekly", label: "주 몇 번" },
  { value: "monthly", label: "달 몇 번" },
];

/** Monday first, as the register reads; the numbers are mind's, Sunday 0. */
const WEEKDAYS = [1, 2, 3, 4, 5, 6, 0];

const blank = { title: "", note: "", cadence: "daily" as Cadence, times: 3, perDay: 1, weekdays: [1, 3, 5], start: "", end: "", remind: "", workout: false };

/** The most a day may ask, as mind holds it. */
const MOST_A_DAY = 20;

function start(goal: Goal | null) {
  if (!goal) return blank;
  return {
    title: goal.title,
    note: goal.note ?? "",
    cadence: goal.cadence,
    times: goal.times ?? 3,
    perDay: goal.perDay ?? 1,
    weekdays: goal.weekdays ?? [1, 3, 5],
    start: goal.start,
    end: goal.end ?? "",
    remind: goal.remind ?? "",
    workout: goal.auto === "workout",
  };
}

/** Making a goal, or changing one: the name and how often first, the rest if wanted. */
export function GoalForm({
  open,
  goal,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  goal: Goal | null;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        className="inset-x-safe pb-safe max-h-[92dvh] gap-0 border-edge-soft bg-background sm:mx-auto sm:max-w-xl sm:rounded-t-2xl sm:border-x"
      >
        {open ? <Form key={goal?.id ?? "new"} goal={goal} onClose={() => onOpenChange(false)} onSaved={onSaved} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function Form({ goal, onClose, onSaved }: { goal: Goal | null; onClose: () => void; onSaved: () => void }) {
  const role = useRole();
  const [v, setV] = useState(() => start(goal));
  const [more, setMore] = useState(Boolean(goal?.note || goal?.end || goal?.remind || goal?.auto));
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const set = <K extends keyof typeof v>(key: K, value: (typeof v)[K]) => setV((cur) => ({ ...cur, [key]: value }));
  const counted = v.cadence === "weekly" || v.cadence === "monthly";
  const most = v.cadence === "weekly" ? 6 : 28;
  // A day asked of may ask it more than once; a count over days asks of no day.
  const many = !counted && v.perDay > 1;
  const ready = v.title.trim() !== "" && (v.cadence !== "days" || v.weekdays.length > 0);

  // A week holds fewer than a month: seven a week is every day, and says so.
  const pick = (cadence: Cadence) =>
    setV((cur) => ({ ...cur, cadence, times: cadence === "weekly" ? Math.min(cur.times, 6) : cur.times }));

  async function save() {
    setBusy(true);
    setFailed(null);
    const draft: Draft = {
      title: v.title,
      note: v.note,
      cadence: v.cadence,
      ...(counted ? { times: v.times } : { perDay: v.perDay }),
      ...(v.cadence === "days" ? { weekdays: v.weekdays } : {}),
      ...(v.start ? { start: v.start } : {}),
      end: v.end,
      remind: v.remind,
      auto: v.workout ? "workout" : "",
    };
    try {
      if (goal) await changeGoal(goal.id, draft);
      else await makeGoal(draft);
      onSaved();
      onClose();
    } catch (e) {
      setFailed(e instanceof Error ? e.message : "저장하지 못했습니다.");
      setBusy(false);
    }
  }

  return (
    <form
      className="scrollbar-hairline flex min-h-0 flex-1 flex-col overflow-y-auto overscroll-contain"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready && !busy) void save();
      }}
    >
      <SheetHeader className="px-4 pt-4 pb-0 sm:px-6">
        <SheetTitle className="text-[16px] font-medium text-foreground">{goal ? "목표 고치기" : "새 목표"}</SheetTitle>
        <SheetDescription className="sr-only">이름과 주기를 정합니다.</SheetDescription>
      </SheetHeader>

      <div className="space-y-5 px-4 pt-4 pb-6 sm:px-6">
        <div>
          <label htmlFor="goal-title" className="mb-1.5 block text-[12.5px] text-dim">
            무엇을 할까요
          </label>
          <input
            id="goal-title"
            value={v.title}
            maxLength={60}
            onChange={(e) => set("title", e.target.value)}
            placeholder="물 2L 마시기"
            className={cn(field, "h-11 mouse:h-9")}
          />
        </div>

        <div>
          <p className="mb-1.5 text-[12.5px] text-dim">얼마나 자주</p>
          <div role="radiogroup" aria-label="얼마나 자주" className="grid grid-cols-4 gap-1 rounded-lg border border-edge-soft bg-glass p-0.5">
            {RULES.map((r) => (
              <button
                key={r.value}
                type="button"
                role="radio"
                aria-checked={v.cadence === r.value}
                onClick={() => pick(r.value)}
                className={cn(
                  "tap-y min-h-9 rounded-md text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  v.cadence === r.value ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim",
                )}
              >
                {r.label}
              </button>
            ))}
          </div>

          {v.cadence === "days" ? (
            <div role="group" aria-label="요일" className="mt-2.5 grid grid-cols-7 gap-1">
              {WEEKDAYS.map((d, i) => {
                const on = v.weekdays.includes(d);
                return (
                  <button
                    key={d}
                    type="button"
                    aria-pressed={on}
                    onClick={() => set("weekdays", on ? v.weekdays.filter((x) => x !== d) : [...v.weekdays, d])}
                    className={cn(
                      "h-11 rounded-lg border text-[13.5px] outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-9",
                      on ? "border-edge bg-glass-raised text-foreground" : "border-edge-soft text-faint hover:bg-glass",
                    )}
                  >
                    {WEEK_ROW[i]}
                  </button>
                );
              })}
            </div>
          ) : null}

          {counted ? (
            <div className="mt-2.5 flex items-center justify-between gap-3">
              <p className="text-[13.5px] text-foreground/90">
                {v.cadence === "weekly" ? "한 주에" : "한 달에"} <span className="tnum font-medium">{v.times}</span>번
              </p>
              <div className="flex gap-1.5">
                <button type="button" aria-label="한 번 줄이기" disabled={v.times <= 1} onClick={() => set("times", v.times - 1)} className={step}>
                  <Minus aria-hidden className="size-4" />
                </button>
                <button type="button" aria-label="한 번 늘리기" disabled={v.times >= most} onClick={() => set("times", v.times + 1)} className={step}>
                  <Plus aria-hidden className="size-4" />
                </button>
              </div>
            </div>
          ) : (
            <div className="mt-2.5 flex items-center justify-between gap-3">
              <p className="text-[13.5px] text-foreground/90">
                하루에 <span className="tnum font-medium">{v.perDay}</span>번
              </p>
              <div className="flex gap-1.5">
                <button type="button" aria-label="하루 횟수 줄이기" disabled={v.perDay <= 1} onClick={() => set("perDay", v.perDay - 1)} className={step}>
                  <Minus aria-hidden className="size-4" />
                </button>
                <button
                  type="button"
                  aria-label="하루 횟수 늘리기"
                  disabled={v.perDay >= MOST_A_DAY}
                  onClick={() => set("perDay", v.perDay + 1)}
                  className={step}
                >
                  <Plus aria-hidden className="size-4" />
                </button>
              </div>
            </div>
          )}
          <p className="mt-2 text-[12px] leading-relaxed text-faint">
            {many
              ? `할 때마다 한 번 했다를 누르고, ${v.perDay}번을 채우면 그날 했다가 돼요. 다 채우지 못한 날은 다음 날 점심에 물어봐요.`
              : v.cadence === "daily"
              ? "하루라도 기록이 없으면 다음 날 점심에 혹시 했는지 물어봐요."
              : v.cadence === "days"
                ? "고른 요일만 세요. 기록이 없으면 다음 날 점심에 물어봐요."
                : v.cadence === "weekly"
                  ? "월요일부터 일요일까지 세요. 남은 날을 다 해야 맞출 수 있게 되면 그날 밤 알려 드려요."
                  : "그달 1일부터 말일까지 세요. 남은 날을 다 해야 맞출 수 있게 되면 그날 밤 알려 드려요."}
          </p>
        </div>

        {more ? (
          <div className="space-y-4">
            <div>
              <label htmlFor="goal-note" className="mb-1.5 block text-[12.5px] text-dim">
                왜 하려는지 (선택)
              </label>
              <input id="goal-note" value={v.note} maxLength={200} onChange={(e) => set("note", e.target.value)} className={cn(field, "h-11 mouse:h-9")} />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="min-w-0">
                <label htmlFor="goal-start" className="mb-1.5 block text-[12.5px] text-dim">
                  시작
                </label>
                <input id="goal-start" type="date" value={v.start} onChange={(e) => set("start", e.target.value)} className={cn(field, "h-11 mouse:h-9")} />
              </div>
              <div className="min-w-0">
                <label htmlFor="goal-end" className="mb-1.5 block text-[12.5px] text-dim">
                  끝 (선택)
                </label>
                <input id="goal-end" type="date" value={v.end} onChange={(e) => set("end", e.target.value)} className={cn(field, "h-11 mouse:h-9")} />
              </div>
            </div>
            <div>
              <label htmlFor="goal-remind" className="mb-1.5 block text-[12.5px] text-dim">
                매일 이때 물어보기 (선택)
              </label>
              <input id="goal-remind" type="time" value={v.remind} onChange={(e) => set("remind", e.target.value)} className={cn(field, "h-11 mouse:h-9")} />
            </div>
            {role === "owner" ? (
              <label className="flex min-h-11 items-center justify-between gap-3 text-[13.5px] text-foreground/90">
                운동 기록을 저장하면 그날 했다로
                <input type="checkbox" checked={v.workout} onChange={(e) => set("workout", e.target.checked)} className="size-5 accent-[var(--approve)]" />
              </label>
            ) : null}
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setMore(true)}
            className="tap-y text-[12.5px] text-faint underline-offset-2 outline-none hover:text-dim hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            이유, 기간, 물어볼 시각 정하기
          </button>
        )}

        {failed ? (
          <p role="alert" className="text-[12.5px] text-reject">
            {failed}
          </p>
        ) : null}

        <Button
          type="submit"
          variant="outline"
          disabled={!ready || busy}
          className="h-12 w-full border-edge bg-glass-raised text-[14px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve mouse:h-10 dark:bg-glass-raised dark:hover:bg-approve/12"
        >
          {goal ? "저장" : "목표 만들기"}
        </Button>
      </div>
    </form>
  );
}

const step =
  "flex size-11 items-center justify-center rounded-lg border border-edge-soft text-dim outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30 mouse:size-8";
