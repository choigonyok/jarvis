"use client";

import { useMemo, useState } from "react";
import { Plus, Search } from "lucide-react";
import { Panel } from "@/components/workout/panel";
import {
  COMMON_EXERCISES,
  MUSCLE_GROUPS,
  dayLabel,
  groupOf,
  recentExercises,
  setLabel,
  type MuscleGroup,
  type Session,
} from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * Choosing a movement.
 *
 * What you trained recently comes first, with how it went, because the
 * movement you want is almost always one you have done before. Everything
 * else is narrowed by body part or by typing; a name that matches nothing is
 * offered as a new movement, so the list is a shortcut and never a wall.
 */
export function ExercisePicker({
  open,
  onOpenChange,
  sessions,
  taken = [],
  onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sessions: Session[];
  /** Already in the session or routine - shown, but marked. */
  taken?: string[];
  onPick: (name: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [group, setGroup] = useState<MuscleGroup | null>(null);

  const rows = useMemo(() => {
    const recent = recentExercises(sessions);
    const known = new Set(recent.map((r) => r.name.toLowerCase()));
    const rest = COMMON_EXERCISES.filter((e) => !known.has(e.name.toLowerCase())).map((e) => ({
      name: e.name,
      date: null as string | null,
      top: null as { weight: number; reps: number } | null,
    }));
    return [...recent.map((r) => ({ ...r, date: r.date as string | null })), ...rest];
  }, [sessions]);

  const q = query.trim().toLowerCase();
  const shown = rows.filter(
    (r) => (!group || groupOf(r.name) === group) && (!q || r.name.toLowerCase().includes(q)),
  );
  const exact = rows.some((r) => r.name.toLowerCase() === q);
  const takenSet = new Set(taken.map((t) => t.trim().toLowerCase()));

  function pick(name: string) {
    onPick(name.trim());
    setQuery("");
    setGroup(null);
    onOpenChange(false);
  }

  return (
    <Panel open={open} onOpenChange={onOpenChange} title="종목 추가">
      <label className="relative mb-2.5 block">
        <span className="sr-only">종목 찾기</span>
        <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing && q) pick(shown[0]?.name ?? query);
          }}
          placeholder="찾거나 새 이름 입력"
          className="h-11 w-full rounded-lg border border-edge-soft bg-glass pr-3 pl-9 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50"
        />
      </label>

      <div className="-mx-1 mb-2 flex gap-1 overflow-x-auto px-1 pb-1" role="radiogroup" aria-label="부위">
        {[null, ...MUSCLE_GROUPS].map((g) => (
          <button
            key={g ?? "all"}
            type="button"
            role="radio"
            aria-checked={group === g}
            onClick={() => setGroup(g)}
            className={cn(
              "min-h-11 shrink-0 rounded-full px-3.5 text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              group === g ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim",
            )}
          >
            {g ?? "전체"}
          </button>
        ))}
      </div>

      <ul className="space-y-px">
        {q && !exact ? (
          <li>
            <button
              type="button"
              onClick={() => pick(query)}
              className="flex min-h-12 w-full items-center gap-3 rounded-lg px-2 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <Plus aria-hidden className="size-4 shrink-0 text-dim" />
              <span className="min-w-0 flex-1 truncate text-[14px] text-foreground">
                ‘{query.trim()}’ 새 종목으로 추가
              </span>
            </button>
          </li>
        ) : null}
        {shown.map((row) => {
          const already = takenSet.has(row.name.toLowerCase());
          return (
            <li key={row.name}>
              <button
                type="button"
                onClick={() => pick(row.name)}
                className="flex min-h-12 w-full items-center gap-3 rounded-lg px-2 py-1.5 text-left outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[14px] text-foreground/90">{row.name}</span>
                  <span className="tnum block truncate text-[11.5px] text-faint">
                    {groupOf(row.name)}
                    {row.date && row.top
                      ? ` · ${dayLabel(row.date)} ${setLabel(row.top)}`
                      : ""}
                  </span>
                </span>
                {already ? <span className="shrink-0 text-[11.5px] text-faint">추가됨</span> : null}
              </button>
            </li>
          );
        })}
      </ul>
      {shown.length === 0 && !q ? (
        <p className="px-2 py-4 text-[13px] text-faint">이 부위로 기록한 종목이 없어요. 이름을 입력해 추가하세요.</p>
      ) : null}
    </Panel>
  );
}
