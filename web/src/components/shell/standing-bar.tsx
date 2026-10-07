"use client";

import Link from "next/link";
import { Bell, X } from "lucide-react";
import { summarize } from "@/lib/ledger";
import type { Proposal } from "@/lib/thread";
import { useNotify } from "@/lib/use-notify";
import { cn } from "@/lib/utils";

/**
 * What is waiting, said once, at the top of wherever you are.
 *
 * Not a toast: a toast leaves on a timer, and a request that disappears by
 * itself is the one failure this product cannot have. This stands until the
 * queue is empty, because that is exactly how long the agent stands there.
 * There is nothing to dismiss and nothing to mark as read - the decision is
 * the only way out, which is why the only control is the one that takes you
 * to it.
 */
export function StandingBar({
  pending,
  href = "/",
  /** Set where the waiting card is already on screen; saying it twice is nagging. */
  muted,
}: {
  pending: Proposal[];
  href?: string;
  muted?: boolean;
}) {
  const notify = useNotify(pending);
  const count = pending.length;
  const show = count > 0 && !muted;

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
          {/* The newest one by name: "2건" alone tells you there is work
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

        {/* Offered in words before a permission prompt: asking on load is the
            quickest way to be denied for good. */}
        {notify.state === "off" ? (
          <button
            type="button"
            onClick={() => void notify.enable()}
            aria-label="알림 받기"
            className="tap flex shrink-0 items-center gap-1.5 rounded-lg px-1 text-[12px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-0 sm:min-w-0"
          >
            <Bell aria-hidden className="size-3.5" />
            <span className="hidden sm:inline">알림 받기</span>
          </button>
        ) : notify.state === "on" ? (
          <button
            type="button"
            onClick={notify.disable}
            aria-label="알림 끄기"
            className="tap flex shrink-0 items-center gap-1.5 rounded-lg px-1 text-[12px] text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-0 sm:min-w-0"
          >
            <Bell aria-hidden className="size-3.5 fill-current" />
            <X aria-hidden className="size-3" />
          </button>
        ) : null}

        <Link
          href={href}
          className="tap flex shrink-0 items-center rounded-lg px-2 text-[12.5px] text-foreground transition-colors outline-none hover:text-foreground/80 focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-0 sm:min-w-0 sm:py-1"
        >
          결재하기
        </Link>
      </div>
    </div>
  );
}
