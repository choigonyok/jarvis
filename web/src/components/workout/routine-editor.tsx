"use client";

import { useState } from "react";
import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import { ExercisePicker } from "@/components/workout/exercise-picker";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import type { Routine, Session } from "@/lib/workout";

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
  // Edited and not saved: every way out but 저장 asks first - a pull on the
  // sheet or an edge swipe back had dropped minutes of reordering unasked.
  const dirty = open && JSON.stringify(draft) !== JSON.stringify(routines);
  const [asking, setAsking] = useState(false);

  // The picker opens over the editor rather than in its place: closed and
  // opened again in one stroke, each left its back entry behind (two for a
  // movement added, for the back gesture to spend), and the editor came back
  // scrolled to its top.
  return (
    <>
      <Panel open={open} onOpenChange={onOpenChange} title="루틴 편집" guard={dirty}>
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
                    className="h-11 w-full rounded-lg border border-edge-soft bg-glass px-3 text-field font-medium text-foreground placeholder:text-faint focus-ring"
                  />
                </label>
                {draft.length > 1 ? (
                  <Button
                    variant="discard"
                    size="touch-sm"
                    onClick={() => setDraft((d) => d.filter((r) => r.id !== routine.id))}
                    aria-label={`${routine.name || "새 루틴"} 지우기`}
                    className="shrink-0"
                  >
                    지우기
                  </Button>
                ) : null}
              </div>

              <ol className="space-y-px">
                {routine.exercises.map((name, i) => (
                  <li key={`${name}-${i}`} className="flex min-h-11 items-center gap-1 ps-3">
                    <span className="tnum w-5 shrink-0 text-meta text-faint">{i + 1}</span>
                    <span className="min-w-0 flex-1 truncate text-body text-ink">{name}</span>
                    <button
                      type="button"
                      onClick={() => move(routine.id, i, -1)}
                      disabled={i === 0}
                      aria-label={`${name} 위로`}
                      className="flex size-11 items-center justify-center rounded-md text-faint hover:text-foreground focus-ring disabled:opacity-30"
                    >
                      <ArrowUp aria-hidden className="size-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => move(routine.id, i, 1)}
                      disabled={i === routine.exercises.length - 1}
                      aria-label={`${name} 아래로`}
                      className="flex size-11 items-center justify-center rounded-md text-faint hover:text-foreground focus-ring disabled:opacity-30"
                    >
                      <ArrowDown aria-hidden className="size-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        patch(routine.id, (r) => ({ ...r, exercises: r.exercises.filter((_, j) => j !== i) }))
                      }
                      aria-label={`${name} 빼기`}
                      className="flex size-11 items-center justify-center rounded-md text-faint hover:text-reject focus-ring"
                    >
                      <X aria-hidden className="size-4" />
                    </button>
                  </li>
                ))}
              </ol>
              <Button variant="quiet" size="touch-sm" onClick={() => setAdding(routine.id)} className="mt-0.5 text-faint">
                <Plus aria-hidden className="size-3.5" />
                종목
              </Button>
            </section>
          ))}
        </div>

        <button
          type="button"
          onClick={() =>
            setDraft((d) => [...d, { id: `r-${Date.now()}`, name: "", exercises: [] }])
          }
          className="mt-5 flex h-11 w-full items-center justify-center gap-1.5 rounded-lg bg-glass text-ui text-dim hover:text-foreground focus-ring"
        >
          <Plus aria-hidden className="size-4" />새 루틴
        </button>

        <div className="mt-5 flex gap-2">
          <Button
            variant="soft"
            size="touch-sm"
            onClick={() => (dirty ? setAsking(true) : onOpenChange(false))}
            className="flex-1"
          >
            닫기
          </Button>
          <Button
            variant="raised"
            size="touch"
            disabled={!valid}
            onClick={() => {
              onSave(draft.map((r) => ({ ...r, name: r.name.trim() })));
              onOpenChange(false);
            }}
            className="flex-1"
          >
            저장
          </Button>
        </div>
        {!valid ? <p className="mt-2 text-center text-aside text-faint">이름 없는 루틴이 있어요.</p> : null}

        {/* 닫기 is the editor's own way out, which the sheet's guard does not
            see; it asks the same question. Drawn inside the sheet, so a tap
            on it is not a tap outside. */}
        <AlertDialog open={asking} onOpenChange={setAsking}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>쓰던 내용을 버릴까요?</AlertDialogTitle>
              <AlertDialogDescription>닫으면 적은 것이 사라져요. 되돌릴 수 없어요.</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>두기</AlertDialogCancel>
              <AlertDialogAction onClick={() => onOpenChange(false)}>버리기</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
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
