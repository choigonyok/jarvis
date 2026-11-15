"use client";

import { krw } from "@/lib/portfolio";
import type { Book } from "@/lib/spending";
import { cn } from "@/lib/utils";

/**
 * Where the month went, largest first. Each row uses the pace bar's grammar
 * at a smaller size - fill for what was spent, a hairline for the budget - so
 * nothing new has to be learned to read it. A row is also the filter for the
 * list below, the way the calendar's legend is its filter.
 */
export function Categories({
  book,
  selected,
  onSelect,
}: {
  book: Book;
  selected: string | null;
  onSelect: (name: string | null) => void;
}) {
  const rows = book.categories;
  if (rows.length === 0) return null;
  const scale = Math.max(1, ...rows.map((c) => Math.max(c.totalKrw, c.budgetKrw ?? 0)));

  return (
    <ul className="space-y-px">
      {rows.map((c) => {
        const active = selected === c.name;
        // The rows not picked step down a tone rather than fade: the faint
        // lines in them, under an opacity, fell below what can be read.
        const muted = selected !== null && !active;
        const over = c.budgetKrw !== null && c.totalKrw > c.budgetKrw;
        const share = book.totalKrw > 0 ? c.totalKrw / book.totalKrw : 0;
        return (
          <li key={c.name} data-focus={`category:${c.name}`}>
            <button
              type="button"
              onClick={() => onSelect(active ? null : c.name)}
              aria-pressed={active}
              className={cn(
                "w-full rounded-lg px-2 py-2.5 text-left transition-colors focus-ring",
                active ? "bg-glass-raised" : "hover:bg-glass",
              )}
            >
              <span className="flex items-baseline justify-between gap-3">
                <span className={cn("text-ui", muted ? "text-dim" : "text-ink")}>
                  {c.name}
                  <span className="tnum ms-2 text-meta text-faint">
                    {c.count ? `${c.count}건` : ""}
                    {c.count && share >= 0.01 ? ` · ${Math.round(share * 100)}%` : ""}
                  </span>
                </span>
                <span className={cn("tnum shrink-0 text-ui", muted ? "text-dim" : "text-ink")}>
                  {krw(c.totalKrw)}
                </span>
              </span>
              <span className="relative mt-2 block h-1 w-full rounded-full bg-glass">
                <span
                  className={cn("block h-full rounded-full", muted ? "bg-foreground/25" : "bg-foreground/45")}
                  style={{ width: `${(Math.max(0, c.totalKrw) / scale) * 100}%` }}
                />
                {c.budgetKrw !== null ? (
                  <span
                    aria-hidden
                    className={cn("absolute -top-1 h-3 w-px", muted ? "bg-faint" : "bg-dim")}
                    style={{ left: `calc(${(c.budgetKrw / scale) * 100}% - 0.5px)` }}
                  />
                ) : null}
              </span>
              <span className="mt-1.5 block text-meta text-faint">
                {c.budgetKrw !== null
                  ? over
                    ? `예산 ${krw(c.budgetKrw)}보다 ${krw(c.totalKrw - c.budgetKrw)} 더 씀`
                    : `예산 ${krw(c.budgetKrw)} 중 ${krw(c.budgetKrw - c.totalKrw)} 남음`
                  : c.prevKrw > 0
                    ? `지난달 ${krw(c.prevKrw)}`
                    : "지난달 없음"}
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
