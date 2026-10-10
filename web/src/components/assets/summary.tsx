"use client";

import type { ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { type Portfolio, krw, signedKrw } from "@/lib/portfolio";
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
  open,
  onToggle,
  children,
  detail,
}: {
  id: string;
  title: string;
  open: boolean;
  onToggle: () => void;
  /** The answer, always visible. */
  children: ReactNode;
  /** The section the answer comes from, shown when open. */
  detail: ReactNode;
}) {
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
export function ProfitBridge({
  realizedKrw,
  unrealizedKrw,
  otherKrw,
}: {
  realizedKrw: number;
  unrealizedKrw: number;
  otherKrw: number;
}) {
  const parts = [
    { key: "realized", label: "판 것", value: realizedKrw, tone: 0.9 },
    { key: "unrealized", label: "들고 있는 것", value: unrealizedKrw, tone: 0.55 },
    { key: "other", label: "환율·수수료·기타", value: otherKrw, tone: 0.3 },
  ];
  const scale = parts.reduce((sum, p) => sum + Math.abs(p.value), 0) || 1;
  return (
    <div>
      <div
        className="flex h-2 w-full gap-[2px] overflow-hidden rounded-full"
        role="img"
        aria-label={parts.map((p) => `${p.label} ${signedKrw(p.value)}`).join(", ")}
      >
        {parts.map((p) =>
          Math.abs(p.value) / scale >= 0.005 ? (
            <span
              key={p.key}
              className={p.value >= 0 ? "bg-approve" : "bg-reject"}
              style={{ width: `${(Math.abs(p.value) / scale) * 100}%`, opacity: p.tone }}
            />
          ) : null,
        )}
      </div>
      <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px]">
        {parts.map((p) => (
          <li key={p.key} className="tnum flex items-baseline gap-1.5">
            <span
              aria-hidden
              className={cn("size-1.5 self-center rounded-full", p.value >= 0 ? "bg-approve" : "bg-reject")}
              style={{ opacity: p.tone }}
            />
            <span className="text-faint">{p.label}</span>
            <span className={p.value >= 0 ? "text-approve/85" : "text-reject/85"}>{signedKrw(p.value)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * What wants doing, in sentences: a bucket out of its band (as the transfer
 * that fixes it), a ledger that disagrees with the trades, a year's gains
 * nearing the tax-free allowance. Empty is an answer too.
 */
export function todos(data: Portfolio): { text: string; where: "target" | "principal" | "realized" }[] {
  const out: { text: string; where: "target" | "principal" | "realized" }[] = [];
  const a = data.allocation;
  if (a) {
    const label = (id: string) => a.rows.find((r) => r.id === id)?.label ?? id;
    for (const m of a.moves.filter((m) => !m.optional)) {
      out.push({ text: `${label(m.from)}에서 ${label(m.to)}로 ${krw(m.amountKrw)} 옮기기`, where: "target" });
    }
  }
  for (const c of data.principal?.checks ?? []) {
    out.push({ text: `원금 기록 확인: ${c.message.split(". ")[0]}.`, where: "principal" });
  }
  const overseas = data.realized?.tax.baskets.find((b) => b.kind === "overseas" && b.inForce);
  if (overseas) {
    if (overseas.taxKrw > 0) {
      out.push({ text: `올해 해외주식 양도세 약 ${krw(overseas.taxKrw)} - 내년 5월에 신고해요`, where: "realized" });
    } else if (overseas.gainKrw >= overseas.deductionKrw * 0.8) {
      out.push({
        text: `해외주식 공제가 ${krw(overseas.deductionKrw - overseas.gainKrw)}만 남았어요 - 더 팔면 세금이 붙어요`,
        where: "realized",
      });
    }
  }
  return out;
}
