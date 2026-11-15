"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Spinner } from "@/components/ui/spinner";
import { ACTION } from "@/lib/kinds";
import type { Decision, Proposal } from "@/lib/thread";
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
  return p.action.kind === ACTION.browserPayment;
}

/**
 * The cards whose held approval was let go by leaving - another tab, the phone
 * put away, a call, voice mode - and not sent. Kept past the card's own life
 * on screen: coming back, the card says so and asks again.
 */
const letGo = new Set<string>();

/** How the last hold on a card ended with nothing sent: by leaving the screen, or by 되돌리기. */
export type Unsent = "left" | "undone";

/**
 * How soon after a hold begins 되돌리기 is not taken. It stands where 승인 was,
 * so the second tap of a double tap on 승인 - or one more, the press felt
 * slow - landed on it, and the approval was let go with nothing said.
 */
const UNDO_AFTER_MS = 500;

/**
 * An approval held for HOLD_MS before it is sent. 되돌리기 lets go of it and
 * nothing was sent. Leaving the screen, or the phone putting the page away,
 * lets go of it too: a payment is not sent in the moment nobody is looking -
 * a held approval sent on the way out was the commonest way the wait came to
 * nothing. The card then says which (unsent), and 다시 승인 holds it again.
 */
export function useHold(id: string, send: () => void) {
  const [until, setUntil] = useState<number | null>(null);
  const [now, setNow] = useState(0);
  const [unsent, setUnsent] = useState<Unsent | null>(() => (letGo.has(id) ? "left" : null));
  const latest = useRef(send);
  useEffect(() => {
    latest.current = send;
  });
  const armed = useRef(false);

  const fire = useCallback(() => {
    if (!armed.current) return;
    armed.current = false;
    letGo.delete(id);
    setUntil(null);
    latest.current();
  }, [id]);

  // Gone from the screen while waiting: held back, not sent.
  const holdBack = useCallback(() => {
    if (!armed.current) return;
    armed.current = false;
    letGo.add(id);
    setUntil(null);
    setUnsent("left");
  }, [id]);

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
    // Hidden is the phone put away or another app in front: a timer left
    // running would send the moment the page thaws.
    const hidden = () => document.visibilityState === "hidden" && holdBack();
    window.addEventListener("pagehide", holdBack);
    document.addEventListener("visibilitychange", hidden);
    return () => {
      window.removeEventListener("pagehide", holdBack);
      document.removeEventListener("visibilitychange", hidden);
      holdBack();
    };
  }, [holdBack]);

  return {
    holding: until !== null,
    /** How the last hold ended with nothing sent, if it did. */
    unsent,
    left: until === null ? 0 : Math.max(1, Math.ceil((until - now) / 1000)),
    start: () => {
      armed.current = true;
      letGo.delete(id);
      setUnsent(null);
      const t = Date.now();
      setNow(t);
      setUntil(t + HOLD_MS);
    },
    /** Lets go of the hold - false when too soon after it began to be meant. */
    cancel: () => {
      if (until !== null && Date.now() - (until - HOLD_MS) < UNDO_AFTER_MS) return false;
      armed.current = false;
      letGo.delete(id);
      setUnsent("undone");
      setUntil(null);
      return true;
    },
  };
}

/**
 * The decision on its way, where the buttons were. Without it the pair would
 * come back dimmed the moment the hold ended, and read as "not sent yet".
 */
export function Sending({ className }: { className?: string }) {
  return (
    <p role="status" className={cn("anim-settle flex min-h-12 items-center gap-2 px-1 text-aside text-dim mouse:min-h-8", className)}>
      <Spinner className="size-3.5" />
      결정을 보내는 중…
    </p>
  );
}

/**
 * The decision taken, and the card not back with it yet - where the buttons
 * were, until the card reads 승인함 or 반려함. The breathing dot is the one a
 * waiting card has: it is still waiting, on Jarvis now, not on you. Still no
 * green: the colour comes with the card's own word that it is done.
 */
export function Sent({
  decision,
  yesNo,
  label,
  className,
}: {
  decision: Decision;
  yesNo?: boolean;
  /** The answer sent, as the card's own button said it. */
  label?: string;
  className?: string;
}) {
  // A question's answer is said as the answer, not as an approval (lib/outcome.ts).
  const said = label
    ? `'${label}' 보냈어요`
    : yesNo
      ? (decision === "approved" ? "예라고 답했어요" : "아니요라고 답했어요")
      : (decision === "approved" ? "승인을 보냈어요" : "반려를 보냈어요");
  return (
    <p role="status" className={cn("anim-settle flex min-h-12 items-center gap-2 px-1 text-aside text-dim mouse:min-h-8", className)}>
      <span aria-hidden className="anim-breathe size-[5px] shrink-0 rounded-full bg-dim" />
      <span>
        {said}
        <span className="text-faint"> · 반영 중</span>
      </span>
    </p>
  );
}

/**
 * What a card says of a hold let go: nothing was sent, and it can be asked
 * again. 되돌리기 says so too - the buttons coming back alone read, after a
 * stray tap, as a payment on its way.
 */
export function HeldBack({ by, className }: { by: Unsent; className?: string }) {
  return (
    <p className={cn("mb-2 px-0.5 text-aside text-dim", className)}>
      {by === "left" ? "승인이 보류됐어요. 화면을 떠나서 결제를 보내지 않았어요." : "되돌렸어요. 결제를 보내지 않았어요."}
    </p>
  );
}

/**
 * Where 반려 and 승인 were: what is about to happen, and the way out of it -
 * a button that empties as the seconds go. No green yet: nothing has been
 * decided until it is sent.
 *
 * The seconds going down are for the eye. Read out, the whole sentence was
 * said again every second, over the way to 되돌리기; a screen reader hears it
 * once, with the full five, and again on 되돌리기 itself.
 */
export function HoldBar({ left, onCancel, className }: { left: number; onCancel: () => void; className?: string }) {
  const said = useId();
  return (
    <div className={cn("anim-settle", className)}>
      <p id={said} className="mb-2 px-0.5 text-aside text-dim">
        <span aria-hidden className="tnum text-foreground">{left}초</span>
        <span className="sr-only">{HOLD_MS / 1000}초</span> 뒤에 결제를 진행해요. 화면을 떠나면 보류돼요.
      </p>
      <button
        type="button"
        onClick={onCancel}
        aria-describedby={said}
        className="relative h-11 w-full overflow-hidden rounded-lg border border-edge bg-glass text-ui font-medium text-foreground focus-ring mouse:h-8 mouse:text-[0.8rem]"
      >
        <span aria-hidden className="hold-drain absolute inset-0 bg-foreground/12" style={{ animationDuration: `${HOLD_MS}ms` }} />
        <span className="relative">되돌리기</span>
      </button>
    </div>
  );
}
