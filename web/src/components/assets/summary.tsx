"use client";

import type { ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { DetailSheet } from "@/components/ui/detail-sheet";
import { type Bridge, signedKrw } from "@/lib/portfolio";

/**
 * One question, answered in a line, with the detail in a sheet.
 *
 * Every section at full length would be four phone screens, and tabs would
 * hide the answers behind a choice. A row says what matters in the line
 * itself; the section it came from opens over the page for whoever wants to
 * check the working, and the page stays the length it was.
 */
export function Row({
  id,
  title,
  open = false,
  onToggle,
  children,
  detail,
  focus,
}: {
  id: string;
  title: string;
  open?: boolean;
  onToggle?: () => void;
  /** What a notification calls this row (?focus=, lib/focus.ts). */
  focus?: string;
  /** The answer, always visible. */
  children: ReactNode;
  /** The section the answer comes from, in a sheet when open. None: the row is the whole answer. */
  detail?: ReactNode;
}) {
  if (detail === undefined || !onToggle) {
    return (
      <section aria-labelledby={`${id}-title`} data-focus={focus} className="flex items-start gap-3 border-t border-edge-soft py-4">
        <span id={`${id}-title`} className="w-[4.5rem] shrink-0 pt-px text-aside text-faint">
          {title}
        </span>
        <div className="min-w-0 flex-1">{children}</div>
      </section>
    );
  }
  return (
    <section aria-labelledby={`${id}-title`} data-focus={focus} className="border-t border-edge-soft">
      <button
        type="button"
        onClick={onToggle}
        aria-haspopup="dialog"
        aria-expanded={open}
        className="group flex w-full items-start gap-3 py-4 text-left focus-ring"
      >
        <span id={`${id}-title`} className="w-[4.5rem] shrink-0 pt-px text-aside text-faint">
          {title}
        </span>
        <span className="min-w-0 flex-1">{children}</span>
        <ChevronRight aria-hidden className="mt-0.5 size-4 shrink-0 text-faint group-hover:text-dim" />
      </button>
      <DetailSheet title={title} open={open} onOpenChange={onToggle}>
        {detail}
      </DetailSheet>
    </section>
  );
}

/**
 * Where the return against principal came from, as one bar:
 *   sold (realized) + still held (unrealized) + everything else = the total.
 *
 * "Everything else" is what neither side carries: dollars held while the
 * rate moved, exchange and transfer fees, dividends. It is the difference,
 * not a guess - so the three always add up to the headline.
 */
const parts = ({ realizedKrw, unrealizedKrw, otherKrw }: Bridge) => [
  { key: "realized", label: "판 것", value: realizedKrw, tone: 0.9 },
  { key: "unrealized", label: "들고 있는 것", value: unrealizedKrw, tone: 0.55 },
  { key: "other", label: "환율·수수료·기타", value: otherKrw, tone: 0.3 },
];

/**
 * The three parts, one per line - the folded view. Each line is a number
 * with its sign; together they add up to the headline return.
 */
export function ProfitLines(bridge: Bridge) {
  return (
    <ul className="space-y-1">
      {parts(bridge).map((p) => (
        <li key={p.key} className="tnum flex items-baseline justify-between gap-3 text-ui">
          <span className="text-dim">{p.label}</span>
          <span className={p.value >= 0 ? "text-approve" : "text-reject"}>{signedKrw(p.value)}</span>
        </li>
      ))}
    </ul>
  );
}

/** What "환율·수수료·기타" is made of: each cause measured on its own, the last what is left. */
export function ProfitOther({ other, otherKrw }: Bridge) {
  if (!other.length) return null;
  return (
    <div>
      <p className="tnum mb-1.5 flex justify-between text-aside text-faint">
        <span>환율·수수료·기타 세부</span>
        <span>{signedKrw(otherKrw)}</span>
      </p>
      <ul className="space-y-0.5 border-l border-edge-soft ps-3 text-aside">
        {other.map((o) => (
          <li key={o.id} className="tnum flex justify-between gap-3">
            <span className="text-faint">{o.label}</span>
            <span className="text-dim">{signedKrw(o.krw)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
