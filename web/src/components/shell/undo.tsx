"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

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
 */
export const UNDO_MS = 5000;

type Pending = { id: number; label: string; restore: () => void; commit?: () => void };

export function useUndo() {
  const [undo, setUndo] = useState<Pending | null>(null);
  const current = useRef<Pending | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const settle = useCallback((commit: boolean) => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
    const was = current.current;
    current.current = null;
    if (commit) was?.commit?.();
  }, []);

  const offer = useCallback(
    (label: string, restore: () => void, commit?: () => void) => {
      settle(true);
      const next = { id: Date.now(), label, restore, commit };
      current.current = next;
      setUndo(next);
      timer.current = setTimeout(() => {
        settle(true);
        setUndo((u) => (u?.id === next.id ? null : u));
      }, UNDO_MS);
    },
    [settle],
  );

  const take = useCallback(() => {
    const was = current.current;
    settle(false);
    setUndo(null);
    was?.restore();
  }, [settle]);

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
    <div
      role="status"
      className="mb-2 flex items-center gap-2 rounded-xl bg-popover/95 py-1 ps-3.5 pe-1 text-[13px] shadow-lg ring-1 ring-edge"
    >
      <span className="min-w-0 flex-1 truncate text-foreground/90">{label}</span>
      <button
        type="button"
        onClick={onUndo}
        className="tap shrink-0 rounded-lg px-3 font-medium text-foreground underline underline-offset-4 outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        되돌리기
      </button>
    </div>
  );
}

/** The line where nothing else holds it: just above the tab bar (or the home indicator). */
export function FloatingUndo({ children }: { children: ReactNode }) {
  return (
    <div
      className="pointer-events-none fixed inset-x-0 z-30 mx-auto max-w-[42rem] px-4 sm:px-8"
      style={{
        bottom: "calc(max(var(--tabbar-space), env(safe-area-inset-bottom)) + 0.75rem)",
      }}
    >
      <div className="pointer-events-auto">{children}</div>
    </div>
  );
}
