"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { UNDO_TOASTER } from "@/components/shell/toasts";

/**
 * One undo at a time, for five seconds - long enough to see what you did,
 * short enough that it is gone before it is in the way.
 *
 * Two kinds of act take one. Some are done at once and undone by putting
 * things back (a workout's set: restore). Some are only done when the five
 * seconds are up (a calendar entry: commit), so an undo means nothing ever
 * left the device - not even the partner's calendar saw it. A commit still
 * waiting is done at once when another undo replaces it, when the screen
 * goes, or when the page is put away.
 *
 * The line is a toast (sonner, components/shell/toasts.tsx): its five
 * seconds, and its closing by itself, are the commit's; swiping it away
 * keeps what was done, as letting it run out does.
 */
export const UNDO_MS = 5000;

type Pending = { id: string; label: string; restore: () => void; commit?: () => void };

export function useUndo() {
  const [undo, setUndo] = useState<Pending | null>(null);
  const current = useRef<Pending | null>(null);

  const settle = useCallback((commit: boolean) => {
    const was = current.current;
    current.current = null;
    if (was) toast.dismiss(was.id);
    setUndo((u) => (u && u.id === was?.id ? null : u));
    if (commit) was?.commit?.();
  }, []);

  const take = useCallback(() => {
    const was = current.current;
    settle(false);
    was?.restore();
  }, [settle]);

  const offer = useCallback(
    (label: string, restore: () => void, commit?: () => void) => {
      settle(true);
      const next: Pending = { id: `undo-${Date.now()}`, label, restore, commit };
      current.current = next;
      setUndo(next);
      // The toast's own end is this undo's end - unless another took its place.
      const ended = () => {
        if (current.current?.id === next.id) settle(true);
      };
      toast.custom(
        () => (
          <UndoNotice
            label={label}
            onUndo={() => {
              if (current.current?.id === next.id) take();
            }}
          />
        ),
        { id: next.id, toasterId: UNDO_TOASTER, duration: UNDO_MS, onAutoClose: ended, onDismiss: ended },
      );
    },
    [settle, take],
  );

  // Leaving the screen, or the phone putting the page away, is not an undo.
  useEffect(() => {
    const away = () => settle(true);
    window.addEventListener("pagehide", away);
    return () => {
      window.removeEventListener("pagehide", away);
      settle(true);
    };
  }, [settle]);

  return { undo, offer, take };
}

/** The line itself: what was done, and the way back. */
export function UndoNotice({ label, onUndo }: { label: string; onUndo: () => void }) {
  return (
    <div role="status" className="liquid-glass flex w-full items-center gap-2 rounded-2xl py-1 ps-3.5 pe-1 text-ui">
      <span className="min-w-0 flex-1 truncate text-ink">{label}</span>
      <button
        type="button"
        onClick={onUndo}
        className="tap shrink-0 rounded-lg px-3 font-medium text-foreground underline underline-offset-4 focus-ring"
      >
        되돌리기
      </button>
    </div>
  );
}
