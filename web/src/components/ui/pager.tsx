"use client";

import { ChevronLeft, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * The pages of a long list (lib/paging.ts), under it: where you are in it on
 * the left, the pages on the right. Up to five numbers - all of them, or the
 * first, the last and the one you are on - so a 360px phone holds the line.
 * Nothing at all when the list fits on one page.
 */
export function slots(page: number, count: number): (number | "gap")[] {
  if (count <= 5) return Array.from({ length: count }, (_, i) => i + 1);
  if (page <= 3) return [1, 2, 3, "gap", count];
  if (page >= count - 2) return [1, "gap", count - 2, count - 1, count];
  return [1, "gap", page, "gap", count];
}

// 32px wide and 36px tall, the press reaching 44px above and below (tap-y):
// at the last pages - ‹ 1 … 6 7 8 › - the line still fits a 360px phone.
const button =
  "tap-y flex h-9 min-w-8 items-center justify-center rounded-md px-1 text-[12.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-7 mouse:min-w-7";

export function Pager({
  page,
  pageCount,
  total,
  from,
  to,
  go,
  unit = "건",
  className,
}: {
  page: number;
  pageCount: number;
  total: number;
  from: number;
  to: number;
  go: (page: number) => void;
  /** What a row is called after its count: 건, 장, 번. */
  unit?: string;
  className?: string;
}) {
  if (pageCount <= 1) return null;
  return (
    <nav
      aria-label="페이지"
      className={cn("mt-4 flex flex-wrap items-center justify-between gap-x-3 gap-y-1", className)}
    >
      <p className="tnum text-[11.5px] text-faint">
        {from}–{to} / {total}
        {unit}
      </p>
      <div className="tnum flex items-center gap-0.5">
        <button
          type="button"
          aria-label="이전 페이지"
          disabled={page <= 1}
          onClick={() => go(page - 1)}
          className={cn(button, "text-dim hover:text-foreground disabled:pointer-events-none disabled:text-faint/40")}
        >
          <ChevronLeft aria-hidden className="size-4" />
        </button>
        {slots(page, pageCount).map((s, i) =>
          s === "gap" ? (
            <span key={`gap-${i}`} aria-hidden className="w-4 text-center text-[12px] text-faint">
              …
            </span>
          ) : (
            <button
              key={s}
              type="button"
              aria-label={`${s}쪽`}
              aria-current={s === page ? "page" : undefined}
              onClick={() => go(s)}
              className={cn(button, s === page ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim")}
            >
              {s}
            </button>
          ),
        )}
        <button
          type="button"
          aria-label="다음 페이지"
          disabled={page >= pageCount}
          onClick={() => go(page + 1)}
          className={cn(button, "text-dim hover:text-foreground disabled:pointer-events-none disabled:text-faint/40")}
        >
          <ChevronRight aria-hidden className="size-4" />
        </button>
      </div>
    </nav>
  );
}
