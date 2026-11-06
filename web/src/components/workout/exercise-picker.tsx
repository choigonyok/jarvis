"use client";

import { useMemo, useState } from "react";
import { Plus } from "lucide-react";
import { Panel } from "@/components/ui/bottom-sheet";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
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

/**
 * Choosing a movement.
 *
 * What you trained recently comes first, with how it went, because the
 * movement you want is almost always one you have done before. Everything
 * else is narrowed by body part or by typing; a name that matches nothing is
 * offered as a new movement, so the list is a shortcut and never a wall.
 * The arrow keys go down the list and Enter takes the lit row - the first
 * movement that matches, as it was before there were keys.
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
  // The row Enter takes. Typing lights the first match again (the offer of
  // a new name sits above the matches but is not the default); the arrow
  // keys move it from there. With nothing typed nothing is lit, and Enter
  // takes nothing - as before there were keys.
  const NEW = "__new";
  const [lit, setLit] = useState("");
  const first = q ? (shown[0]?.name ?? NEW) : "";
  const [litFor, setLitFor] = useState({ query, group });
  if (litFor.query !== query || litFor.group !== group) {
    setLitFor({ query, group });
    setLit(first);
  }

  function pick(name: string) {
    onPick(name.trim());
    setQuery("");
    setGroup(null);
    onOpenChange(false);
  }

  return (
    <Panel open={open} onOpenChange={onOpenChange} title="종목 추가">
      <Command shouldFilter={false} value={lit || first} onValueChange={setLit} label="종목 찾기">
        <CommandInput
          value={query}
          onValueChange={setQuery}
          placeholder="찾거나 새 이름 입력"
          aria-label="종목 찾기"
          wrapperClassName="mb-2.5"
        />

        <ToggleGroup
          type="single"
          aria-label="부위"
          variant="pill"
          size="touch"
          spacing={1}
          value={group ?? "all"}
          onValueChange={(v) => {
            if (v) setGroup(v === "all" ? null : (v as MuscleGroup));
          }}
          className="-mx-1 mb-2 w-[calc(100%+0.5rem)] overflow-x-auto px-1 pb-1"
        >
          {[null, ...MUSCLE_GROUPS].map((g) => (
            <ToggleGroupItem key={g ?? "all"} value={g ?? "all"} className="px-3.5">
              {g ?? "전체"}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>

        <CommandList className="*:space-y-px">
          {q && !exact ? (
            <CommandItem value={NEW} onSelect={() => pick(query)} className="min-h-12">
              <Plus aria-hidden className="text-dim" />
              <span className="min-w-0 flex-1 truncate text-body text-foreground">‘{query.trim()}’ 새 종목으로 추가</span>
            </CommandItem>
          ) : null}
          {shown.map((row) => {
            const already = takenSet.has(row.name.toLowerCase());
            return (
              <CommandItem key={row.name} value={row.name} onSelect={() => pick(row.name)} className="min-h-12 py-1.5">
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-body text-ink">{row.name}</span>
                  <span className="tnum block truncate text-meta text-faint">
                    {groupOf(row.name)}
                    {row.date && row.top ? ` · ${dayLabel(row.date)} ${setLabel(row.top)}` : ""}
                  </span>
                </span>
                {already ? <span className="shrink-0 text-meta text-faint">추가됨</span> : null}
              </CommandItem>
            );
          })}
          {!q ? <CommandEmpty>이 부위로 기록한 종목이 없어요. 이름을 입력해 추가하세요.</CommandEmpty> : null}
        </CommandList>
      </Command>
    </Panel>
  );
}
