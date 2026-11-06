"use client";

import Link from "next/link";
import { memo } from "react";
import { summarize } from "@/lib/ledger";
import type { Proposal } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * What is waiting, said once, at the top of wherever you are.
 *
 * Not a toast: a toast leaves on a timer, and a request that disappears by
 * itself is the one failure this product cannot have. This stands until the
 * queue is empty, because that is exactly how long the agent stands there.
 * There is nothing to dismiss and nothing to mark as read - the decision is
 * the only way out, which is why the only control is the one that takes you
 * to it: the oldest waiting card itself, in the conversation, as a tapped
 * push does. It took every screen to the 기록 tab, a tab away even from the
 * conversation the card stood in, and the guest, who cannot open 기록, back to
 * the conversation's bottom (2026-11-06 review).
 */
function StandingBarView({
  pending,
  onGo,
  /** Set where the waiting card is already on screen; saying it twice is nagging. */
  muted,
}: {
  pending: Proposal[];
  /** In the conversation: going to the card is scrolling to it, not leaving. */
  onGo?: (id: string) => void;
  muted?: boolean;
}) {
  const count = pending.length;
  const show = count > 0 && !muted;
  // The agent lists cards as they were raised: the first is the oldest, the
  // one that has waited longest, and the one named below.
  const first = pending[0];
  const go =
    "tap flex shrink-0 items-center rounded-lg px-2 text-[12.5px] text-foreground transition-colors outline-none hover:text-foreground/80 focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-0 mouse:min-w-0 mouse:py-1";

  return (
    <div
      aria-live="polite"
      className={cn(
        "inset-x-safe shrink-0 overflow-hidden border-edge-soft bg-glass transition-[height,border] duration-200 motion-reduce:transition-none",
        show ? "border-b" : "h-0 border-b-0",
      )}
    >
      <div className="mx-auto flex w-full max-w-[46rem] items-center gap-2.5 px-4 py-2 sm:px-8">
        <span aria-hidden className="anim-breathe size-[5px] shrink-0 rounded-full bg-dim" />

        <p className="min-w-0 flex-1 truncate text-[12.5px] text-dim">
          {/* The oldest one by name: "2건" alone tells you there is work
              without telling you whether it can wait. */}
          {count > 1 ? (
            <>
              <span className="tnum text-foreground">{count}건</span> 대기 ·{" "}
              {summarize(pending[0])}
            </>
          ) : count === 1 ? (
            summarize(pending[0])
          ) : null}
        </p>

        {onGo ? (
          <button type="button" onClick={() => first && onGo(first.id)} className={go}>
            결재하기
          </button>
        ) : (
          <Link href={first ? `/?proposal=${encodeURIComponent(first.id)}` : "/"} className={go}>
            결재하기
          </Link>
        )}
      </div>
    </div>
  );
}

// Drawn again only when what is waiting changes: the thread above it renders
// on every frame of an answer.
export const StandingBar = memo(StandingBarView);
