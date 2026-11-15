"use client";

import { Suspense, useEffect, type CSSProperties } from "react";
import { useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { useRole } from "@/components/shell/role";
import { Toaster } from "@/components/ui/sonner";
import { DENIED_PARAM } from "@/lib/role";

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
      {/* useSearchParams wants a boundary of its own. */}
      <Suspense fallback={null}>
        <TurnedAway />
      </Suspense>
    </>
  );
}

/**
 * A guest sent from a page that is not theirs - an operator's link, an old
 * bookmark - lands on the conversation marked so (/?denied=1, the
 * middleware), and it says why, once, where the app's notes appear: the
 * conversation had opened without a word. The mark then leaves the address,
 * so a reload does not say it again.
 */
function TurnedAway() {
  const role = useRole();
  const denied = useSearchParams().has(DENIED_PARAM);
  useEffect(() => {
    if (!denied) return;
    if (role === "guest") {
      toast.custom(
        () => (
          <div role="status" className="liquid-glass flex min-h-13 w-full items-center rounded-2xl px-3.5 py-2 text-ui text-ink">
            손님 계정으로는 열 수 없는 화면이에요.
          </div>
        ),
        { id: "denied", toasterId: UNDO_TOASTER, duration: 4000 },
      );
    }
    const url = new URL(window.location.href);
    url.searchParams.delete(DENIED_PARAM);
    // null, as Next's guide has it: the router keeps its own state and takes
    // the new address, rather than writing the old one back later.
    window.history.replaceState(null, "", url.pathname + url.search + url.hash);
  }, [denied, role]);
  return null;
}
