"use client";

import { useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import type { Cursor } from "@/lib/month";
import { cn } from "@/lib/utils";

/** Another month, picked by its year and name rather than paged to. */
export function MonthPicker({
  open,
  onOpenChange,
  cursor,
  onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  cursor: Cursor;
  onPick: (cursor: Cursor) => void;
}) {
  const [year, setYear] = useState(cursor.year);
  const [seen, setSeen] = useState(open);
  if (open !== seen) {
    setSeen(open);
    if (open) setYear(cursor.year);
  }
  const now = new Date();
  return (
    <Panel open={open} onOpenChange={onOpenChange} title="다른 달로">
      <div className="flex items-center justify-between">
        <Button variant="ghost" size="icon-touch" onClick={() => setYear((y) => y - 1)} aria-label="이전 해" className="text-faint hover:text-foreground">
          <ChevronLeft aria-hidden className="size-5" />
        </Button>
        <span className="text-title font-medium text-foreground tabular-nums">{year}년</span>
        <Button variant="ghost" size="icon-touch" onClick={() => setYear((y) => y + 1)} aria-label="다음 해" className="text-faint hover:text-foreground">
          <ChevronRight aria-hidden className="size-5" />
        </Button>
      </div>
      <div className="mt-3 grid grid-cols-4 gap-2">
        {Array.from({ length: 12 }, (_, m) => {
          const on = year === cursor.year && m === cursor.month;
          const thisMonth = year === now.getFullYear() && m === now.getMonth();
          return (
            <button
              key={m}
              type="button"
              aria-pressed={on}
              onClick={() => {
                onPick({ year, month: m });
                onOpenChange(false);
              }}
              className={cn(
                "flex h-12 items-center justify-center rounded-lg text-ui transition-colors focus-ring",
                on ? "bg-foreground font-semibold text-background" : "bg-glass text-ink hover:bg-glass-raised",
                thisMonth && !on && "ring-1 ring-edge",
              )}
            >
              {m + 1}월
            </button>
          );
        })}
      </div>
    </Panel>
  );
}
