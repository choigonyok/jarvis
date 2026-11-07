"use client";

import { useLayoutEffect, type ReactNode, type RefObject } from "react";
import { ArrowDown } from "lucide-react";
import type { ChatMode } from "@/components/chat/mode-toggle";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";

/**
 * The composer and the tab bar float over the thread's end: how tall they
 * stand is the room under its last row (--footer-space, set on the screen's
 * root), kept as it changes - a long message, a quote, the bar stepping away
 * while typing.
 */
export function useFooterSpace(
  rootRef: RefObject<HTMLDivElement | null>,
  footerRef: RefObject<HTMLElement | null>,
  /** The voice stage builds the screen anew on its return. */
  mode: ChatMode,
) {
  useLayoutEffect(() => {
    const root = rootRef.current;
    const footer = footerRef.current;
    if (!root || !footer) return;
    const put = () => root.style.setProperty("--footer-space", `${footer.getBoundingClientRect().height}px`);
    put();
    const watch = new ResizeObserver(put);
    // Its border box: the tab bar's room is the footer's own padding, which
    // a content-box watch never sees change.
    watch.observe(footer, { box: "border-box" });
    return () => watch.disconnect();
    // Again for the screen the voice stage's return builds.
  }, [mode, rootRef, footerRef]);
}

/**
 * The conversation's footer: the composer, and over it the way back down.
 *
 * Lifted above the floating tab bar; it drops back down while typing, when
 * the bar hides. On a phone it floats a gutter above the bar and a gutter in
 * from the edges - the bar's own spacing, so the two read as a pair. It
 * floats over the thread's end rather than cutting it short: what it does not
 * cover is the thread's, to see and to scroll by (its height is
 * --footer-space, the room under the last row).
 */
export function ThreadFooter({
  footerRef,
  seeking,
  away,
  unseen,
  onBottom,
  typing,
  children,
}: {
  footerRef: RefObject<HTMLElement | null>;
  /** Pages above are being read for a card a push asked for. */
  seeking: boolean;
  /** The newest turn is out of sight. */
  away: boolean;
  /** Replies of Jarvis's that came while away. */
  unseen: number;
  onBottom: () => void;
  /** The keyboard is up: the bar below has stepped away. */
  typing: boolean;
  /** The composer, and what goes with it. */
  children: ReactNode;
}) {
  return (
    <footer
      ref={footerRef}
      className="pb-tabbar pointer-events-none relative z-20 shrink-0 transition-[padding] duration-300 ease-out motion-reduce:transition-none"
    >
      {/* Back to the newest: a bead of the composer's own glass, centred
          over it, there only while the newest turn is out of sight. It
          says how many replies came meanwhile, so a reply that landed while
          you were reading back is not missed. */}
      {seeking ? (
        <div className="pointer-events-none absolute inset-x-0 bottom-full pb-3">
          <div className="inset-x-safe">
            <div className="mx-auto flex w-full max-w-page justify-center px-4 sm:px-8">
              <p
                role="status"
                className="anim-settle flex h-11 items-center gap-2 rounded-full bg-popover px-4 text-ui text-dim shadow-lg ring-1 ring-edge mouse:h-10"
              >
                <Spinner className="size-3.5" />
                카드를 찾는 중…
              </p>
            </div>
          </div>
        </div>
      ) : away ? (
        <div className="pointer-events-none absolute inset-x-0 bottom-full pb-3">
          <div className="inset-x-safe">
            <div className="mx-auto flex w-full max-w-page justify-center px-4 sm:px-8">
              <button
                type="button"
                onClick={onBottom}
                aria-label={unseen ? `새 메시지 ${unseen}개, 맨 아래로` : "맨 아래로"}
                className={cn(
                  "anim-settle pointer-events-auto flex h-11 items-center justify-center gap-1.5 rounded-full bg-popover text-dim shadow-lg ring-1 ring-edge transition-colors hover:text-foreground focus-ring mouse:h-10",
                  unseen ? "pr-3.5 pl-4" : "w-11 sm:w-10",
                )}
              >
                {unseen ? <span className="tnum text-ui text-foreground">새 메시지 {unseen}</span> : null}
                <ArrowDown aria-hidden className="size-[18px] shrink-0" strokeWidth={1.8} />
              </button>
            </div>
          </div>
        </div>
      ) : null}
      {/* Two boxes: the safe-area padding and the gutter would otherwise
          be one padding, and the safe area (0 on most phones) would win. */}
      <div className="inset-x-safe">
        <div className={cn("mx-auto w-full max-w-page px-4 sm:px-8 sm:pb-6", typing ? "pb-2" : "pb-0")}>{children}</div>
      </div>
    </footer>
  );
}
