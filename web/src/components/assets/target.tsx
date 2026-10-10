"use client";

import { useMemo, useState } from "react";
import { plannedGains } from "@/components/assets/realized";
import { type Holding, type Realized, krw, signedKrw, taxFor } from "@/lib/portfolio";
import type { Allocation, Bucket } from "@/lib/target";
import { type Order, depositPlan, rebalancePlan } from "@/lib/trade-plan";

/**
 * Each bucket keeps one hue everywhere it appears. None of them is green or
 * red - those mean profit and loss on this surface - and gold is the one
 * colour allowed to be literal.
 */
const HUE: Record<Bucket, string> = {
  growth: "oklch(0.7 0.085 252)",
  coin: "oklch(0.7 0.1 298)",
  cash: "oklch(0.82 0.008 265)",
  dividend: "oklch(0.6 0.045 225)",
  gold: "oklch(0.8 0.105 82)",
};

const pct = (rate: number) => {
  const v = rate * 100;
  return `${v > 0 && v < 1 ? v.toFixed(1) : Math.round(v)}%`;
};

function Dot({ id }: { id: Bucket }) {
  return (
    <span
      aria-hidden
      className="size-2 shrink-0 rounded-[2px]"
      style={{ background: HUE[id] }}
    />
  );
}

/**
 * Where the money sits, as one bar with each target's end ticked on it: a
 * slice running past its tick is over target. The folded view.
 */
export function AllocationBar({ allocation }: { allocation: Allocation }) {
  const { rows } = allocation;
  const ticks = rows.slice(0, -1).map((_, i) => rows.slice(0, i + 1).reduce((sum, r) => sum + r.target, 0));
  return (
    <div className="relative">
      <div
        className="flex h-2 w-full gap-[2px] overflow-hidden rounded-full"
        role="img"
        aria-label={rows.map((r) => `${r.label} ${pct(r.current)}, 목표 ${pct(r.target)}`).join("; ")}
      >
        {rows.map((r) =>
          r.current > 0.001 ? <span key={r.id} style={{ width: `${r.current * 100}%`, background: HUE[r.id] }} /> : null,
        )}
      </div>
      {ticks.map((t, i) => (
        <span key={i} aria-hidden className="absolute -top-1 h-4 w-px bg-foreground/70" style={{ left: `${t * 100}%` }} />
      ))}
    </div>
  );
}

/**
 * The bar explained, and what to do about it: the buckets as "now (target)",
 * the orders only when something has left its band, and a field for new
 * cash that answers with what to buy. The opened view.
 */
export function AllocationDetail({
  allocation,
  holdings,
  goldGramKrw,
  tax,
}: {
  allocation: Allocation;
  holdings: Holding[];
  goldGramKrw?: number | null;
  tax?: Realized["tax"];
}) {
  const { rows } = allocation;
  const [amount, setAmount] = useState("");
  const deposit = Number(amount.replace(/[^\d]/g, "")) || 0;
  const rebalance = useMemo(() => rebalancePlan(allocation, holdings, { goldGramKrw }), [allocation, holdings, goldGramKrw]);
  const fill = useMemo(
    () => (deposit > 0 ? depositPlan(allocation, holdings, deposit, { goldGramKrw }) : null),
    [allocation, holdings, deposit, goldGramKrw],
  );
  const sells = rebalance.orders.filter((o) => o.side === "sell");
  const gains = plannedGains(sells);
  const overseas = tax?.baskets.find((b) => b.kind === "overseas" && b.inForce);
  const taxUp = overseas ? taxFor(overseas.gainKrw + gains.overseas, overseas) - taxFor(overseas.gainKrw, overseas) : 0;

  return (
    <div className="space-y-2.5">
      <ul className="flex flex-wrap gap-x-3 gap-y-1 text-[12px]">
        {rows.map((r) => (
          <li key={r.id} className="tnum flex items-center gap-1.5">
            <Dot id={r.id} />
            <span className={r.inBand ? "text-faint" : "text-foreground"}>{r.label}</span>
            <span className={r.inBand ? "text-dim" : "font-semibold text-foreground"}>{pct(r.current)}</span>
            <span className="text-faint">({pct(r.target)})</span>
          </li>
        ))}
      </ul>
      <p className="text-[11px] leading-relaxed text-faint">
        막대 위 눈금이 목표 경계예요. 허용 범위는 목표 ±7%p, 비중이 작은 자산은 목표의 절반까지예요.
      </p>

      {rebalance.orders.length || rebalance.unpicked.length ? (
        <div className="text-[12.5px] leading-relaxed">
          <p className="text-dim">
            주 단위로 맞추면{" "}
            {[
              ...rebalance.orders.map((o) => orderText(o)),
              ...rebalance.unpicked.map((u) => `${u.label} ${krw(u.amountKrw)} (종목 골라야 함)`),
            ].join(" · ")}
          </p>
          {overseas && Math.abs(gains.overseas) >= 1_000 ? (
            <p className="tnum text-faint">
              이번 매도 실현손익 약 {signedKrw(gains.overseas)} ·{" "}
              {taxUp > 0 ? `양도세 약 ${krw(taxUp)} 늘어남` : "공제 안이라 양도세 없음"}
            </p>
          ) : null}
        </div>
      ) : null}

      <label className="flex flex-wrap items-center gap-x-2 gap-y-1.5 text-[12.5px] text-dim">
        <span className="shrink-0">새로 넣을 돈</span>
        <span className="relative">
          <input
            inputMode="numeric"
            value={deposit ? deposit.toLocaleString("ko-KR") : ""}
            onChange={(e) => setAmount(e.target.value)}
            placeholder="예: 1,000,000"
            className="tnum h-9 w-36 rounded-lg border border-edge-soft bg-glass px-2.5 pe-7 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-8 sm:text-[13px]"
          />
          <span aria-hidden className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-[12px] text-faint">
            원
          </span>
        </span>
        {fill ? (
          <span className="basis-full text-dim sm:basis-auto">
            {fill.orders.length
              ? `→ ${fill.orders.map((o) => orderText(o)).join(" · ")}`
              : "→ 이 금액으로는 살 수 있는 단위가 없어요"}
            {fill.cashChangeKrw >= 1 ? <span className="text-faint"> · 남는 현금 {krw(fill.cashChangeKrw)}</span> : null}
          </span>
        ) : null}
      </label>
    </div>
  );
}

/** "스페이스X 2주 팔기 약 ₩44만", "BTC ₩32만 사기". */
function orderText(o: Order): string {
  const name = o.holding?.name || o.holding?.symbol || "";
  const verb = o.side === "sell" ? "팔기" : "사기";
  const man = `₩${Math.round(o.amountKrw / 10_000).toLocaleString("ko-KR")}만`;
  if (o.units === null) return `${name} ${o.amountKrw >= 10_000 ? man : krw(o.amountKrw)} ${verb}`;
  const unit = o.holding?.kind === "gold" ? "g" : "주";
  return `${name} ${o.units}${unit} ${verb} 약 ${o.amountKrw >= 10_000 ? man : krw(o.amountKrw)}`;
}
