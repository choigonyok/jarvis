"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { ChevronLeft } from "lucide-react";
import { Header, PageTop } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { TabBar } from "@/components/shell/tab-bar";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

/**
 * A screen of the couple's space: the console's header and bar around it, the
 * page one scroll under the header's glass. A block's page has the way back to
 * 우리 above its name - it is one of 우리's, not a place of its own.
 */
export function CoupleScreen({
  back,
  wide,
  children,
}: {
  /** A block's page: the way back to the home it is on. */
  back?: boolean;
  /** Two columns on a wide screen (the home). */
  wide?: boolean;
  children: ReactNode;
}) {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={waiting.length} />
        <StandingBar pending={waiting} />
      </PageTop>
      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        <div className={cn("mx-auto w-full px-4 pt-5 pb-12 sm:px-8 sm:pt-8", wide ? "max-w-page-wide" : "max-w-page")}>
          {back ? (
            <Link
              href="/couple"
              className="tap -ml-2 mb-1 inline-flex items-center gap-0.5 rounded-lg pr-2 text-ui text-dim transition-colors hover:text-foreground focus-ring"
            >
              <ChevronLeft aria-hidden className="size-5" />
              우리
            </Link>
          ) : null}
          {children}
        </div>
      </main>
      <TabBar pending={waiting.length} />
    </div>
  );
}

/** A block's name at the top of its page, and what stands beside it. */
export function BlockTitle({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <h1 className="min-w-0 text-display leading-[1.2] font-semibold tracking-tight break-keep text-foreground">{title}</h1>
      {children ? <div className="flex shrink-0 items-center gap-1 pt-1">{children}</div> : null}
    </div>
  );
}
