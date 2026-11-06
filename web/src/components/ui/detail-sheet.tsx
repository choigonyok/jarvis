"use client";

import type { ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { BottomSheetBody, BottomSheetContent, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Sheet } from "@/components/ui/sheet";

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
      <BottomSheetContent width="xl" aria-describedby={undefined}>
        <BottomSheetHeader className="sm:px-6">
          <BottomSheetTitle className="pr-0 text-body">{title}</BottomSheetTitle>
        </BottomSheetHeader>
        <BottomSheetBody className="flex-1 pb-6 sm:px-6">{children}</BottomSheetBody>
      </BottomSheetContent>
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
        className="flex min-h-14 w-full items-center gap-3 py-3 text-left focus-ring"
      >
        {mark}
        <span className="min-w-0 flex-1">
          <span className="block text-body text-ink">{title}</span>
          {line ? <span className="mt-0.5 block truncate text-ui text-faint">{line}</span> : null}
        </span>
        {figure ? <span className="tnum shrink-0 text-aside text-faint">{figure}</span> : null}
        <ChevronRight aria-hidden className="size-4 shrink-0 text-faint" />
      </button>
      <DetailSheet title={title} open={open} onOpenChange={onOpenChange}>
        {children}
      </DetailSheet>
    </li>
  );
}
