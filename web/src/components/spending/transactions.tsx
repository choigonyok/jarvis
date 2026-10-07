"use client";

import { krw } from "@/lib/portfolio";
import { type Tx, cardLabel, clock, dayLabel, groupByDay } from "@/lib/spending";
import { cn } from "@/lib/utils";

/**
 * Every charge, newest first, by day. The time sits in a narrow rail on the
 * left as it does in the thread; the merchant is the line you read; the
 * amount is on the right where a column of them can be scanned.
 *
 * A cancelled charge and its cancellation stay in the list, struck through:
 * the list should match the alerts on the phone, and a charge that silently
 * vanished would read as a lost alert.
 */
export function Transactions({ txs, onOpen }: { txs: Tx[]; onOpen: (t: Tx) => void }) {
  const days = groupByDay(txs);
  return (
    <div className="space-y-5">
      {days.map((g) => (
        <section key={g.date} aria-label={dayLabel(g.date)}>
          <div className="mb-1 flex items-baseline justify-between px-2">
            <h3 className="text-[12px] text-dim">{dayLabel(g.date)}</h3>
            <span className="tnum text-[11.5px] text-faint">{krw(g.totalKrw)}</span>
          </div>
          <ul className="space-y-px">
            {g.txs.map((t) => (
              <li key={t.id}>
                <Row t={t} onOpen={() => onOpen(t)} />
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function Row({ t, onOpen }: { t: Tx; onOpen: () => void }) {
  const struck = t.status === "cancelled" || t.status === "cancel";
  const quiet = struck || t.status === "excluded";
  const items = t.items ?? [];
  const notes = [
    // An opened-up order is not one category any more; its lines say which.
    items.length > 0 ? `상품 ${items.length}개` : t.category,
    cardLabel(t),
    t.installment > 0 ? `${t.installment}개월` : "",
    t.status === "cancelled" ? "취소됨" : "",
    t.status === "cancel" ? "취소 알림" : "",
    t.status === "refund" ? "부분 취소" : "",
    t.status === "excluded" ? "합계에서 뺌" : "",
    t.enrichStatus === "pending" ? "주문 확인 중" : "",
    t.enrichStatus === "not_found" ? "주문을 못 찾음" : "",
    t.enrichStatus === "login_required" ? "로그인 필요" : "",
    t.memo,
  ].filter(Boolean);

  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex w-full items-start gap-3 rounded-lg px-2 py-2.5 text-left transition-colors outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span className="tnum w-9 shrink-0 pt-[3px] text-[11px] text-faint">{clock(t.approvedAt)}</span>
      <span className="min-w-0 flex-1">
        <span
          className={cn(
            "block truncate text-[14px]",
            quiet ? "text-faint" : "text-foreground/90",
            struck && "line-through decoration-faint",
          )}
        >
          {t.merchant}
        </span>
        <span className="block truncate text-[11.5px] text-faint">{notes.join(" · ")}</span>
        {items.length > 0 ? (
          // What the order was, under the charge it explains. A hairline on
          // the left ties the lines to the row without boxing them.
          <span className="mt-1.5 block space-y-0.5 border-s border-edge ps-2.5">
            {items.map((it) => (
              <span key={it.id} className="flex items-baseline gap-2 text-[12px]">
                <span className="min-w-0 flex-1 truncate text-dim">
                  {it.name}
                  {it.quantity > 1 ? <span className="text-faint"> ×{it.quantity}</span> : null}
                </span>
                <span className="shrink-0 text-[11px] text-faint">{it.category}</span>
                <span className="tnum w-[4.5rem] shrink-0 text-right text-dim">{krw(it.amountKrw)}</span>
              </span>
            ))}
          </span>
        ) : null}
      </span>
      <span className="shrink-0 text-right">
        <span
          className={cn(
            "tnum block text-[14px]",
            quiet ? "text-faint" : "text-foreground/90",
            struck && "line-through decoration-faint",
          )}
        >
          {t.status === "refund" ? "−" : ""}
          {t.estimated ? "약 " : ""}
          {krw(t.amountKrw)}
        </span>
        {t.foreignAmount !== null ? (
          <span className="tnum block text-[11px] text-faint">${t.foreignAmount.toFixed(2)}</span>
        ) : null}
      </span>
    </button>
  );
}
