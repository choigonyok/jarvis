"use client";

import { useState } from "react";
import { Minus, Plus } from "lucide-react";
import { useRole } from "@/components/shell/role";
import { BottomSheetContent, BottomSheetDescription, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Sheet } from "@/components/ui/sheet";
import { SwitchRow } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { type Cadence, type Draft, type Goal, changeGoal, makeGoal, WEEK_ROW } from "@/lib/goals";
import { SAY } from "@/lib/say";

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
      <BottomSheetContent width="lg" height="tall" keepFocus={false}>
        {open ? <Form key={goal?.id ?? "new"} goal={goal} onClose={() => onOpenChange(false)} onSaved={onSaved} /> : null}
      </BottomSheetContent>
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
      setFailed(e instanceof Error ? e.message : SAY.couldNotSave);
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
      <BottomSheetHeader className="pb-0 sm:px-6">
        <BottomSheetTitle className="pr-0">{goal ? "목표 고치기" : "새 목표"}</BottomSheetTitle>
        <BottomSheetDescription className="sr-only">이름과 주기를 정해요.</BottomSheetDescription>
      </BottomSheetHeader>

      <div className="space-y-5 px-4 pt-4 pb-6 sm:px-6">
        <Field>
          <FieldLabel htmlFor="goal-title">무엇을 할까요</FieldLabel>
          <Input id="goal-title" value={v.title} maxLength={60} onChange={(e) => set("title", e.target.value)} placeholder="물 2L 마시기" />
        </Field>

        <div>
          <p id="goal-cadence" className="mb-1 text-aside text-dim">
            얼마나 자주
          </p>
          <ToggleGroup
            type="single"
            aria-labelledby="goal-cadence"
            variant="segment"
            size="segment"
            spacing={1}
            value={v.cadence}
            onValueChange={(c) => {
              if (c) pick(c as Cadence);
            }}
            className="grid w-full grid-cols-4 rounded-lg border border-edge-soft bg-glass p-0.5"
          >
            {RULES.map((r) => (
              <ToggleGroupItem key={r.value} value={r.value} className="min-h-9 px-0 text-ui">
                {r.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          {v.cadence === "days" ? (
            <ToggleGroup
              type="multiple"
              aria-label="요일"
              variant="tile"
              spacing={1}
              value={v.weekdays.map(String)}
              onValueChange={(days) => set("weekdays", days.map(Number))}
              className="mt-2.5 grid w-full grid-cols-7"
            >
              {WEEKDAYS.map((d, i) => (
                <ToggleGroupItem key={d} value={String(d)} className="h-11 px-0 text-ui text-faint mouse:h-9">
                  {WEEK_ROW[i]}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          ) : null}

          {counted ? (
            <div className="mt-2.5 flex items-center justify-between gap-3">
              <p className="text-ui text-ink">
                {v.cadence === "weekly" ? "한 주에" : "한 달에"} <span className="tnum font-medium">{v.times}</span>번
              </p>
              <div className="flex gap-1.5">
                <Button type="button" variant="soft" size="icon-touch" aria-label="한 번 줄이기" disabled={v.times <= 1} onClick={() => set("times", v.times - 1)} className="disabled:opacity-30">
                  <Minus aria-hidden />
                </Button>
                <Button type="button" variant="soft" size="icon-touch" aria-label="한 번 늘리기" disabled={v.times >= most} onClick={() => set("times", v.times + 1)} className="disabled:opacity-30">
                  <Plus aria-hidden />
                </Button>
              </div>
            </div>
          ) : (
            <div className="mt-2.5 flex items-center justify-between gap-3">
              <p className="text-ui text-ink">
                하루에 <span className="tnum font-medium">{v.perDay}</span>번
              </p>
              <div className="flex gap-1.5">
                <Button type="button" variant="soft" size="icon-touch" aria-label="하루 횟수 줄이기" disabled={v.perDay <= 1} onClick={() => set("perDay", v.perDay - 1)} className="disabled:opacity-30">
                  <Minus aria-hidden />
                </Button>
                <Button
                  type="button"
                  variant="soft"
                  size="icon-touch"
                  aria-label="하루 횟수 늘리기"
                  disabled={v.perDay >= MOST_A_DAY}
                  onClick={() => set("perDay", v.perDay + 1)}
                  className="disabled:opacity-30"
                >
                  <Plus aria-hidden />
                </Button>
              </div>
            </div>
          )}
          <p className="mt-2 text-aside leading-relaxed text-faint">
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
            <Field>
              <FieldLabel htmlFor="goal-note">왜 하려는지 (선택)</FieldLabel>
              <Input id="goal-note" value={v.note} maxLength={200} onChange={(e) => set("note", e.target.value)} />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field className="min-w-0">
                <FieldLabel htmlFor="goal-start">시작</FieldLabel>
                <Input id="goal-start" type="date" value={v.start} onChange={(e) => set("start", e.target.value)} />
              </Field>
              <Field className="min-w-0">
                <FieldLabel htmlFor="goal-end">끝 (선택)</FieldLabel>
                <Input id="goal-end" type="date" value={v.end} onChange={(e) => set("end", e.target.value)} />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="goal-remind">매일 이때 물어보기 (선택)</FieldLabel>
              <Input id="goal-remind" type="time" value={v.remind} onChange={(e) => set("remind", e.target.value)} />
            </Field>
            {role === "owner" ? (
              <SwitchRow checked={v.workout} onCheckedChange={(on) => set("workout", on)} label="운동 기록을 저장하면 그날 했다로" />
            ) : null}
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setMore(true)}
            className="tap-y text-aside text-faint underline-offset-2 hover:text-dim hover:underline focus-ring"
          >
            이유, 기간, 물어볼 시각 정하기
          </button>
        )}

        {failed ? (
          <p role="alert" className="text-aside text-reject">
            {failed}
          </p>
        ) : null}

        <Button type="submit" variant="approve" size="touch" disabled={!ready || busy} className="h-12 w-full mouse:h-10">
          {goal ? "저장" : "목표 만들기"}
        </Button>
      </div>
    </form>
  );
}
