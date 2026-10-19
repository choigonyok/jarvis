"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { Proposal } from "@/lib/thread";
import { cn } from "@/lib/utils";

/** How long an approval that spends money waits, with 되돌리기 on the card. */
export const HOLD_MS = 5000;

/**
 * The cards whose approval spends money: the browser's payment guard stops a
 * checkout and asks (agent module/jobs/runtools.go). Approving one is the one
 * tap here that cannot be taken back once it is sent, so it is not sent at
 * once. (mind's rebalance card arrives as mind.approve, which says nothing
 * of what it does - it is not held.)
 */
export function holdsMoney(p: Proposal): boolean {
  return p.action.kind === "browser.payment";
}

/**
 * An approval held for HOLD_MS before it is sent. 되돌리기 lets go of it and
 * nothing was sent. Leaving the screen, or the phone putting the page away,
 * is not an undo: the approval goes then.
 */
export function useHold(send: () => void) {
  const [until, setUntil] = useState<number | null>(null);
  const [now, setNow] = useState(0);
  const latest = useRef(send);
  useEffect(() => {
    latest.current = send;
  });
  const armed = useRef(false);

  const fire = useCallback(() => {
    if (!armed.current) return;
    armed.current = false;
    setUntil(null);
    latest.current();
  }, []);

  useEffect(() => {
    if (until === null) return;
    const tick = window.setInterval(() => setNow(Date.now()), 250);
    const done = window.setTimeout(fire, Math.max(0, until - Date.now()));
    return () => {
      window.clearInterval(tick);
      window.clearTimeout(done);
    };
  }, [until, fire]);

  useEffect(() => {
    window.addEventListener("pagehide", fire);
    return () => {
      window.removeEventListener("pagehide", fire);
      fire();
    };
  }, [fire]);

  return {
    holding: until !== null,
    left: until === null ? 0 : Math.max(1, Math.ceil((until - now) / 1000)),
    start: () => {
      armed.current = true;
      const t = Date.now();
      setNow(t);
      setUntil(t + HOLD_MS);
    },
    cancel: () => {
      armed.current = false;
      setUntil(null);
    },
  };
}

/**
 * Where 반려 and 승인 were: what is about to happen, and the way out of it -
 * a button that empties as the seconds go. No green yet: nothing has been
 * decided until it is sent.
 */
export function HoldBar({ left, onCancel, className }: { left: number; onCancel: () => void; className?: string }) {
  return (
    <div className={cn("anim-settle", className)} role="status">
      <p className="mb-2 px-0.5 text-[12.5px] text-dim">
        <span className="tnum text-foreground">{left}초</span> 뒤에 결제를 진행해요.
      </p>
      <button
        type="button"
        onClick={onCancel}
        className="relative h-11 w-full overflow-hidden rounded-lg border border-edge bg-glass text-[13.5px] font-medium text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-8 mouse:text-[0.8rem]"
      >
        <span aria-hidden className="hold-drain absolute inset-0 bg-foreground/12" style={{ animationDuration: `${HOLD_MS}ms` }} />
        <span className="relative">되돌리기</span>
      </button>
    </div>
  );
}
