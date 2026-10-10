"use client";

import type { ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { type Bridge, signedKrw } from "@/lib/portfolio";
import { cn } from "@/lib/utils";

/**
 * One question, answered in a line, with the detail folded under it.
 *
 * The tab used to lay every section out at full length - four phone screens
 * - and then split them into tabs, which hid the answers behind a choice. A
 * row says what matters in the line itself; the section it came from opens
 * in place for whoever wants to check the working.
 */
export function Row({
  id,
  title,
  open = false,
  onToggle,
  children,
  detail,
}: {
  id: string;
  title: string;
  open?: boolean;
  onToggle?: () => void;
  /** The answer, always visible. */
  children: ReactNode;
  /** The section the answer comes from, shown when open. None: the row is the whole answer. */
  detail?: ReactNode;
}) {
  if (detail === undefined || !onToggle) {
    return (
      <section aria-labelledby={`${id}-title`} className="flex items-start gap-3 border-t border-edge-soft py-4">
        <span id={`${id}-title`} className="w-[4.5rem] shrink-0 pt-px text-[12.5px] text-faint">
          {title}
        </span>
        <div className="min-w-0 flex-1">{children}</div>
      </section>
    );
  }
  return (
    <section aria-labelledby={`${id}-title`} className="border-t border-edge-soft">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        aria-controls={`${id}-detail`}
        className="group flex w-full items-start gap-3 py-4 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <span id={`${id}-title`} className="w-[4.5rem] shrink-0 pt-px text-[12.5px] text-faint">
          {title}
        </span>
        <span className="min-w-0 flex-1">{children}</span>
        <ChevronDown
          aria-hidden
          className={cn(
            "mt-0.5 size-4 shrink-0 text-faint transition-transform group-hover:text-dim motion-reduce:transition-none",
            open && "rotate-180",
          )}
        />
      </button>
      {open ? (
        <div id={`${id}-detail`} className="pb-6">
          {detail}
        </div>
      ) : null}
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
        <li key={p.key} className="tnum flex items-baseline justify-between gap-3 text-[13px]">
          <span className="text-dim">{p.label}</span>
          <span className={p.value >= 0 ? "text-approve/85" : "text-reject/85"}>{signedKrw(p.value)}</span>
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
      <p className="tnum mb-1.5 flex justify-between text-[12px] text-faint">
        <span>환율·수수료·기타 세부</span>
        <span>{signedKrw(otherKrw)}</span>
      </p>
      <ul className="space-y-0.5 border-l border-edge-soft ps-3 text-[12px]">
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
