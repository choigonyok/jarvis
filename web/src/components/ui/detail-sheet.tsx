"use client";

import type { ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";

/**
 * What a line opens: the working behind it, over the page. A page that unfolds
 * in place is as long as everything on it (자산 5.6 screens, 작업 6.6); one that
 * opens a sheet stays the length it was, and closing it finds you where you were.
 */
export function DetailSheet({
  title,
  open,
  onOpenChange,
  children,
}: {
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        aria-describedby={undefined}
        onOpenAutoFocus={(e) => e.preventDefault()}
        className="inset-x-safe pb-safe max-h-[88dvh] gap-0 border-edge-soft bg-background sm:mx-auto sm:max-w-2xl sm:rounded-t-2xl sm:border"
      >
        <SheetHeader className="shrink-0 px-4 pt-4 pb-2 sm:px-6">
          <SheetTitle className="text-[14px] font-medium text-foreground">{title}</SheetTitle>
        </SheetHeader>
        <div className="scrollbar-hairline min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pb-6 sm:px-6">{children}</div>
      </SheetContent>
    </Sheet>
  );
}

/** A line that opens one: its name, what it comes to, and the figure on the right. */
export function DetailRow({
  title,
  line,
  figure,
  mark,
  focus,
  open,
  onOpenChange,
  children,
}: {
  title: string;
  /** The line under the name. */
  line?: ReactNode;
  figure?: ReactNode;
  /** A mark before the name: how it stands. */
  mark?: ReactNode;
  /** What a notification calls this line (?focus=, lib/focus.ts). */
  focus?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}) {
  return (
    <li data-focus={focus} className="scroll-mt-24">
      <button
        type="button"
        onClick={() => onOpenChange(true)}
        aria-haspopup="dialog"
        aria-expanded={open}
        className="flex min-h-14 w-full items-center gap-3 py-3 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        {mark}
        <span className="min-w-0 flex-1">
          <span className="block text-[14.5px] text-foreground/90">{title}</span>
          {line ? <span className="mt-0.5 block truncate text-[13px] text-faint">{line}</span> : null}
        </span>
        {figure ? <span className="tnum shrink-0 text-[12px] text-faint">{figure}</span> : null}
        <ChevronRight aria-hidden className="size-4 shrink-0 text-faint" />
      </button>
      <DetailSheet title={title} open={open} onOpenChange={onOpenChange}>
        {children}
      </DetailSheet>
    </li>
  );
}
