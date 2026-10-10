"use client";

import { useState } from "react";
import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import { ExercisePicker } from "@/components/workout/exercise-picker";
import { Panel, quietButton, saveButton } from "@/components/workout/panel";
import type { Routine, Session } from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * Writing the split.
 *
 * Edits are made on a copy and kept with 저장, so a half-finished rename or a
 * movement removed to be re-added somewhere else never shows up in the picker
 * mid-thought. Order matters - it is the order the session lays out - so each
 * movement can be moved up and down.
 */
export function RoutineEditor({
  open,
  onOpenChange,
  routines,
  sessions,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  routines: Routine[];
  sessions: Session[];
  onSave: (routines: Routine[]) => void;
}) {
  const [draft, setDraft] = useState<Routine[]>(routines);
  const [adding, setAdding] = useState<string | null>(null);

  // Re-read the saved list every time the sheet opens.
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setDraft(routines);
  }

  const patch = (id: string, fn: (r: Routine) => Routine) =>
    setDraft((d) => d.map((r) => (r.id === id ? fn(r) : r)));

  const move = (id: string, i: number, by: number) =>
    patch(id, (r) => {
      const exercises = [...r.exercises];
      const j = i + by;
      if (j < 0 || j >= exercises.length) return r;
      [exercises[i], exercises[j]] = [exercises[j], exercises[i]];
      return { ...r, exercises };
    });

  const valid = draft.every((r) => r.name.trim());

  return (
    <>
      <Panel open={open && adding === null} onOpenChange={onOpenChange} title="루틴 편집">
        <div className="space-y-6">
          {draft.map((routine) => (
            <section key={routine.id} aria-label={routine.name || "새 루틴"}>
              <div className="mb-1.5 flex items-center gap-2">
                <label className="min-w-0 flex-1">
                  <span className="sr-only">루틴 이름</span>
                  <input
                    value={routine.name}
                    onChange={(e) => patch(routine.id, (r) => ({ ...r, name: e.target.value }))}
                    placeholder="루틴 이름"
                    className="h-11 w-full rounded-lg border border-edge-soft bg-glass px-3 text-[16px] font-medium text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50"
                  />
                </label>
                {draft.length > 1 ? (
                  <button
                    type="button"
                    onClick={() => setDraft((d) => d.filter((r) => r.id !== routine.id))}
                    className={cn(quietButton, "shrink-0 text-faint hover:text-reject")}
                  >
                    삭제
                  </button>
                ) : null}
              </div>

              <ol className="space-y-px">
                {routine.exercises.map((name, i) => (
                  <li key={`${name}-${i}`} className="flex min-h-11 items-center gap-1 ps-3">
                    <span className="tnum w-5 shrink-0 text-[11.5px] text-faint">{i + 1}</span>
                    <span className="min-w-0 flex-1 truncate text-[14px] text-foreground/90">{name}</span>
                    <button
                      type="button"
                      onClick={() => move(routine.id, i, -1)}
                      disabled={i === 0}
                      aria-label={`${name} 위로`}
                      className="flex size-11 items-center justify-center rounded-md text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
                    >
                      <ArrowUp aria-hidden className="size-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => move(routine.id, i, 1)}
                      disabled={i === routine.exercises.length - 1}
                      aria-label={`${name} 아래로`}
                      className="flex size-11 items-center justify-center rounded-md text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
                    >
                      <ArrowDown aria-hidden className="size-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        patch(routine.id, (r) => ({ ...r, exercises: r.exercises.filter((_, j) => j !== i) }))
                      }
                      aria-label={`${name} 빼기`}
                      className="flex size-11 items-center justify-center rounded-md text-faint outline-none hover:text-reject focus-visible:ring-3 focus-visible:ring-ring/50"
                    >
                      <X aria-hidden className="size-4" />
                    </button>
                  </li>
                ))}
              </ol>
              <button
                type="button"
                onClick={() => setAdding(routine.id)}
                className={cn(quietButton, "mt-0.5 flex items-center gap-1.5 text-faint")}
              >
                <Plus aria-hidden className="size-3.5" />
                종목
              </button>
            </section>
          ))}
        </div>

        <button
          type="button"
          onClick={() =>
            setDraft((d) => [...d, { id: `r-${Date.now()}`, name: "", exercises: [] }])
          }
          className="mt-5 flex h-11 w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-edge text-[13px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <Plus aria-hidden className="size-4" />새 루틴
        </button>

        <div className="mt-5 flex gap-2">
          <button type="button" onClick={() => onOpenChange(false)} className={cn(quietButton, "flex-1 bg-glass")}>
            닫기
          </button>
          <button
            type="button"
            disabled={!valid}
            onClick={() => {
              onSave(draft.map((r) => ({ ...r, name: r.name.trim() })));
              onOpenChange(false);
            }}
            className={saveButton}
          >
            저장
          </button>
        </div>
        {!valid ? <p className="mt-2 text-center text-[12px] text-faint">이름 없는 루틴이 있어요.</p> : null}
      </Panel>

      <ExercisePicker
        open={adding !== null}
        onOpenChange={(o) => !o && setAdding(null)}
        sessions={sessions}
        taken={draft.find((r) => r.id === adding)?.exercises ?? []}
        onPick={(name) => {
          if (adding) patch(adding, (r) => ({ ...r, exercises: [...r.exercises, name] }));
          setAdding(null);
        }}
      />
    </>
  );
}
