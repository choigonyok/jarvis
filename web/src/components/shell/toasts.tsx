"use client";

import type { CSSProperties } from "react";
import { Toaster } from "@/components/ui/sonner";

/** Where a toast goes (sonner's toasterId). */
export const UNDO_TOASTER = "undo";
export const PUSH_TOASTER = "push";

/**
 * The two places a toast appears, mounted once in the root layout.
 *
 * 되돌리기 sits just above the tab bar (or the home indicator), and above a
 * running workout's dock when there is one (the dock publishes where its top
 * is, workout/dock.tsx). A push shown in the app drops in from above the
 * screen.
 */
const UNDO_BOTTOM =
  "max(calc(max(var(--tabbar-space), env(safe-area-inset-bottom)) + 1.25rem), calc(var(--dock-top, 0px) + 0.5rem))";

export function Toasts() {
  return (
    <>
      <Toaster
        id={UNDO_TOASTER}
        position="bottom-center"
        offset={{ bottom: UNDO_BOTTOM }}
        mobileOffset={{ bottom: UNDO_BOTTOM, left: "1rem", right: "1rem" }}
        style={{ "--width": "min(38rem, calc(100vw - 4rem))" } as CSSProperties}
      />
      <Toaster
        id={PUSH_TOASTER}
        position="top-center"
        offset={{ top: "calc(env(safe-area-inset-top) + 8px)" }}
        mobileOffset={{ top: "calc(env(safe-area-inset-top) + 8px)", left: "1rem", right: "1rem" }}
      />
    </>
  );
}
