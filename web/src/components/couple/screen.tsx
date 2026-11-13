"use client";

import Link from "next/link";
import { useState, type ReactNode } from "react";
import { ChevronLeft, Pencil } from "lucide-react";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { writeCouple, type Block } from "@/lib/couple";
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

/**
 * A block's name at the top of its page, and what stands beside it. With
 * the block, the name can be changed: the pencil opens a sheet.
 */
export function BlockTitle({
  title,
  block,
  onRenamed,
  children,
}: {
  title: string;
  block?: Block;
  onRenamed?: () => unknown;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex items-start justify-between gap-3">
      <h1 className="min-w-0 text-display leading-[1.2] font-semibold tracking-tight break-keep text-foreground">
        {title}
        {block ? (
          <button
            type="button"
            onClick={() => setOpen(true)}
            aria-label="블록 이름 바꾸기"
            className="tap ml-1 inline-flex translate-y-[-2px] items-center justify-center align-middle text-faint transition-colors hover:text-foreground focus-ring"
          >
            <Pencil aria-hidden className="size-4" />
          </button>
        ) : null}
      </h1>
      {children ? <div className="flex shrink-0 items-center gap-1 pt-1">{children}</div> : null}
      {block ? (
        <Panel open={open} onOpenChange={setOpen} title="블록 이름">
          {open ? <Rename block={block} onClose={() => setOpen(false)} onRenamed={onRenamed} /> : null}
        </Panel>
      ) : null}
    </div>
  );
}

function Rename({ block, onClose, onRenamed }: { block: Block; onClose: () => void; onRenamed?: () => unknown }) {
  const [title, setTitle] = useState(block.title);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ready = title.trim() !== "" && title.trim() !== block.title;
  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setError(null);
    const sent = await writeCouple("PATCH", `/blocks/${block.id}`, { title: title.trim() });
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    onClose();
    await onRenamed?.();
  }
  return (
    <form onSubmit={(e) => void save(e)} className="space-y-3">
      <Input autoFocus surface="glass" value={title} maxLength={40} onChange={(e) => setTitle(e.target.value)} aria-label="블록 이름" />
      <p className="text-aside text-faint">상대의 uniple 앱에서도 이 이름으로 보여요.</p>
      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}
      <div className="flex gap-2">
        <Button type="button" variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
          두기
        </Button>
        <Button type="submit" variant="raised" size="touch" disabled={!ready || busy} className="flex-1">
          {busy ? "저장하는 중…" : "저장"}
        </Button>
      </div>
    </form>
  );
}
